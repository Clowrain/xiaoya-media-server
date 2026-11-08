package embyserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newSQLitePlaybackApp builds the SQLite harness plus the playback-policy table
// the Postgres schema owns, so reserve() permits the test user.
func newSQLitePlaybackApp(t *testing.T) *App {
	t.Helper()
	a := newTestSQLiteApp(t)
	if _, e := a.db.Exec("CREATE TABLE user_playback(user_id TEXT PRIMARY KEY,allowed INT)"); e != nil {
		t.Fatal(e)
	}
	var uid string
	if e := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO user_playback VALUES(?,1)", uid); e != nil {
		t.Fatal(e)
	}
	return a
}

func insertSQLiteItem(t *testing.T, a *App, id, url string) {
	t.Helper()
	insertSQLiteLibrary(t, a, "/media/test")
	insertSQLiteItemRow(t, a, id, "/media/test/a.strm", url)
}

func insertSQLiteLibrary(t *testing.T, a *App, path string) {
	t.Helper()
	if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('l','test',?,'movies')", path); e != nil {
		t.Fatal(e)
	}
}

func insertSQLiteItemRow(t *testing.T, a *App, id, path, url string) {
	t.Helper()
	if _, e := a.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'g')",
		id, "l", "l", "movie", "Movie", path, url, "", "", 0, 0, 0, 0, 0); e != nil {
		t.Fatal(e)
	}
}

func newSQLiteToken(t *testing.T, a *App, value, device string) string {
	t.Helper()
	var uid string
	if e := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", digest(value), uid, device, time.Now().Unix()+600); e != nil {
		t.Fatal(e)
	}
	return uid
}

func TestClientTicksTolerant(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want clientTicks
	}{
		{"null", `null`, 0},
		{"empty-string", `""`, 0},
		{"quoted-int", `"123"`, 123},
		{"bare-int", `123`, 123},
		{"float-truncates", `123.9`, 123},
		{"negative-float-truncates", `-5.7`, -5},
		{"garbage", `"abc"`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n clientTicks
			if e := n.UnmarshalJSON([]byte(tc.in)); e != nil {
				t.Fatalf("UnmarshalJSON(%s) returned error: %v", tc.in, e)
			}
			if n != tc.want {
				t.Fatalf("UnmarshalJSON(%s) = %d, want %d", tc.in, n, tc.want)
			}
		})
	}
}

func TestSessionsPlayingNullTicks(t *testing.T) {
	a := newSQLitePlaybackApp(t)
	insertSQLiteItem(t, a, "null-ticks", "http://origin/movie.mkv")
	uid := newSQLiteToken(t, a, "null-ticks-tok", "dev-null-ticks")
	r := httptest.NewRequest("POST", "/Sessions/Playing", strings.NewReader(`{"ItemId":"null-ticks","PositionTicks":null,"RunTimeTicks":null}`))
	r.Header.Set("X-Emby-Token", "null-ticks-tok")
	w := httptest.NewRecorder()
	a.serve(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var position int
	if e := a.db.QueryRow("SELECT position FROM userdata WHERE user_id=? AND item=?", uid, "null-ticks").Scan(&position); e == nil && position != 0 {
		t.Fatalf("null ticks must not record progress, got %d", position)
	}
}

func TestSessionsPlayingQueryFallback(t *testing.T) {
	a := newSQLitePlaybackApp(t)
	insertSQLiteItem(t, a, "query-item", "http://origin/movie.mkv")
	uid := newSQLiteToken(t, a, "query-progress-tok", "dev-progress")

	// 无报文、无可用参数（且该设备无播放记录）→ 400。
	noParams := httptest.NewRequest("POST", "/Sessions/Playing", nil)
	noParams.Header.Set("X-Emby-Token", "query-progress-tok")
	w := httptest.NewRecorder()
	a.serve(w, noParams)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "缺少播放参数") {
		t.Fatalf("expected 400 缺少播放参数, got %d: %s", w.Code, w.Body.String())
	}

	// 全部参数走查询串、报文为空 → 204 并写入进度。
	r := httptest.NewRequest("POST", "/Sessions/Playing/Progress?ItemId=query-item&PositionTicks=123&RunTimeTicks=456", nil)
	r.Header.Set("X-Emby-Token", "query-progress-tok")
	w = httptest.NewRecorder()
	a.serve(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("progress HTTP %d: %s", w.Code, w.Body.String())
	}
	var position int
	if e := a.db.QueryRow("SELECT position FROM userdata WHERE user_id=? AND item=?", uid, "query-item").Scan(&position); e != nil || position != 123 {
		t.Fatalf("userdata position = %d, err=%v, want 123", position, e)
	}

	// /Stopped 只带查询参数、报文为空 → 204。
	stop := httptest.NewRequest("POST", "/Sessions/Playing/Stopped?ItemId=query-item", nil)
	stop.Header.Set("X-Emby-Token", "query-progress-tok")
	w = httptest.NewRecorder()
	a.serve(w, stop)
	if w.Code != http.StatusNoContent {
		t.Fatalf("stopped HTTP %d: %s", w.Code, w.Body.String())
	}
}

func TestStreamMediaSourceIdFallback(t *testing.T) {
	t.Setenv("NANSHARE_URL", "")
	t.Setenv("PUBLIC_PLAYBACK_URL", "")
	a := newTestSQLiteApp(t)
	insertSQLiteItem(t, a, "src-item", "http://origin/movie.mkv")
	for _, ms := range []string{"nonexistent", "src-item"} {
		w := httptest.NewRecorder()
		a.stream(w, httptest.NewRequest("GET", "/Videos/src-item/stream.mkv?MediaSourceId="+ms, nil), User{API: true}, "src-item")
		if w.Code != http.StatusFound || w.Header().Get("Location") != "http://origin/movie.mkv" {
			t.Fatalf("MediaSourceId=%s: HTTP %d Location %q", ms, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestStreamMediaSourceIdVersionSwitch(t *testing.T) {
	t.Setenv("NANSHARE_URL", "")
	t.Setenv("PUBLIC_PLAYBACK_URL", "")
	a := testApp(t)
	for _, l := range []string{"a"} {
		if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES(?,?,?,'movies')", l, l, "/media/"+l); e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []struct{ id, path, url string }{
		{"a1", "/media/a/Film{tmdb-123}/1080.strm", "http://origin/1080.mkv"},
		{"a2", "/media/a/Film{tmdb-123}/2160.strm", "http://origin/2160.mkv"},
	} {
		if _, e := a.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,url,year,seen) VALUES(?,?,'a','Film','Movie',?,?,2020,'g')", v.id, "a", v.path, v.url); e != nil {
			t.Fatal(e)
		}
	}
	var uid string
	if e := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); e != nil {
		t.Fatal(e)
	}
	x1, e := a.item("a1")
	if e != nil {
		t.Fatal(e)
	}
	if len(a.versions(x1)) != 2 {
		t.Fatalf("expected 2 merged versions, got %d", len(a.versions(x1)))
	}
	u := User{ID: uid, Name: "Alice", Device: "version-switch", Max: 5}
	w := httptest.NewRecorder()
	a.stream(w, httptest.NewRequest("GET", "/Videos/a1/stream.mkv?MediaSourceId=a2", nil), u, "a1")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "http://origin/2160.mkv" {
		t.Fatalf("HTTP %d Location %q, want 302 to a2 URL", w.Code, w.Header().Get("Location"))
	}
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	found := false
	for _, v := range a.activity.entries {
		if v.Category == "playback" && v.ItemID == "a2" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("playback activity must reference switched version a2, entries=%d", len(a.activity.entries))
	}
}

func TestPlaybackInfoNoCompatibleStreams(t *testing.T) {
	a := newTestSQLiteApp(t)
	insertSQLiteItem(t, a, "empty-src", "")
	r := httptest.NewRequest("POST", "/Items/empty-src/PlaybackInfo", nil)
	w := httptest.NewRecorder()
	a.playback(w, r, User{API: true}, "empty-src")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var info struct {
		MediaSources  []map[string]any
		PlaySessionId string
		ErrorCode     string
	}
	if e := json.Unmarshal(w.Body.Bytes(), &info); e != nil {
		t.Fatal(e)
	}
	if len(info.MediaSources) != 0 || info.ErrorCode != "NoCompatibleStreams" || info.PlaySessionId == "" {
		t.Fatalf("unexpected PlaybackInfo payload: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	a.playback(w, httptest.NewRequest("POST", "/Items/missing/PlaybackInfo", nil), User{API: true}, "missing")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown item: HTTP %d", w.Code)
	}
}

func TestEmbeddedSubtitleIndexNotMisdelivered(t *testing.T) {
	tmp := t.TempDir()
	RegisterMediaRoot(tmp)
	a := newTestSQLiteApp(t)
	path := filepath.Join(tmp, "E01.strm")
	if e := os.WriteFile(path, []byte("http://origin/E01.mp4\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	srt := "1\n00:00:01,000 --> 00:00:02,000\n你好字幕\n"
	if e := os.WriteFile(filepath.Join(tmp, "E01.chs.srt"), []byte(srt), 0o644); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('l','test',?,'movies')", tmp); e != nil {
		t.Fatal(e)
	}
	insertSQLiteItemRow(t, a, "sub-item", path, "http://origin/E01.mp4")
	newSQLiteToken(t, a, "sub-tok", "dev-sub")
	get := func(index int) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/emby/Videos/sub-item/sub-item/Subtitles/"+strconv.Itoa(index)+"/Stream.srt?api_key=sub-tok", nil)
		a.serve(w, r)
		return w
	}

	w := get(0)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "你好字幕") {
		t.Fatalf("no-nfo index 0: HTTP %d body %q", w.Code, w.Body.String())
	}

	// 内嵌流计数依赖 nfo 的 streamdetails：fileinfo 必须位于根元素之内，
	// 且空元素 <video/> 不会生成条目，因此每条流都要带子元素。
	nfo := "<movie><fileinfo><streamdetails><video><codec>h264</codec></video><audio><codec>aac</codec></audio><subtitle><codec>srt</codec></subtitle></streamdetails></fileinfo></movie>"
	if e := os.WriteFile(filepath.Join(tmp, "E01.nfo"), []byte(nfo), 0o644); e != nil {
		t.Fatal(e)
	}
	for _, idx := range []int{0, 1, 2} {
		if w := get(idx); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "字幕索引超出范围") {
			t.Fatalf("embedded index %d must be rejected, got HTTP %d: %s", idx, w.Code, w.Body.String())
		}
	}
	if w := get(3); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "你好字幕") {
		t.Fatalf("external index 3: HTTP %d body %q", w.Code, w.Body.String())
	}
	if w := get(9); w.Code != http.StatusNotFound {
		t.Fatalf("index 9: HTTP %d body %s", w.Code, w.Body.String())
	}
}

func TestSubtitleContentTypeNoCharset(t *testing.T) {
	for _, tc := range []struct{ ext, want string }{
		{"srt", "application/x-subrip"},
		{"ass", "text/x-ssa"},
		{"ssa", "text/x-ssa"},
		{"vtt", "text/vtt; charset=utf-8"},
	} {
		if got := subtitleContentType(tc.ext); got != tc.want {
			t.Fatalf("subtitleContentType(%q) = %q, want %q", tc.ext, got, tc.want)
		}
	}
}
