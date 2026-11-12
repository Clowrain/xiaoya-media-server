package embyserver

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"testing"
)

type fixedMediaItemRepository struct {
	item  Item
	err   error
	calls int
}

func (r *fixedMediaItemRepository) GetByID(string) (Item, error) {
	r.calls++
	return r.item, r.err
}

func (r *fixedMediaItemRepository) Exists(string) (bool, error) {
	return r.item.ID != "" && r.err == nil, r.err
}

func (r *fixedMediaItemRepository) LibraryRootForItem(string) (string, error) {
	return "", r.err
}

func (r *fixedMediaItemRepository) DeleteItemsUnderLibraryPath(itemWriter, string, string) error {
	return r.err
}
func (r *fixedMediaItemRepository) DeleteItemsByLibrary(itemWriter, string) error  { return r.err }
func (r *fixedMediaItemRepository) SetManualName(itemWriter, string, string) error { return r.err }
func (r *fixedMediaItemRepository) DeleteItem(itemWriter, string) error            { return r.err }
func (r *fixedMediaItemRepository) ItemTree(string) ([]Item, error)                { return nil, r.err }
func (r *fixedMediaItemRepository) ItemsAtOrUnderPath(string) ([]ItemPathRecord, error) {
	return nil, r.err
}
func (r *fixedMediaItemRepository) CountByKind(string) (int, error)            { return 0, r.err }
func (r *fixedMediaItemRepository) CountAll() (int, error)                     { return 0, r.err }
func (r *fixedMediaItemRepository) CountPlayableByLibrary(string) (int, error) { return 0, r.err }
func (r *fixedMediaItemRepository) NextEpisode(string, int, int) (Item, error) { return r.item, r.err }

func (r *fixedMediaItemRepository) LibraryImageCandidate(string, string) (Item, error) {
	return r.item, r.err
}

func (r *fixedMediaItemRepository) LibraryCollageCandidates(string) ([]Item, error) {
	return []Item{r.item}, r.err
}

func (r *fixedMediaItemRepository) YearsInLibrary(string) ([]int, error) {
	return nil, r.err
}

func (r *fixedMediaItemRepository) GenreNamesByIDs([]string) ([]string, error) {
	return []string{}, nil
}

func (r *fixedMediaItemRepository) GenreNamesInLibrary(string) ([]string, error) {
	return []string{}, nil
}

func (r *fixedMediaItemRepository) VersionsByGrouping(string, string) ([]Item, error) {
	return []Item{r.item}, r.err
}
func (r *fixedMediaItemRepository) SimilarItems(Item, bool, string, int) ([]Item, error) {
	return []Item{r.item}, r.err
}

func (r *fixedMediaItemRepository) BrowseMetadata([]Item) (map[string]BrowseItemMetadata, error) {
	return map[string]BrowseItemMetadata{}, nil
}

func (r *fixedMediaItemRepository) PersonName(string) (string, error)   { return "", nil }
func (r *fixedMediaItemRepository) PersonItemCount(string) (int, error) { return 0, nil }
func (r *fixedMediaItemRepository) CountPeople(PersonPageQuery) (int, error) {
	return 0, nil
}
func (r *fixedMediaItemRepository) ListPeople(PersonPageQuery) ([]PersonRecord, error) {
	return nil, nil
}
func (r *fixedMediaItemRepository) PersonImageThumb(string) (string, error) { return "", nil }
func (r *fixedMediaItemRepository) PersonImageFailed(string, string, int64) (bool, error) {
	return false, nil
}
func (r *fixedMediaItemRepository) RecordPersonImageFailure(string, string, int64) error {
	return nil
}

func TestAppItemUsesInjectedRepository(t *testing.T) {
	repository := &fixedMediaItemRepository{item: Item{ID: "media-1", Name: "Film", Kind: "Movie"}}
	app := &App{mediaItemRepository: repository}

	item, err := app.item("media-1")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "media-1" || repository.calls != 1 {
		t.Fatalf("repository result was not returned: item=%+v calls=%d", item, repository.calls)
	}
}

func TestSQLMediaItemRepositoryPeopleQueriesAndKeysetPage(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, row := range [][6]string{
		{"item-a", "person-b", "Beta", "", "Actor", ""},
		{"item-a", "person-a", "Alpha", "", "Actor", ""},
		{"item-b", "person-a", "Alpha", "", "Actor", ""},
	} {
		if _, err := app.db.Exec("INSERT INTO item_people VALUES(?,?,?,?,?,?)", row[0], row[1], row[2], row[3], row[4], row[5]); err != nil {
			t.Fatal(err)
		}
	}
	repository := sqlMediaItemRepository{db: app.db}
	name, err := repository.PersonName("person-a")
	if err != nil || name != "Alpha" {
		t.Fatalf("person name = %q, err=%v", name, err)
	}
	itemCount, err := repository.PersonItemCount("person-a")
	if err != nil || itemCount != 2 {
		t.Fatalf("person item count = %d, err=%v", itemCount, err)
	}
	count, err := repository.CountPeople(PersonPageQuery{Where: "1=1"})
	if err != nil || count != 2 {
		t.Fatalf("people count = %d, err=%v", count, err)
	}
	if _, err := app.db.Exec("UPDATE item_people SET thumb=? WHERE person=?", "https://image.tmdb.org/t/p/w185/beta.jpg", "person-b"); err != nil {
		t.Fatal(err)
	}
	thumb, err := repository.PersonImageThumb("person-b")
	if err != nil || thumb != "https://image.tmdb.org/t/p/w185/beta.jpg" {
		t.Fatalf("person image thumb = %q, err=%v", thumb, err)
	}
	imageQuery := PersonPageQuery{Where: "1=1", Limit: 10, RequireAvailableImage: true, ImageCheckedAt: 100}
	withImage, err := repository.ListPeople(imageQuery)
	if err != nil || len(withImage) != 1 || withImage[0].ID != "person-b" {
		t.Fatalf("people with available images = %+v, err=%v", withImage, err)
	}
	imageCount, err := repository.CountPeople(imageQuery)
	if err != nil || imageCount != 1 {
		t.Fatalf("people count with available images = %d, err=%v", imageCount, err)
	}
	if err := repository.RecordPersonImageFailure("person-b", thumb, 200); err != nil {
		t.Fatal(err)
	}
	failed, err := repository.PersonImageFailed("person-b", thumb, 100)
	if err != nil || !failed {
		t.Fatalf("person image failed = %v, err=%v", failed, err)
	}
	expired, err := repository.PersonImageFailed("person-b", thumb, 200)
	if err != nil || expired {
		t.Fatalf("expired person image failure = %v, err=%v", expired, err)
	}
	withImage, err = repository.ListPeople(imageQuery)
	if err != nil || len(withImage) != 0 {
		t.Fatalf("failed person image should be hidden: %+v, err=%v", withImage, err)
	}
	first, err := repository.ListPeople(PersonPageQuery{Where: "1=1", Limit: 1})
	if err != nil || len(first) != 2 || first[0].ID != "person-a" || first[1].ID != "person-b" {
		t.Fatalf("people first page = %+v, err=%v", first, err)
	}
	second, err := repository.ListPeople(PersonPageQuery{Where: "1=1", AfterName: first[0].Name, AfterID: first[0].ID, Limit: 1})
	if err != nil || len(second) != 1 || second[0].ID != "person-b" {
		t.Fatalf("people keyset page = %+v, err=%v", second, err)
	}
}

func TestSQLMediaItemRepositoryReadsItem(t *testing.T) {
	app := newTestSQLiteApp(t)
	item := Item{
		ID: "media-2", Lib: "library-1", Parent: "parent-1", Name: "Film", Kind: "Movie",
		Path: "/media/film.strm", URL: "https://media.example/film.mkv", Overview: "Summary",
		Poster: "/media/poster.jpg", Year: 2024, Season: 2, Episode: 3, Mtime: 100, Size: 200,
	}
	if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
		item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview,
		item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
		t.Fatal(err)
	}

	got, err := (sqlMediaItemRepository{db: app.db}).GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != item {
		t.Fatalf("GetByID() = %+v, want %+v", got, item)
	}
	if _, err := (sqlMediaItemRepository{db: app.db}).GetByID("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing item error = %v, want sql.ErrNoRows", err)
	}
}

func TestBrowseFiltersResolveGenreIDsThroughRepository(t *testing.T) {
	app := newTestSQLiteApp(t)
	genre := "Drama"
	if _, err := app.db.Exec("INSERT INTO item_genres(item,genre) VALUES(?,?)", "media-1", genre); err != nil {
		t.Fatal(err)
	}
	genreID := "genre-" + digest(genre)[:32]
	request := httptest.NewRequest("GET", "/Items?GenreIds="+genreID, nil)

	where, args := app.browseFilters(request, User{ID: "test-user"}, "1=1", nil, false)
	if q(request, "Genres") != genre || len(args) != 1 || args[0] != genre {
		t.Fatalf("genre IDs were not translated through the repository: where=%q args=%v query=%q", where, args, q(request, "Genres"))
	}
}

func TestSQLMediaItemRepositoryFindsSimilarItems(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, item := range []Item{
		{ID: "source", Lib: "lib", Parent: "lib", Name: "Source", Kind: "Movie", Year: 2020},
		{ID: "genre-match", Lib: "lib", Parent: "lib", Name: "Genre match", Kind: "Movie", Year: 2021},
		{ID: "year-match", Lib: "lib", Parent: "lib", Name: "Year match", Kind: "Movie", Year: 2020},
		{ID: "other-kind", Lib: "lib", Parent: "lib", Name: "Episode", Kind: "Episode", Year: 2020},
	} {
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview, item.Poster,
			item.Year, item.Season, item.Episode, item.Mtime, item.Size, "t"); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range [][2]string{{"source", "Drama"}, {"genre-match", "Drama"}} {
		if _, err := app.db.Exec("INSERT INTO item_genres(item,genre) VALUES(?,?)", relation[0], relation[1]); err != nil {
			t.Fatal(err)
		}
	}
	items, err := (sqlMediaItemRepository{db: app.db}).SimilarItems(Item{ID: "source", Kind: "Movie", Year: 2020}, true, "id", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "genre-match" || items[1].ID != "year-match" {
		t.Fatalf("similar items = %+v", items)
	}
}

func TestSQLMediaItemRepositoryListsGenresByLibrary(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, item := range []struct{ id, library, genre string }{
		{"genre-a", "lib-a", "Drama"},
		{"genre-b", "lib-a", "Comedy"},
		{"genre-c", "lib-b", "Documentary"},
		{"genre-a2", "lib-a", "Drama"},
	} {
		if _, err := app.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,seen) VALUES(?,?,?,'name','Movie',?,'t')", item.id, item.library, item.library, "/media/"+item.id); err != nil {
			t.Fatal(err)
		}
		if _, err := app.db.Exec("INSERT INTO item_genres(item,genre) VALUES(?,?)", item.id, item.genre); err != nil {
			t.Fatal(err)
		}
	}
	repository := sqlMediaItemRepository{db: app.db}
	names, err := repository.GenreNamesInLibrary("lib-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "Comedy" || names[1] != "Drama" {
		t.Fatalf("unexpected library genres: %v", names)
	}
	all, err := repository.GenreNamesInLibrary("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0] != "Comedy" || all[1] != "Documentary" || all[2] != "Drama" {
		t.Fatalf("unexpected all-library genres: %v", all)
	}
}
