package embyserver

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestAppWithDB(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	schema := `
	CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT, path TEXT, kind TEXT);
	CREATE TABLE items (id TEXT PRIMARY KEY, lib TEXT, parent TEXT, name TEXT, kind TEXT, path TEXT UNIQUE, url TEXT, overview TEXT, poster TEXT, year INT, season INT, episode INT, mtime BIGINT, size BIGINT, seen INT, rating DOUBLE PRECISION DEFAULT 0);
	CREATE TABLE item_metadata (item TEXT PRIMARY KEY, data TEXT);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	app := &App{db: &Database{db}, serverID: "test-server"}
	return app, db
}

func TestPublishDirectMediaMovie(t *testing.T) {
	app, db := setupTestAppWithDB(t)
	defer db.Close()

	_, _ = db.Exec("INSERT INTO libraries VALUES('lib-movies', '电影', '/media/movies', 'movies')")

	movieSTRM := "/media/movies/Inception (2010)/Inception (2010).strm"
	pub := DirectMediaPublication{
		MediaType:  "MOVIE",
		LocalSTRM:  movieSTRM,
		StreamURL:  "http://xiaoya:5678/d/movies/Inception.mp4",
		Title:      "盗梦空间",
		Year:       2010,
		Rating:     8.8,
		Overview:   "盗梦猎人的冒险",
		TMDBID:     "27205",
		Genres:     []string{"科幻", "悬疑"},
		PosterPath: "/media/movies/Inception (2010)/Inception (2010)-poster.jpg",
	}

	if err := app.PublishDirectMedia(pub); err != nil {
		t.Fatalf("PublishDirectMedia failed: %v", err)
	}

	var name, kind, parent string
	var year int
	var rating float64
	err := db.QueryRow("SELECT name, kind, parent, year, rating FROM items WHERE path=?", movieSTRM).Scan(&name, &kind, &parent, &year, &rating)
	if err != nil {
		t.Fatalf("query published movie: %v", err)
	}
	if name != "盗梦空间" || kind != "Movie" || parent != "lib-movies" || year != 2010 || rating != 8.8 {
		t.Errorf("unexpected published movie: name=%q kind=%q parent=%q year=%d rating=%f", name, kind, parent, year, rating)
	}
}

func TestPublishDirectMediaEpisodeHierarchy(t *testing.T) {
	app, db := setupTestAppWithDB(t)
	defer db.Close()

	_, _ = db.Exec("INSERT INTO libraries VALUES('lib-tv', '剧集', '/media/tv', 'tvshows')")

	strmPath := "/media/tv/折月亮 (2026)/Season 1/折月亮 - S01E01 - 第 01 集.strm"
	pub := DirectMediaPublication{
		MediaType:     "EPISODE",
		LocalSTRM:     strmPath,
		StreamURL:     "http://xiaoya:5678/d/tv/zheyueliang/01.mp4",
		Title:         "折月亮",
		Year:          2026,
		SeasonNumber:  1,
		EpisodeNumber: 1,
		EpisodeTitle:  "第 01 集",
		EpisodePlot:   "男女主初遇",
		PosterPath:    "/media/tv/折月亮 (2026)/poster.jpg",
		EpisodeThumb:  "/media/tv/折月亮 (2026)/Season 1/折月亮 - S01E01 - 第 01 集-thumb.jpg",
		Rating:        8.2,
		TMDBID:        "270431",
	}

	if err := app.PublishDirectMedia(pub); err != nil {
		t.Fatalf("PublishDirectMedia episode failed: %v", err)
	}

	seriesDir := filepath.Dir(filepath.Dir(strmPath))
	seasonDir := filepath.Dir(strmPath)

	// Check Series
	var sName, sKind, sParent string
	if err := db.QueryRow("SELECT name, kind, parent FROM items WHERE path=?", seriesDir).Scan(&sName, &sKind, &sParent); err != nil {
		t.Fatalf("query series: %v", err)
	}
	if sName != "折月亮" || sKind != "Series" || sParent != "lib-tv" {
		t.Errorf("unexpected series: %+v", sName)
	}

	// Check Season
	var snName, snKind, snParent string
	var snNum int
	if err := db.QueryRow("SELECT name, kind, parent, season FROM items WHERE path=?", seasonDir).Scan(&snName, &snKind, &snParent, &snNum); err != nil {
		t.Fatalf("query season: %v", err)
	}
	seriesID := digest(seriesDir)[:32]
	if snName != "Season 1" || snKind != "Season" || snParent != seriesID || snNum != 1 {
		t.Errorf("unexpected season: name=%s kind=%s parent=%s season=%d", snName, snKind, snParent, snNum)
	}

	// Check Episode
	var epName, epKind, epParent string
	var epSeason, epEpisode int
	if err := db.QueryRow("SELECT name, kind, parent, season, episode FROM items WHERE path=?", strmPath).Scan(&epName, &epKind, &epParent, &epSeason, &epEpisode); err != nil {
		t.Fatalf("query episode: %v", err)
	}
	seasonID := digest(seasonDir)[:32]
	if epName != "第 01 集" || epKind != "Episode" || epParent != seasonID || epSeason != 1 || epEpisode != 1 {
		t.Errorf("unexpected episode: name=%s kind=%s parent=%s s=%d e=%d", epName, epKind, epParent, epSeason, epEpisode)
	}
}

func TestDirectFlatEpisodeKeepsSeries(t *testing.T) {
	a, db := setupTestAppWithDB(t)
	defer db.Close()
	db.Exec("INSERT INTO libraries VALUES('tv','TV','/media/tv','tvshows')")
	for _, season := range []int{0, 1, 2} {
		p := DirectMediaPublication{MediaType: "EPISODE", LocalSTRM: fmt.Sprintf("/media/tv/Show/S%dE1.strm", season), SeasonNumber: season, EpisodeNumber: 1}
		if e := a.PublishDirectMedia(p); e != nil {
			t.Fatal(e)
		}
	}
	var kind, parent, id string
	if e := db.QueryRow("SELECT id,kind,parent FROM items WHERE path='/media/tv/Show'").Scan(&id, &kind, &parent); e != nil {
		t.Fatal(e)
	}
	if kind != "Series" || parent == id {
		t.Fatalf("invalid series %s %s %s", id, kind, parent)
	}
	var count int
	db.QueryRow("SELECT count(*) FROM items WHERE kind='Season' AND parent=?", id).Scan(&count)
	if count != 3 {
		t.Fatalf("seasons=%d", count)
	}
}

func TestDirectFlatPostgresAndLibraryKind(t *testing.T) {
	a := testApp(t)
	if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('a','Movies','/media/movies','movies'),('z','TV','/media/tv','tvshows')"); e != nil {
		t.Fatal(e)
	}
	for _, season := range []int{0, 1} {
		if e := a.PublishDirectMedia(DirectMediaPublication{MediaType: "EPISODE", LocalSTRM: fmt.Sprintf("/outside/Show/S%dE1.strm", season), SeasonNumber: season, EpisodeNumber: 1}); e != nil {
			t.Fatal(e)
		}
	}
	var kind, lib string
	if e := a.db.QueryRow("SELECT kind,lib FROM items WHERE path='/outside/Show'").Scan(&kind, &lib); e != nil {
		t.Fatal(e)
	}
	if kind != "Series" || lib != "z" {
		t.Fatalf("kind=%s lib=%s", kind, lib)
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM items WHERE kind='Season' AND parent=id").Scan(&n)
	if n != 0 {
		t.Fatal("季层级自引用")
	}
}
