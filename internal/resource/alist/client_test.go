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
