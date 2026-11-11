package embyserver

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"xiaoya-media-server/internal/resource/discovery"
)

func TestInventoryScopedCommitAndLegacyMigration(t *testing.T) {
	a := testApp(t)
	if _, e := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('l','test','/media','movies')"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec(`INSERT INTO scan_snapshots VALUES('l','{"/media/a.strm":{"Size":1,"Mtime":2},"/media/b.strm":{"Size":3,"Mtime":4}}')`); e != nil {
		t.Fatal(e)
	}
	if e := a.postgresSchema(); e != nil {
		t.Fatal(e)
	}
	s := &scanInventory{app: a, lib: "l", generation: id()}
	defer s.close()
	if same, e := s.unchanged("/media/b.strm", fileStamp{3, 4}, "missing"); e != nil || same {
		t.Fatalf("missing item must not be skipped: %v %v", same, e)
	}
	if _, e := a.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,seen) VALUES('b','l','l','B','Movie','/media/b.strm','g')"); e != nil {
		t.Fatal(e)
	}
	if same, e := s.unchanged("/media/b.strm", fileStamp{3, 4}, "b"); e != nil || !same {
		t.Fatalf("migration: %v %v", same, e)
	}
	var prior string
	a.db.QueryRow("SELECT xmin::text FROM scan_file_stamps WHERE path='/media/b.strm'").Scan(&prior)
	if e := s.add("/media/a.strm", fileStamp{5, 6}); e != nil {
		t.Fatal(e)
	}
	if e := s.flush(); e != nil {
		t.Fatal(e)
	}
	if e := s.commit(false, []string{"/media/a.strm"}); e != nil {
		t.Fatal(e)
	}
	var after string
	a.db.QueryRow("SELECT xmin::text FROM scan_file_stamps WHERE path='/media/b.strm'").Scan(&after)
	if prior != after {
		t.Fatal("局部提交改写了其他路径")
	}
	if same, e := s.unchanged("/media/a.strm", fileStamp{5, 6}, "a"); e != nil || same {
		t.Fatalf("missing item with new stamp must be reindexed: %v %v", same, e)
	}
	if _, e := a.db.Exec("INSERT INTO items(id,lib,parent,name,kind,path,seen) VALUES('a','l','l','A','Movie','/media/a.strm','g')"); e != nil {
		t.Fatal(e)
	}
	if same, e := s.unchanged("/media/a.strm", fileStamp{5, 6}, "a"); e != nil || !same {
		t.Fatalf("new stamp and existing item: %v %v", same, e)
	}
	missing := &scanInventory{app: a, lib: "l", generation: id()}
	defer missing.close()
	if e := missing.commit(true, []string{"/media/a.strm"}); e != nil {
		t.Fatal(e)
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM scan_file_stamps WHERE lib='l'").Scan(&n)
	if n != 1 {
		t.Fatalf("remaining stamps=%d", n)
	}
}

func TestInventoryScale400KScoped(t *testing.T) {
	if os.Getenv("SCALE_TEST") != "1" {
		t.Skip("SCALE_TEST=1 enables 400k snapshot test")
	}
	a := testApp(t)
	a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('l','test','/media','movies')")
	start := time.Now()
	if _, e := a.db.Exec("INSERT INTO scan_file_stamps SELECT 'l','/media/'||n||'.strm',1,2 FROM generate_series(1,400000)n"); e != nil {
		t.Fatal(e)
	}
	t.Logf("400k snapshot seed: %s", time.Since(start))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start = time.Now()
	s := &scanInventory{app: a, lib: "l", generation: id()}
	defer s.close()
	if e := s.add("/media/1.strm", fileStamp{2, 3}); e != nil {
		t.Fatal(e)
	}
	if e := s.flush(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.changedSidecars([]string{"/media/1.strm"}); e != nil {
		t.Fatal(e)
	}
	if e := s.commit(false, []string{"/media/1.strm"}); e != nil {
		t.Fatal(e)
	}
	runtime.ReadMemStats(&after)
	t.Logf("400k scoped update: %s alloc=%.2f MiB", time.Since(start), float64(after.TotalAlloc-before.TotalAlloc)/(1<<20))
	if after.TotalAlloc-before.TotalAlloc > 8<<20 {
		t.Fatal("局部更新分配超过 8 MiB")
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM scan_file_stamps WHERE lib='l'").Scan(&n)
	if n != 400000 {
		t.Fatal(n)
	}
}

func TestManualStreamPostgresMissingAndBoundedBatches(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	store := discovery.NewSQLStore(a.SQLDB(), "postgres")
	svc := discovery.NewService(store, nil, nil)
	roots, e := svc.RegisterMonitorRoots(ctx, []string{"/show"}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	m := discovery.ManualSnapshot{RootPath: "/show", OriginType: discovery.OriginXiaoyaSQLite, Mode: discovery.ManageManualSeriesDir}
	read := func(n int) func(func(discovery.ManualNode) error) error {
		return func(yield func(discovery.ManualNode) error) error {
			for i := 0; i < n; i++ {
				path := fmt.Sprintf("/show/%03d.strm", i)
				if e := yield(discovery.ManualNode{Path: path, RelPath: path[1:], Name: fmt.Sprintf("%03d.strm", i), Content: "https://example.com/a"}); e != nil {
					return e
				}
			}
			return nil
		}
	}
	if _, e = svc.ApplyManualStream(ctx, m, roots[0].ID, read(401), nil); e != nil {
		t.Fatal(e)
	}
	b, e := svc.ApplyManualStream(ctx, m, roots[0].ID, read(1), nil)
	if e != nil {
		t.Fatal(e)
	}
	if b.MissingCount != 400 {
		t.Fatalf("missing=%d", b.MissingCount)
	}
	b, e = svc.ApplyManualStream(ctx, m, roots[0].ID, read(401), nil)
	if e != nil {
		t.Fatal(e)
	}
	if b.RestoredCount != 400 {
		t.Fatalf("restored=%d", b.RestoredCount)
	}
}
