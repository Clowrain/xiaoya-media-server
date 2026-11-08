package xiaoya

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func seedZheYueLiangSQLite(t *testing.T, dbPath string) {
	t.Helper()
	rwDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open rw sqlite: %v", err)
	}
	defer rwDB.Close()

	// Enable WAL mode to verify read-only reader works seamlessly with WAL SQLite databases
	if _, err := rwDB.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			parent_id INTEGER NOT NULL DEFAULT 0,
			is_dir BOOLEAN NOT NULL DEFAULT 0,
			content TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatalf("create nodes schema: %v", err)
	}

	insert := func(id int64, name string, parentID int64, isDir bool, content string) {
		dirInt := 0
		if isDir {
			dirInt = 1
		}
		if _, err := rwDB.Exec("INSERT INTO nodes(id, name, parent_id, is_dir, content) VALUES(?, ?, ?, ?, ?)", id, name, parentID, dirInt, content); err != nil {
			t.Fatalf("insert node %d (%s): %v", id, name, err)
		}
	}

	// Exact V1.0 Spec Section 3.1 tree + 国产剧（115） sibling root + ambiguous folder test fixture
	insert(568922, "每日更新", 0, true, "")
	insert(617762, "电视剧", 568922, true, "")
	insert(639780, "国产剧", 617762, true, "")
	insert(639781, "国产剧（115）", 617762, true, "")
	insert(641716, "折月亮 (2026)", 639780, true, "")
	insert(641717, "{tmdb-270431}", 641716, true, "")
	insert(641718, "Season 1", 641717, true, "")
	for ep := 1; ep <= 27; ep++ {
		nodeID := int64(641718 + ep) // 641719..641745
		name := fmt.Sprintf("E%02d", ep)
		content := fmt.Sprintf("http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/%02d.mp4?sign=MOCK_%02d:0", ep, ep)
		insert(nodeID, name, 641718, false, content)
	}

	// Duplicate directory name under 568922 to test ErrAmbiguousPath
	insert(700001, "重名目录", 568922, true, "")
	insert(700002, "重名目录", 568922, true, "")
	// File node to test ErrNotDirectory
	insert(700003, "说明.txt", 568922, false, "http://xiaoya.host:5678/d/每日更新/说明.txt")
}

func TestReaderReadOnlyAndZheYueLiangSubtree(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "strm_internal.db")
	seedZheYueLiangSQLite(t, dbPath)

	r, err := OpenReader(dbPath)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	ctx := context.Background()
	if err := r.VerifyReadOnly(ctx); err != nil {
		t.Fatalf("VerifyReadOnly: %v", err)
	}

	// Any write or DDL attempt MUST fail because of mode=ro & query_only=1
	if _, err := r.db.ExecContext(ctx, "INSERT INTO nodes(name, parent_id, is_dir) VALUES('hack', 0, 1)"); err == nil {
		t.Fatalf("expected write to read-only SQLite connection to fail")
	}
	if _, err := r.db.ExecContext(ctx, "CREATE INDEX idx_test ON nodes(parent_id)"); err == nil {
		t.Fatalf("expected DDL on read-only SQLite connection to fail")
	}

	// Verify both V1.0 initial monitor roots resolve distinctly (fullwidth parentheses preserved)
	root1, err := r.ResolvePath(ctx, "  /每日更新//电视剧/国产剧/ ")
	if err != nil || root1.ID != 639780 || root1.FullPath != "/每日更新/电视剧/国产剧" {
		t.Fatalf("ResolvePath 国产剧 got (%+v, %v)", root1, err)
	}
	root2, err := r.ResolvePath(ctx, "/每日更新/电视剧/国产剧（115）")
	if err != nil || root2.ID != 639781 || root2.FullPath != "/每日更新/电视剧/国产剧（115）" {
		t.Fatalf("ResolvePath 国产剧（115） got (%+v, %v)", root2, err)
	}

	// ASCII '(115)' must NOT match fullwidth '（115）'
	if _, err := r.ResolvePath(ctx, "/每日更新/电视剧/国产剧(115)"); !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("expected ErrPathNotFound for ASCII (115), got %v", err)
	}
	// Missing path -> ErrPathNotFound
	if _, err := r.ResolvePath(ctx, "/每日更新/电视剧/国产剧/折月亮"); !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("expected ErrPathNotFound for exact mismatch '折月亮' vs '折月亮 (2026)', got %v", err)
	}
	// Ambiguous duplicate directory -> ErrAmbiguousPath
	if _, err := r.ResolvePath(ctx, "/每日更新/重名目录"); !errors.Is(err, ErrAmbiguousPath) {
		t.Fatalf("expected ErrAmbiguousPath, got %v", err)
	}
	// File path -> ErrNotDirectory
	if _, err := r.ResolvePath(ctx, "/每日更新/说明.txt"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("expected ErrNotDirectory, got %v", err)
	}
	file, err := r.ResolveAnyPath(ctx, "/每日更新/说明.txt")
	if err != nil || file.IsDir || file.Name != "说明.txt" {
		t.Fatalf("ResolveAnyPath file got (%+v, %v)", file, err)
	}

	// Batched walk with tiny pageSize=5 to verify cursor pagination across all 30 nodes
	var collected []Node
	stats, err := r.WalkSubtreeBatched(ctx, root1, 5, func(batch []Node) error {
		if len(batch) > 5 {
			t.Fatalf("batch size %d exceeded pageSize=5", len(batch))
		}
		collected = append(collected, batch...)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkSubtreeBatched: %v", err)
	}
	if stats.TotalNodes != 30 || stats.DirNodes != 3 || stats.FileNodes != 27 || stats.MaxNodeID != 641745 {
		t.Fatalf("unexpected WalkStats: %+v (want Total=30, Dir=3, File=27, MaxNodeID=641745)", stats)
	}
	if len(collected) != 30 {
		t.Fatalf("collected %d nodes, want 30", len(collected))
	}
	last := collected[len(collected)-1]
	if last.ID != 641745 || last.Name != "E27" || last.RelPath != "折月亮 (2026)/{tmdb-270431}/Season 1/E27" {
		t.Fatalf("unexpected last node: %+v", last)
	}
}

func TestSnapshotKeepsMovedFileAndDefersNewNode(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "source.db")
	w, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, err = w.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE nodes(id INTEGER PRIMARY KEY,name TEXT,parent_id INTEGER,is_dir INTEGER,content TEXT);
INSERT INTO nodes VALUES(1,'Root',0,1,''),(2,'A',1,1,''),(3,'B',1,1,''),(4,'E01',2,0,'url'),(5,'E02',3,0,'url');`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var high int64
	seen := map[int64]string{}
	err = r.WithSnapshot(ctx, func(view *Reader) error {
		root, e := view.ResolvePath(ctx, "/Root")
		if e != nil {
			return e
		}
		_, e = view.WalkSubtreeBatched(ctx, root, 1, func(nodes []Node) error {
			n := nodes[0]
			seen[n.ID] = n.FullPath
			if n.ID == 4 {
				_, e := w.Exec("UPDATE nodes SET parent_id=1 WHERE id=5; INSERT INTO nodes VALUES(6,'E03',2,0,'url')")
				return e
			}
			return nil
		})
		if e != nil {
			return e
		}
		high, e = view.MaxNodeID(ctx)
		return e
	})
	if err != nil || high != 5 || seen[5] != "/Root/B/E02" || seen[6] != "" {
		t.Fatalf("mixed snapshot: seen=%v high=%d err=%v", seen, high, err)
	}
	err = r.WithSnapshot(ctx, func(view *Reader) error {
		root, e := view.ResolvePath(ctx, "/Root")
		if e != nil {
			return e
		}
		return view.WalkNewUnderRoot(ctx, root, high, 6, func(nodes []Node) error {
			for _, n := range nodes {
				seen[n.ID] = n.FullPath
			}
			return nil
		})
	})
	if err != nil || seen[6] != "/Root/A/E03" {
		t.Fatalf("next incremental scan did not see E03: %v %v", seen, err)
	}
}
