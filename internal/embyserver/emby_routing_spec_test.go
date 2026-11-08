package embyserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newRoutingSQLiteApp extends the SQLite harness with the tables the routing spec
// exercises (api_keys / user_playback / library_order) that newTestSQLiteApp omits.
func newRoutingSQLiteApp(t *testing.T) *App {
	t.Helper()
	a := newTestSQLiteApp(t)
	for _, s := range []string{
		"CREATE TABLE api_keys(id TEXT PRIMARY KEY, name TEXT, hash TEXT, created INT);",
		"CREATE TABLE user_playback(user_id TEXT PRIMARY KEY, allowed INT);",
		"CREATE TABLE library_order(library_id TEXT PRIMARY KEY, position INT);",
	} {
		if _, err := a.db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	// 生产环境 init() 会写入 settings.server_id；这里显式赋值，避免空 serverID
	// 使 items() 的 `parent == a.serverID` 分支把无 ParentId 的列表查询误判为根视图。
	a.serverID = "route-server"
	return a
}

func routingToken(t *testing.T, a *App, uid string) string {
	t.Helper()
	tok := "route-token-" + uid
	if _, err := a.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", digest(tok), uid, "device", time.Now().Unix()+3600); err != nil {
		t.Fatal(err)
	}
	return tok
}

func routingUID(t *testing.T, a *App) string {
	t.Helper()
	var uid string
	if err := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); err != nil {
		t.Fatalf("获取测试用户失败: %v", err)
	}
	return uid
}

func insertRouteItem(t *testing.T, a *App, it Item) {
	t.Helper()
	if _, err := a.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
		it.ID, it.Lib, it.Parent, it.Name, it.Kind, it.Path, it.URL, it.Overview, it.Poster, it.Year, it.Season, it.Episode, it.Mtime, it.Size); err != nil {
		t.Fatalf("插入测试条目失败: %v", err)
	}
}

func routingServe(a *App, method, path string, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	a.serve(w, req)
	return w
}

func decodeRoutingM(t *testing.T, w *httptest.ResponseRecorder) M {
	t.Helper()
	var m M
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是合法 JSON: %v body=%s", err, w.Body.String())
	}
	return m
}

// A1: /Users/{uid}/Items/{id}/{suffix} dispatch table.
func TestUserItemsSubRoutesDispatch(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	t.Setenv("PROBE_PLAYBACK_URL", "http://127.0.0.1:1") // 探针立即失败，保证用例快速返回
	uid := routingUID(t, a)
	tok := routingToken(t, a, uid)
	insertRouteItem(t, a, Item{ID: "route-m1", Lib: "lib-route", Name: "路由电影", Kind: "Movie", URL: "http://cdn/x.mp4"})
	base := "/emby/Users/" + uid + "/Items/route-m1"

	// PlaybackInfo → MediaSources
	w := routingServe(a, "GET", base+"/PlaybackInfo?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("PlaybackInfo 应返回 200: %d %s", w.Code, w.Body.String())
	}
	if m := decodeRoutingM(t, w); len(m["MediaSources"].([]any)) == 0 {
		t.Fatalf("PlaybackInfo 应包含 MediaSources: %+v", m)
	}

	// Similar 子路由必须分发到 a.similar：不允许 404，也不允许退化成详情接口。
	// （完整 QueryResult/StartIndex 断言见 TestSimilarQueryResultOnPostgres：similar 的
	// CTE 使用 Postgres 方言括号语法，SQLite 内存库无法执行该查询。）
	w = routingServe(a, "GET", base+"/Similar?Limit=1&api_key="+tok, "")
	if w.Code == 404 {
		t.Fatalf("Similar 子路由应已实现: %d %s", w.Code, w.Body.String())
	}
	var similarBody M
	_ = json.Unmarshal(w.Body.Bytes(), &similarBody)
	if similarBody["Id"] == "route-m1" {
		t.Fatalf("Similar 子路由不应退化成详情响应: %s", w.Body.String())
	}

	// 纯详情路由
	w = routingServe(a, "GET", base+"?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("Items/{id} 详情应返回 200: %d %s", w.Code, w.Body.String())
	}
	if m := decodeRoutingM(t, w); m["Id"] != "route-m1" || m["Type"] != "Movie" {
		t.Fatalf("详情应返回条目本体: %+v", m)
	}

	// 未知子路由必须 404
	w = routingServe(a, "GET", base+"/UnknownThing?api_key="+tok, "")
	if w.Code != 404 {
		t.Fatalf("未知子路由应返回 404: %d %s", w.Code, w.Body.String())
	}

	// RefreshMediaInfo 到达探针路径（SQLite 测试中探针必然失败，返回优雅的 502；不允许 404/500 路由错误）
	w = routingServe(a, "POST", base+"/RefreshMediaInfo?api_key="+tok, "")
	if !(w.Code < 400 || w.Code == 502) {
		t.Fatalf("RefreshMediaInfo 应到达探针处理路径: %d %s", w.Code, w.Body.String())
	}
}

// A2: /Users/{uid}/Shows/{sid}/Seasons|Episodes.
func TestUserShowsSeasonsEpisodes(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	uid := routingUID(t, a)
	tok := routingToken(t, a, uid)
	insertRouteItem(t, a, Item{ID: "sh-ser", Lib: "lib-shows", Name: "路由剧集", Kind: "Series", Year: 2024})
	insertRouteItem(t, a, Item{ID: "sh-s1", Lib: "lib-shows", Parent: "sh-ser", Name: "第一季", Kind: "Season", Season: 1})
	insertRouteItem(t, a, Item{ID: "sh-s2", Lib: "lib-shows", Parent: "sh-ser", Name: "第二季", Kind: "Season", Season: 2})
	insertRouteItem(t, a, Item{ID: "sh-e1", Lib: "lib-shows", Parent: "sh-s1", Name: "第1集", Kind: "Episode", Season: 1, Episode: 1})
	insertRouteItem(t, a, Item{ID: "sh-e2", Lib: "lib-shows", Parent: "sh-s2", Name: "第2季第1集", Kind: "Episode", Season: 2, Episode: 1})

	w := routingServe(a, "GET", "/emby/Users/"+uid+"/Shows/sh-ser/Seasons?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("User Shows/Seasons 应返回 200: %d %s", w.Code, w.Body.String())
	}
	var seasons struct {
		Items            []M
		TotalRecordCount int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &seasons)
	if seasons.TotalRecordCount != 2 || len(seasons.Items) != 2 {
		t.Fatalf("Seasons 应返回 2 个: %+v", seasons)
	}
	for _, it := range seasons.Items {
		if it["Type"] != "Season" {
			t.Fatalf("Seasons 列表只应包含 Season: %+v", it)
		}
	}

	w = routingServe(a, "GET", "/emby/Users/"+uid+"/Shows/sh-ser/Episodes?api_key="+tok, "")
	var episodes struct {
		Items            []M
		TotalRecordCount int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &episodes)
	if w.Code != 200 || episodes.TotalRecordCount != 2 || len(episodes.Items) != 2 {
		t.Fatalf("Episodes 应返回 2 集: code=%d %+v", w.Code, episodes)
	}

	w = routingServe(a, "GET", "/emby/Users/"+uid+"/Shows/sh-ser/Episodes?SeasonId=sh-s2&api_key="+tok, "")
	var filtered struct {
		Items            []M
		TotalRecordCount int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &filtered)
	if w.Code != 200 || filtered.TotalRecordCount != 1 || len(filtered.Items) != 1 || filtered.Items[0]["Id"] != "sh-e2" {
		t.Fatalf("按 SeasonId 过滤应只返回 sh-e2: code=%d %+v", w.Code, filtered)
	}
}

// A3: GET /Users/{uid}/FavoriteItems & /Users/{uid}/PlayedItems.
func TestFavoriteItemsPlayedItemsGetLists(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	uid := routingUID(t, a)
	tok := routingToken(t, a, uid)
	insertRouteItem(t, a, Item{ID: "fav-m1", Lib: "lib-fav", Name: "收藏电影", Kind: "Movie", URL: "http://cdn/m1.mp4"})
	insertRouteItem(t, a, Item{ID: "fav-m2", Lib: "lib-fav", Name: "已看电影", Kind: "Movie", URL: "http://cdn/m2.mp4"})

	// POST 收藏 fav-m1
	w := routingServe(a, "POST", "/emby/Users/"+uid+"/FavoriteItems/fav-m1?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("收藏失败: %d %s", w.Code, w.Body.String())
	}
	// GET 收藏列表只包含 fav-m1
	w = routingServe(a, "GET", "/emby/Users/"+uid+"/FavoriteItems?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("FavoriteItems 列表应返回 200: %d %s", w.Code, w.Body.String())
	}
	m := decodeRoutingM(t, w)
	for _, key := range []string{"Items", "TotalRecordCount", "StartIndex"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("FavoriteItems 应返回标准 QueryResult, 缺少 %s: %+v", key, m)
		}
	}
	items := m["Items"].([]any)
	if m["TotalRecordCount"] != float64(1) || len(items) != 1 || items[0].(M)["Id"] != "fav-m1" {
		t.Fatalf("收藏列表应只包含 fav-m1: %+v", m)
	}

	// POST 标记已看 fav-m2
	w = routingServe(a, "POST", "/emby/Users/"+uid+"/PlayedItems/fav-m2?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("标记已看失败: %d %s", w.Code, w.Body.String())
	}
	w = routingServe(a, "GET", "/emby/Users/"+uid+"/PlayedItems?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("PlayedItems 列表应返回 200: %d %s", w.Code, w.Body.String())
	}
	m = decodeRoutingM(t, w)
	items = m["Items"].([]any)
	if m["TotalRecordCount"] != float64(1) || len(items) != 1 || items[0].(M)["Id"] != "fav-m2" {
		t.Fatalf("已看列表应只包含 fav-m2: %+v", m)
	}

	// 第二个非管理员用户查询他人列表必须 403
	if _, err := a.db.Exec("INSERT INTO users(id,name,hash,admin,first_admin,max_devices) VALUES('route-user-b','b','hash',0,0,5)"); err != nil {
		t.Fatal(err)
	}
	tokB := routingToken(t, a, "route-user-b")
	w = routingServe(a, "GET", "/emby/Users/"+uid+"/FavoriteItems?api_key="+tokB, "")
	if w.Code != 403 {
		t.Fatalf("非管理员查询他人收藏列表应 403: %d %s", w.Code, w.Body.String())
	}
	w = routingServe(a, "GET", "/emby/Users/"+uid+"/PlayedItems?api_key="+tokB, "")
	if w.Code != 403 {
		t.Fatalf("非管理员查询他人已看列表应 403: %d %s", w.Code, w.Body.String())
	}
}

// A4: /Shows/{sid} 详情、空子集合与 /Episodes。
func TestShowsBareDetailAndEmptySubCollections(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	uid := routingUID(t, a)
	tok := routingToken(t, a, uid)
	insertRouteItem(t, a, Item{ID: "sh-bare", Lib: "lib-bare", Name: "空子集合剧集", Kind: "Series"})
	insertRouteItem(t, a, Item{ID: "sh-bare-s1", Lib: "lib-bare", Parent: "sh-bare", Name: "第一季", Kind: "Season", Season: 1})
	insertRouteItem(t, a, Item{ID: "sh-bare-e1", Lib: "lib-bare", Parent: "sh-bare-s1", Name: "第1集", Kind: "Episode", Season: 1, Episode: 1})

	w := routingServe(a, "GET", "/emby/Shows/sh-bare?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/Shows/{sid} 详情应返回 200: %d %s", w.Code, w.Body.String())
	}
	if m := decodeRoutingM(t, w); m["Type"] != "Series" || m["Id"] != "sh-bare" {
		t.Fatalf("/Shows/{sid} 应返回 Series 详情: %+v", m)
	}

	for _, path := range []string{
		"/emby/Shows/sh-bare/Extras",
		"/emby/Items/sh-bare/Extras",
		"/emby/Shows/sh-bare/Intros",
		"/emby/Shows/sh-bare/ThemeSongs",
		"/emby/Shows/sh-bare/ThemeVideos",
		"/emby/Shows/sh-bare/SpecialFeatures",
	} {
		w = routingServe(a, "GET", path+"?api_key="+tok, "")
		if w.Code != 200 {
			t.Fatalf("%s 应返回 200: %d %s", path, w.Code, w.Body.String())
		}
		m := decodeRoutingM(t, w)
		if items, ok := m["Items"].([]any); !ok || len(items) != 0 {
			t.Fatalf("%s 应返回空 Items: %+v", path, m)
		}
	}

	w = routingServe(a, "GET", "/emby/Episodes?SeriesId=sh-bare&api_key="+tok, "")
	var episodes struct {
		Items            []M
		TotalRecordCount int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &episodes)
	if w.Code != 200 || episodes.TotalRecordCount != 1 || len(episodes.Items) != 1 || episodes.Items[0]["Id"] != "sh-bare-e1" {
		t.Fatalf("/Episodes?SeriesId 应返回 1 集: code=%d %+v", w.Code, episodes)
	}

	w = routingServe(a, "GET", "/emby/Episodes?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/Episodes 缺少 SeriesId 应返回 200: %d %s", w.Code, w.Body.String())
	}
	m := decodeRoutingM(t, w)
	if m["TotalRecordCount"] != float64(0) {
		t.Fatalf("/Episodes 缺少 SeriesId 应返回空 QueryResult: %+v", m)
	}
}

// A5: SearchHints / SearchSuggestions / Years / Genres/{name}/Items。
// 注意：SearchTerm 走 name ILIKE（Postgres 方言），SQLite 内存库不支持，
// 因此“命中条目”的断言放在 TestSearchHintsAndSuggestionsOnPostgres（无 TEST_DATABASE_URL 时自动跳过）。
func TestSearchHintsSuggestionsYearsGenreItems(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	uid := routingUID(t, a)
	tok := routingToken(t, a, uid)
	insertRouteItem(t, a, Item{ID: "sr-m1", Lib: "lib-search", Name: "流浪地球2024", Kind: "Movie", URL: "http://cdn/a.mp4", Year: 2024})
	insertRouteItem(t, a, Item{ID: "sr-m2", Lib: "lib-search", Name: "星际穿越", Kind: "Movie", URL: "http://cdn/b.mp4", Year: 2019})
	if _, err := a.db.Exec("INSERT INTO item_genres VALUES('sr-m1','剧情'),('sr-m2','科幻')"); err != nil {
		t.Fatal(err)
	}

	// Years
	w := routingServe(a, "GET", "/emby/Years?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/Years 应返回 200: %d %s", w.Code, w.Body.String())
	}
	m := decodeRoutingM(t, w)
	items := m["Items"].([]any)
	found := false
	for _, v := range items {
		if it := v.(M); it["Name"] == "2024" && it["Id"] == "2024" {
			found = true
		}
	}
	if !found || m["TotalRecordCount"] != float64(len(items)) {
		t.Fatalf("/Years 应包含 2024 年份: %+v", m)
	}

	// /Genres/{name}/Items 原样中文
	w = routingServe(a, "GET", "/emby/Genres/剧情/Items?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/Genres/剧情/Items 应返回 200: %d %s", w.Code, w.Body.String())
	}
	m = decodeRoutingM(t, w)
	genreItems := m["Items"].([]any)
	if m["TotalRecordCount"] != float64(1) || len(genreItems) != 1 || genreItems[0].(M)["Id"] != "sr-m1" {
		t.Fatalf("剧情类型应只返回 sr-m1: %+v", m)
	}

	// URL 编码的中文
	w = routingServe(a, "GET", "/emby/Genres/%E5%89%A7%E6%83%85/Items?api_key="+tok, "")
	m = decodeRoutingM(t, w)
	genreItems = m["Items"].([]any)
	if w.Code != 200 || len(genreItems) != 1 || genreItems[0].(M)["Id"] != "sr-m1" {
		t.Fatalf("URL 编码的类型应同样只返回 sr-m1: code=%d %+v", w.Code, m)
	}

	// SearchHints 空词
	w = routingServe(a, "GET", "/emby/SearchHints?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/SearchHints 空词应返回 200: %d %s", w.Code, w.Body.String())
	}
	m = decodeRoutingM(t, w)
	if hints, ok := m["SearchHints"].([]any); !ok || len(hints) != 0 || m["TotalRecordCount"] != float64(0) {
		t.Fatalf("/SearchHints 空词应返回空列表: %+v", m)
	}

	// SearchHints 带词：路由必须优雅返回结构（SQLite 下底层 ILIKE 搜索不可用，列表为空；命中断言见 Postgres 用例）
	w = routingServe(a, "GET", "/emby/SearchHints?SearchTerm=%E6%B5%81%E6%B5%AA&api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/SearchHints 带词应优雅返回 200: %d %s", w.Code, w.Body.String())
	}
	m = decodeRoutingM(t, w)
	if _, ok := m["SearchHints"].([]any); !ok {
		t.Fatalf("/SearchHints 带词应返回 SearchHints 数组: %+v", m)
	}

	// SearchSuggestions 返回标准 QueryResult
	w = routingServe(a, "GET", "/emby/SearchSuggestions?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/SearchSuggestions 应返回 200: %d %s", w.Code, w.Body.String())
	}
	m = decodeRoutingM(t, w)
	for _, key := range []string{"Items", "TotalRecordCount", "StartIndex"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("/SearchSuggestions 应返回标准 QueryResult, 缺少 %s: %+v", key, m)
		}
	}
}

// 命中断言需要 Postgres 方言（ILIKE / media_initials），无 TEST_DATABASE_URL 时自动跳过。
func TestSearchHintsAndSuggestionsOnPostgres(t *testing.T) {
	a := testApp(t)
	if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('pg-search','Search','/pg-search','movies')"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,url,year,seen) VALUES('pg-m1','pg-search','pg-search','流浪地球2024','Movie','/pg-search/a','http://cdn/a.mp4',2024,'t')"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO item_genres(item,genre) VALUES('pg-m1','剧情')"); e != nil {
		t.Fatal(e)
	}

	w := httptest.NewRecorder()
	a.serve(w, httptest.NewRequest("GET", "/emby/SearchHints?SearchTerm=流浪", nil))
	if w.Code != 200 {
		t.Fatalf("SearchHints 应返回 200: %d %s", w.Code, w.Body.String())
	}
	var hints struct {
		SearchHints []M
	}
	_ = json.Unmarshal(w.Body.Bytes(), &hints)
	if len(hints.SearchHints) < 1 {
		t.Fatalf("SearchHints 应至少命中 1 条: %s", w.Body.String())
	}
	h := hints.SearchHints[0]
	if h["Id"] == nil || h["Id"] != h["ItemId"] {
		t.Fatalf("SearchHint 的 Id 与 ItemId 必须一致: %+v", h)
	}
	if h["MatchedTerm"] != "流浪" || h["Type"] != "Movie" || h["MediaType"] != "Video" {
		t.Fatalf("SearchHint 字段不符合规范: %+v", h)
	}

	w = httptest.NewRecorder()
	a.serve(w, httptest.NewRequest("GET", "/emby/SearchSuggestions?query=流浪", nil))
	if w.Code != 200 {
		t.Fatalf("SearchSuggestions 应返回 200: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Items            []M
		TotalRecordCount int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result.TotalRecordCount < 1 || len(result.Items) < 1 {
		t.Fatalf("SearchSuggestions query= 应命中条目: %s", w.Body.String())
	}
}

// A6: /System/Info/Public 必须包含 Name 字段。
func TestServerInfoName(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	if _, err := a.db.Exec("INSERT INTO settings(k,v) VALUES('server_name','路由测试服务器') ON CONFLICT(k) DO UPDATE SET v=excluded.v"); err != nil {
		t.Fatal(err)
	}
	w := routingServe(a, "GET", "/emby/System/Info/Public", "")
	if w.Code != 200 {
		t.Fatalf("/System/Info/Public 应返回 200: %d %s", w.Code, w.Body.String())
	}
	m := decodeRoutingM(t, w)
	if m["Name"] != "路由测试服务器" || m["ServerName"] != "路由测试服务器" {
		t.Fatalf("ServerInfo 的 Name/ServerName 应为 settings.server_name: %+v", m)
	}
}

// A8 断言（Similar 必须携带 StartIndex=0）依赖 Postgres 方言的 similar 查询，
// 无 TEST_DATABASE_URL 时自动跳过。
func TestSimilarQueryResultOnPostgres(t *testing.T) {
	a := testApp(t)
	if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('pg-sim','Sim','/pg-sim','movies')"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,url,year,seen) VALUES('pg-s1','pg-sim','pg-sim','相似电影一','Movie','/pg-sim/a','http://cdn/a.mp4',2024,'t')"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,url,year,seen) VALUES('pg-s2','pg-sim','pg-sim','相似电影二','Movie','/pg-sim/b','http://cdn/b.mp4',2023,'t')"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO item_genres(item,genre) VALUES('pg-s1','剧情'),('pg-s2','剧情')"); e != nil {
		t.Fatal(e)
	}

	w := httptest.NewRecorder()
	a.serve(w, httptest.NewRequest("GET", "/emby/Users/anything/Items/pg-s1/Similar?Limit=1", nil))
	if w.Code != 200 {
		t.Fatalf("Similar 应返回 200: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Items            []M
		TotalRecordCount int
		StartIndex       *int
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatalf("Similar 响应解析失败: %v %s", e, w.Body.String())
	}
	if result.StartIndex == nil || *result.StartIndex != 0 {
		t.Fatalf("Similar 响应缺少 StartIndex=0: %s", w.Body.String())
	}
	if result.TotalRecordCount < 1 || len(result.Items) < 1 {
		t.Fatalf("Similar 应至少命中 1 条: %s", w.Body.String())
	}
}

// A7: /Videos/{id}/original.mkv 也要走直链重定向。
func TestStreamOriginalWithExtension(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	t.Setenv("NANSHARE_URL", "")
	t.Setenv("PUBLIC_PLAYBACK_URL", "")
	insertRouteItem(t, a, Item{ID: "st-m1", Lib: "lib-stream", Name: "直链电影", Kind: "Movie", URL: "http://cdn/x.mp4"})
	if _, err := a.db.Exec("INSERT INTO api_keys VALUES('route-key','test',?,0)", digest("route-key")); err != nil {
		t.Fatal(err)
	}
	w := routingServe(a, "GET", "/emby/Videos/st-m1/original.mkv?api_key=route-key", "")
	if w.Code != 302 {
		t.Fatalf("/Videos/{id}/original.mkv 应返回 302: %d %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "http://cdn/x.mp4" {
		t.Fatalf("302 Location 应为媒体直链: %q", loc)
	}
}

// A9: 各类存根端点。
func TestStubEndpoints(t *testing.T) {
	a := newRoutingSQLiteApp(t)
	uid := routingUID(t, a)
	tok := routingToken(t, a, uid)

	w := routingServe(a, "GET", "/emby/ScheduledTasks?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/ScheduledTasks 应返回 200: %d %s", w.Code, w.Body.String())
	}
	var tasks []any
	if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil || len(tasks) != 0 {
		t.Fatalf("/ScheduledTasks 应返回空数组: %s", w.Body.String())
	}

	w = routingServe(a, "POST", "/emby/ScheduledTasks/Running/abc?api_key="+tok, "")
	if w.Code != 204 {
		t.Fatalf("POST /ScheduledTasks/Running/{id} 应返回 204: %d %s", w.Code, w.Body.String())
	}

	// Branding 在登录页被访问，无需令牌
	w = routingServe(a, "GET", "/emby/Branding/Configuration", "")
	if w.Code != 200 {
		t.Fatalf("/Branding/Configuration 应返回 200: %d %s", w.Code, w.Body.String())
	}
	m := decodeRoutingM(t, w)
	for _, key := range []string{"LoginDisclaimer", "CustomCss", "SplashscreenEnabled"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("/Branding/Configuration 缺少 %s: %+v", key, m)
		}
	}

	// FolderViews 返回 CollectionFolder 库
	if _, err := a.db.Exec("INSERT INTO libraries(id,name,path,kind,status,scanned,error,count,duration) VALUES('lib-fv','电影','/media/m','movies','ok',0,'',3,0)"); err != nil {
		t.Fatal(err)
	}
	w = routingServe(a, "GET", "/emby/Users/"+uid+"/Items/FolderViews?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("FolderViews 应返回 200: %d %s", w.Code, w.Body.String())
	}
	m = decodeRoutingM(t, w)
	folderItems := m["Items"].([]any)
	if len(folderItems) != 1 || folderItems[0].(M)["Type"] != "CollectionFolder" {
		t.Fatalf("FolderViews 应返回 CollectionFolder 库: %+v", m)
	}

	// /Users/Authenticate 走到登录处理（错误凭据 401，而非 404）
	w = routingServe(a, "POST", "/emby/Users/Authenticate", `{"Username":"nobody","Pw":"wrong-password"}`)
	if w.Code != 401 {
		t.Fatalf("/Users/Authenticate 错误凭据应返回 401: %d %s", w.Code, w.Body.String())
	}
}

// A10: 小修复合集。
func TestRoutingSmallFixes(t *testing.T) {
	// token() 大小写不敏感 Bearer 解析
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "bearer abc123")
	if got := token(r); got != "abc123" {
		t.Fatalf("token() 应解析小写 bearer: %q", got)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	if got := token(r); got != "abc123" {
		t.Fatalf("token() 应解析标准 Bearer: %q", got)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearerx abc")
	if got := token(r); got != "Bearerx abc" {
		t.Fatalf("token() 不应把非 Bearer 前缀拆开: %q", got)
	}

	a := newRoutingSQLiteApp(t)
	uid := routingUID(t, a)
	tok := routingToken(t, a, uid)

	// Ids 上限放宽到 1000
	ids999 := make([]string, 0, 999)
	for i := 0; i < 999; i++ {
		ids999 = append(ids999, fmt.Sprintf("no-item-%d", i))
	}
	w := routingServe(a, "GET", "/emby/Items?Ids="+strings.Join(ids999, ",")+"&api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("999 个 Ids 应返回 200: %d %s", w.Code, w.Body.String())
	}
	ids1001 := make([]string, 0, 1001)
	for i := 0; i < 1001; i++ {
		ids1001 = append(ids1001, fmt.Sprintf("no-item-%d", i))
	}
	w = routingServe(a, "GET", "/emby/Items?Ids="+strings.Join(ids1001, ",")+"&api_key="+tok, "")
	if w.Code != 400 {
		t.Fatalf("1001 个 Ids 应返回 400: %d %s", w.Code, w.Body.String())
	}

	// LastLoginDate 规范化输出；无登录记录的用户不得携带该键
	if _, err := a.db.Exec("CREATE TABLE user_logins(user_id TEXT PRIMARY KEY, last_login BIGINT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO user_logins VALUES(?,?)", uid, int64(1700000000)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO users(id,name,hash,admin,first_admin,max_devices) VALUES('route-user-c','c','hash',0,0,5)"); err != nil {
		t.Fatal(err)
	}
	w = routingServe(a, "GET", "/emby/Users?api_key="+tok, "")
	if w.Code != 200 {
		t.Fatalf("/Users 应返回 200: %d %s", w.Code, w.Body.String())
	}
	var list []M
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("/Users 应返回用户数组: %s", w.Body.String())
	}
	layout := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.0000000Z$`)
	var sawAdmin, sawPlain bool
	for _, dto := range list {
		switch dto["Id"] {
		case uid:
			sawAdmin = true
			v, ok := dto["LastLoginDate"].(string)
			if !ok || !layout.MatchString(v) {
				t.Fatalf("LastLoginDate 应符合 ISO 规范: %+v", dto["LastLoginDate"])
			}
		case "route-user-c":
			sawPlain = true
			if _, exists := dto["LastLoginDate"]; exists {
				t.Fatalf("无登录记录的用户不应携带 LastLoginDate: %+v", dto)
			}
		}
	}
	if !sawAdmin || !sawPlain {
		t.Fatalf("/Users 应同时返回两个用户: %+v", list)
	}
}
