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

func TestMultiVersionAndMirrorDeduplication(t *testing.T) {
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
	outDir := filepath.Join(root, "out")
	engine := NewEngine(Config{OutputRoot: outDir, PreserveSourceStructure: true, PathStore: store, TargetBaseURL: "http://xiaoya.host:5678"})
	svc := discovery.NewService(store, nil, engine)

	// 1. Initial baseline version (e.g. standard 1080P version)
	_, rs1, err := svc.ApplyManualSnapshot(ctx, discovery.ManualSnapshot{
		OriginType: discovery.OriginXiaoyaSQLite,
		RootPath:   "/动漫/合集/天线宝宝",
		OutputDir:  outDir,
		Nodes: []discovery.ManualNode{{
			Path: "/动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.1080p.mkv",
			RelPath: "动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.1080p.mkv",
			Name: "天线宝宝.S01E03.1080p.mkv",
			Content: "http://xiaoya.host:5678/d/anime/ep03_1080p.mkv",
			SourceNodeID: 201,
		}},
	})
	if err != nil || len(rs1) != 1 {
		t.Fatalf("snapshot 1 failed: %v", err)
	}
	strm1 := rs1[0].LocalSTRMPath
	if !strings.HasSuffix(strm1, "天线宝宝 - S01E03.strm") {
		t.Errorf("expected initial baseline strm name, got %s", strm1)
	}

	// 2. Duplicate mirror with identical CleanPlayURL (even if source path differs or ?sign differs)
	_, rs2, err := svc.ApplyManualSnapshot(ctx, discovery.ManualSnapshot{
		OriginType: discovery.OriginXiaoyaSQLite,
		RootPath:   "/动漫/合集/天线宝宝",
		OutputDir:  outDir,
		Nodes: []discovery.ManualNode{{
			Path: "/动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.dup.mkv",
			RelPath: "动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.dup.mkv",
			Name: "天线宝宝.S01E03.dup.mkv",
			Content: "http://xiaoya.host:5678/d/anime/ep03_1080p.mkv?sign=SOME_SIGN:0",
			SourceNodeID: 202,
		}},
	})
	if err != nil || len(rs2) != 1 {
		t.Fatalf("snapshot 2 failed: %v", err)
	}
	// Identical content must reuse the first STRM (first-claim wins) without creating a redundant file
	if rs2[0].LocalSTRMPath != strm1 {
		t.Errorf("mirror must share first-claimed strm %s, got %s", strm1, rs2[0].LocalSTRMPath)
	}

	// 3. True multi-version (4K version with different content URL)
	_, rs3, err := svc.ApplyManualSnapshot(ctx, discovery.ManualSnapshot{
		OriginType: discovery.OriginXiaoyaSQLite,
		RootPath:   "/动漫/合集/天线宝宝",
		OutputDir:  outDir,
		Nodes: []discovery.ManualNode{{
			Path: "/动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.4K.DV.mkv",
			RelPath: "动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.4K.DV.mkv",
			Name: "天线宝宝.S01E03.4K.DV.mkv",
			Content: "http://xiaoya.host:5678/d/115/anime/ep03_4k.mkv",
			SourceNodeID: 203,
		}},
	})
	if err != nil || len(rs3) != 1 {
		t.Fatalf("snapshot 3 failed: %v", err)
	}
	strm3 := rs3[0].LocalSTRMPath
	if !strings.Contains(strm3, "- [4K 杜比视界].strm") && !strings.Contains(strm3, "- [4K].strm") {
		t.Errorf("expected 4K multi-version tag, got %s", strm3)
	}

	// 4. Different source with no explicit quality tags (fallback to source netdisk origin [115])
	_, rs4, err := svc.ApplyManualSnapshot(ctx, discovery.ManualSnapshot{
		OriginType: discovery.OriginXiaoyaSQLite,
		RootPath:   "/动漫/合集/天线宝宝",
		OutputDir:  outDir,
		Nodes: []discovery.ManualNode{{
			Path: "/动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.115.mkv",
			RelPath: "动漫/合集/天线宝宝/Season 1/天线宝宝.S01E03.115.mkv",
			Name: "天线宝宝.S01E03.115.mkv",
			Content: "http://xiaoya.host:5678/d/different_storage/ep03.mkv",
			SourceNodeID: 204,
		}},
	})
	if err != nil || len(rs4) != 1 {
		t.Fatalf("snapshot 4 failed: %v", err)
	}
	strm4 := rs4[0].LocalSTRMPath
	if !strings.Contains(strm4, "- [115].strm") {
		t.Errorf("expected netdisk origin [115] tag, got %s", strm4)
	}
}
