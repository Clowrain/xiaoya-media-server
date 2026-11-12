package embyserver

import "testing"

func TestSQLLibraryRepositoryManagementWrites(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("CREATE TABLE library_order(library_id TEXT PRIMARY KEY,position INT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := app.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	repository := sqlLibraryRepository{db: app.db}
	if err := repository.Create(tx, LibraryRecord{ID: "managed", Name: "Movies", Path: "/media/movies", Kind: "movies"}); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateDetails(tx, "managed", "Films", "tvshows"); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdatePath(tx, "managed", "/media/films"); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetOrderPosition(tx, "managed", 3); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	library, err := repository.GetByID("managed")
	if err != nil || library.Name != "Films" || library.Kind != "tvshows" || library.Path != "/media/films" {
		t.Fatalf("unexpected managed library: %+v err=%v", library, err)
	}
	if err := repository.DeleteOrderPosition(app.db, "managed"); err != nil {
		t.Fatal(err)
	}
	result, err := repository.Delete(app.db, "managed")
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("deleted rows = %d, err=%v", count, err)
	}
}

func TestSQLLibraryRepositoryScanStatus(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('scan-lib','Scan','/media/scan','movies')"); err != nil {
		t.Fatal(err)
	}
	repository := sqlLibraryRepository{db: app.db}
	if err := repository.SetScanStatus("scan-lib", "scanning", ""); err != nil {
		t.Fatal(err)
	}
	if count, err := repository.CountByStatus("scanning"); err != nil || count != 1 {
		t.Fatalf("scanning library count = %d, err=%v", count, err)
	}
	if err := repository.CompleteScan("scan-lib", "error", "failed", 7, 1.5, 123); err != nil {
		t.Fatal(err)
	}
	library, err := repository.GetByID("scan-lib")
	if err != nil {
		t.Fatal(err)
	}
	if library.Status != "error" || library.Error != "failed" || library.Count != 7 || library.Duration != 1.5 || library.Scanned != 123 {
		t.Fatalf("unexpected scan state: %+v", library)
	}
}

func TestSQLLibraryRepositoryListAndExistence(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("CREATE TABLE library_order(library_id TEXT PRIMARY KEY,position INT)"); err != nil {
		t.Fatal(err)
	}
	for _, library := range []LibraryRecord{
		{ID: "library-b", Name: "Beta", Path: "/media/b", Kind: "movies", Status: "idle", Count: 2},
		{ID: "library-a", Name: "Alpha", Path: "/media/a", Kind: "tvshows", Status: "idle", Count: 3},
	} {
		if _, err := app.db.Exec("INSERT INTO libraries(id,name,path,kind,status,count) VALUES(?,?,?,?,?,?)", library.ID, library.Name, library.Path, library.Kind, library.Status, library.Count); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.db.Exec("INSERT INTO library_order(library_id,position) VALUES(?,?)", "library-b", 0); err != nil {
		t.Fatal(err)
	}

	repository := sqlLibraryRepository{db: app.db}
	libraries, err := repository.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(libraries) != 2 || libraries[0].ID != "library-b" || libraries[1].ID != "library-a" {
		t.Fatalf("libraries were not returned in configured order: %+v", libraries)
	}
	record, err := repository.GetByID("library-a")
	if err != nil || record.Name != "Alpha" || record.Path != "/media/a" || record.Kind != "tvshows" {
		t.Fatalf("unexpected library record: %+v err=%v", record, err)
	}
	if exists, err := repository.Exists("library-a"); err != nil || !exists {
		t.Fatalf("existing library lookup = %t, err=%v", exists, err)
	}
	if exists, err := repository.Exists("missing"); err != nil || exists {
		t.Fatalf("missing library lookup = %t, err=%v", exists, err)
	}
	if count, err := repository.Count(); err != nil || count != 2 {
		t.Fatalf("library count = %d, err=%v", count, err)
	}
	tx, err := app.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if count, err := repository.CountWith(tx); err != nil || count != 2 {
		t.Fatalf("transactional library count = %d, err=%v", count, err)
	}
}
