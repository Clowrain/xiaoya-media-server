package embyserver

import "testing"

func TestSQLItemPageRepositoryResumeOrderingBindsUserAndCursor(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, itemID := range []string{"resume-a", "resume-b", "resume-c"} {
		item := Item{ID: itemID, Lib: "lib", Parent: "lib", Name: itemID, Kind: "Movie", Path: "/" + itemID}
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview,
			item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}
	userID := "resume-user' OR 1=1 --"
	for _, row := range [][2]any{{"resume-a", int64(100)}, {"resume-b", int64(300)}, {"resume-c", int64(200)}} {
		if _, err := app.db.Exec("INSERT INTO resume_activity(user_id,item,updated) VALUES(?,?,?)", userID, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	repository := sqlMediaItemRepository{db: app.db}
	query := ItemPageQuery{Where: "1=1", Order: "resume", UserID: userID, Limit: 1}
	first, err := repository.LoadPage(query)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Items) != 1 || first.Items[0].ID != "resume-b" {
		t.Fatalf("unexpected first resume page: %+v", first)
	}
	query.CursorValues = first.CursorValues
	query.Position = 1
	second, err := repository.LoadPage(query)
	if err != nil {
		t.Fatal(err)
	}
	if !second.HasMore || len(second.Items) != 1 || second.Items[0].ID != "resume-c" {
		t.Fatalf("unexpected second resume page: %+v", second)
	}
}

func TestSQLItemPageRepositoryUsesKeysetCursor(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, item := range []Item{
		{ID: "page-1", Lib: "page-lib", Name: "Alpha", Kind: "Movie"},
		{ID: "page-2", Lib: "page-lib", Name: "Bravo", Kind: "Movie"},
		{ID: "page-3", Lib: "page-lib", Name: "Charlie", Kind: "Movie"},
	} {
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview,
			item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}

	repository := sqlMediaItemRepository{db: app.db}
	count, err := repository.Count("lib=?", []any{"page-lib"})
	if err != nil || count != 3 {
		t.Fatalf("Count() = %d, err=%v; want 3 items", count, err)
	}
	query := ItemPageQuery{Where: "lib=?", Args: []any{"page-lib"}, Order: "name ASC,id ASC", Limit: 1}
	first, err := repository.LoadPage(query)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Items) != 1 || first.Items[0].ID != "page-1" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	query.CursorValues = first.CursorValues
	query.Position = 1
	second, err := repository.LoadPage(query)
	if err != nil {
		t.Fatal(err)
	}
	if !second.HasMore || len(second.Items) != 1 || second.Items[0].ID != "page-2" {
		t.Fatalf("unexpected second page: %+v", second)
	}
	query.CursorValues = second.CursorValues
	query.Position = 2
	third, err := repository.LoadPage(query)
	if err != nil {
		t.Fatal(err)
	}
	if third.HasMore || len(third.Items) != 1 || third.Items[0].ID != "page-3" {
		t.Fatalf("unexpected final page: %+v", third)
	}
}
