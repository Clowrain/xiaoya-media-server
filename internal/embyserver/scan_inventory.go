package embyserver

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lib/pq"
)

type stampedPath struct {
	path  string
	stamp fileStamp
}
type scanInventory struct {
	app             *App
	lib, generation string
	pending         []stampedPath
}

func (s *scanInventory) close() {
	s.app.db.Exec("DELETE FROM scan_file_stage WHERE generation=?", s.generation)
}
func (s *scanInventory) add(path string, stamp fileStamp) error {
	s.pending = append(s.pending, stampedPath{path, stamp})
	if len(s.pending) >= 500 {
		return s.flush()
	}
	return nil
}
func (s *scanInventory) flush() error {
	if len(s.pending) == 0 {
		return nil
	}
	tx, e := s.app.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// COPY 到临时表后合并，允许重叠的扫描范围重复遇到同一路径。
	if _, e = tx.Exec("CREATE TEMP TABLE scan_page (LIKE scan_file_stage INCLUDING DEFAULTS) ON COMMIT DROP"); e != nil {
		return e
	}
	stmt, e := tx.Tx.Prepare(pq.CopyIn("scan_page", "generation", "lib", "path", "size", "mtime", "media", "sidecar"))
	if e != nil {
		return e
	}
	defer stmt.Close()
	for _, v := range s.pending {
		if _, e = stmt.Exec(s.generation, s.lib, v.path, v.stamp.Size, v.stamp.Mtime, isScannableMediaFile(v.path), sidecarFile(v.path)); e != nil {
			return e
		}
	}
	if _, e = stmt.Exec(); e != nil {
		return e
	}
	if e = stmt.Close(); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO scan_file_stage SELECT DISTINCT ON (generation,path) * FROM scan_page ON CONFLICT(generation,path) DO UPDATE SET size=excluded.size,mtime=excluded.mtime"); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.pending = s.pending[:0]
	return nil
}
func snapshotScope(column string, scopes []string) (string, []any) {
	if len(scopes) == 0 {
		return "", nil
	}
	clauses := []string{}
	args := []any{}
	for _, p := range scopes {
		clauses = append(clauses, "("+column+"=? OR ("+column+` COLLATE "C">=? AND `+column+` COLLATE "C"<?))`)
		args = append(args, p, p+"/", p+"0")
	}
	return " AND (" + strings.Join(clauses, " OR ") + ")", args
}
func (s *scanInventory) unchanged(path string, stamp fileStamp, itemID string) (bool, error) {
	var same bool
	e := s.app.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM scan_file_stamps stamp
		JOIN items item ON item.id=? AND item.lib=stamp.lib AND item.path=stamp.path
		WHERE stamp.lib=? AND stamp.path=? AND stamp.size=? AND stamp.mtime=?
	)`, itemID, s.lib, path, stamp.Size, stamp.Mtime).Scan(&same)
	return same, e
}
func (s *scanInventory) changedSidecars(scopes []string) (map[string]bool, error) {
	filter, args := snapshotScope("old.path", scopes)
	q := `SELECT cur.path FROM scan_file_stage cur LEFT JOIN scan_file_stamps old ON old.lib=? AND old.path=cur.path
 WHERE cur.generation=? AND cur.sidecar AND (old.path IS NULL OR old.size<>cur.size OR old.mtime<>cur.mtime)
 UNION SELECT old.path FROM scan_file_stamps old WHERE old.lib=?` + filter + ` AND NOT EXISTS(SELECT 1 FROM scan_file_stage cur WHERE cur.generation=? AND cur.path=old.path)`
	params := []any{s.lib, s.generation, s.lib}
	params = append(params, args...)
	params = append(params, s.generation)
	rows, e := s.app.db.Query(q, params...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	dirs := map[string]bool{}
	for rows.Next() {
		var path string
		if e = rows.Scan(&path); e != nil {
			return nil, e
		}
		if sidecarFile(path) {
			dirs[filepath.Dir(path)] = true
		}
	}
	return dirs, rows.Err()
}
func (s *scanInventory) commit(allowEmpty bool, scopes []string) error {
	tx, e := s.app.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if !allowEmpty {
		var present, existing bool
		if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM scan_file_stage WHERE generation=? AND media),EXISTS(SELECT 1 FROM items WHERE lib=?)", s.generation, s.lib).Scan(&present, &existing); e != nil {
			return e
		}
		if !present && existing {
			return fmt.Errorf("目录扫描为空，保留原索引和快照以防挂载异常")
		}
	}
	filter, scopeArgs := snapshotScope("items.path", scopes)
	// 只更新本次范围，并跳过值未变化的条目，避免增量扫描重写整个媒体库。
	desired := `CASE WHEN EXISTS(SELECT 1 FROM media_resources mr WHERE mr.local_strm_path=items.path AND mr.status='MISSING') AND NOT EXISTS(SELECT 1 FROM media_resources mr WHERE mr.local_strm_path=items.path AND mr.status<>'MISSING') THEN 'MISSING' ELSE 'AVAILABLE' END`
	presentArgs := []any{s.lib, s.generation}
	presentArgs = append(presentArgs, scopeArgs...)
	if _, e = tx.Exec("UPDATE items SET availability="+desired+" WHERE lib=? AND kind IN ('Movie','Episode') AND EXISTS(SELECT 1 FROM scan_file_stage p WHERE p.generation=? AND p.media AND p.path=items.path)"+filter+" AND availability<>"+desired, presentArgs...); e != nil {
		return e
	}
	cleanupFilter := ""
	if len(scopes) > 0 {
		parentArgs := []any{s.lib}
		parentArgs = append(parentArgs, scopeArgs...)
		parentArgs = append(parentArgs, s.lib)
		if _, e = tx.Exec(`CREATE TEMP TABLE cleanup_parents ON COMMIT DROP AS WITH RECURSIVE parents(id) AS (SELECT parent FROM items WHERE lib=?`+filter+` UNION SELECT i.parent FROM items i JOIN parents p ON i.id=p.id WHERE i.lib=?) SELECT id FROM parents`, parentArgs...); e != nil {
			return e
		}
		cleanupFilter = " AND id IN (SELECT id FROM cleanup_parents)"
	}
	absent := ` WHERE lib=? AND kind IN ('Movie','Episode') AND NOT EXISTS(SELECT 1 FROM scan_file_stage p WHERE p.generation=? AND p.media AND p.path=items.path)` + filter
	args := []any{s.lib, s.generation}
	args = append(args, scopeArgs...)
	keep := `EXISTS(SELECT 1 FROM media_resources mr JOIN managed_resources mg ON mg.resource_id=mr.id WHERE mr.local_strm_path=items.path AND mg.deletion_policy='keep')`
	if _, e = tx.Exec("UPDATE items SET availability='MISSING'"+absent+" AND availability<>'MISSING' AND "+keep, args...); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM items"+absent+" AND NOT "+keep, args...); e != nil {
		return e
	}
	for _, kind := range []string{"Season", "Series"} {
		if _, e = tx.Exec("DELETE FROM items WHERE lib=? AND kind=? AND NOT EXISTS(SELECT 1 FROM items child WHERE child.parent=items.id)"+cleanupFilter, s.lib, kind); e != nil {
			return e
		}
	}
	sf, sa := snapshotScope("path", scopes)
	args = []any{s.lib}
	args = append(args, sa...)
	args = append(args, s.generation)
	if _, e = tx.Exec("DELETE FROM scan_file_stamps WHERE lib=?"+sf+" AND NOT EXISTS(SELECT 1 FROM scan_file_stage p WHERE p.generation=? AND p.path=scan_file_stamps.path)", args...); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO scan_file_stamps(lib,path,size,mtime) SELECT ?,path,size,mtime FROM scan_file_stage WHERE generation=?
 ON CONFLICT(lib,path) DO UPDATE SET size=excluded.size,mtime=excluded.mtime WHERE scan_file_stamps.size<>excluded.size OR scan_file_stamps.mtime<>excluded.mtime`, s.lib, s.generation); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO scan_snapshots(lib,data) VALUES(?,'{}') ON CONFLICT(lib) DO NOTHING", s.lib); e != nil {
		return e
	}
	return tx.Commit()
}
