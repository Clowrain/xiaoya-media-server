package embyserver

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func insertUXItem(t *testing.T, a *App, x Item) {
	t.Helper()
	if _, e := a.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
		x.ID, x.Lib, x.Parent, x.Name, x.Kind, x.Path, x.URL, x.Overview, x.Poster, x.Year, x.Season, x.Episode, x.Mtime, x.Size); e != nil {
		t.Fatalf("插入测试条目失败 %s: %v", x.ID, e)
	}
}

func uxToken(t *testing.T, a *App) (string, string) {
	t.Helper()
	var uid string
	if e := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); e != nil {
		t.Fatalf("获取测试用户失败: %v", e)
	}
	tokenVal := "ux-token-" + strings.ToLower(strings.ReplaceAll(t.Name(), "Test", ""))
	if _, e := a.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", digest(tokenVal), uid, "device", time.Now().Unix()+3600); e != nil {
		t.Fatalf("插入令牌失败: %v", e)
	}
	return uid, tokenVal
}

func decodeItems(t *testing.T, w *httptest.ResponseRecorder) []M {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("请求失败: code=%d body=%s", w.Code, w.Body.String())
	}
	var res struct {
		Items []M
	}
	if e := json.Unmarshal(w.Body.Bytes(), &res); e != nil {
		t.Fatalf("解析响应失败: %v body=%s", e, w.Body.String())
	}
	return res.Items
}

// C1 + C2: episode list rows carry series/season ancestry and truthful MediaSourceCount.
func TestEpisodeListRowsCarrySeriesInfo(t *testing.T) {
	a := newTestSQLiteApp(t)
	_, tokenVal := uxToken(t, a)

	insertUXItem(t, a, Item{ID: "ux-series", Lib: "uxlib", Name: "测试剧集", Kind: "Series", Path: "/ux/series"})
	insertUXItem(t, a, Item{ID: "ux-season", Lib: "uxlib", Parent: "ux-series", Name: "Season 1", Kind: "Season", Path: "/ux/series/s1", Season: 1, Episode: 1})
	insertUXItem(t, a, Item{ID: "ux-ep1", Lib: "uxlib", Parent: "ux-season", Name: "第一集", Kind: "Episode", Path: "/ux/series/s1/e1.strm", URL: "http://xiaoya.host/e1.mp4", Season: 1, Episode: 1, Mtime: 100})
	insertUXItem(t, a, Item{ID: "ux-ep2", Lib: "uxlib", Parent: "ux-season", Name: "第二集", Kind: "Episode", Path: "/ux/series/s1/e2.strm", URL: "http://xiaoya.host/e2.mp4", Season: 1, Episode: 2, Mtime: 200})
	// Directly parented to the Series: season join must stay empty, series join must still resolve.
	insertUXItem(t, a, Item{ID: "ux-ep3", Lib: "uxlib", Parent: "ux-series", Name: "特别篇", Kind: "Episode", Path: "/ux/series/e3.strm", URL: "http://xiaoya.host/e3.mp4", Mtime: 300})

	fetch := func(fields string) []M {
		path := "/emby/Shows/ux-series/Episodes?api_key=" + tokenVal
		if fields != "" {
			path += "&Fields=" + fields
		}
		w := httptest.NewRecorder()
		a.serve(w, httptest.NewRequest("GET", path, nil))
		return decodeItems(t, w)
	}

	rows := fetch("")
	if len(rows) != 3 {
		t.Fatalf("应返回 3 集，实际 %d: %+v", len(rows), rows)
	}
	for _, row := range rows {
		id, _ := row["Id"].(string)
		if row["SeriesId"] != "ux-series" {
			t.Fatalf("行 %s 缺少 SeriesId: %+v", id, row)
		}
		if row["SeriesName"] != "测试剧集" {
			t.Fatalf("行 %s 缺少 SeriesName: %+v", id, row)
		}
		if _, hasCount := row["MediaSourceCount"]; hasCount {
			t.Fatalf("未请求 MediaSources 时行 %s 不应有 MediaSourceCount: %+v", id, row)
		}
		if id == "ux-ep3" {
			if v, ok := row["SeasonId"]; ok && v != "" {
				t.Fatalf("直挂 Series 的剧集不应有 SeasonId: %+v", row)
			}
			continue
		}
		if row["SeasonId"] != "ux-season" || row["SeasonName"] != "Season 1" {
			t.Fatalf("行 %s 缺少 Season 信息: %+v", id, row)
		}
	}

	// api_key path already covered above; now the detail path with Fields.
	detail := fetch("MediaSources,Overview")
	if len(detail) != 3 {
		t.Fatalf("详情路径应返回 3 集，实际 %d", len(detail))
	}
	for _, row := range detail {
		id, _ := row["Id"].(string)
		sources, ok := row["MediaSources"].([]any)
		if !ok || len(sources) == 0 {
			t.Fatalf("详情行 %s 缺少 MediaSources: %+v", id, row)
		}
		count, ok := row["MediaSourceCount"].(float64)
		if !ok || int(count) != len(sources) {
			t.Fatalf("详情行 %s MediaSourceCount 应为 %d: %+v", id, len(sources), row["MediaSourceCount"])
		}
		if row["SeriesId"] != "ux-series" {
			t.Fatalf("详情行 %s 仍应携带 SeriesId: %+v", id, row)
		}
	}
}

// C3: folder kinds and yearless items omit numeric/year fields.
func TestListDTOSeriesFields(t *testing.T) {
	a := newTestSQLiteApp(t)

	m := a.listDTO(Item{Kind: "Series", Year: 0, ID: "s1"}, nil, User{})
	for _, key := range []string{"IndexNumber", "ParentIndexNumber", "ProductionYear"} {
		if _, exists := m[key]; exists {
			t.Fatalf("Series 列表不应包含 %s", key)
		}
	}
	d := a.dto(Item{Kind: "Series", Year: 0, ID: "s2"})
	for _, key := range []string{"IndexNumber", "ParentIndexNumber", "ProductionYear"} {
		if _, exists := d[key]; exists {
			t.Fatalf("Series 详情不应包含 %s", key)
		}
	}
	if _, ok := d["MediaType"]; ok {
		t.Fatal("Series 详情不应包含 MediaType")
	}

	s := a.listDTO(Item{Kind: "Season", ID: "se1", Parent: "ser1", Season: 3}, nil, User{})
	if v, ok := s["IndexNumber"].(int); !ok || v != 3 {
		t.Fatalf("Season 列表应保留 IndexNumber=3: %+v", s["IndexNumber"])
	}
	if s["SeriesId"] != "ser1" {
		t.Fatalf("Season 列表应保留 SeriesId: %+v", s["SeriesId"])
	}

	mov := a.listDTO(Item{Kind: "Movie", Year: 2024, ID: "m1"}, nil, User{})
	if v, ok := mov["ProductionYear"].(int); !ok || v != 2024 {
		t.Fatalf("Year>0 的电影应保留 ProductionYear: %+v", mov["ProductionYear"])
	}
	if _, exists := mov["IndexNumber"]; exists {
		t.Fatal("电影列表不应包含 IndexNumber")
	}

	ep := a.listDTO(Item{Kind: "Episode", Season: 2, Episode: 5, ID: "e1"}, nil, User{})
	if v, ok := ep["IndexNumber"].(int); !ok || v != 5 {
		t.Fatalf("剧集应保留 IndexNumber=5: %+v", ep["IndexNumber"])
	}
	if v, ok := ep["ParentIndexNumber"].(int); !ok || v != 2 {
		t.Fatalf("剧集应保留 ParentIndexNumber=2: %+v", ep["ParentIndexNumber"])
	}
}

// C4: DisplayPreferences persists per user + client via the settings KV.
func TestDisplayPreferencesPersistence(t *testing.T) {
	a := newTestSQLiteApp(t)
	_, tokenVal := uxToken(t, a)

	authHeader := func(client string) string {
		return `MediaBrowser Client="` + client + `", Device="d", DeviceId="dev1", Version="1.0"`
	}
	get := func(client string) M {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/emby/DisplayPreferences/home?api_key="+tokenVal, nil)
		if client != "" {
			r.Header.Set("X-Emby-Authorization", authHeader(client))
		}
		a.serve(w, r)
		if w.Code != 200 {
			t.Fatalf("GET DisplayPreferences 失败: %d %s", w.Code, w.Body.String())
		}
		var out M
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatalf("解析 DisplayPreferences 失败: %v %s", e, w.Body.String())
		}
		return out
	}
	post := func(client, body string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/emby/DisplayPreferences/home?api_key="+tokenVal, strings.NewReader(body))
		if client != "" {
			r.Header.Set("X-Emby-Authorization", authHeader(client))
		}
		a.serve(w, r)
		return w.Code
	}

	defaults := get("TestClient")
	if defaults["SortBy"] != "SortName" || defaults["SortOrder"] != "Ascending" || defaults["IndexBy"] != "None" || defaults["ViewType"] != "Poster" {
		t.Fatalf("默认显示偏好不正确: %+v", defaults)
	}
	if defaults["CustomPrefs"] == nil {
		t.Fatal("默认 CustomPrefs 不应为 nil")
	}

	if code := post("TestClient", `{"SortBy":"DateCreated","SortOrder":"Descending","RememberSorting":true,"CustomPrefs":{"a":"b"}}`); code != 204 {
		t.Fatalf("POST DisplayPreferences 应返回 204，实际 %d %s", code, "")
	}
	saved := get("TestClient")
	if saved["SortBy"] != "DateCreated" || saved["SortOrder"] != "Descending" {
		t.Fatalf("持久化后读取失败: %+v", saved)
	}
	prefs, ok := saved["CustomPrefs"].(map[string]any)
	if !ok || prefs["a"] != "b" {
		t.Fatalf("CustomPrefs 未正确往返: %+v", saved["CustomPrefs"])
	}

	other := get("OtherClient")
	if other["SortBy"] != "SortName" || other["SortOrder"] != "Ascending" {
		t.Fatalf("不同 Client 应读到默认值: %+v", other)
	}

	// No client header at all: values persist under the empty-client key.
	if code := post("", `{"SortBy":"DatePlayed"}`); code != 204 {
		t.Fatalf("无 Client 的 POST 应返回 204: %d", code)
	}
	if noClient := get(""); noClient["SortBy"] != "DatePlayed" {
		t.Fatalf("空 Client 键应持久化: %+v", noClient)
	}
}

// C5: client SortBy aliases resolve to the canonical sort columns.
func TestSortByAliases(t *testing.T) {
	a := newTestSQLiteApp(t)
	_, tokenVal := uxToken(t, a)
	// The harness leaves serverID empty; a plain /Items request would otherwise
	// be treated as the root view and return libraries instead of items.
	a.serverID = "ux-server"

	insertUXItem(t, a, Item{ID: "m1", Lib: "uxlib", Name: "AAA", Kind: "Movie", Path: "/ux/a.strm", Mtime: 100})
	insertUXItem(t, a, Item{ID: "m2", Lib: "uxlib", Name: "BBB", Kind: "Movie", Path: "/ux/b.strm", Mtime: 300})
	insertUXItem(t, a, Item{ID: "m3", Lib: "uxlib", Name: "CCC", Kind: "Movie", Path: "/ux/c.strm", Mtime: 200})

	order := func(sortBy, sortOrder string) []string {
		w := httptest.NewRecorder()
		a.serve(w, httptest.NewRequest("GET", "/emby/Items?SortBy="+sortBy+"&SortOrder="+sortOrder+"&api_key="+tokenVal, nil))
		rows := decodeItems(t, w)
		ids := []string{}
		for _, row := range rows {
			ids = append(ids, row["Id"].(string))
		}
		return ids
	}

	byDate := order("DateCreated", "Descending")
	if got := order("DateLastMediaAdded", "Descending"); strings.Join(got, ",") != strings.Join(byDate, ",") {
		t.Fatalf("DateLastMediaAdded 排序应等于 DateCreated 排序: %v vs %v", got, byDate)
	}
	if strings.Join(byDate, ",") != "m2,m3,m1" {
		t.Fatalf("按修改时间倒序应为 m2,m3,m1: %v", byDate)
	}
	byName := order("SortName", "Ascending")
	if got := order("SeriesSortName", "Ascending"); strings.Join(got, ",") != strings.Join(byName, ",") {
		t.Fatalf("SeriesSortName 排序应等于 SortName 排序: %v vs %v", got, byName)
	}
	if strings.Join(byName, ",") != "m1,m2,m3" {
		t.Fatalf("按名称正序应为 m1,m2,m3: %v", byName)
	}
}

// C5/C6: CommunityRating sorts over the rating column, and the scanner hook
// parses sidecar ratings into that column.
func TestCommunityRatingSort(t *testing.T) {
	a := newTestSQLiteApp(t)
	_, tokenVal := uxToken(t, a)
	a.serverID = "ux-server"

	insertUXItem(t, a, Item{ID: "m-low", Lib: "uxlib", Name: "低分", Kind: "Movie", Path: "/ux/low.strm", Mtime: 100})
	insertUXItem(t, a, Item{ID: "m-high", Lib: "uxlib", Name: "高分", Kind: "Movie", Path: "/ux/high.strm", Mtime: 200})
	insertUXItem(t, a, Item{ID: "m-mid", Lib: "uxlib", Name: "中分", Kind: "Movie", Path: "/ux/mid.strm", Mtime: 300})
	for id, rating := range map[string]float64{"m-low": 2.0, "m-high": 8.5, "m-mid": 5.0} {
		if _, e := a.db.Exec("UPDATE items SET rating=? WHERE id=?", rating, id); e != nil {
			t.Fatalf("设置评分失败: %v", e)
		}
	}

	w := httptest.NewRecorder()
	a.serve(w, httptest.NewRequest("GET", "/emby/Items?SortBy=CommunityRating&SortOrder=Descending&api_key="+tokenVal, nil))
	rows := decodeItems(t, w)
	ids := []string{}
	for _, row := range rows {
		ids = append(ids, row["Id"].(string))
	}
	if strings.Join(ids, ",") != "m-high,m-mid,m-low" {
		t.Fatalf("按评分倒序应为 m-high,m-mid,m-low: %v", ids)
	}

	// Deep pages must carry a real rating cursor key, not an empty one.
	for i, want := range []string{"m-high", "m-mid", "m-low"} {
		w := httptest.NewRecorder()
		a.serve(w, httptest.NewRequest("GET", fmt.Sprintf("/emby/Items?SortBy=CommunityRating&SortOrder=Descending&Limit=1&StartIndex=%d&api_key=%s", i, tokenVal), nil))
		rows := decodeItems(t, w)
		if len(rows) != 1 || rows[0]["Id"] != want {
			t.Fatalf("评分排序分页第 %d 页应为 %s: %v", i, want, rows)
		}
	}

	// Scanner side: a sidecar rating is parsed by localInfo and stored with the
	// same statement shape the scan transaction uses (full SQLite scan tests are
	// Postgres-only because commitSnapshot relies on pq COPY).
	dir := t.TempDir()
	strm := filepath.Join(dir, "rated.strm")
	if e := os.WriteFile(strm, []byte("http://example.com/r.mp4"), 0644); e != nil {
		t.Fatal(e)
	}
	nfoPath := filepath.Join(dir, "rated.nfo")
	if e := os.WriteFile(nfoPath, []byte("<movie><title>Rated</title><rating>7.5</rating></movie>"), 0644); e != nil {
		t.Fatal(e)
	}
	x := Item{ID: "ux-rated", Kind: "Movie", Path: strm, Name: "rated"}
	if got := localInfo(strm, &x); got != 7.5 {
		t.Fatalf("localInfo 应解析出 7.5 评分: %v", got)
	}
	insertUXItem(t, a, x)
	if _, e := a.db.Exec("UPDATE items SET rating=? WHERE path=?", 7.5, strm); e != nil {
		t.Fatalf("评分更新语句失败: %v", e)
	}
	var stored float64
	if e := a.db.QueryRow("SELECT rating FROM items WHERE path=?", strm).Scan(&stored); e != nil || stored != 7.5 {
		t.Fatalf("评分列应存为 7.5: %v %v", stored, e)
	}

	// Unrated sidecars return 0 and never clear an existing rating.
	unrated := filepath.Join(dir, "plain.strm")
	if e := os.WriteFile(unrated, []byte("http://example.com/p.mp4"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "plain.nfo"), []byte("<movie><title>Plain</title></movie>"), 0644); e != nil {
		t.Fatal(e)
	}
	var noRating Item
	noRating.Path = unrated
	if got := localInfo(unrated, &noRating); got != 0 {
		t.Fatalf("无评分 sidecar 应返回 0: %v", got)
	}
}

// Postgres-only: the additive rating migration applies cleanly.
func TestPostgresRatingMigrationSmoke(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL required for PostgreSQL integration tests")
	}
	a := testApp(t)
	if e := a.postgresSchema(); e != nil {
		t.Fatalf("postgresSchema 迁移失败: %v", e)
	}
	// 空表上 SELECT ... LIMIT 1 会因无行而失败；种子一条 item 再验证 rating 列可用。
	if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('pg-rate','Rate','/pg-rate','movies')"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,url,year,seen) VALUES('pg-rate-1','pg-rate','pg-rate','评分电影','Movie','/pg-rate/a','http://cdn/a.mp4',2024,'t')"); e != nil {
		t.Fatal(e)
	}
	var rating float64
	if e := a.db.QueryRow("SELECT rating FROM items WHERE id='pg-rate-1'").Scan(&rating); e != nil {
		t.Fatalf("rating 列不可用: %v", e)
	}
}
