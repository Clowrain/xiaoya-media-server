package embyserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type localRoundTripper struct {
	handler http.Handler
}

func (l *localRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	l.handler.ServeHTTP(rec, req)
	res := rec.Result()
	res.Request = req
	return res, nil
}

func TestValidateResolvedLink(t *testing.T) {
	nasTarget, _ := url.Parse("http://192.168.110.172:5678")

	tests := []struct {
		name      string
		link      string
		target    *url.URL
		want      string
		expectErr bool
	}{
		{
			name:      "public cloud cdn",
			link:      "https://cn-hangzhou-data.alicloudccp.com/video.mp4?sig=abc",
			target:    nasTarget,
			want:      "https://cn-hangzhou-data.alicloudccp.com/video.mp4?sig=abc",
			expectErr: false,
		},
		{
			name:      "nas lan direct address matches target",
			link:      "http://192.168.110.172:5678/p/%E7%94%B5%E8%A7%86%E5%89%A7/%E4%BA%BA%E6%B0%91%E7%9A%84%E5%90%8D%E4%B9%89/01.mp4",
			target:    nasTarget,
			want:      "http://192.168.110.172:5678/p/%E7%94%B5%E8%A7%86%E5%89%A7/%E4%BA%BA%E6%B0%91%E7%9A%84%E5%90%8D%E4%B9%89/01.mp4",
			expectErr: false,
		},
		{
			name:      "relative redirect resolved against target",
			link:      "/p/%E7%94%B5%E8%A7%86%E5%89%A7/%E4%BA%BA%E6%B0%91%E7%9A%84%E5%90%8D%E4%B9%89/01.mp4",
			target:    nasTarget,
			want:      "http://192.168.110.172:5678/p/%E7%94%B5%E8%A7%86%E5%89%A7/%E4%BA%BA%E6%B0%91%E7%9A%84%E5%90%8D%E4%B9%89/01.mp4",
			expectErr: false,
		},
		{
			name:      "docker bridge internal rewritten to target host",
			link:      "http://172.17.0.2:80/d/%E7%94%B5%E8%A7%86%E5%89%A7/%E4%BA%BA%E6%B0%91%E7%9A%84%E5%90%8D%E4%B9%89/01.mp4",
			target:    nasTarget,
			want:      "http://192.168.110.172:5678/d/%E7%94%B5%E8%A7%86%E5%89%A7/%E4%BA%BA%E6%B0%91%E7%9A%84%E5%90%8D%E4%B9%89/01.mp4",
			expectErr: false,
		},
		{
			name:      "container loopback rewritten to target host",
			link:      "http://127.0.0.1:5678/d/test.mp4",
			target:    nasTarget,
			want:      "http://192.168.110.172:5678/d/test.mp4",
			expectErr: false,
		},
		{
			name:      "xiaoya host alias rewritten to target host",
			link:      "http://xiaoya.host:5678/d/test.mp4",
			target:    nasTarget,
			want:      "http://192.168.110.172:5678/d/test.mp4",
			expectErr: false,
		},
		{
			name:      "reject cloud metadata ip",
			link:      "http://169.254.169.254/latest/meta-data/",
			target:    nasTarget,
			expectErr: true,
		},
		{
			name:      "reject cloud metadata hostname",
			link:      "http://metadata.google.internal/computeMetadata/v1/",
			target:    nasTarget,
			expectErr: true,
		},
		{
			name:      "reject unspecified 0.0.0.0",
			link:      "http://0.0.0.0/stream",
			target:    nasTarget,
			expectErr: true,
		},
		{
			name:      "reject non http scheme",
			link:      "ftp://192.168.110.172:5678/test.mp4",
			target:    nasTarget,
			expectErr: true,
		},
		{
			name:      "reject userinfo",
			link:      "http://user:pass@192.168.110.172:5678/test.mp4",
			target:    nasTarget,
			expectErr: true,
		},
		{
			name:      "reject crlf injection",
			link:      "http://192.168.110.172:5678/test.mp4\r\nInjected: Header",
			target:    nasTarget,
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateResolvedLink(tc.link, tc.target)
			if (err != nil) != tc.expectErr {
				t.Fatalf("expectErr=%v, got err=%v", tc.expectErr, err)
			}
			if !tc.expectErr && got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestXiaoyaResolver(t *testing.T) {
	origTransport := xiaoyaClient.Transport
	defer func() {
		xiaoyaClient.Transport = origTransport
	}()

	for _, tc := range []struct {
		name, body, location string
		status               int
		ok                   bool
	}{
		{"redirect", "", "https://cdn.example/video?sig=abc", 302, true},
		{"text", "https://cdn.example/video", "", 200, true},
		{"json", `{"url":"https://cdn.example/video"}`, "", 200, true},
		{"metadata_ssrf", "", "http://169.254.169.254/meta-data", 302, false},
		{"error", `{"code":500,"message":"error"}`, "", 200, false},
		{"html", "<html>failure</html>", "", 200, false},
		{"oversize", strings.Repeat("a", 65537), "", 200, false},
		{"upstream", "", "", 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/d/中文 a.mp4" || r.URL.Query().Get("sign") != "" || r.URL.Query().Get("other") != "kept" {
					t.Errorf("unexpected resolver request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("X-Emby-Token") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Alist-OriUA") != "test-player" {
					t.Error("incorrect headers")
				}
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			xiaoyaClient.Transport = &localRoundTripper{handler: handler}

			r := httptest.NewRequest("HEAD", "http://emby/Videos/abcdef/stream", nil)
			r.Header.Set("User-Agent", "test-player")
			r.Header.Set("X-Emby-Token", "secret")
			_, err := xiaoyaLink(r, "http://xiaoya.host:5678/d/中文%20a.mp4?sign=SIGN_STR&other=kept", "http://192.168.110.172:5678")
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v error=%v", tc.ok, err)
			}
		})
	}
}

func TestXiaoyaMultiHopRedirect(t *testing.T) {
	origTransport := xiaoyaClient.Transport
	defer func() {
		xiaoyaClient.Transport = origTransport
	}()

	t.Run("multi-hop 302 resolves to public CDN", func(t *testing.T) {
		step := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			step++
			if step == 1 {
				// Hop 1: Xiaoya internal redirect to /p/
				if r.URL.Path != "/d/test.mp4" {
					t.Errorf("step 1 unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Location", "http://192.168.110.172:5678/p/intermediate.mp4")
				w.WriteHeader(http.StatusFound)
				return
			}
			if step == 2 {
				// Hop 2: /p/ redirects to final Alibaba Cloud CDN
				if r.URL.Path != "/p/intermediate.mp4" {
					t.Errorf("step 2 unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Location", "https://cn-hangzhou-data.alicloudccp.com/real_video.mp4?token=xyz")
				w.WriteHeader(http.StatusFound)
				return
			}
			t.Errorf("unexpected step %d", step)
		})
		xiaoyaClient.Transport = &localRoundTripper{handler: handler}

		r := httptest.NewRequest("GET", "http://emby/Videos/123/stream.mp4", nil)
		r.Header.Set("User-Agent", "SenPlayer/6.1.8")
		got, err := xiaoyaLink(r, "http://xiaoya.host:5678/d/test.mp4", "http://192.168.110.172:5678")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := "https://cn-hangzhou-data.alicloudccp.com/real_video.mp4?token=xyz"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if step != 2 {
			t.Fatalf("expected 2 hops, got %d", step)
		}
	})

	t.Run("docker bridge intermediate rewritten and followed", func(t *testing.T) {
		step := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			step++
			if step == 1 {
				// Hop 1: returns docker bridge 172.17.0.2:80
				w.Header().Set("Location", "http://172.17.0.2:80/p/movie.mp4")
				w.WriteHeader(http.StatusFound)
				return
			}
			if step == 2 {
				// Hop 2: rewrote to target 192.168.110.172:5678 and redirected to 115 CDN
				if r.URL.Path != "/p/movie.mp4" {
					t.Errorf("step 2 unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Location", "https://cdn.115.com/final_115.mp4")
				w.WriteHeader(http.StatusFound)
				return
			}
			t.Errorf("unexpected step %d", step)
		})
		xiaoyaClient.Transport = &localRoundTripper{handler: handler}

		r := httptest.NewRequest("GET", "http://emby/Videos/123/stream.mp4", nil)
		r.Header.Set("User-Agent", "SenPlayer/6.1.8")
		got, err := xiaoyaLink(r, "http://xiaoya.host:5678/d/movie.mp4", "http://192.168.110.172:5678")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := "https://cdn.115.com/final_115.mp4"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if step != 2 {
			t.Fatalf("expected 2 hops, got %d", step)
		}
	})

	t.Run("local streaming 200 media terminates and returns local URL", func(t *testing.T) {
		step := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			step++
			if step == 1 {
				// Hop 1: redirects to /p/local.mp4
				w.Header().Set("Location", "/p/local.mp4")
				w.WriteHeader(http.StatusFound)
				return
			}
			if step == 2 {
				// Hop 2: /p/local.mp4 serves video/mp4 stream directly
				w.Header().Set("Content-Type", "video/mp4")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("fake video payload"))
				return
			}
			t.Errorf("unexpected step %d", step)
		})
		xiaoyaClient.Transport = &localRoundTripper{handler: handler}

		r := httptest.NewRequest("GET", "http://emby/Videos/123/stream.mp4", nil)
		r.Header.Set("User-Agent", "SenPlayer/6.1.8")
		got, err := xiaoyaLink(r, "http://xiaoya.host:5678/d/local.mp4", "http://192.168.110.172:5678")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := "http://192.168.110.172:5678/p/local.mp4"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if step != 2 {
			t.Fatalf("expected 2 hops, got %d", step)
		}
	})
}

func TestPlaybackResolverDoesNotCallAListResourceAPI(t *testing.T) {
	apiCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/fs/get" {
			apiCalls++
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Location", r.URL.Path)
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	a := &App{}
	_, err := a.resolvePlaybackLink(httptest.NewRequest("GET", "/Videos/episode/stream", nil), "http://xiaoya.host:5678/d/episode.mp4", srv.URL)
	if err == nil {
		t.Fatal("expected direct resolver loop to be returned")
	}
	if apiCalls != 0 {
		t.Fatalf("playback must not call AList /api/fs/get, got %d calls", apiCalls)
	}
}
