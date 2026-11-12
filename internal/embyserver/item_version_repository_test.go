package embyserver

import "testing"

func TestSQLMediaItemRepositoryVersionsByGrouping(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, item := range []Item{
		{ID: "version-a", Lib: "library-1", Name: "Film", Kind: "Movie", Path: "/media/Film{tmdb-1}/1080.strm", URL: "https://media.example/1080.mkv"},
		{ID: "version-b", Lib: "library-1", Name: "Film", Kind: "Movie", Path: "/media/Film{tmdb-1}/2160.strm", URL: "https://media.example/2160.mkv"},
		{ID: "other", Lib: "library-2", Name: "Other", Kind: "Movie", Path: "/media/Other{tmdb-2}/1080.strm", URL: "https://media.example/other.mkv"},
	} {
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview,
			item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}

	versions, err := (sqlMediaItemRepository{db: app.db}).VersionsByGrouping("lib", "version-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].ID != "version-a" || versions[1].ID != "version-b" {
		t.Fatalf("unexpected versions from repository: %+v", versions)
	}
}
