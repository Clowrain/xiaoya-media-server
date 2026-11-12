package embyserver

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
)

func TestSQLCoverRepositoryLifecycle(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("CREATE TABLE covers(id TEXT PRIMARY KEY,mime TEXT NOT NULL,data BLOB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	repository := sqlCoverRepository{db: app.db}
	if exists, err := repository.Exists("cover-a"); err != nil || exists {
		t.Fatalf("missing cover exists=%v err=%v", exists, err)
	}
	want := []byte{0, 1, 2, 255}
	if err := repository.Save(app.db, "cover-a", "image/png", want); err != nil {
		t.Fatal(err)
	}
	if exists, err := repository.Exists("cover-a"); err != nil || !exists {
		t.Fatalf("saved cover exists=%v err=%v", exists, err)
	}
	cover, err := repository.Get("cover-a")
	if err != nil || cover.MIME != "image/png" || !bytes.Equal(cover.Data, want) {
		t.Fatalf("cover=%+v err=%v", cover, err)
	}
	if err := repository.Save(app.db, "cover-a", "image/webp", []byte{3}); err != nil {
		t.Fatal(err)
	}
	cover, err = repository.Get("cover-a")
	if err != nil || cover.MIME != "image/webp" || !bytes.Equal(cover.Data, []byte{3}) {
		t.Fatalf("updated cover=%+v err=%v", cover, err)
	}
	if err := repository.Delete(app.db, "cover-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get("cover-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted cover get error=%v, want sql.ErrNoRows", err)
	}
}

func TestAppCoverEntityExistsAcrossLibraryAndMediaDomains(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('library-a','A','/media/a','movies')"); err != nil {
		t.Fatal(err)
	}
	item := Item{ID: "item-a", Lib: "library-a", Parent: "library-a", Name: "Film", Kind: "Movie"}
	if _, err := app.db.Exec("INSERT INTO items("+cols+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview, item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"library-a", "item-a"} {
		exists, err := app.coverEntityExists(id)
		if err != nil || !exists {
			t.Fatalf("cover entity %q exists=%v err=%v", id, exists, err)
		}
	}
	if exists, err := app.coverEntityExists("missing"); err != nil || exists {
		t.Fatalf("missing cover entity exists=%v err=%v", exists, err)
	}
}
