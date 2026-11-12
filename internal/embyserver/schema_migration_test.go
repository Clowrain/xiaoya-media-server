package embyserver

import "testing"

func TestSchemaBaselineBootstrapsExistingDatabase(t *testing.T) {
	a := testApp(t)
	var userID string
	if err := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&userID); err != nil {
		t.Fatal(err)
	}

	libraryID := "migration-legacy-library"
	if _, err := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES(?,?,?,?)", libraryID, "Legacy", "/legacy", "movies"); err != nil {
		t.Fatal(err)
	}
	legacySnapshot := `{"legacy/movie.mp4":{"Size":123,"Mtime":456}}`
	if _, err := a.db.Exec("INSERT INTO scan_snapshots(lib,data) VALUES(?,?)", libraryID, legacySnapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("DELETE FROM app_schema_migrations"); err != nil {
		t.Fatal(err)
	}

	if err := a.ensureSchema(); err != nil {
		t.Fatalf("bootstrap existing database: %v", err)
	}
	var version int64
	var migrationName string
	if err := a.db.QueryRow("SELECT version,name FROM app_schema_migrations").Scan(&version, &migrationName); err != nil {
		t.Fatal(err)
	}
	if version != 1 || migrationName != "legacy-app-schema-bootstrap" {
		t.Fatalf("unexpected schema baseline record: version=%d name=%q", version, migrationName)
	}

	var size, mtime int64
	if err := a.db.QueryRow("SELECT size,mtime FROM scan_file_stamps WHERE lib=? AND path=?", libraryID, "legacy/movie.mp4").Scan(&size, &mtime); err != nil {
		t.Fatal(err)
	}
	if size != 123 || mtime != 456 {
		t.Fatalf("legacy scan snapshot was not preserved: size=%d mtime=%d", size, mtime)
	}
	var snapshot string
	if err := a.db.QueryRow("SELECT data FROM scan_snapshots WHERE lib=?", libraryID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot != "{}" {
		t.Fatalf("legacy snapshot should be cleared only after import, got %s", snapshot)
	}
	var retainedUser string
	if err := a.db.QueryRow("SELECT id FROM users WHERE id=?", userID).Scan(&retainedUser); err != nil || retainedUser != userID {
		t.Fatalf("existing user was not preserved: id=%q err=%v", retainedUser, err)
	}

	if err := a.ensureSchema(); err != nil {
		t.Fatalf("repeat existing database startup: %v", err)
	}
	var stampCount int
	if err := a.db.QueryRow("SELECT count(*) FROM scan_file_stamps WHERE lib=? AND path=?", libraryID, "legacy/movie.mp4").Scan(&stampCount); err != nil || stampCount != 1 {
		t.Fatalf("legacy stamp should remain idempotent after restart: count=%d err=%v", stampCount, err)
	}
}
