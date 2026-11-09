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

func TestSQLiteEpisodeMaterializesStandardSTRMAndNFO(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "biz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(Config{OutputRoot: filepath.Join(root, "out"), PreserveSourceStructure: true, PathStore: store, TargetBaseURL: "http://xiaoya.host:5678"})
	svc := discovery.NewService(store, nil, engine)
	_, rs, err := svc.ApplyManualSnapshot(ctx, discovery.ManualSnapshot{
		OriginType: discovery.OriginXiaoyaSQLite,
		RootPath:   "/每日更新/电视剧/国产剧/折月亮",
		LibraryID:  "lib",
		OutputDir:  filepath.Join(root, "out"),
		Nodes: []discovery.ManualNode{{
			Path: "/每日更新/电视剧/国产剧/折月亮/01.strm", RelPath: "每日更新/电视剧/国产剧/折月亮/01.strm", Name: "01.strm",
			Content: "http://xiaoya.host:5678/d/折月亮/01.mp4", SourceNodeID: 101,
		}},
	})
	if err != nil || len(rs) != 1 {
		t.Fatalf("apply snapshot: resources=%d err=%v", len(rs), err)
	}
	want := filepath.Join(root, "out", "每日更新", "电视剧", "国产剧", "折月亮", "Season 1", "折月亮 - S01E01.strm")
	if rs[0].LocalSTRMPath != want {
		t.Fatalf("strm path=%q, want %q", rs[0].LocalSTRMPath, want)
	}
	// Under the new zero-nfo pure STRM policy, companion placeholder NFO must NOT be created.
	nfoPath := strings.TrimSuffix(want, ".strm") + ".nfo"
	if _, err := os.Stat(nfoPath); !os.IsNotExist(err) {
		t.Fatalf("expected companion NFO not to be created, but found %s", nfoPath)
	}
}

func TestMovieStandardLayout(t *testing.T) {
	root := t.TempDir()
	path, ok, err := MapStandardMoviePath(root, "/每日更新/电影/华语电影/八仙！ (2026).mp4", "", true, 235, nil)
	if err != nil || !ok {
		t.Fatalf("MapStandardMoviePath failed: ok=%v, err=%v", ok, err)
	}
	want := filepath.Join(root, "每日更新", "电影", "华语电影", "八仙！ (2026)", "八仙！ (2026).strm")
	if path != want {
		t.Fatalf("got movie path=%q, want %q", path, want)
	}
}
