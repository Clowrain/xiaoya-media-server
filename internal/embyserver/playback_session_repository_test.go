package embyserver

import (
	"testing"
)

func TestSQLPlaybackSessionRepositoryReserveAndRelease(t *testing.T) {
	app := newTestSQLiteApp(t)
	repository := sqlPlaybackSessionRepository{db: app.db}

	itemID, err := repository.CurrentItem("test-user", "device-1")
	if err != nil || itemID != "" {
		t.Fatalf("empty session lookup = %q, err=%v", itemID, err)
	}
	limitReached, err := repository.Reserve("test-user", "device-1", "movie-1", 1, 1000)
	if err != nil || limitReached {
		t.Fatalf("first session reserve: limitReached=%t err=%v", limitReached, err)
	}
	itemID, err = repository.CurrentItem("test-user", "device-1")
	if err != nil || itemID != "movie-1" {
		t.Fatalf("current session = %q, err=%v", itemID, err)
	}

	limitReached, err = repository.Reserve("test-user", "device-2", "movie-2", 1, 1001)
	if err != nil || !limitReached {
		t.Fatalf("other-device session limit: limitReached=%t err=%v", limitReached, err)
	}
	limitReached, err = repository.Reserve("test-user", "device-1", "movie-3", 1, 1002)
	if err != nil || limitReached {
		t.Fatalf("same-device session renewal: limitReached=%t err=%v", limitReached, err)
	}
	itemID, err = repository.CurrentItem("test-user", "device-1")
	if err != nil || itemID != "movie-3" {
		t.Fatalf("renewed current session = %q, err=%v", itemID, err)
	}

	if err := repository.Release("test-user", "device-1"); err != nil {
		t.Fatal(err)
	}
	itemID, err = repository.CurrentItem("test-user", "device-1")
	if err != nil || itemID != "" {
		t.Fatalf("released session lookup = %q, err=%v", itemID, err)
	}
}

func TestSQLPlaybackSessionRepositoryReleaseUser(t *testing.T) {
	app := newTestSQLiteApp(t)
	repository := sqlPlaybackSessionRepository{db: app.db}
	for _, session := range [][4]any{
		{"user-a", "device-1", "item-1", int64(10)},
		{"user-a", "device-2", "item-2", int64(11)},
		{"user-b", "device-3", "item-3", int64(12)},
	} {
		if _, err := app.db.Exec("INSERT INTO plays(user_id,device,item,updated) VALUES(?,?,?,?)", session[:]...); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.ReleaseUser("user-a"); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := app.db.QueryRow("SELECT count(*) FROM plays WHERE user_id=?", "user-a").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("user-a sessions remaining = %d", remaining)
	}
	if err := app.db.QueryRow("SELECT count(*) FROM plays WHERE user_id=?", "user-b").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("user-b sessions changed: %d", remaining)
	}
}

func TestSQLPlaybackSessionRepositoryReadsActivePlayback(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, item := range []Item{
		{ID: "active-item", Lib: "lib", Parent: "lib", Name: "Active", Kind: "Movie", Path: "/media/active.mkv"},
		{ID: "stale-item", Lib: "lib", Parent: "lib", Name: "Stale", Kind: "Movie", Path: "/media/stale.mkv"},
	} {
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview,
			item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.db.Exec("INSERT INTO plays(user_id,device,item,updated) VALUES('user','active-device','active-item',100),('user','stale-device','stale-item',10)"); err != nil {
		t.Fatal(err)
	}
	repository := sqlPlaybackSessionRepository{db: app.db}
	count, err := repository.ActiveCountSince(50)
	if err != nil || count != 1 {
		t.Fatalf("active session count = %d, err=%v", count, err)
	}
	paths, err := repository.ActiveItemPaths(50)
	if err != nil || len(paths) != 1 || paths[0] != "/media/active.mkv" {
		t.Fatalf("active item paths = %v, err=%v", paths, err)
	}
}

func TestSQLPlaybackSessionRepositoryExpiresStaleLeases(t *testing.T) {
	app := newTestSQLiteApp(t)
	repository := sqlPlaybackSessionRepository{db: app.db}
	if _, err := app.db.Exec("INSERT INTO plays(user_id,device,item,updated) VALUES(?,?,?,?)", "test-user", "old-device", "old-item", 1); err != nil {
		t.Fatal(err)
	}
	limitReached, err := repository.Reserve("test-user", "new-device", "new-item", 1, 1000)
	if err != nil || limitReached {
		t.Fatalf("expired lease should not block a session: limitReached=%t err=%v", limitReached, err)
	}
	var count int
	if err := app.db.QueryRow("SELECT count(*) FROM plays WHERE user_id=? AND device=?", "test-user", "old-device").Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale lease was not removed: count=%d err=%v", count, err)
	}
}

func TestSQLPlaybackSessionRepositoryTouchesResumePerUser(t *testing.T) {
	app := newTestSQLiteApp(t)
	repository := sqlPlaybackSessionRepository{db: app.db}
	if err := repository.TouchResume("test-user", "media-1", 100); err != nil {
		t.Fatal(err)
	}
	if err := repository.TouchResume("test-user", "media-1", 200); err != nil {
		t.Fatal(err)
	}
	if err := repository.TouchResume("other-user", "media-1", 300); err != nil {
		t.Fatal(err)
	}
	var updated int64
	if err := app.db.QueryRow("SELECT updated FROM resume_activity WHERE user_id=? AND item=?", "test-user", "media-1").Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated != 200 {
		t.Fatalf("resume timestamp = %d, want 200", updated)
	}
	var records int
	if err := app.db.QueryRow("SELECT count(*) FROM resume_activity WHERE item=?", "media-1").Scan(&records); err != nil || records != 2 {
		t.Fatalf("resume records must remain per-user: count=%d err=%v", records, err)
	}
}
