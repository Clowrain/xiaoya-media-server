package alist

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/strm"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(obj any) *http.Response {
	b, _ := json.Marshal(obj)
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(b))),
		Header:     h,
	}
}

func TestAListBrowseSearchImportAndSeriesAutoTrack(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	strmOut := filepath.Join(tmp, "strm")

	bizDB, err := sql.Open("sqlite", filepath.Join(tmp, "biz.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer bizDB.Close()

	store := discovery.NewSQLStore(bizDB, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	seasonEpisodes := []Entry{
		{Name: "S01E01.mkv", Size: 1024, IsDir: false, Sign: "SIG_E01:0"},
		{Name: "S01E02.mkv", Size: 2048, IsDir: false, Sign: "SIG_E02:0"},
	}

	mockHTTP := &http.Client{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			if r.Body != nil {
				_ = json.NewDecoder(r.Body).Decode(&body)
			}
			switch r.URL.Path {
			case "/api/auth/login":
				return jsonResp(map[string]any{"code": 200, "message": "success", "data": map[string]any{"token": "jwt-tok-1"}}), nil
			case "/api/fs/search":
				return jsonResp(map[string]any{
					"code": 200,
					"data": map[string]any{
						"total": 1,
						"content": []map[string]any{
							{"parent": "/阿里云盘/剧集", "name": "漫长的季节 (2023)", "is_dir": true, "size": 0},
						},
					},
				}), nil
			case "/api/fs/get":
				p, _ := body["path"].(string)
				if strings.HasSuffix(p, ".mkv") {
					return jsonResp(map[string]any{
						"code": 200,
						"data": map[string]any{"name": filepath.Base(p), "is_dir": false, "size": 4096, "sign": "SIG_SINGLE:0"},
					}), nil
				}
				return jsonResp(map[string]any{
					"code": 200,
					"data": map[string]any{"name": filepath.Base(p), "is_dir": true, "size": 0},
				}), nil
			case "/api/fs/list":
				p, _ := body["path"].(string)
				switch p {
				case "/阿里云盘/剧集/漫长的季节 (2023)":
					return jsonResp(map[string]any{
						"code": 200,
						"data": map[string]any{
							"total":   1,
							"content": []Entry{{Name: "Season 1", IsDir: true}},
						},
					}), nil
				case "/阿里云盘/剧集/漫长的季节 (2023)/Season 1":
					return jsonResp(map[string]any{
						"code": 200,
						"data": map[string]any{
							"total":   len(seasonEpisodes),
							"content": seasonEpisodes,
						},
					}), nil
				}
			}
			return jsonResp(map[string]any{"code": 404, "message": "not found"}), nil
		}),
	}

	cli := NewClient(Options{
		BaseURL:    "http://xiaoya.host:5678",
		Username:   "admin",
		Password:   "pass",
		HTTPClient: mockHTTP,
	})

	// 1. Search test
	searchItems, total, err := cli.Search(ctx, "/", "漫长的季节", 0, 1, 20)
	if err != nil || total != 1 || searchItems[0].FullPath != "/阿里云盘/剧集/漫长的季节 (2023)" {
		t.Fatalf("Search failed: items=%+v total=%d err=%v", searchItems, total, err)
	}

	strmEng := strm.NewEngine(strm.Config{
		OutputRoot:              strmOut,
		PreserveSourceStructure: true,
		TargetBaseURL:           "http://xiaoya.host:5678",
		AttachmentStore:         store,
	})
	discSvc := discovery.NewService(store, nil, strmEng)
	imp := NewImporter(cli, discSvc, strmEng, nil)

	// 2. Whole-series directory import with auto-tracking
	sum, err := imp.Import(ctx, ImportRequest{
		Path:         "/阿里云盘/剧集/漫长的季节 (2023)",
		TrackUpdates: true,
	})
	if err != nil || sum.ImportedFiles != 2 || len(sum.GeneratedSTRMs) != 2 {
		t.Fatalf("Directory Import failed: sum=%+v err=%v", sum, err)
	}

	// 3. Simulate S01E03 added in AList -> SyncTrackedDirectories automatically discovers & generates S01E03.strm!
	seasonEpisodes = append(seasonEpisodes, Entry{Name: "S01E03.mkv", Size: 3072, IsDir: false, Sign: "SIG_E03:0"})
	syncedDirs, err := imp.SyncTrackedDirectories(ctx)
	if err != nil || syncedDirs != 1 {
		t.Fatalf("SyncTrackedDirectories failed: synced=%d err=%v", syncedDirs, err)
	}
	ep3STRM := filepath.Join(strmOut, "阿里云盘", "剧集", "漫长的季节 (2023)", "Season 1", "S01E03.strm")
	if raw, rErr := os.ReadFile(ep3STRM); rErr != nil || !strings.Contains(string(raw), "SIG_E03:0") {
		t.Fatalf("auto-tracked S01E03.strm missing or invalid: content=%q err=%v", string(raw), rErr)
	}
}

func TestAListReloginAlsoRefreshesPlaybackLink(t *testing.T) {
	logins := 0
	cli := NewClient(Options{BaseURL: "http://xiaoya.host:5678", Username: "fixture", Password: "fixture", HTTPClient: &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/auth/login":
			logins++
			tok := "expired"
			if logins == 2 {
				tok = "new"
			}
			return jsonResp(map[string]any{"code": 200, "data": map[string]string{"token": tok}}), nil
		case "/api/fs/get":
			if r.Header.Get("Authorization") != "new" {
				return jsonResp(map[string]any{"code": 401, "message": "expired"}), nil
			}
			if r.Header.Get("X-Alist-OriUA") != "VidHub" {
				t.Errorf("missing client user agent")
			}
			return jsonResp(map[string]any{"code": 200, "data": map[string]string{"raw_url": "https://cdn.example/episode.mp4", "sign": "new-sign"}}), nil
		}
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})}})
	u, sign, err := cli.PlaybackFile(context.Background(), "/Tracked/E01.mp4", "VidHub")
	if err != nil || u != "https://cdn.example/episode.mp4" || sign != "new-sign" || logins != 2 {
		t.Fatalf("u=%q sign=%q logins=%d err=%v", u, sign, logins, err)
	}
}

func TestOnlyOptedInDirectoryTracksAndAbsenceRequiresHealthyListing(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(tmp, "biz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	entries := map[string][]Entry{"/Once": {{Name: "Season", IsDir: true}}, "/Once/Season": {{Name: "E01.mp4"}}, "/Tracked": {{Name: "E01.mp4"}}}
	fail := false
	client := NewClient(Options{HTTPClient: &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		var b struct{ Path string }
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			return nil, e
		}
		if fail && b.Path == "/Tracked" {
			return jsonResp(map[string]any{"code": 503, "message": "offline"}), nil
		}
		es := entries[b.Path]
		return jsonResp(map[string]any{"code": 200, "data": map[string]any{"content": es, "total": len(es)}}), nil
	})}})
	e := strm.NewEngine(strm.Config{OutputRoot: filepath.Join(tmp, "out"), PreserveSourceStructure: true, PathStore: store})
	s := discovery.NewService(store, nil, e)
	im := NewImporter(client, s, e, nil)
	dir := true
	if _, err = im.Import(ctx, ImportRequest{Path: "/Once", IsDir: &dir}); err != nil {
		t.Fatal(err)
	}
	if _, err = im.Import(ctx, ImportRequest{Path: "/Tracked", IsDir: &dir, TrackUpdates: true}); err != nil {
		t.Fatal(err)
	}
	entries["/Once/Season"] = append(entries["/Once/Season"], Entry{Name: "E02.mp4"})
	entries["/Tracked"] = nil
	fail = true
	if _, err = im.SyncTrackedDirectories(ctx); err == nil {
		t.Fatal("upstream failure was swallowed")
	}
	r, _, _ := store.GetResourceBySourceKey(ctx, "alist:/Tracked/E01.mp4")
	if r.Status != discovery.StatusActive {
		t.Fatalf("failed inventory changed state to %s", r.Status)
	}
	fail = false
	if n, e := im.SyncTrackedDirectories(ctx); n != 1 || e != nil {
		t.Fatalf("tracking n=%d err=%v", n, e)
	}
	if _, exists, _ := store.GetResourceBySourceKey(ctx, "alist:/Once/Season/E02.mp4"); exists {
		t.Fatal("one-shot import started tracking")
	}
	r, _, _ = store.GetResourceBySourceKey(ctx, "alist:/Tracked/E01.mp4")
	if r.Status != discovery.StatusMissing {
		t.Fatalf("confirmed absence was not recorded: %+v", r)
	}
	if _, err = os.Stat(r.LocalSTRMPath); err != nil {
		t.Fatalf("keep lost STRM: %v", err)
	}
	entries["/Tracked"] = []Entry{{Name: "E01.mp4"}}
	if _, err = im.SyncTrackedDirectories(ctx); err != nil {
		t.Fatal(err)
	}
	next, _, _ := store.GetResourceBySourceKey(ctx, r.SourceKey)
	if next.Status != discovery.StatusActive || next.ID != r.ID || next.LocalSTRMPath != r.LocalSTRMPath {
		t.Fatalf("restored resource lost identity: %+v", next)
	}
}
