package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractAndParseZheYueLiang(t *testing.T) {
	samplePath := "/media/strm/每日更新/电视剧/国产剧/折月亮 (2026)/{tmdb-270431}/Season 1/E01.strm"
	if got := ExtractTMDBID(samplePath); got != "270431" {
		t.Fatalf("ExtractTMDBID = %q, want 270431", got)
	}
	if !IsPureMetadataTagDir("{tmdb-270431}") {
		t.Fatal("expected {tmdb-270431} to be recognized as pure metadata tag dir")
	}
	if IsPureMetadataTagDir("折月亮 (2026) {tmdb-270431}") {
		t.Fatal("mixed title+tag folder must not be classified as pure tag dir")
	}
	title, year := ParseTitleAndYear("折月亮 (2026)")
	if title != "折月亮" || year != 2026 {
		t.Fatalf("ParseTitleAndYear = (%q, %d), want (折月亮, 2026)", title, year)
	}
	for _, tc := range []struct {
		stem       string
		seasonHint int
		wantS      int
		wantE      int
	}{
		{"E01", 1, 1, 1},
		{"E27", 1, 1, 27},
		{"S02E15", 0, 2, 15},
		{"第08集", 1, 1, 8},
		{"05", 1, 1, 5},
	} {
		s, e, ok := ParseSeasonEpisode(tc.stem, tc.seasonHint)
		if !ok || s != tc.wantS || e != tc.wantE {
			t.Fatalf("ParseSeasonEpisode(%q, %d) = (%d, %d, %v), want (%d, %d, true)", tc.stem, tc.seasonHint, s, e, ok, tc.wantS, tc.wantE)
		}
	}
}

func TestClientGetTVAndSeason(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/3/tv/270431", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":               270431,
			"name":             "折月亮",
			"original_name":    "折月亮",
			"overview":         "都市爱情剧折月亮剧情简介",
			"first_air_date":   "2026-06-01",
			"vote_average":     8.2,
			"poster_path":      "/zheyueliang_poster.jpg",
			"backdrop_path":    "/zheyueliang_bg.jpg",
			"episode_run_time": []int{45},
			"genres":           []map[string]string{{"name": "剧情"}},
			"networks":         []map[string]string{{"name": "优酷"}},
			"credits": map[string]any{
				"cast": []map[string]string{
					{"name": "演员甲", "character": "主角", "profile_path": "/actor1.jpg"},
				},
			},
		})
	})
	mux.HandleFunc("/3/tv/270431/season/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            1001,
			"name":          "第 1 季",
			"season_number": 1,
			"poster_path":   "/s1.jpg",
			"episodes": []map[string]any{
				{
					"id":             2001,
					"season_number":  1,
					"episode_number": 1,
					"name":           "第 1 集",
					"overview":       "首集简介",
					"air_date":       "2026-06-01",
					"runtime":        45,
					"still_path":     "/ep1.jpg",
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := NewClient(Options{
		APIKey:      "test-key",
		Language:    "zh-CN",
		BaseURL:     srv.URL + "/3",
		ImageURL:    "https://image.tmdb.org/t/p",
		MinInterval: time.Millisecond,
	})
	ctx := context.Background()
	tv, err := cli.GetTV(ctx, "270431")
	if err != nil {
		t.Fatalf("GetTV failed: %v", err)
	}
	if tv.Title != "折月亮" || tv.Year != 2026 || tv.PosterURL != "https://image.tmdb.org/t/p/w500/zheyueliang_poster.jpg" {
		t.Fatalf("unexpected TV metadata: %+v", tv)
	}
	season, eps, err := cli.GetTVSeason(ctx, "270431", 1)
	if err != nil {
		t.Fatalf("GetTVSeason failed: %v", err)
	}
	if season.Title != "第 1 季" || eps[1] == nil || eps[1].Overview != "首集简介" {
		t.Fatalf("unexpected season/episode metadata: %+v %+v", season, eps[1])
	}
}

func TestClientRetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"status_code":25,"status_message":"Your request count (#) is over the allowed limit of (40)."}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           999,
			"title":        "测试成功电影",
			"overview":     "测试成功简介",
			"release_date": "2026-01-01",
		})
	}))
	defer srv.Close()

	cli := NewClient(Options{
		APIKey:      "test-key",
		BaseURL:     srv.URL,
		MinInterval: time.Millisecond,
	})
	movie, err := cli.GetMovie(context.Background(), "999")
	if err != nil {
		t.Fatalf("expected 429 retry to succeed, got error: %v", err)
	}
	if movie.Title != "测试成功电影" {
		t.Fatalf("unexpected title: %s", movie.Title)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestIsRateLimitedOrTimeout(t *testing.T) {
	if !IsRateLimitedOrTimeout(context.DeadlineExceeded) {
		t.Error("expected context.DeadlineExceeded to be detected")
	}
}
