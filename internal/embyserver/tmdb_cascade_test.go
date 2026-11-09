package embyserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCascadeSeriesToEpisodesScraping(t *testing.T) {
	tmpDir := t.TempDir()
	RegisterMediaRoot(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	app := &App{db: &Database{sqlDB}}

	// 初始化所需的表结构
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
		CREATE TABLE IF NOT EXISTS tmdb_metadata_cache(cache_key TEXT PRIMARY KEY, tmdb_id TEXT, kind TEXT, data TEXT, updated BIGINT);
		CREATE TABLE IF NOT EXISTS tmdb_item_matches(item TEXT PRIMARY KEY, tmdb_id TEXT, media_type TEXT, updated BIGINT);
		CREATE TABLE IF NOT EXISTS media_display_names(item TEXT PRIMARY KEY, name TEXT);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	// 模拟 TMDB 响应：
	// 1. GET /3/tv/275297 -> 剧集主信息
	// 2. GET /3/tv/275297/season/1 -> 第 1 季包含每一集详情 (仍有每一集名称、分集简介、分集剧照 still_path)
	mockTMDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/3/tv/275297" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":              275297,
				"name":            "走马上任",
				"overview":        "电视剧整体简介",
				"first_air_date":  "2026-01-01",
				"poster_path":     "/series_poster.jpg",
				"backdrop_path":   "/series_backdrop.jpg",
				"number_of_seasons": 1,
				"networks": []map[string]any{
					{"name": "Tencent Video"},
				},
			})
			return
		}
		if r.URL.Path == "/3/tv/275297/season/1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            1001,
				"name":          "Season 1",
				"season_number": 1,
				"overview":      "第一季简介",
				"episodes": []map[string]any{
					{
						"id":             2001,
						"episode_number": 1,
						"season_number":  1,
						"name":           "新官上任三把火",
						"overview":       "第 1 集详细分集剧情：主角前往任所...",
						"air_date":       "2026-01-01",
						"still_path":     "/ep01_still.jpg",
						"vote_average":   8.5,
					},
					{
						"id":             2002,
						"episode_number": 2,
						"season_number":  1,
						"name":           "暗流涌动",
						"overview":       "第 2 集详细分集剧情：察觉县衙异常...",
						"air_date":       "2026-01-02",
						"still_path":     "/ep02_still.jpg",
						"vote_average":   8.6,
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockTMDB.Close()

	// 存入 TMDB 设置，使 a.TMDBClient() 指向 mockTMDB
	tmdbSettingsJSON, _ := json.Marshal(map[string]any{
		"enabled":  true,
		"api_key":  "test-api-key",
		"base_url": mockTMDB.URL + "/3",
		"language": "zh-CN",
	})
	_, _ = sqlDB.Exec("INSERT INTO settings(k,v) VALUES('tmdb_settings', ?)", string(tmdbSettingsJSON))

	// 创建剧集目录与轻量级占位 NFO (模拟未刮削时的初始状态)
	seriesDir := filepath.Join(tmpDir, "每日更新", "电视剧", "国产剧", "走马上任")
	seasonDir := filepath.Join(seriesDir, "Season 1")
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ep1Path := filepath.Join(seasonDir, "01.strm")
	ep2Path := filepath.Join(seasonDir, "02.strm")
	_ = os.WriteFile(ep1Path, []byte("http://xiaoya.host:5678/d/01.mp4\n"), 0o644)
	_ = os.WriteFile(ep2Path, []byte("http://xiaoya.host:5678/d/02.mp4\n"), 0o644)

	// 写入一个空剧情的占位 NFO，测试 force 刷新时必须能穿透该 NFO 并从 TMDB 获取真实分集数据
	placeholderNFO := `<episodedetails><title>第 1 集</title><season>1</season><episode>1</episode></episodedetails>`
	_ = os.WriteFile(filepath.Join(seasonDir, "01.nfo"), []byte(placeholderNFO), 0o644)

	seriesID := "series-zmsr"
	seasonID := "season-zmsr-s1"
	ep1ID := "ep-zmsr-01"
	ep2ID := "ep-zmsr-02"

	// 插入数据库初始数据
	now := int64(1700000000)
	_, err = sqlDB.Exec(`INSERT INTO items(id,lib,parent,name,kind,path,url,overview,poster,year,season,episode,mtime,size,seen) VALUES
		(?, 'lib1', 'lib1', '走马上任', 'Series', ?, '', '', '', 2026, 0, 0, ?, 0, '1'),
		(?, 'lib1', ?, 'Season 1', 'Season', ?, '', '', '', 2026, 1, 1, ?, 0, '1'),
		(?, 'lib1', ?, '01.mp4', 'Episode', ?, 'http://xiaoya.host:5678/d/01.mp4', '', '', 2026, 1, 1, ?, 100, '1'),
		(?, 'lib1', ?, '02.mp4', 'Episode', ?, 'http://xiaoya.host:5678/d/02.mp4', '', '', 2026, 1, 2, ?, 100, '1')`,
		seriesID, seriesDir, now,
		seasonID, seriesID, seasonDir, now,
		ep1ID, seasonID, ep1Path, now,
		ep2ID, seasonID, ep2Path, now,
	)
	if err != nil {
		t.Fatalf("insert items: %v", err)
	}

	// 模拟用户绑定或识别 Series 为 TMDB 275297
	_, err = sqlDB.Exec("INSERT INTO tmdb_item_matches(item,tmdb_id,media_type,updated) VALUES(?,?,?,?)", seriesID, "275297", "tv", now)
	if err != nil {
		t.Fatalf("insert match: %v", err)
	}

	// 执行级联刷新
	if err := app.refreshSingleItemMetadata(seriesID, true); err != nil {
		t.Fatalf("refreshSingleItemMetadata failed: %v", err)
	}

	// 1. 验证 Episode 1 已经获得分集标题、分集剧情简介、分集剧照
	var ep1Name, ep1Overview, ep1Poster string
	err = sqlDB.QueryRow("SELECT name,overview,poster FROM items WHERE id=?", ep1ID).Scan(&ep1Name, &ep1Overview, &ep1Poster)
	if err != nil {
		t.Fatalf("query ep1: %v", err)
	}
	if ep1Name != "新官上任三把火" {
		t.Errorf("ep1Name = %q, want %q", ep1Name, "新官上任三把火")
	}
	if ep1Overview != "第 1 集详细分集剧情：主角前往任所..." {
		t.Errorf("ep1Overview = %q, want %q", ep1Overview, "第 1 集详细分集剧情：主角前往任所...")
	}
	if ep1Poster != "https://image.tmdb.org/t/p/w500/ep01_still.jpg" {
		t.Errorf("ep1Poster = %q, want %q", ep1Poster, "https://image.tmdb.org/t/p/w500/ep01_still.jpg")
	}

	// 2. 验证 Episode 2 同样获得专属分集标题、分集剧情简介、分集剧照
	var ep2Name, ep2Overview, ep2Poster string
	err = sqlDB.QueryRow("SELECT name,overview,poster FROM items WHERE id=?", ep2ID).Scan(&ep2Name, &ep2Overview, &ep2Poster)
	if err != nil {
		t.Fatalf("query ep2: %v", err)
	}
	if ep2Name != "暗流涌动" {
		t.Errorf("ep2Name = %q, want %q", ep2Name, "暗流涌动")
	}
	if ep2Overview != "第 2 集详细分集剧情：察觉县衙异常..." {
		t.Errorf("ep2Overview = %q, want %q", ep2Overview, "第 2 集详细分集剧情：察觉县衙异常...")
	}
	if ep2Poster != "https://image.tmdb.org/t/p/w500/ep02_still.jpg" {
		t.Errorf("ep2Poster = %q, want %q", ep2Poster, "https://image.tmdb.org/t/p/w500/ep02_still.jpg")
	}

	// 3. 验证客户端请求 DTO 时，图片标签已正确包含剧照
	ep1Item, err := app.item(ep1ID)
	if err != nil {
		t.Fatalf("load ep1 item: %v", err)
	}
	dto := app.listDTO(ep1Item, httptest.NewRequest("GET", "/Shows/"+seriesID+"/Episodes", nil), User{})
	tags, ok := dto["ImageTags"].(M)
	if !ok || tags["Thumb"] == nil || tags["Primary"] == nil {
		t.Fatalf("ep1 DTO ImageTags 未包含 Thumb/Primary: %+v", dto["ImageTags"])
	}

	// 4. 验证 imagePath 返回剧照网络地址
	gotPath := app.imagePath(ep1ID, "Thumb")
	if gotPath != "https://image.tmdb.org/t/p/w500/ep01_still.jpg" {
		t.Errorf("imagePath(Thumb) = %q, want %q", gotPath, "https://image.tmdb.org/t/p/w500/ep01_still.jpg")
	}
}
