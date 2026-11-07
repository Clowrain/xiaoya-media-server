package embyserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalSubtitlesDiscoveryAndSRTToVTT(t *testing.T) {
	tmp := t.TempDir()
	RegisterMediaRoot(tmp)

	seasonDir := filepath.Join(tmp, "折月亮 (2026)", "{tmdb-270431}", "Season 1")
	subsChild := filepath.Join(seasonDir, "Subs")
	if err := os.MkdirAll(subsChild, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ep1 := filepath.Join(seasonDir, "E01.strm")
	ep2 := filepath.Join(seasonDir, "E02.strm")
	_ = os.WriteFile(ep1, []byte("http://xiaoya.host:5678/d/E01.mp4\n"), 0o644)
	_ = os.WriteFile(ep2, []byte("http://xiaoya.host:5678/d/E02.mp4\n"), 0o644)

	// Matching subtitles for E01 + non-matching subtitle for E02
	srtRaw := "\xef\xbb\xbf1\r\n00:00:01,250 --> 00:00:04,500\r\n你好，折月亮！\r\n"
	_ = os.WriteFile(filepath.Join(seasonDir, "E01.chs.default.srt"), []byte(srtRaw), 0o644)
	_ = os.WriteFile(filepath.Join(seasonDir, "E01.cht.ass"), []byte("[Script Info]\nTitle: E01\n"), 0o644)
	_ = os.WriteFile(filepath.Join(subsChild, "E01.en.vtt"), []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n"), 0o644)
	_ = os.WriteFile(filepath.Join(seasonDir, "E02.chs.srt"), []byte(srtRaw), 0o644)

	x := Item{ID: "ep01", Kind: "Episode", Name: "E01", Path: ep1}
	subs := discoverExternalSubtitles(x)
	if len(subs) != 3 {
		t.Fatalf("expected 3 external subtitles for E01, got %d: %+v", len(subs), subs)
	}

	dst := M{
		"MediaStreams": []M{
			{"Type": "Video", "Index": 0, "Codec": "hevc", "IsExternal": false},
			{"Type": "Audio", "Index": 1, "Codec": "aac", "IsExternal": false},
		},
	}
	appendExternalSubtitles(x, dst, "tok123")
	streams := dst["MediaStreams"].([]M)
	if len(streams) != 5 {
		t.Fatalf("expected 2 embedded + 3 external = 5 streams, got %d", len(streams))
	}
	firstSub := streams[2]
	if firstSub["Index"] != 2 || firstSub["IsExternal"] != true || firstSub["DeliveryMethod"] != "External" {
		t.Fatalf("unexpected external subtitle stream metadata: %+v", firstSub)
	}
	if !strings.Contains(firstSub["DeliveryUrl"].(string), "/Videos/ep01/ep01/Subtitles/2/Stream.") {
		t.Fatalf("unexpected DeliveryUrl: %v", firstSub["DeliveryUrl"])
	}

	// Verify SRT -> WebVTT conversion
	vtt := string(srtToWebVTT([]byte(srtRaw)))
	if !strings.HasPrefix(vtt, "WEBVTT\n\n") || !strings.Contains(vtt, "00:00:01.250 --> 00:00:04.500") || !strings.Contains(vtt, "你好，折月亮！") {
		t.Fatalf("unexpected WebVTT output:\n%s", vtt)
	}
}

func TestLocalMediaFileRangeAndHeadStreaming(t *testing.T) {
	tmp := t.TempDir()
	RegisterMediaRoot(tmp)

	videoPath := filepath.Join(tmp, "sample.mp4")
	payload := []byte("0123456789ABCDEF")
	if err := os.WriteFile(videoPath, payload, 0o644); err != nil {
		t.Fatalf("write sample.mp4: %v", err)
	}

	// 1. HEAD request
	headReq := httptest.NewRequest(http.MethodHead, "/Videos/v1/stream.mp4", nil)
	headRec := httptest.NewRecorder()
	serveLocalMediaStream(headRec, headReq, "file://"+videoPath)
	if headRec.Code != http.StatusOK || headRec.Header().Get("Content-Type") != "video/mp4" || headRec.Body.Len() != 0 {
		t.Fatalf("HEAD failed: status=%d ct=%q len=%d", headRec.Code, headRec.Header().Get("Content-Type"), headRec.Body.Len())
	}

	// 2. HTTP Range request
	rangeReq := httptest.NewRequest(http.MethodGet, "/Videos/v1/stream.mp4", nil)
	rangeReq.Header.Set("Range", "bytes=4-9")
	rangeRec := httptest.NewRecorder()
	serveLocalMediaStream(rangeRec, rangeReq, "file://"+videoPath)
	if rangeRec.Code != http.StatusPartialContent || rangeRec.Body.String() != "456789" {
		t.Fatalf("Range failed: status=%d body=%q", rangeRec.Code, rangeRec.Body.String())
	}

	// 3. Path outside MEDIA_ROOTS must be rejected with 403
	unauthDir, err := os.MkdirTemp("", "unauth-media-*")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	defer os.RemoveAll(unauthDir)
	unauthFile := filepath.Join(unauthDir, "secret.mp4")
	_ = os.WriteFile(unauthFile, payload, 0o644)

	forbidRec := httptest.NewRecorder()
	serveLocalMediaStream(forbidRec, httptest.NewRequest(http.MethodGet, "/Videos/v2/stream.mp4", nil), "file://"+unauthFile)
	if forbidRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for outside media root, got %d", forbidRec.Code)
	}
}

func TestXiaoyaExpiredSignFallbacks(t *testing.T) {
	t.Run("fallback_strip_expired_sign", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("sign") == "EXPIRED_SIG:0" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"code":401,"message":"sign expired"}`))
				return
			}
			w.Header().Set("Location", "https://cdn.example/unsigned-ok.mp4")
			w.WriteHeader(http.StatusFound)
		}))
		defer srv.Close()

		req := httptest.NewRequest(http.MethodGet, "/Videos/ep1/stream", nil)
		link, err := xiaoyaLinkWithToken(req, "http://xiaoya.host:5678/d/每日更新/E01.mp4?sign=EXPIRED_SIG:0", srv.URL, "")
		if err != nil || link != "https://cdn.example/unsigned-ok.mp4" {
			t.Fatalf("got link=%q err=%v", link, err)
		}
	})

	t.Run("fallback_alist_fs_get_raw_url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/fs/get" && r.Method == http.MethodPost {
				if r.Header.Get("Authorization") != "alist-tok" {
					t.Errorf("missing Authorization token on /api/fs/get")
				}
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["path"] != "/每日更新/E01.mp4" {
					t.Errorf("unexpected fs/get path: %q", body["path"])
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":200,"message":"success","data":{"raw_url":"https://cdn.example/refreshed-raw.mp4","sign":"NEW_SIG:0"}}`))
				return
			}
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		req := httptest.NewRequest(http.MethodGet, "/Videos/ep1/stream", nil)
		link, err := xiaoyaLinkWithToken(req, "http://xiaoya.host:5678/d/%E6%AF%8F%E6%97%A5%E6%9B%B4%E6%96%B0/E01.mp4?sign=OLD:0", srv.URL, "alist-tok")
		if err != nil || link != "https://cdn.example/refreshed-raw.mp4" {
			t.Fatalf("got link=%q err=%v", link, err)
		}
	})

	t.Run("fallback_alist_fs_get_new_sign_redirect", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/fs/get" && r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":200,"message":"success","data":{"raw_url":"","sign":"FRESH_SIG:999"}}`))
				return
			}
			if r.URL.Query().Get("sign") == "FRESH_SIG:999" {
				w.Header().Set("Location", "https://cdn.example/fresh-signed.mp4")
				w.WriteHeader(http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		req := httptest.NewRequest(http.MethodGet, "/Videos/ep1/stream", nil)
		link, err := xiaoyaLinkWithToken(req, "http://xiaoya.host:5678/d/show/E01.mp4?sign=OLD:0", srv.URL, "")
		if err != nil || link != "https://cdn.example/fresh-signed.mp4" {
			t.Fatalf("got link=%q err=%v", link, err)
		}
	})
}

func TestComputeNextUpPerSeries(t *testing.T) {
	candidates := []nextUpCandidate{
		// Series A: E01 & E02 played -> NextUp should be E03 (ignoring Season 0 special)
		{Item: Item{ID: "a-s0e1", Season: 0, Episode: 1}, SeriesID: "series-A"},
		{Item: Item{ID: "a-s1e1", Season: 1, Episode: 1}, SeriesID: "series-A", Played: true, LastActivity: 100},
		{Item: Item{ID: "a-s1e2", Season: 1, Episode: 2}, SeriesID: "series-A", Played: true, LastActivity: 200},
		{Item: Item{ID: "a-s1e3", Season: 1, Episode: 3}, SeriesID: "series-A", Played: false},
		{Item: Item{ID: "a-s1e4", Season: 1, Episode: 4}, SeriesID: "series-A", Played: false},

		// Series B: S01E01 played, S01E02 half-watched (Position > 0) with more recent activity 500 -> NextUp should be S01E02 first!
		{Item: Item{ID: "b-s1e1", Season: 1, Episode: 1}, SeriesID: "series-B", Played: true, LastActivity: 300},
		{Item: Item{ID: "b-s1e2", Season: 1, Episode: 2}, SeriesID: "series-B", Played: false, Position: 600000000, LastActivity: 500},
		{Item: Item{ID: "b-s1e3", Season: 1, Episode: 3}, SeriesID: "series-B", Played: false},

		// Series C: completely unwatched -> must NOT appear in general NextUp
		{Item: Item{ID: "c-s1e1", Season: 1, Episode: 1}, SeriesID: "series-C"},
	}

	got := computeNextUpPerSeries(candidates, "")
	if len(got) != 2 {
		t.Fatalf("expected 2 NextUp items, got %d: %+v", len(got), got)
	}
	if got[0].ID != "b-s1e2" || got[1].ID != "a-s1e3" {
		t.Fatalf("unexpected NextUp order/items: [%s, %s], want [b-s1e2, a-s1e3]", got[0].ID, got[1].ID)
	}

	// Explicit SeriesId query on unwatched Series C should return S01E01
	gotExplicit := computeNextUpPerSeries(candidates, "series-C")
	if len(gotExplicit) != 1 || gotExplicit[0].ID != "c-s1e1" {
		t.Fatalf("explicit SeriesId NextUp failed: %+v", gotExplicit)
	}
}
