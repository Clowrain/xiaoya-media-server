package integration

import (
	"context"
	"database/sql"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"testing"
	"time"
	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/resource/discovery"
)

func TestManifestRemovalAndFailurePropagation(t *testing.T) {
	ctx := context.Background()
	d := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(d, "biz.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := discovery.NewSQLStore(db, "sqlite")
	if e = s.EnsureSchema(ctx); e != nil {
		t.Fatal(e)
	}
	cfg := &config.Config{}
	cfg.STRM.OutputRoot = filepath.Join(d, "out")
	cfg.Xiaoya.SQLitePath = filepath.Join(d, "strm_internal.db")
	manifest := filepath.Join(d, "strm.txt")
	os.WriteFile(manifest, []byte("movies/A.strm#http://example.com/a\nmovies/B.strm#http://example.com/b\n"), 0600)
	p, e := NewPipeline(ctx, cfg, nil, s)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	roots, e := p.Discovery().RegisterMonitorRoots(ctx, []string{"/movies"}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.SyncRootsFromManifest(ctx, roots); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(manifest, []byte("movies/B.strm#http://example.com/b\n"), 0600)
	if _, e = p.RunFullSyncRoot(ctx, roots[0].ID); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = db.QueryRow("SELECT status FROM media_resources WHERE source_path='/movies/A.strm'").Scan(&status); e != nil {
		t.Fatal(e)
	}
	t.Logf("removed A after full reconcile: status=%s", status)
	if status != "MISSING" {
		t.Errorf("want MISSING, got %s", status)
	}

	if e = os.WriteFile(manifest, []byte("movies/A.strm#http://example.com/a\nmovies/B.strm#http://example.com/b\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = p.RunFullSyncRoot(ctx, roots[0].ID); e != nil {
		t.Fatal(e)
	}
	db.QueryRow("SELECT status FROM media_resources WHERE source_path='/movies/A.strm'").Scan(&status)
	if status != "ACTIVE" {
		t.Fatal("资源未恢复", status)
	}
	os.WriteFile(manifest, []byte("movies/B.strm#http://example.com/b\nbroken line\n"), 0600)
	if _, e = p.RunFullSyncRoot(ctx, roots[0].ID); e == nil {
		t.Fatal("损坏清单未报错")
	}
	db.QueryRow("SELECT status FROM media_resources WHERE source_path='/movies/A.strm'").Scan(&status)
	if status != "ACTIVE" {
		t.Fatal("损坏清单误标缺失", status)
	}
	os.WriteFile(manifest, []byte("unrelated/B.strm#http://example.com/b\n"), 0600)
	if _, e = p.RunFullSyncRoot(ctx, roots[0].ID); e != nil {
		t.Fatal(e)
	}
	db.QueryRow("SELECT status FROM media_resources WHERE source_path='/movies/A.strm'").Scan(&status)
	if status != "MISSING" {
		t.Fatal("空根未对账", status)
	}
	root, e := s.GetMonitorRoot(ctx, roots[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	if root.SourceNodeID != 0 || root.LastScannedAt == 0 {
		t.Fatalf("空根状态错误: %+v", root)
	}
	db.Close()
	_, e = p.SyncRootsFromManifest(ctx, roots)
	t.Logf("closed database sync error=%v", e)
	if e == nil {
		t.Error("failed sync returned success")
	}
}
