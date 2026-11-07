package strm

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
	"xiaoya-media-server/internal/resource/discovery"
)

func TestResourcePathOwnershipSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "biz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := Config{OutputRoot: filepath.Join(dir, "out"), PreserveSourceStructure: true, PathStore: store}
	svc := discovery.NewService(store, nil, NewEngine(cfg))
	insert := func(name string) discovery.MediaResource {
		p := "/Show/" + name
		r, err := svc.RegisterManualResource(ctx, p, p, name, false, "http://xiaoya.host:5678/d"+p, discovery.ManageManualFile, "", "")
		if err != nil || r.LocalSTRMPath == "" {
			t.Fatalf("import %s: %+v %v", p, r, err)
		}
		return r
	}
	mp4 := insert("E01.mp4")
	mkv := insert("E01.mkv")
	if mp4.LocalSTRMPath == mkv.LocalSTRMPath {
		t.Fatal("same output path for different sources")
	}
	svc = discovery.NewService(store, nil, NewEngine(cfg))
	r, err := svc.ReprocessResource(ctx, mkv.ID)
	if err != nil || r.LocalSTRMPath != mkv.LocalSTRMPath {
		t.Fatalf("restart lost mapping: %+v %v", r, err)
	}
	data, err := os.ReadFile(mp4.LocalSTRMPath)
	if err != nil || !strings.HasSuffix(string(data), "/Show/E01.mp4") {
		t.Fatalf("second resource replaced the first: %s %v", data, err)
	}
}
