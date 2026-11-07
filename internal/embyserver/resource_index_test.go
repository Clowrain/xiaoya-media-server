package embyserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"xiaoya-media-server/internal/resource/discovery"
)

func TestKeptResourceStaysMissingAfterMediaScanAndRestoresInPlace(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOTS", root)
	p := filepath.Join(root, "KeepMe.strm")
	if err := os.WriteFile(p, []byte("https://example.invalid/KeepMe.mp4"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('l','fixture',?,'movies')", root); err != nil {
		t.Fatal(err)
	}
	a.scanLibraryMode("l", false)
	var item, user string
	if err := a.db.QueryRow("SELECT id FROM items WHERE path=?", p).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO userdata(user_id,item,position) VALUES(?,?,123)", user, item); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := discovery.NewSQLStore(a.db.DB, "postgres")
	svc := discovery.NewService(store, nil, nil)
	n := discovery.ManualNode{Path: "/Show/KeepMe.mp4", Name: "KeepMe.mp4", Content: "https://example.invalid/KeepMe.mp4"}
	m := discovery.ManualSnapshot{RootPath: "/Show", Mode: discovery.ManageManualSeriesDir, Nodes: []discovery.ManualNode{n}}
	_, rs, err := svc.ApplyManualSnapshot(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	r.LocalSTRMPath = p
	if err = store.UpsertResource(ctx, a.db.DB, r); err != nil {
		t.Fatal(err)
	}
	m.Nodes = nil
	m.Missing = []string{n.Path}
	if _, _, err = svc.ApplyManualSnapshot(ctx, m); err != nil {
		t.Fatal(err)
	}
	a.scanLibraryMode("l", true)
	check := func(want string) {
		t.Helper()
		var got, id string
		var pos int
		err := a.db.QueryRow("SELECT i.availability,i.id,u.position FROM items i JOIN userdata u ON u.item=i.id WHERE i.path=?", p).Scan(&got, &id, &pos)
		if err != nil || got != want || id != item || pos != 123 {
			t.Fatalf("got status=%q id=%q pos=%d err=%v, want status=%q same id and progress", got, id, pos, err, want)
		}
	}
	check("MISSING")
	m.Missing = nil
	m.Nodes = []discovery.ManualNode{n}
	if _, _, err = svc.ApplyManualSnapshot(ctx, m); err != nil {
		t.Fatal(err)
	}
	check("AVAILABLE")
}

func TestAutoImportScansSecondaryLibraryPath(t *testing.T) {
	a := testApp(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEDIA_ROOTS", root)
	primary, secondary := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, p := range []string{primary, secondary} {
		if err = os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('l','fixture',?,'movies')", primary); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("INSERT INTO settings VALUES('library-paths:l',?)", `["`+primary+`","`+secondary+`"]`); err != nil {
		t.Fatal(err)
	}
	target, err := a.ResolveImportTarget("l", secondary)
	if err != nil || target != secondary {
		t.Fatalf("target=%q err=%v", target, err)
	}
	p := filepath.Join(secondary, "Imported.strm")
	if err = os.WriteFile(p, []byte("https://example.invalid/Imported.mp4"), 0644); err != nil {
		t.Fatal(err)
	}
	a.TriggerScopedPathScan(secondary)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err = a.db.QueryRow("SELECT count(*) FROM items WHERE path=?", p).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("automatic scan did not index the configured secondary directory")
}
