package embyserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type unreadMediaBody struct {
	read, closed bool
}

func (b *unreadMediaBody) Read([]byte) (int, error) {
	b.read = true
	return 0, io.EOF
}

func (b *unreadMediaBody) Close() error {
	b.closed = true
	return nil
}

func TestXiaoyaDirectMediaDoesNotReadBody(t *testing.T) {
	original := xiaoyaClient.Transport
	t.Cleanup(func() { xiaoyaClient.Transport = original })
	for _, mediaType := range []string{"video/mp4", "audio/mpeg"} {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(mediaType+"/"+method, func(t *testing.T) {
				body := &unreadMediaBody{}
				xiaoyaClient.Transport = staticTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != "GET" || r.UserAgent() != "test-player" || r.Header.Get("X-Alist-OriUA") != "test-player" {
						t.Error("解析请求应保留播放器 UA 并使用 GET")
					}
					if r.Header.Get("Authorization") != "" || r.Header.Get("X-Emby-Token") != "" {
						t.Error("不应转发 Emby 凭据")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {mediaType}}, Body: body, Request: r}, nil
				})
				r := httptest.NewRequest(method, "/Videos/movie/stream", nil)
				r.Header.Set("User-Agent", "test-player")
				r.Header.Set("Authorization", "Bearer viewer")
				r.Header.Set("X-Emby-Token", "viewer")
				got, err := xiaoyaLinkWithToken(r, "http://xiaoya.host:5678/d/movie?sign=VALID:0", "http://192.168.1.10:5678", "")
				if err != nil || got != "http://192.168.1.10:5678/d/movie?sign=VALID%3A0" || body.read || !body.closed {
					t.Fatalf("应关闭媒体响应并返回原播放地址: url=%q err=%v read=%v closed=%v", got, err, body.read, body.closed)
				}
			})
		}
	}
}

func TestDirectSTRMMediaClientRangeAndHead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "movie.mp4", time.Time{}, strings.NewReader("0123456789ABCDEF"))
	}))
	defer upstream.Close()
	a := &App{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.resolveSTRM(w, r, Item{URL: "http://xiaoya.host:5678/d/movie.mp4"}, upstream.URL)
	}))
	defer server.Close()
	for _, method := range []string{"GET", "HEAD"} {
		r, _ := http.NewRequest(method, server.URL+"/Videos/movie/stream", nil)
		if method == "GET" {
			r.Header.Set("Range", "bytes=4-9")
		}
		res, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if method == "GET" && (res.StatusCode != 206 || string(body) != "456789" || res.Header.Get("Content-Range") != "bytes 4-9/16") {
			t.Fatalf("重定向后 Range 读取失败: status=%d body=%q", res.StatusCode, body)
		}
		if method == "HEAD" && (res.StatusCode != 200 || len(body) != 0 || res.Header.Get("Content-Length") != "16") {
			t.Fatalf("重定向后 HEAD 失败: status=%d body=%q", res.StatusCode, body)
		}
	}
}

func TestXiaoyaRedirectFailures(t *testing.T) {
	for _, format := range []string{"redirect", "json", "text"} {
		for _, chain := range []string{"self", "cycle", "limit"} {
			t.Run(format+"/"+chain, func(t *testing.T) {
				calls := 0
				var base string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/fs/get" {
						w.WriteHeader(403)
						return
					}
					calls++
					next := "/d/movie"
					if chain == "cycle" && r.URL.Path == next {
						next = "/p/movie"
					} else if chain == "limit" {
						next = fmt.Sprintf("/p/%d", calls)
					}
					switch format {
					case "redirect":
						w.Header().Set("Location", next)
						w.WriteHeader(http.StatusFound)
					case "json":
						_ = json.NewEncoder(w).Encode(map[string]string{"url": base + next})
					case "text":
						fmt.Fprint(w, base+next)
					}
				}))
				defer srv.Close()
				base = srv.URL
				a := &App{}
				wantCalls, wantError := 1, "loop"
				if chain == "cycle" {
					wantCalls = 2
				} else if chain == "limit" {
					wantCalls, wantError = 10, "limit"
				}
				// 再次请求仍应访问上游，失败结果不能进入直链缓存。
				for attempt := 1; attempt <= 2; attempt++ {
					w := httptest.NewRecorder()
					a.resolveSTRM(w, httptest.NewRequest("GET", "/Videos/movie/stream", nil), Item{URL: "http://xiaoya.host:5678/d/movie"}, base)
					if w.Code != 502 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), wantError) {
						t.Fatalf("应拒绝无终点链路: status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
					}
					if calls != attempt*wantCalls {
						t.Fatalf("请求次数=%d，期望=%d", calls, attempt*wantCalls)
					}
				}
			})
		}
	}
}

func TestXiaoyaInternalRedirectLoopUsesAList(t *testing.T) {
	for _, lookup := range []string{"http", "callback"} {
		t.Run(lookup, func(t *testing.T) {
			apiCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/fs/get" {
					apiCalls++
					if r.Header.Get("Authorization") != "alist-test-token" {
						t.Error("AList 请求缺少已配置的令牌")
					}
					if r.UserAgent() != "test-player" || r.Header.Get("X-Alist-OriUA") != "test-player" {
						t.Error("AList 请求缺少播放器 UA")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]string{"raw_url": "https://cdn.example/resolved.mp4"}})
					return
				}
				// 实机响应保留路径，但返回不可达的容器内部端口。
				w.Header().Set("Location", "http://172.17.0.2:45678"+r.URL.EscapedPath())
				w.WriteHeader(302)
			}))
			defer srv.Close()
			var get alistFileLookup
			if lookup == "callback" {
				get = func(_ context.Context, path, ua string) (string, string, error) {
					apiCalls++
					if path != "/每日更新/测试.mp4" || ua != "test-player" {
						t.Errorf("刷新参数错误: path=%q ua=%q", path, ua)
					}
					return "https://cdn.example/resolved.mp4", "", nil
				}
			}
			r := httptest.NewRequest("GET", "/Videos/movie/stream", nil)
			r.Header.Set("User-Agent", "test-player")
			got, err := xiaoyaLinkWithRefresh(r, "http://xiaoya.host:5678/d/每日更新/测试.mp4", srv.URL, "alist-test-token", get)
			if err != nil || got != "https://cdn.example/resolved.mp4" || apiCalls != 1 {
				t.Fatalf("循环后应通过已认证 AList 获取直链: url=%q calls=%d err=%v", got, apiCalls, err)
			}
		})
	}
}

func TestXiaoyaLastAllowedRequestCanResolve(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		next := fmt.Sprintf("/p/%d", calls)
		if calls == 10 {
			next = "https://cdn.example/movie.mp4"
		}
		w.Header().Set("Location", next)
		w.WriteHeader(302)
	}))
	defer srv.Close()
	got, err := xiaoyaLinkWithToken(httptest.NewRequest("GET", "/", nil), "http://xiaoya.host:5678/d/movie", srv.URL, "")
	if err != nil || got != "https://cdn.example/movie.mp4" || calls != 10 {
		t.Fatalf("最后一次请求应允许成功: url=%q calls=%d err=%v", got, calls, err)
	}
}

func TestXiaoyaRefreshRejectsLoopAndPreservesNewSign(t *testing.T) {
	for _, lookup := range []string{"http", "callback"} {
		for _, freshSign := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fresh=%v", lookup, freshSign), func(t *testing.T) {
				var base string
				sign := ""
				if freshSign {
					sign = "NEW:0"
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.URL.Path == "/api/fs/get":
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]string{"raw_url": base + "/p/loop", "sign": sign}})
					case r.URL.Path == "/p/loop":
						w.Header().Set("Location", "/p/loop")
						w.WriteHeader(302)
					case r.URL.Query().Get("sign") == "NEW:0":
						w.Header().Set("Location", "https://cdn.example/fresh.mp4")
						w.WriteHeader(302)
					default:
						w.WriteHeader(403)
					}
				}))
				defer srv.Close()
				base = srv.URL
				var get alistFileLookup
				if lookup == "callback" {
					get = func(context.Context, string, string) (string, string, error) {
						return base + "/p/loop", sign, nil
					}
				}
				got, err := xiaoyaLinkWithRefresh(httptest.NewRequest("GET", "/", nil), "http://xiaoya.host:5678/d/movie?sign=OLD:0", base, "", get)
				if freshSign {
					if err != nil || got != "https://cdn.example/fresh.mp4" {
						t.Fatalf("raw_url 失效后应使用新签名: url=%q err=%v", got, err)
					}
				} else if err == nil || got != "" || !strings.Contains(err.Error(), "loop") {
					t.Fatalf("刷新不能掩盖循环错误: url=%q err=%v", got, err)
				}
			})
		}
	}
}
