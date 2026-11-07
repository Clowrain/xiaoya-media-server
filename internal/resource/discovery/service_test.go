package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"xiaoya-media-server/internal/resource/xiaoya"
)

type mockSTRMProcessor struct {
	calls int
}

func (m *mockSTRMProcessor) ProcessResource(_ context.Context, _ MonitorRoot, res MediaResource, _ string) (string, error) {
	m.calls++
	return "/media/strm" + res.SourcePath + ".strm", nil
}

func setupTestXiaoyaAndStore(t *testing.T) (string, *sql.DB, *xiaoya.Reader, *SQLStore) {
	t.Helper()
	tmp := t.TempDir()
	xiaoyaPath := filepath.Join(tmp, "strm_internal.db")
	rwXiaoya, err := sql.Open("sqlite", xiaoyaPath)
	if err != nil {
		t.Fatalf("open rw xiaoya: %v", err)
	}
	t.Cleanup(func() { _ = rwXiaoya.Close() })

	if _, err := rwXiaoya.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			parent_id INTEGER NOT NULL DEFAULT 0,
			is_dir BOOLEAN NOT NULL DEFAULT 0,
			content TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatalf("init xiaoya schema: %v", err)
	}

	ins := func(id int64, name string, parentID int64, isDir bool, content string) {
		dirFlag := 0
		if isDir {
			dirFlag = 1
		}
		if _, err := rwXiaoya.Exec("INSERT INTO nodes(id, name, parent_id, is_dir, content) VALUES(?, ?, ?, ?, ?)", id, name, parentID, dirFlag, content); err != nil {
			t.Fatalf("insert node %d: %v", id, err)
		}
	}

	ins(568922, "每日更新", 0, true, "")
	ins(617762, "电视剧", 568922, true, "")
	ins(639780, "国产剧", 617762, true, "")
	ins(641716, "折月亮 (2026)", 639780, true, "")
	ins(641717, "{tmdb-270431}", 641716, true, "")
	ins(641718, "Season 1", 641717, true, "")
	for ep := 1; ep <= 27; ep++ {
		id := int64(641718 + ep)
		ins(id, fmt.Sprintf("E%02d", ep), 641718, false, fmt.Sprintf("http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/%02d.mp4?sign=V1_%02d:0", ep, ep))
	}

	reader, err := xiaoya.OpenReader(xiaoyaPath)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	bizDBPath := filepath.Join(tmp, "business.db")
	bizDB, err := sql.Open("sqlite", bizDBPath)
	if err != nil {
		t.Fatalf("open business db: %v", err)
	}
	t.Cleanup(func() { _ = bizDB.Close() })

	store := NewSQLStore(bizDB, "sqlite")
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return xiaoyaPath, rwXiaoya, reader, store
}

func TestDiscoveryLifecycleBaselineIncrementalDiffMissingRestore(t *testing.T) {
	_, rwXiaoya, reader, store := setupTestXiaoyaAndStore(t)
	proc := &mockSTRMProcessor{}
	svc := NewService(store, reader, proc)
	ctx := context.Background()

	roots, err := svc.RegisterMonitorRoots(ctx, []string{"/每日更新/电视剧/国产剧"}, 10*time.Minute)
	if err != nil || len(roots) != 1 {
		t.Fatalf("RegisterMonitorRoots: %v", err)
	}
	rootID := roots[0].ID

	// 1. Baseline scan: 30 nodes (3 dirs, 27 files), marked IsBaseline=true
	b1, err := svc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("baseline ScanRootFull: %v", err)
	}
	if !b1.IsBaseline || b1.ScanMode != ScanModeBaseline || b1.TotalSeen != 30 || b1.AddedCount != 30 || b1.MaxSourceNodeID != 641745 {
		t.Fatalf("unexpected baseline batch: %+v", b1)
	}
	if proc.calls != 27 {
		t.Fatalf("expected processor called for 27 file nodes, got %d", proc.calls)
	}

	// 2. Unchanged second full scan: idempotent, 0 changes, 0 processor calls
	proc.calls = 0
	b2, err := svc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("unchanged ScanRootFull: %v", err)
	}
	if b2.IsBaseline || b2.AddedCount != 0 || b2.ModifiedCount != 0 || b2.MissingCount != 0 || proc.calls != 0 {
		t.Fatalf("expected idempotent rescan, got batch=%+v procCalls=%d", b2, proc.calls)
	}

	// 3. Append E28 (node 641746) + unrelated node outside root (node 641747) -> Incremental scan
	if _, err := rwXiaoya.Exec("INSERT INTO nodes(id, name, parent_id, is_dir, content) VALUES(641746, 'E28', 641718, 0, 'http://xiaoya.host:5678/d/折月亮/28.mp4')"); err != nil {
		t.Fatalf("insert E28: %v", err)
	}
	if _, err := rwXiaoya.Exec("INSERT INTO nodes(id, name, parent_id, is_dir, content) VALUES(641747, 'OtherMovie.mp4', 568922, 0, 'http://xiaoya.host:5678/d/OtherMovie.mp4')"); err != nil {
		t.Fatalf("insert unrelated node: %v", err)
	}

	b3, err := svc.ScanRootIncremental(ctx, rootID)
	if err != nil {
		t.Fatalf("ScanRootIncremental: %v", err)
	}
	if b3.AddedCount != 1 || b3.MaxSourceNodeID != 641747 || proc.calls != 1 {
		t.Fatalf("unexpected incremental batch: %+v (procCalls=%d)", b3, proc.calls)
	}
	// Verify "newly added after baseline" query returns ONLY E28 (not the 30 baseline nodes)
	addedChanges, err := store.ListChanges(ctx, "", ChangeAdded, 0, 50)
	if err != nil || len(addedChanges) != 1 || addedChanges[0].SourceNodeID != 641746 {
		t.Fatalf("expected exactly 1 post-baseline ADDED change (E28), got %+v (err=%v)", addedChanges, err)
	}

	// 4. Modify E01 content URL AND rename series directory "折月亮 (2026)" -> "折月亮 4K (2026)"
	if _, err := rwXiaoya.Exec("UPDATE nodes SET content = 'http://xiaoya.host:5678/d/折月亮/01_v2.mp4?sign=NEW:0' WHERE id = 641719"); err != nil {
		t.Fatalf("update E01 content: %v", err)
	}
	if _, err := rwXiaoya.Exec("UPDATE nodes SET name = '折月亮 4K (2026)' WHERE id = 641716"); err != nil {
		t.Fatalf("rename series dir: %v", err)
	}

	b4, err := svc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("diff ScanRootFull: %v", err)
	}
	if b4.ModifiedCount != 1 || b4.RenamedCount != 1 || b4.MovedCount != 30 {
		t.Fatalf("expected 1 modified, 1 renamed, 30 moved (2 dirs + 28 episodes), got %+v", b4)
	}
	ep28, ok, err := store.GetResourceBySourceKey(ctx, "sqlite:641746")
	if err != nil || !ok || ep28.RelPath != "折月亮 4K (2026)/{tmdb-270431}/Season 1/E28" {
		t.Fatalf("expected cascaded child RelPath update on E28, got %+v", ep28)
	}

	// 5. Failed scan (e.g. monitor root temporarily unavailable) MUST record FAILED batch,
	// MUST NOT advance cursor, and MUST NOT mark existing resources MISSING!
	curBefore, _, _ := store.GetCursor(ctx, rootID)
	_, _ = rwXiaoya.Exec("UPDATE nodes SET name = '国产剧_离线维护' WHERE id = 639780")
	bFail, err := svc.ScanRootFull(ctx, rootID)
	_, _ = rwXiaoya.Exec("UPDATE nodes SET name = '国产剧' WHERE id = 639780")
	if err == nil || bFail.Status != BatchFailed {
		t.Fatalf("expected failed scan batch Status=FAILED, got batch=%+v err=%v", bFail, err)
	}
	curAfter, _, _ := store.GetCursor(ctx, rootID)
	if curAfter.LastSuccessBatchID != curBefore.LastSuccessBatchID {
		t.Fatalf("failed scan must NEVER advance cursor: before=%s after=%s", curBefore.LastSuccessBatchID, curAfter.LastSuccessBatchID)
	}
	_, missingTotal, _ := store.ListResources(ctx, ResourceFilter{RootID: rootID, Status: StatusMissing})
	if missingTotal != 0 {
		t.Fatalf("failed scan must NEVER mark resources MISSING, got %d", missingTotal)
	}

	// 6. Delete E28 from SQLite -> Complete scan marks E28 MISSING while preserving LocalSTRMPath (`keep` policy)
	if _, err := rwXiaoya.Exec("DELETE FROM nodes WHERE id = 641746"); err != nil {
		t.Fatalf("delete E28: %v", err)
	}
	b5, err := svc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("missing ScanRootFull: %v", err)
	}
	if b5.MissingCount != 1 {
		t.Fatalf("expected MissingCount=1, got %+v", b5)
	}
	ep28Missing, _, _ := store.GetResourceBySourceKey(ctx, "sqlite:641746")
	if ep28Missing.Status != StatusMissing || ep28Missing.LocalSTRMPath == "" {
		t.Fatalf("expected E28 Status=MISSING with preserved LocalSTRMPath, got %+v", ep28Missing)
	}

	// Unconfirmed manual delete must be blocked and return impact preview
	preview, delErr := store.ConfirmDeleteResource(ctx, ep28Missing.ID, false)
	if !errors.Is(delErr, ErrConfirmationRequired) || !preview.RequiresConfirm || preview.LocalSTRMPath == "" {
		t.Fatalf("expected ErrConfirmationRequired with impact preview, got preview=%+v err=%v", preview, delErr)
	}

	// 7. Restore E28 in SQLite -> Complete scan transitions E28 from MISSING back to ACTIVE (RESTORED)
	if _, err := rwXiaoya.Exec("INSERT INTO nodes(id, name, parent_id, is_dir, content) VALUES(641746, 'E28', 641718, 0, 'http://xiaoya.host:5678/d/折月亮/28.mp4')"); err != nil {
		t.Fatalf("re-insert E28: %v", err)
	}
	b6, err := svc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("restore ScanRootFull: %v", err)
	}
	if b6.RestoredCount != 1 {
		t.Fatalf("expected RestoredCount=1, got %+v", b6)
	}
	ep28Restored, _, _ := store.GetResourceBySourceKey(ctx, "sqlite:641746")
	if ep28Restored.Status != StatusActive {
		t.Fatalf("expected restored E28 Status=ACTIVE, got %+v", ep28Restored)
	}
}

func TestReconciliationRollsBackChangesWhenCursorCannotCommit(t *testing.T) {
	_, source, reader, store := setupTestXiaoyaAndStore(t)
	ctx := context.Background()
	s := NewService(store, reader, &mockSTRMProcessor{})
	roots, err := s.RegisterMonitorRoots(ctx, []string{"/每日更新/电视剧/国产剧"}, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	id := roots[0].ID
	if _, err = s.ScanRootFull(ctx, id); err != nil {
		t.Fatal(err)
	}
	previous, _, _ := store.GetCursor(ctx, id)
	if _, err = source.Exec("DELETE FROM nodes WHERE id=641745"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().Exec(`CREATE TRIGGER fail_cursor BEFORE UPDATE ON sync_cursors BEGIN SELECT RAISE(ABORT,'cursor write failed');END;`); err != nil {
		t.Fatal(err)
	}
	b, err := s.ScanRootFull(ctx, id)
	if err == nil || b.Status != BatchFailed {
		t.Fatalf("expected failed batch: %+v %v", b, err)
	}
	got, _, _ := store.GetCursor(ctx, id)
	r, _, _ := store.GetResourceBySourceKey(ctx, "sqlite:641745")
	missing, e := store.ListChanges(ctx, "", ChangeMissing, 0, 50)
	if e != nil || got != previous || r.Status != StatusActive || len(missing) != 0 {
		t.Fatalf("partial snapshot escaped failed transaction: %+v %+v %+v %v", got, r, missing, e)
	}
}

func TestCancelDirectoryManagementPreservesRecordsAndSkipsFutureDescendants(t *testing.T) {
	_, source, reader, store := setupTestXiaoyaAndStore(t)
	ctx := context.Background()
	s := NewService(store, reader, &mockSTRMProcessor{})
	root, err := store.UpsertMonitorRoot(ctx, "/每日更新/电视剧/国产剧", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ScanRootFull(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	dir, _, _ := store.GetResourceBySourceKey(ctx, "sqlite:641716")
	file, _, _ := store.GetResourceBySourceKey(ctx, "sqlite:641719")
	if p, e := s.SetUnmanaged(ctx, dir.ID, true); e != nil || !p.Unmanaged || p.AffectedResources != 30 {
		t.Fatalf("preview=%+v error=%v", p, e)
	}
	if _, err = source.Exec("INSERT INTO nodes VALUES(641746,'E28',641718,0,'http://xiaoya.host:5678/d/E28.mp4')"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ScanRootIncremental(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.GetResourceBySourceKey(ctx, "sqlite:641746"); ok {
		t.Fatal("cancelled subtree accepted new episode")
	}
	kept, err := store.GetResource(ctx, file.ID)
	if err != nil || kept.LocalSTRMPath != file.LocalSTRMPath {
		t.Fatalf("existing file mapping lost: %+v %v", kept, err)
	}
	if err = s.RestoreManagement(ctx, dir.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ScanRootIncremental(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.GetResourceBySourceKey(ctx, "sqlite:641746"); !ok {
		t.Fatal("restored subtree did not catch up")
	}
}

func TestLegacyTrackingMigrationKeepsOnlyConfirmedRoots(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(resourceEngineDDL); err != nil {
		t.Fatal(err)
	}
	store := NewSQLStore(db, "sqlite")
	for _, v := range []struct {
		id, path string
		enabled  bool
	}{
		{"once", "/Once", false}, {"ambiguous", "/Once/Season 1", true},
		{"tracked", "/Tracked", true}, {"implicit", "/Tracked/Season 1", true},
	} {
		if err = store.UpsertResource(ctx, db, MediaResource{ID: v.id, SourceKey: "alist:" + v.path, OriginType: OriginAListManual, SourcePath: v.path, Name: v.path, IsDir: true, Status: StatusActive}); err != nil {
			t.Fatal(err)
		}
		if err = store.UpsertManagedResource(ctx, db, ManagedResource{ResourceID: v.id, ManageMode: ManageManualSeriesDir, Enabled: v.enabled}); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(review bool, want string) {
		t.Helper()
		rs, e := store.TrackedAListRoots(ctx, review)
		if e != nil || len(rs) != 1 || rs[0].ResourceID != want {
			t.Fatalf("review=%t roots=%+v err=%v", review, rs, e)
		}
	}
	check(false, "tracked")
	check(true, "ambiguous")
	if err = store.SetAListTracking(ctx, "ambiguous", true); err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	review, e := store.TrackedAListRoots(ctx, true)
	if e != nil || len(review) != 0 {
		t.Fatalf("restart reintroduced resolved review: %+v %v", review, e)
	}
	active, e := store.TrackedAListRoots(ctx, false)
	if e != nil || len(active) != 2 {
		t.Fatalf("restart lost tracking settings: %+v %v", active, e)
	}
}
