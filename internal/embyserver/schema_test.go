package embyserver

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func schemaTestDB(t *testing.T) *Database {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return &Database{DB: db}
}

func TestRunSchemaMigrationsAppliesEachVersionOnce(t *testing.T) {
	db := schemaTestDB(t)
	applied := 0
	migrations := []schemaMigration{{
		version: 1,
		name:    "test-baseline",
		apply: func() error {
			applied++
			_, err := db.Exec("CREATE TABLE IF NOT EXISTS baseline_test(id INTEGER PRIMARY KEY)")
			return err
		},
	}}

	if err := runSchemaMigrations(db, migrations); err != nil {
		t.Fatal(err)
	}
	if err := runSchemaMigrations(db, migrations); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("migration applied %d times; want exactly once", applied)
	}
	var version int64
	if err := db.QueryRow("SELECT version FROM app_schema_migrations WHERE version=1").Scan(&version); err != nil || version != 1 {
		t.Fatalf("migration ledger version = %d, err = %v", version, err)
	}
}

func TestRunSchemaMigrationsCanRetryUnrecordedFailure(t *testing.T) {
	db := schemaTestDB(t)
	applyCalls := 0
	wantErr := errors.New("temporary schema failure")
	migrations := []schemaMigration{{
		version: 1,
		name:    "test-baseline",
		apply: func() error {
			applyCalls++
			if applyCalls == 1 {
				return wantErr
			}
			return nil
		},
	}}

	if err := runSchemaMigrations(db, migrations); !errors.Is(err, wantErr) {
		t.Fatalf("first migration error = %v, want %v", err, wantErr)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM app_schema_migrations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration was recorded: count=%d err=%v", count, err)
	}
	if err := runSchemaMigrations(db, migrations); err != nil {
		t.Fatal(err)
	}
	if applyCalls != 2 {
		t.Fatalf("migration retry count = %d, want 2", applyCalls)
	}
}

func TestRunSchemaMigrationsRetriesAfterPartialIdempotentDDL(t *testing.T) {
	db := schemaTestDB(t)
	calls := 0
	migration := schemaMigration{
		version: 1,
		name:    "partial-ddl",
		apply: func() error {
			calls++
			if _, err := db.Exec("CREATE TABLE IF NOT EXISTS partial_test(id INTEGER PRIMARY KEY)"); err != nil {
				return err
			}
			if calls == 1 {
				return errors.New("failure after DDL")
			}
			return nil
		},
	}
	if err := runSchemaMigrations(db, []schemaMigration{migration}); err == nil {
		t.Fatal("expected first attempt to fail after creating the table")
	}
	if err := runSchemaMigrations(db, []schemaMigration{migration}); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM app_schema_migrations WHERE version=1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("successful retry ledger count = %d, err=%v", count, err)
	}
}

func TestAppSchemaAdvisoryLockSerializesAndReleasesAfterFailure(t *testing.T) {
	app := testApp(t)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	firstFailure := errors.New("first schema callback failed")
	go func() {
		firstDone <- app.withSchemaLock(func() error {
			close(firstEntered)
			<-releaseFirst
			return firstFailure
		})
	}()
	<-firstEntered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- app.withSchemaLock(func() error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second schema callback entered while first held advisory lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; !errors.Is(err, firstFailure) {
		t.Fatalf("first lock callback error = %v, want %v", err, firstFailure)
	}
	select {
	case <-secondEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("second schema callback did not enter after lock release")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestRunSchemaMigrationsRejectsUnknownAndGappedVersions(t *testing.T) {
	t.Run("future database version", func(t *testing.T) {
		db := schemaTestDB(t)
		if _, err := db.Exec(`CREATE TABLE app_schema_migrations(version BIGINT PRIMARY KEY, name TEXT NOT NULL, applied_at BIGINT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO app_schema_migrations VALUES(2,'future',0)"); err != nil {
			t.Fatal(err)
		}
		err := runSchemaMigrations(db, []schemaMigration{{version: 1, name: "baseline", apply: func() error { return nil }}})
		if err == nil {
			t.Fatal("expected unsupported future migration version error")
		}
	})

	t.Run("version gap", func(t *testing.T) {
		db := schemaTestDB(t)
		migrations := []schemaMigration{
			{version: 1, name: "baseline", apply: func() error { return nil }},
			{version: 3, name: "gap", apply: func() error { return nil }},
		}
		if err := runSchemaMigrations(db, migrations); err == nil {
			t.Fatal("expected migration sequence gap error")
		}
	})
}
