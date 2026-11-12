package embyserver

import "testing"

func TestSQLMediaItemRepositoryBrowseMetadata(t *testing.T) {
	app := newTestSQLiteApp(t)
	items := []Item{
		{ID: "series-1", Lib: "lib-1", Name: "Series", Kind: "Series"},
		{ID: "season-1", Lib: "lib-1", Parent: "series-1", Name: "Season 1", Kind: "Season", Season: 1},
		{ID: "episode-1", Lib: "lib-1", Parent: "season-1", Name: "Episode 1", Kind: "Episode", Season: 1, Episode: 1},
		{ID: "episode-2", Lib: "lib-1", Parent: "season-1", Name: "Episode 2", Kind: "Episode", Season: 1, Episode: 2},
		{ID: "episode-special", Lib: "lib-1", Parent: "series-1", Name: "Special", Kind: "Episode"},
	}
	for _, item := range items {
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview,
			item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}

	metadata, err := (sqlMediaItemRepository{db: app.db}).BrowseMetadata(items)
	if err != nil {
		t.Fatal(err)
	}
	series := metadata["series-1"]
	if series.ChildCount != 2 || series.EpisodeCount != 3 {
		t.Fatalf("unexpected recursive series counts: %+v", series)
	}
	season := metadata["season-1"]
	if season.ChildCount != 2 {
		t.Fatalf("unexpected season child count: %+v", season)
	}
	episode := metadata["episode-1"]
	if episode.SeasonID != "season-1" || episode.SeasonName != "Season 1" || episode.SeriesID != "series-1" || episode.SeriesName != "Series" {
		t.Fatalf("unexpected episode ancestry: %+v", episode)
	}
	flat := metadata["episode-special"]
	if flat.SeasonID != "" || flat.SeriesID != "series-1" || flat.SeriesName != "Series" {
		t.Fatalf("unexpected flat episode ancestry: %+v", flat)
	}
}
