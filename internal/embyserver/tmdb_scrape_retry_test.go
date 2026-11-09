package embyserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
