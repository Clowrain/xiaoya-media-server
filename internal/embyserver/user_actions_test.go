package embyserver

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestSQLiteApp(t *testing.T) *App {
	t.Helper()
	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rawDB.Close() })
	tables := []string{
		"CREATE TABLE users(id TEXT PRIMARY KEY,name TEXT,hash TEXT,admin INT,first_admin INT,max_devices INT);",
		"CREATE TABLE items(id TEXT PRIMARY KEY,lib TEXT,parent TEXT,name TEXT,kind TEXT,path TEXT,url TEXT,overview TEXT,poster TEXT,year INT,season INT,episode INT,mtime INT,size INT,seen TEXT);",
		"CREATE TABLE libraries(id TEXT PRIMARY KEY,name TEXT,path TEXT,kind TEXT,status TEXT,scanned INT,error TEXT,count INT,duration REAL);",
		"CREATE TABLE userdata(user_id TEXT,item TEXT,position INT DEFAULT 0,played INT DEFAULT 0,PRIMARY KEY(user_id,item));",
		"CREATE TABLE user_favorites(user_id TEXT,item TEXT,created_at INT DEFAULT 0,PRIMARY KEY(user_id,item));",
		"CREATE TABLE resume_activity(user_id TEXT,item TEXT,updated INT,PRIMARY KEY(user_id,item));",
		"CREATE TABLE plays(user_id TEXT,device TEXT,item TEXT,updated INT,PRIMARY KEY(user_id,device));",
		"CREATE TABLE settings(k TEXT PRIMARY KEY,v TEXT);",
		"CREATE TABLE tokens(hash TEXT PRIMARY KEY,user_id TEXT,device TEXT,expires INT);",
	}
	for _, s := range tables {
		if _, err := rawDB.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = rawDB.Exec("INSERT INTO settings(k,v) VALUES('merge_versions_libraries','false'),('merge_versions_folder','false')")
	_, _ = rawDB.Exec("INSERT INTO users(id,name,hash,admin,first_admin,max_devices) VALUES('test-user','admin','hash',1,1,10)")
	db := &Database{rawDB}
	return &App{db: db, attempts: map[string][]time.Time{}}
}

func TestUserFavoritesFilterAndActions(t *testing.T) {
	a := newTestSQLiteApp(t)

	var uid string
	if err := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); err != nil {
		t.Fatalf("获取测试用户失败: %v", err)
	}

	// 插入测试影片
	item1 := Item{ID: "fav-m1", Lib: "lib-fav", Name: "电影一", Kind: "Movie", URL: "http://xiaoya.host:5678/d/m1.mp4"}
	item2 := Item{ID: "fav-m2", Lib: "lib-fav", Name: "电影二", Kind: "Movie", URL: "http://xiaoya.host:5678/d/m2.mp4"}
	for _, it := range []Item{item1, item2} {
		_, err := a.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			it.ID, it.Lib, it.Parent, it.Name, it.Kind, it.Path, it.URL, it.Overview, it.Poster, it.Year, it.Season, it.Episode, it.Mtime, it.Size)
		if err != nil {
			t.Fatalf("插入测试条目失败: %v", err)
		}
	}

	u := User{ID: uid}
	tokenVal := "test-token"
	_, _ = a.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", digest(tokenVal), uid, "device", time.Now().Unix()+3600)

	// 1. 用户未收藏任何内容时，请求 Filters=IsFavorite 必须返回空列表，不能返回全部剧集
	rEmpty := httptest.NewRequest("GET", "/emby/Users/"+uid+"/Items?Filters=IsFavorite&api_key="+tokenVal, nil)
	wEmpty := httptest.NewRecorder()
	a.items(wEmpty, rEmpty, u, false)
	if wEmpty.Code != 200 {
		t.Fatalf("查询收藏失败: code=%d", wEmpty.Code)
	}
	var resEmpty struct {
		Items []M
	}
	_ = json.Unmarshal(wEmpty.Body.Bytes(), &resEmpty)
	if len(resEmpty.Items) != 0 {
		t.Fatalf("用户未收藏任何内容时，收藏列表应为空，却返回了 %d 条: %+v", len(resEmpty.Items), resEmpty.Items)
	}

	// 2. 调用 POST /Users/{userId}/FavoriteItems/{itemId} 收藏电影一
	rAdd := httptest.NewRequest("POST", "/emby/Users/"+uid+"/FavoriteItems/"+item1.ID+"?api_key="+tokenVal, nil)
	wAdd := httptest.NewRecorder()
	a.serve(wAdd, rAdd)
	if wAdd.Code != 200 {
		t.Fatalf("收藏接口返回非 200: code=%d body=%s", wAdd.Code, wAdd.Body.String())
	}
	var addUD M
	_ = json.Unmarshal(wAdd.Body.Bytes(), &addUD)
	if addUD["IsFavorite"] != true {
		t.Fatalf("收藏后 UserData.IsFavorite 应为 true: got %v", addUD["IsFavorite"])
	}

	// 3. 再次查询 Filters=IsFavorite，必须且仅返回电影一
	rFav := httptest.NewRequest("GET", "/emby/Users/"+uid+"/Items?Filters=IsFavorite&Recursive=true&api_key="+tokenVal, nil)
	wFav := httptest.NewRecorder()
	a.items(wFav, rFav, u, false)
	var resFav struct {
		Items []M
	}
	_ = json.Unmarshal(wFav.Body.Bytes(), &resFav)
	if len(resFav.Items) != 1 || resFav.Items[0]["Id"] != item1.ID {
		t.Fatalf("收藏列表应只包含电影一，实际结果: %+v", resFav.Items)
	}

	// 4. 调用 DELETE /Users/{userId}/FavoriteItems/{itemId} 取消收藏
	rDel := httptest.NewRequest("DELETE", "/emby/Users/"+uid+"/FavoriteItems/"+item1.ID+"?api_key="+tokenVal, nil)
	wDel := httptest.NewRecorder()
	a.serve(wDel, rDel)
	if wDel.Code != 200 {
		t.Fatalf("取消收藏接口返回非 200: code=%d body=%s", wDel.Code, wDel.Body.String())
	}
	var delUD M
	_ = json.Unmarshal(wDel.Body.Bytes(), &delUD)
	if delUD["IsFavorite"] != false {
		t.Fatalf("取消收藏后 UserData.IsFavorite 应为 false: got %v", delUD["IsFavorite"])
	}

	// 5. 再次查询，收藏列表恢复为空
	wFav2 := httptest.NewRecorder()
	a.items(wFav2, rFav, u, false)
	var resFav2 struct {
		Items []M
	}
	_ = json.Unmarshal(wFav2.Body.Bytes(), &resFav2)
	if len(resFav2.Items) != 0 {
		t.Fatalf("取消收藏后收藏列表应恢复为空，实际结果: %+v", resFav2.Items)
	}
}

func TestPlayedItemsAndResumeControl(t *testing.T) {
	a := newTestSQLiteApp(t)

	var uid string
	if err := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); err != nil {
		t.Fatalf("获取测试用户失败: %v", err)
	}

	item := Item{ID: "resume-m1", Lib: "lib-res", Name: "断点续播电影", Kind: "Movie", URL: "http://xiaoya.host:5678/d/res.mp4"}
	_, err := a.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
		item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview, item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size)
	if err != nil {
		t.Fatalf("插入测试条目失败: %v", err)
	}

	u := User{ID: uid}
	tokenVal := "test-token-res"
	_, _ = a.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", digest(tokenVal), uid, "device", time.Now().Unix()+3600)

	// 模拟上报了播放进度：播放了 50000 ticks（position>0, played=0）
	_, _ = a.db.Exec("INSERT INTO userdata(user_id,item,position,played) VALUES(?,?,50000,0)", uid, item.ID)

	// 1. 此时继续观看（Resume）接口应返回该电影
	rRes := httptest.NewRequest("GET", "/emby/Users/"+uid+"/Items/Resume?Recursive=true&api_key="+tokenVal, nil)
	wRes := httptest.NewRecorder()
	a.items(wRes, rRes, u, false)
	var resBody struct {
		Items []M
	}
	_ = json.Unmarshal(wRes.Body.Bytes(), &resBody)
	if len(resBody.Items) != 1 || resBody.Items[0]["Id"] != item.ID {
		t.Fatalf("继续观看应包含有进度的电影，实际结果: %+v", resBody.Items)
	}

	// 2. 客户端调用 POST /emby/Users/{userId}/PlayedItems/{itemId} 标记为已播放
	rMarkPlayed := httptest.NewRequest("POST", "/emby/Users/"+uid+"/PlayedItems/"+item.ID+"?api_key="+tokenVal, nil)
	wMarkPlayed := httptest.NewRecorder()
	a.serve(wMarkPlayed, rMarkPlayed)
	if wMarkPlayed.Code != 200 {
		t.Fatalf("标记已播放接口返回非 200: %d body=%s", wMarkPlayed.Code, wMarkPlayed.Body.String())
	}

	// 3. 再次查询继续观看，已播放电影应自动从继续观看中消失
	wRes2 := httptest.NewRecorder()
	a.items(wRes2, rRes, u, false)
	var resBody2 struct {
		Items []M
	}
	_ = json.Unmarshal(wRes2.Body.Bytes(), &resBody2)
	if len(resBody2.Items) != 0 {
		t.Fatalf("标记已播放后，电影应从继续观看中清除，却仍有: %+v", resBody2.Items)
	}

	// 4. 客户端调用 DELETE /emby/Users/{userId}/PlayedItems/{itemId} 重置进度/标记未播放
	rReset := httptest.NewRequest("DELETE", "/emby/Users/"+uid+"/PlayedItems/"+item.ID+"?api_key="+tokenVal, nil)
	wReset := httptest.NewRecorder()
	a.serve(wReset, rReset)
	if wReset.Code != 200 {
		t.Fatalf("重置进度接口返回非 200: %d body=%s", wReset.Code, wReset.Body.String())
	}
	// 重置后 position=0, played=0，依然不应出现在继续观看中
	wRes3 := httptest.NewRecorder()
	a.items(wRes3, rRes, u, false)
	var resBody3 struct {
		Items []M
	}
	_ = json.Unmarshal(wRes3.Body.Bytes(), &resBody3)
	if len(resBody3.Items) != 0 {
		t.Fatalf("重置进度后，电影不应出现在继续观看中，却仍有: %+v", resBody3.Items)
	}
}

func TestNextUpExcludesUnwatchedShows(t *testing.T) {
	a := newTestSQLiteApp(t)

	var uid string
	if err := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); err != nil {
		t.Fatalf("获取测试用户失败: %v", err)
	}

	// 模拟两部电视剧：一部全新未看（Show A），一部看了第 1 集（Show B）
	showA := Item{ID: "show-a", Name: "全新未看剧", Kind: "Series", Lib: "lib-tv", Path: "/media/show-a", URL: "dir"}
	epA1 := Item{ID: "ep-a1", Parent: "show-a", Name: "第1集", Kind: "Episode", Lib: "lib-tv", Season: 1, Episode: 1, URL: "http://xiaoya.host:5678/d/a1.mp4"}

	showB := Item{ID: "show-b", Name: "正在追的剧", Kind: "Series", Lib: "lib-tv", Path: "/media/show-b", URL: "dir"}
	epB1 := Item{ID: "ep-b1", Parent: "show-b", Name: "第1集", Kind: "Episode", Lib: "lib-tv", Season: 1, Episode: 1, URL: "http://xiaoya.host:5678/d/b1.mp4"}
	epB2 := Item{ID: "ep-b2", Parent: "show-b", Name: "第2集", Kind: "Episode", Lib: "lib-tv", Season: 1, Episode: 2, URL: "http://xiaoya.host:5678/d/b2.mp4"}

	for _, it := range []Item{showA, epA1, showB, epB1, epB2} {
		_, _ = a.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			it.ID, it.Lib, it.Parent, it.Name, it.Kind, it.Path, it.URL, it.Overview, it.Poster, it.Year, it.Season, it.Episode, it.Mtime, it.Size)
	}

	// 用户只看过了 Show B 的第 1 集
	_, _ = a.db.Exec("INSERT INTO userdata(user_id,item,played,position) VALUES(?,?,1,0)", uid, epB1.ID)

	tokenVal := "test-token-next"
	_, _ = a.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", digest(tokenVal), uid, "device", time.Now().Unix()+3600)

	// 测试无论是 /emby/Shows/NextUp 还是 /emby/Users/{userId}/Shows/NextUp
	for _, path := range []string{"/emby/Shows/NextUp?api_key=" + tokenVal, "/emby/Users/" + uid + "/Shows/NextUp?api_key=" + tokenVal} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		a.serve(w, r)
		if w.Code != 200 {
			t.Fatalf("请求 NextUp 失败: path=%s code=%d body=%s", path, w.Code, w.Body.String())
		}
		var nextUpRes struct {
			Items []M
		}
		_ = json.Unmarshal(w.Body.Bytes(), &nextUpRes)

		// 检查：绝对不能包含从未看过的 Show A 的任何剧集！
		for _, it := range nextUpRes.Items {
			if it["Id"] == epA1.ID {
				t.Fatalf("NextUp 错误包含了未看过的电视剧剧集: %+v", it)
			}
		}

		// 检查：必须包含 Show B 的下一集（第 2 集）
		foundB2 := false
		for _, it := range nextUpRes.Items {
			if it["Id"] == epB2.ID {
				foundB2 = true
				break
			}
		}
		if !foundB2 {
			t.Fatalf("NextUp 应推荐正在追的剧集下一集 ep-b2，实际返回: %+v", nextUpRes.Items)
		}
	}
}
