package embyserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestScrapeStateTrackingAndProcessPending(t *testing.T) {
	tmpDir := t.TempDir()
	RegisterMediaRoot(tmpDir)

	dbPath := filepath.Join(tmpDir, "scrape_test.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	app := &App{db: &Database{sqlDB}}
	_, err = sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS settings(k TEXT PRIMARY KEY, v TEXT);
		CREATE TABLE IF NOT EXISTS items (
			id text primary key,
			lib text not null,
			parent text not null,
			name text not null,
			kind text not null,
			path text not null unique,
			url text not null,
			overview text not null default '',
			poster text not null default '',
			year bigint not null default 0,
			season bigint not null default 0,
			episode bigint not null default 0,
			mtime bigint not null default 0,
			size bigint not null default 0,
			seen text not null default '',
			rating real not null default 0
		);
		CREATE TABLE IF NOT EXISTS item_metadata(item TEXT PRIMARY KEY, data TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS item_people(item TEXT, person TEXT, name TEXT, role TEXT, type TEXT, thumb TEXT, PRIMARY KEY(item, person, type));
		CREATE TABLE IF NOT EXISTS media_display_names(item TEXT PRIMARY KEY, name TEXT);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}
	if err := app.tmdbSchema(); err != nil {
		t.Fatalf("tmdbSchema: %v", err)
	}

	searchCalled := 0
	tvDetailCalled := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/tv":
			searchCalled++
			q := r.URL.Query().Get("query")
			if q == "未收录剧集" {
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": 8888, "name": "连载中国产剧", "first_air_date": "2026-05-01"},
				},
			})
		case "/3/tv/8888":
			tvDetailCalled++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             8888,
				"name":           "连载中国产剧",
				"first_air_date": "2026-05-01",
				"status":         "Returning Series",
				"in_production":  true,
				"overview":       "这是连载中的剧情简介",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 配置 TMDB Settings
	settingsJSON, _ := json.Marshal(map[string]any{
		"enabled":   true,
		"api_key":   "fake-key",
		"base_url":  srv.URL + "/3",
		"image_url": "https://image.tmdb.org/t/p",
	})
	_, err = sqlDB.Exec("INSERT INTO settings(k,v) VALUES('tmdb_settings', ?)", string(settingsJSON))
	if err != nil {
		t.Fatalf("insert settings: %v", err)
	}

	// 1. 测试未收录剧集 -> 应该被标记为 PENDING_TMDB
	unlistedItem := Item{
		ID:   "item-unlisted",
		Lib:  "lib1",
		Name: "未收录剧集 (2026)",
		Kind: "Series",
		Path: filepath.Join(tmpDir, "未收录剧集 (2026)"),
	}
	_, _ = sqlDB.Exec("INSERT INTO items(id,lib,parent,name,kind,path,url,year) VALUES(?,?,?,?,?,?,?,?)",
		unlistedItem.ID, unlistedItem.Lib, "lib1", unlistedItem.Name, unlistedItem.Kind, unlistedItem.Path, "", 2026)
	_ = app.resolveItemSidecar(unlistedItem, false)

	var unlistedState string
	var nextRetry int64
	err = sqlDB.QueryRow("SELECT state, next_retry_at FROM tmdb_scrape_tasks WHERE item_id=?", unlistedItem.ID).Scan(&unlistedState, &nextRetry)
	if err != nil {
		t.Fatalf("query unlisted task: %v", err)
	}
	if unlistedState != string(ScrapeStatePendingTMDB) {
		t.Fatalf("expected state %s, got %s", ScrapeStatePendingTMDB, unlistedState)
	}
	if nextRetry <= time.Now().Unix() {
		t.Fatalf("expected next_retry_at to be in the future, got %d", nextRetry)
	}

	// 2. 测试连载中剧集 -> 应该被标记为 UPDATING
	ongoingItem := Item{
		ID:   "item-ongoing",
		Lib:  "lib1",
		Name: "连载中国产剧 (2026)",
		Kind: "Series",
		Path: filepath.Join(tmpDir, "连载中国产剧 (2026)"),
	}
	_, _ = sqlDB.Exec("INSERT INTO items(id,lib,parent,name,kind,path,url,year) VALUES(?,?,?,?,?,?,?,?)",
		ongoingItem.ID, ongoingItem.Lib, "lib1", ongoingItem.Name, ongoingItem.Kind, ongoingItem.Path, "", 2026)
	_ = app.resolveItemSidecar(ongoingItem, false)

	var ongoingState string
	err = sqlDB.QueryRow("SELECT state FROM tmdb_scrape_tasks WHERE item_id=?", ongoingItem.ID).Scan(&ongoingState)
	if err != nil {
		t.Fatalf("query ongoing task: %v", err)
	}
	if ongoingState != string(ScrapeStateUpdating) {
		t.Fatalf("expected state %s, got %s", ScrapeStateUpdating, ongoingState)
	}

	// 3. 测试 ProcessPendingScrapes: 将 next_retry_at 改为过去，触发重试
	_, err = sqlDB.Exec("UPDATE tmdb_scrape_tasks SET next_retry_at=? WHERE item_id=?", time.Now().Unix()-10, unlistedItem.ID)
	if err != nil {
		t.Fatalf("update next_retry_at: %v", err)
	}

	processed, err := app.ProcessPendingScrapes(context.Background(), 10)
	if err != nil {
		t.Fatalf("ProcessPendingScrapes: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 processed item, got %d", processed)
	}
}

func TestBatchScanSeriesCascadeAndArtifactsPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	RegisterMediaRoot(tmpDir)

	dbPath := filepath.Join(tmpDir, "cascade_test.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	app := &App{db: &Database{sqlDB}}
	_, err = sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS settings(k TEXT PRIMARY KEY, v TEXT);
		CREATE TABLE IF NOT EXISTS items (
			id text primary key,
			lib text not null,
			parent text not null,
			name text not null,
			kind text not null,
			path text not null unique,
			url text not null,
			overview text not null default '',
			poster text not null default '',
			year bigint not null default 0,
			season bigint not null default 0,
			episode bigint not null default 0,
			mtime bigint not null default 0,
			size bigint not null default 0,
			seen text not null default '',
			rating real not null default 0
		);
		CREATE TABLE IF NOT EXISTS item_metadata(item TEXT PRIMARY KEY, data TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS item_people(item TEXT, person TEXT, name TEXT, role TEXT, type TEXT, thumb TEXT, PRIMARY KEY(item, person, type));
		CREATE TABLE IF NOT EXISTS media_display_names(item TEXT PRIMARY KEY, name TEXT);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}
	if err := app.tmdbSchema(); err != nil {
		t.Fatalf("tmdbSchema: %v", err)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/tv":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": 7777, "name": "追更热播剧", "first_air_date": "2026-06-01"},
				},
			})
		case "/3/tv/7777":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             7777,
				"name":           "追更热播剧",
				"overview":       "这是整剧简介",
				"first_air_date": "2026-06-01",
				"poster_path":    "/poster.jpg",
			})
		case "/3/tv/7777/season/1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            101,
				"season_number": 1,
				"name":          "第 1 季",
				"poster_path":   "/season_poster.jpg",
				"episodes": []map[string]any{
					{
						"id":             201,
						"episode_number": 1,
						"name":           "第一集 开端",
						"overview":       "开端详细剧情简介",
						"still_path":     "/ep01_still.jpg",
					},
				},
			})
		case "/t/p/w500/poster.jpg", "/t/p/w500/season_poster.jpg", "/t/p/w500/ep01_still.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fake-jpeg-binary-data"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	settingsJSON, _ := json.Marshal(map[string]any{
		"enabled":                    true,
		"api_key":                    "fake-key",
		"base_url":                   srv.URL + "/3",
		"image_url":                  srv.URL + "/t/p",
		"save_artifacts_to_media_dir": true,
	})
	_, _ = sqlDB.Exec("INSERT INTO settings(k,v) VALUES('tmdb_settings', ?)", string(settingsJSON))

	// 创建目录与文件
	seriesDir := filepath.Join(tmpDir, "追更热播剧 (2026)")
	seasonDir := filepath.Join(seriesDir, "Season 1")
	_ = os.MkdirAll(seasonDir, 0755)
	epPath := filepath.Join(seasonDir, "01.strm")
	_ = os.WriteFile(epPath, []byte("http://media.test/01.mp4"), 0644)

	seriesID := "id-series"
	seasonID := "id-season"
	epID := "id-ep"

	// 模拟 scanner 插入的数据，seen 标记为 'gen-test'
	_, _ = sqlDB.Exec(`INSERT INTO items(id,lib,parent,name,kind,path,url,year,season,episode,seen) VALUES
		(?, 'lib1', 'lib1', '追更热播剧 (2026)', 'Series', ?, '', 2026, 0, 0, 'gen-test'),
		(?, 'lib1', ?, 'Season 1', 'Season', ?, '', 2026, 1, 1, 'gen-test'),
		(?, 'lib1', ?, '01.mp4', 'Episode', ?, 'http://media.test/01.mp4', 2026, 1, 1, 'gen-test')`,
		seriesID, seriesDir,
		seasonID, seriesID, seasonDir,
		epID, seasonID, epPath,
	)

	// 运行索引
	if err := app.indexMetadataGeneration("lib1", "gen-test"); err != nil {
		t.Fatalf("indexMetadataGeneration failed: %v", err)
	}

	// 验证 Episode 元数据已成功级联填入（标题、剧照、剧情）
	var epName, epOverview, epPoster string
	err = sqlDB.QueryRow("SELECT name, overview, poster FROM items WHERE id=?", epID).Scan(&epName, &epOverview, &epPoster)
	if err != nil {
		t.Fatalf("query episode item: %v", err)
	}
	if epName != "第一集 开端" {
		t.Errorf("epName = %q, want '第一集 开端'", epName)
	}
	if epOverview != "开端详细剧情简介" {
		t.Errorf("epOverview = %q, want '开端详细剧情简介'", epOverview)
	}
	if epPoster != srv.URL+"/t/p/w500/ep01_still.jpg" {
		t.Errorf("epPoster = %q, want %q", epPoster, srv.URL+"/t/p/w500/ep01_still.jpg")
	}

	// 稍作等待以确保 goroutine 中的 maybePersistArtifactsToDisk 完成
	time.Sleep(300 * time.Millisecond)

	// 验证整剧海报和 NFO 落盘
	if _, err := os.Stat(filepath.Join(seriesDir, "poster.jpg")); err != nil {
		t.Errorf("series poster.jpg not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(seriesDir, "tvshow.nfo")); err != nil {
		t.Errorf("series tvshow.nfo not created: %v", err)
	}

	// 验证分集剧照和分集 NFO 落盘
	if _, err := os.Stat(filepath.Join(seasonDir, "01-thumb.jpg")); err != nil {
		t.Errorf("episode 01-thumb.jpg not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(seasonDir, "01.nfo")); err != nil {
		t.Errorf("episode 01.nfo not created: %v", err)
	}
}

func TestCurrentTMDBSettingsEnvOverrides(t *testing.T) {
	t.Setenv("TMDB_ENABLED", "true")
	t.Setenv("TMDB_API_KEY", "env-key-999")
	t.Setenv("TMDB_PROXY", "http://10.0.0.1:7890")
	t.Setenv("TMDB_BASE_URL", "https://api.mirror.com/3")
	t.Setenv("TMDB_IMAGE_URL", "https://image.mirror.com/t/p")
	t.Setenv("TMDB_LANGUAGE", "zh-CN")
	t.Setenv("TMDB_RATE_LIMIT_MS", "350")
	t.Setenv("TMDB_SAVE_ARTIFACTS", "true")

	app := &App{}
	s := app.currentTMDBSettings()
	if !s.Enabled {
		t.Error("expected Enabled to be true")
	}
	if s.APIKey != "env-key-999" {
		t.Errorf("APIKey = %q, want 'env-key-999'", s.APIKey)
	}
	if s.ProxyURL != "http://10.0.0.1:7890" {
		t.Errorf("ProxyURL = %q, want 'http://10.0.0.1:7890'", s.ProxyURL)
	}
	if s.BaseURL != "https://api.mirror.com/3" {
		t.Errorf("BaseURL = %q, want 'https://api.mirror.com/3'", s.BaseURL)
	}
	if s.ImageURL != "https://image.mirror.com/t/p" {
		t.Errorf("ImageURL = %q, want 'https://image.mirror.com/t/p'", s.ImageURL)
	}
	if s.Language != "zh-CN" {
		t.Errorf("Language = %q, want 'zh-CN'", s.Language)
	}
	if s.RateLimitMs != 350 {
		t.Errorf("RateLimitMs = %d, want 350", s.RateLimitMs)
	}
	if !s.SaveArtifactsToMediaDir {
		t.Error("expected SaveArtifactsToMediaDir to be true")
	}
}
