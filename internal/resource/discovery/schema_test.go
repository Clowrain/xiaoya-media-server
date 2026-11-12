package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"xiaoya-media-server/migrations"
)

func openSchemaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func openPostgresSchemaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for PostgreSQL schema tests")
	}
	base, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Ping(); err != nil {
		base.Close()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_discovery_%d", time.Now().UnixNano())
	if _, err := base.Exec("CREATE SCHEMA " + schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = base.Exec("DROP SCHEMA " + schema + " CASCADE")
		_ = base.Close()
	})

	testDSN := dsn + " search_path=" + schema + ",public"
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema+",public")
		parsed.RawQuery = query.Encode()
		testDSN = parsed.String()
	}
	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestEnsureSchemaPostgresBackfillsLegacyLifecycleOnce(t *testing.T) {
	db := openPostgresSchemaTestDB(t)
	ctx := context.Background()
	store := NewSQLStore(db, "postgres")
	if _, err := db.Exec(migrations.ResourceEngine); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []struct {
		id, path, mode, status string
		dir, enabled           bool
	}{
		{"tracked", "/Tracked", ManageManualSeriesDir, StatusActive, true, true},
		{"tracked-child", "/Tracked/Season 1", ManageManualSeriesDir, StatusActive, true, true},
		{"once", "/Once", ManageManualSeriesDir, StatusActive, true, false},
		{"ambiguous", "/Once/Season 1", ManageManualSeriesDir, StatusActive, true, true},
		{"suppressed", "/Suppressed/movie.mkv", ManageManualFile, StatusActive, false, false},
		{"failed-file", "/Failed/movie.mkv", ManageManualFile, StatusFailed, false, false},
	} {
		if err := store.UpsertResource(ctx, db, MediaResource{
			ID: resource.id, SourceKey: "alist:" + resource.path, OriginType: OriginAListManual,
			SourcePath: resource.path, Name: resource.path, IsDir: resource.dir, Status: resource.status,
		}); err != nil {
			t.Fatal(err)
		}
		if resource.status == StatusFailed {
			continue
		}
		if err := store.UpsertManagedResource(ctx, db, ManagedResource{
			ResourceID: resource.id, ManageMode: resource.mode, Enabled: resource.enabled,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	active, err := store.TrackedAListRoots(ctx, false)
	if err != nil || len(active) != 1 || active[0].ResourceID != "tracked" {
		t.Fatalf("active legacy roots = %+v, err=%v", active, err)
	}
	review, err := store.TrackedAListRoots(ctx, true)
	if err != nil || len(review) != 1 || review[0].ResourceID != "ambiguous" {
		t.Fatalf("review legacy roots = %+v, err=%v", review, err)
	}
	var suppressions, pending int
	if err := db.QueryRow("SELECT count(*) FROM resource_suppressions WHERE resource_id='suppressed'").Scan(&suppressions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pending_materialization WHERE resource_id='failed-file'").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if suppressions != 1 || pending != 1 {
		t.Fatalf("backfill rows: suppressions=%d pending=%d", suppressions, pending)
	}

	if err := store.SetAListTracking(ctx, "ambiguous", true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM resource_suppressions WHERE resource_id='suppressed'"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	review, err = store.TrackedAListRoots(ctx, true)
	if err != nil || len(review) != 0 {
		t.Fatalf("resolved review was restored on startup: %+v, err=%v", review, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM resource_suppressions WHERE resource_id='suppressed'").Scan(&suppressions); err != nil || suppressions != 0 {
		t.Fatalf("one-time migration restored removed suppression: count=%d err=%v", suppressions, err)
	}
}

func TestEnsureSchemaPostgresRejectsFutureResourceVersion(t *testing.T) {
	db := openPostgresSchemaTestDB(t)
	if _, err := db.Exec(`CREATE TABLE media_pipeline_schema (
		component TEXT PRIMARY KEY, version BIGINT NOT NULL,
		created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO media_pipeline_schema VALUES($1,$2,$3,$4)", "xiaoya-media-pipeline", 3, 1, 1); err != nil {
		t.Fatal(err)
	}
	err := NewSQLStore(db, "postgres").EnsureSchema(context.Background())
	if err == nil || !strings.Contains(err.Error(), "newer than supported version") {
		t.Fatalf("future resource schema version should fail closed: %v", err)
	}
	var tableName sql.NullString
	if err := db.QueryRow("SELECT to_regclass('monitor_roots')").Scan(&tableName); err != nil || tableName.Valid {
		t.Fatalf("future schema version applied older DDL: table=%v err=%v", tableName, err)
	}
}

func TestEnsureSchemaPostgresLifecycleFailureRollsBackAndRetries(t *testing.T) {
	db := openPostgresSchemaTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE resource_migrations(id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE FUNCTION fail_resource_migration_marker() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected lifecycle marker failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_resource_migration_marker BEFORE INSERT ON resource_migrations
		FOR EACH ROW EXECUTE FUNCTION fail_resource_migration_marker()`); err != nil {
		t.Fatal(err)
	}
	store := NewSQLStore(db, "postgres")
	if err := store.EnsureSchema(ctx); err == nil || !strings.Contains(err.Error(), "injected lifecycle marker failure") {
		t.Fatalf("injected lifecycle failure = %v", err)
	}
	var lifecycleTable, migrationMarker sql.NullString
	if err := db.QueryRow("SELECT to_regclass('alist_tracking_roots')").Scan(&lifecycleTable); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT id FROM resource_migrations WHERE id='alist_tracking_roots_v1'").Scan(&migrationMarker); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed lifecycle migration left marker %q; query error=%v", migrationMarker.String, err)
	}
	var recordedVersion int
	if err := db.QueryRow("SELECT count(*) FROM media_pipeline_schema WHERE component='xiaoya-media-pipeline'").Scan(&recordedVersion); err != nil || recordedVersion != 0 {
		t.Fatalf("failed lifecycle migration recorded resource version: count=%d err=%v", recordedVersion, err)
	}
	if lifecycleTable.Valid {
		t.Fatalf("failed lifecycle transaction left table %q", lifecycleTable.String)
	}

	if _, err := db.Exec("DROP TRIGGER fail_resource_migration_marker ON resource_migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP FUNCTION fail_resource_migration_marker()"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("retry after rolled-back lifecycle migration failed: %v", err)
	}
	if err := db.QueryRow("SELECT id FROM resource_migrations WHERE id='alist_tracking_roots_v1'").Scan(&migrationMarker); err != nil {
		t.Fatalf("lifecycle marker missing after retry: %v", err)
	}
	if err := db.QueryRow("SELECT version FROM media_pipeline_schema WHERE component='xiaoya-media-pipeline'").Scan(&recordedVersion); err != nil || recordedVersion != 2 {
		t.Fatalf("resource version after retry: version=%d err=%v", recordedVersion, err)
	}
}

func TestEnsureSchemaRecordsResourceVersionWithoutRewritingIt(t *testing.T) {
	db := openSchemaTestDB(t)
	store := NewSQLStore(db, "sqlite")
	ctx := context.Background()

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := db.QueryRow("SELECT version FROM media_pipeline_schema WHERE component=?", "xiaoya-media-pipeline").Scan(&version); err != nil || version != 2 {
		t.Fatalf("resource schema version = %d, err = %v", version, err)
	}
	if _, err := db.Exec("UPDATE media_pipeline_schema SET updated_at=123 WHERE component=?", "xiaoya-media-pipeline"); err != nil {
		t.Fatal(err)
	}

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var updatedAt int64
	if err := db.QueryRow("SELECT updated_at FROM media_pipeline_schema WHERE component=?", "xiaoya-media-pipeline").Scan(&updatedAt); err != nil || updatedAt != 123 {
		t.Fatalf("already applied schema version was rewritten: updated_at=%d err=%v", updatedAt, err)
	}
}

func TestEnsureSchemaRejectsUnknownFutureResourceVersion(t *testing.T) {
	db := openSchemaTestDB(t)
	if _, err := db.Exec(`CREATE TABLE media_pipeline_schema (
		component TEXT PRIMARY KEY,
		version BIGINT NOT NULL,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO media_pipeline_schema(component,version,created_at,updated_at) VALUES(?,?,?,?)", "xiaoya-media-pipeline", 3, 1, 1); err != nil {
		t.Fatal(err)
	}

	err := NewSQLStore(db, "sqlite").EnsureSchema(context.Background())
	if err == nil || !strings.Contains(err.Error(), "newer than supported version") {
		t.Fatalf("future resource schema version should fail closed: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='monitor_roots'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unknown future version should not apply older DDL: count=%d err=%v", count, err)
	}
}
