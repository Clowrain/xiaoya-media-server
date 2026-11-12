package embyserver

import "testing"

func TestSQLMediaItemRepositoryLibraryPathPersistence(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('lib-a','A','/media/a','movies')"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []Item{
		{ID: "keep", Lib: "lib-a", Parent: "lib-a", Name: "Keep", Kind: "Movie", Path: "/media/a/keep.strm"},
		{ID: "remove", Lib: "lib-a", Parent: "lib-a", Name: "Remove", Kind: "Movie", Path: "/media/a/sub/remove.strm"},
		{ID: "neighbor", Lib: "lib-a", Parent: "lib-a", Name: "Neighbor", Kind: "Movie", Path: "/media/ab/movie.strm"},
	} {
		if _, err := app.db.Exec("INSERT INTO items("+cols+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview, item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}
	repository := sqlMediaItemRepository{db: app.db}
	if exists, err := repository.Exists("keep"); err != nil || !exists {
		t.Fatalf("existing item exists=%v err=%v", exists, err)
	}
	if exists, err := repository.Exists("missing"); err != nil || exists {
		t.Fatalf("missing item exists=%v err=%v", exists, err)
	}
	if root, err := repository.LibraryRootForItem("remove"); err != nil || root != "/media/a" {
		t.Fatalf("item library root=%q err=%v", root, err)
	}
	tx, err := app.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteItemsUnderLibraryPath(tx, "lib-a", "/media/a/sub"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, itemID := range []string{"keep", "neighbor"} {
		if exists, err := repository.Exists(itemID); err != nil || !exists {
			t.Fatalf("unrelated item %q exists=%v err=%v", itemID, exists, err)
		}
	}
	if exists, err := repository.Exists("remove"); err != nil || exists {
		t.Fatalf("removed path item exists=%v err=%v", exists, err)
	}
}

func TestSQLMediaItemRepositoryBrowseSupportQueries(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('lib-a','A','/media/a','movies')"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []Item{
		{ID: "movie-b", Lib: "lib-a", Parent: "lib-a", Name: "B", Kind: "Movie", Path: "/media/a/b.strm", Poster: "poster-b", Year: 2022},
		{ID: "movie-a", Lib: "lib-a", Parent: "lib-a", Name: "A", Kind: "Movie", Path: "/media/a/a.strm", Poster: "poster-a", Year: 2020},
		{ID: "movie-other", Lib: "other", Parent: "other", Name: "Other", Kind: "Movie", Poster: "poster-o", Year: 2024},
	} {
		if _, err := app.db.Exec("INSERT INTO items("+cols+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview, item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}
	repository := sqlMediaItemRepository{db: app.db}
	primary, err := repository.LibraryImageCandidate("lib-a", "Primary")
	if err != nil || primary.ID != "movie-a" {
		t.Fatalf("primary candidate=%+v err=%v", primary, err)
	}
	if _, err := repository.LibraryImageCandidate("lib-a", "Other"); err == nil {
		t.Fatal("unsupported image kind was accepted")
	}
	years, err := repository.YearsInLibrary("lib-a")
	if err != nil || len(years) != 2 || years[0] != 2022 || years[1] != 2020 {
		t.Fatalf("years=%v err=%v", years, err)
	}
	collage, err := repository.LibraryCollageCandidates("lib-a")
	if err != nil || len(collage) != 2 || collage[0].ID != "movie-b" {
		t.Fatalf("collage candidates=%+v err=%v", collage, err)
	}
}
