package embyserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactPlaybackURLRemovesCredentialsQueryAndFragment(t *testing.T) {
	got := redactPlaybackURL("https://user:password@cdn.example/movie.mkv?sign=secret&api_key=token#fragment")
	if got != "https://cdn.example/movie.mkv" {
		t.Fatalf("redacted URL = %q", got)
	}
	for _, secret := range []string{"password", "secret", "token", "fragment"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted URL contains secret %q: %s", secret, got)
		}
	}
}

func TestRedactPlaybackURLHandlesLocalGatewayPath(t *testing.T) {
	got := redactPlaybackURL("/emby/Videos/item/stream.mp4?Static=true&api_key=token")
	if got != "/emby/Videos/item/stream.mp4" {
		t.Fatalf("redacted relative URL = %q", got)
	}
}

func TestRedactPlaybackURLDoesNotEchoMalformedInput(t *testing.T) {
	if got := redactPlaybackURL("https://bad%zz.example/path?secret=1"); got != "[invalid playback URL]" {
		t.Fatalf("malformed URL redaction = %q", got)
	}
}

type fixedPlaybackResolverError struct{ err error }

func (r fixedPlaybackResolverError) ResolverEndpoint(string) string { return "" }
func (r fixedPlaybackResolverError) Resolve(*http.Request, Item, string) (playbackLinkResolution, error) {
	return playbackLinkResolution{}, r.err
}

func TestSTRMFailureResponseRedactsSignedURLs(t *testing.T) {
	app := &App{playbackURLResolver: fixedPlaybackResolverError{err: errors.New("upstream rejected https://cdn.example/movie.mkv?sign=cdn-secret")}}
	w := httptest.NewRecorder()
	app.resolveSTRM(w, httptest.NewRequest("GET", "/Videos/movie/stream", nil), Item{
		ID:  "movie",
		URL: "https://xiaoya.example/d/movie.mkv?sign=source-secret",
	}, "http://resolver.example")

	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "STRM 直链解析失败") {
		t.Fatalf("unexpected resolver failure response: status=%d body=%s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"cdn-secret", "source-secret"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("resolver failure response exposed signed URL value %q: %s", secret, w.Body.String())
		}
	}
}

func TestSafeProxyErrorRedactsLooseCredentialParameters(t *testing.T) {
	got := safeProxyError(errors.New("resolver failed with sign=source-secret&api_key=token-secret"))
	for _, secret := range []string{"source-secret", "token-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("safe proxy error exposed %q: %s", secret, got)
		}
	}
}
