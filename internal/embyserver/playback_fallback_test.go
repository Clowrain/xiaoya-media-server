package embyserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamUnsupportedAndMissingSourceFallbacks(t *testing.T) {
	t.Setenv("NANSHARE_URL", "")
	t.Setenv("PUBLIC_PLAYBACK_URL", "")
	app := newTestSQLiteApp(t)
	insertSQLiteItem(t, app, "ftp-item", "ftp://origin.example/movie.mkv")
	insertSQLiteItemRow(t, app, "empty-item", "/media/test/empty.strm", "")

	w := httptest.NewRecorder()
	app.stream(w, httptest.NewRequest(http.MethodPost, "/Videos/ftp-item/stream", nil), User{API: true}, "ftp-item")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method status = %d, want 405", w.Code)
	}

	w = httptest.NewRecorder()
	app.stream(w, httptest.NewRequest(http.MethodGet, "/Videos/empty-item/stream", nil), User{API: true}, "empty-item")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "无可播放源") {
		t.Fatalf("missing source response = %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	app.stream(w, httptest.NewRequest(http.MethodGet, "/Videos/ftp-item/stream", nil), User{API: true}, "ftp-item")
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "仅支持 HTTP/HTTPS STRM") {
		t.Fatalf("unsupported URL response = %d %s", w.Code, w.Body.String())
	}
}

func TestStreamHeadKeepsDirectRedirectBehavior(t *testing.T) {
	t.Setenv("NANSHARE_URL", "")
	t.Setenv("PUBLIC_PLAYBACK_URL", "")
	app := newTestSQLiteApp(t)
	insertSQLiteItem(t, app, "head-item", "https://origin.example/movie.mkv")
	w := httptest.NewRecorder()
	app.stream(w, httptest.NewRequest(http.MethodHead, "/Videos/head-item/stream.mkv", nil), User{API: true}, "head-item")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://origin.example/movie.mkv" || w.Body.Len() != 0 {
		t.Fatalf("HEAD direct redirect = status %d location %q body %q", w.Code, w.Header().Get("Location"), w.Body.String())
	}
}

func TestStreamNanShareNonRedirectReturnsBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("video-body-must-not-relay"))
	}))
	defer upstream.Close()
	t.Setenv("NANSHARE_URL", upstream.URL)
	t.Setenv("PUBLIC_PLAYBACK_URL", "")

	app := newSQLitePlaybackApp(t)
	insertSQLiteItem(t, app, "nanshare-item", "https://origin.example/movie.mkv")
	user := User{ID: "test-user", Name: "admin", Device: "device-1", Max: 5}
	w := httptest.NewRecorder()
	app.stream(w, httptest.NewRequest(http.MethodGet, "/Videos/nanshare-item/stream.mkv", nil), user, "nanshare-item")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "NanShare 直链解析失败") {
		t.Fatalf("non-redirect NanShare fallback = status %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "video-body-must-not-relay") {
		t.Fatalf("upstream video body must not be relayed: %s", w.Body.String())
	}
}
