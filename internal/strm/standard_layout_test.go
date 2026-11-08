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
	nfo, err := os.ReadFile(strings.TrimSuffix(want, ".strm") + ".nfo")
	if err != nil || !strings.Contains(string(nfo), "episodedetails") || !strings.Contains(string(nfo), "<episode>1</episode>") {
		t.Fatalf("standard episode NFO missing: err=%v content=%s", err, nfo)
	}
}
