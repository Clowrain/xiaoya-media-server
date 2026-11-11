package integration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/resource/discovery"
)

func TestManifestSyncPartitionsOverlappingRoots(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "business.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "strm.txt")
	content := "shows/A.strm#http://example.test/a\nshows/sub/B.strm#http://example.test/b\n"
	if err := os.WriteFile(manifest, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Xiaoya.StrmTextPath = manifest
	cfg.STRM.OutputRoot = filepath.Join(dir, "out")
	pipe, err := NewPipeline(ctx, cfg, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	roots, err := pipe.Discovery().RegisterMonitorRoots(ctx, []string{"/shows", "/shows/sub"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	batches, err := pipe.SyncRootsFromManifest(ctx, roots)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("registered %d roots, want 2", len(roots))
	}
	want := map[string]int64{roots[0].ID: 3, roots[1].ID: 2}
	for id, count := range want {
		if got := batches[id].TotalSeen; got != count {
			t.Errorf("root %s saw %d entries, want %d", id, got, count)
		}
	}
}
