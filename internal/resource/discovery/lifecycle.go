package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var ErrResourceUnmanaged = errors.New("资源已取消管理")
var ErrResourceMissing = errors.New("源资源已缺失")

func (s *SQLStore) SeedMonitorRoot(ctx context.Context, path string, interval int64) (MonitorRoot, error) {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, s.rebind("INSERT INTO monitor_roots(id,root_path,enabled,interval_sec,created_at,updated_at) VALUES(?,?,1,?,?,?) ON CONFLICT(root_path) DO NOTHING"), StableID("root:"+path), path, interval, now, now)
	if err != nil {
		return MonitorRoot{}, err
	}
	return s.GetMonitorRootByPath(ctx, path)
}

type suppression struct {
	id, path string
	dir      bool
}

func (s *SQLStore) suppressions(ctx context.Context, db sqlQueryer, origin string) ([]suppression, error) {
	rows, err := db.QueryContext(ctx, s.rebind("SELECT resource_id,source_path,is_dir FROM resource_suppressions WHERE origin_type=?"), origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []suppression
	for rows.Next() {
		var v suppression
		var dir int64
		if err := rows.Scan(&v.id, &v.path, &dir); err != nil {
			return nil, err
		}
		v.dir = dir != 0
		out = append(out, v)
	}
	return out, rows.Err()
}

func suppressed(list []suppression, r MediaResource) bool {
	for _, v := range list {
		if v.id == r.ID || v.path == r.SourcePath || (v.dir && strings.HasPrefix(r.SourcePath, v.path+"/")) {
			return true
		}
	}
	return false
}

func (s *SQLStore) IsUnmanaged(ctx context.Context, r MediaResource) (bool, error) {
	list, err := s.suppressions(ctx, s.db, r.OriginType)
	return suppressed(list, r), err
}

func (s *SQLStore) RestoreManagement(ctx context.Context, id string) error {
	r, err := s.GetResource(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, s.rebind("DELETE FROM resource_suppressions WHERE resource_id=?"), id); err != nil {
		return err
	}
	exclusions, err := s.suppressions(ctx, tx, r.OriginType)
	if err != nil {
		return err
	}
	if suppressed(exclusions, r) {
		return fmt.Errorf("请先恢复上级目录的管理")
	}
	if r.OriginType == OriginXiaoyaSQLite {
		prefix := r.SourcePath + "/"
		// A baseline/full comparison on the next tick revisits nodes created
		// while this resource was excluded without dropping history or files.
		if _, err = tx.ExecContext(ctx, s.rebind(`DELETE FROM sync_cursors WHERE root_id IN (
SELECT id FROM monitor_roots WHERE root_path=? OR substr(?,1,length(root_path)+1)=root_path||'/'
OR (?=1 AND substr(root_path,1,length(?))=?))`), r.SourcePath, r.SourcePath, boolToInt(r.IsDir), prefix, prefix); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStore) enqueue(ctx context.Context, db sqlExecer, id string) error {
	_, err := db.ExecContext(ctx, s.rebind("INSERT INTO pending_materialization(resource_id,queued_at) VALUES(?,?) ON CONFLICT(resource_id) DO UPDATE SET queued_at=excluded.queued_at"), id, time.Now().Unix())
	return err
}

func (s *SQLStore) PendingResourceIDs(ctx context.Context, limit int) ([]string, error) {
	q := s.rebind(`SELECT p.resource_id FROM pending_materialization p JOIN media_resources r ON r.id=p.resource_id
WHERE r.status<>'MISSING' AND r.retry_count<5
AND NOT EXISTS(SELECT 1 FROM resource_suppressions sp WHERE sp.origin_type=r.origin_type AND
(sp.resource_id=r.id OR sp.source_path=r.source_path OR (sp.is_dir=1 AND substr(r.source_path,1,length(sp.source_path)+1)=sp.source_path||'/')))
ORDER BY p.queued_at,p.resource_id LIMIT ?`)
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SQLStore) refreshItemAvailability(ctx context.Context, db sqlExecer, path string) error {
	if s.dialect != "postgres" || path == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, s.rebind(`UPDATE items SET availability=CASE
WHEN EXISTS(SELECT 1 FROM media_resources r WHERE r.local_strm_path=items.path AND r.status='MISSING')
AND NOT EXISTS(SELECT 1 FROM media_resources r WHERE r.local_strm_path=items.path AND r.status<>'MISSING')
THEN 'MISSING' ELSE 'AVAILABLE' END WHERE path=?`), path)
	return err
}

func (s *SQLStore) markMissing(ctx context.Context, tx *sql.Tx, batch *SyncBatch, r MediaResource, now int64) error {
	if r.Status == StatusMissing {
		return nil
	}
	if _, err := tx.ExecContext(ctx, s.rebind("UPDATE media_resources SET status='MISSING',last_changed_at=? WHERE id=?"), now, r.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rebind("DELETE FROM pending_materialization WHERE resource_id=?"), r.ID); err != nil {
		return err
	}
	if err := s.InsertChange(ctx, tx, ResourceChange{BatchID: batch.ID, RootID: batch.RootID, ResourceID: r.ID, SourceNodeID: r.SourceNodeID, ChangeType: ChangeMissing, OldPath: r.SourcePath, OldContentHash: r.ContentHash, OldSnapshotHash: r.SnapshotHash, DetectedAt: now}); err != nil {
		return err
	}
	if err := s.refreshItemAvailability(ctx, tx, r.LocalSTRMPath); err != nil {
		return err
	}
	batch.MissingCount++
	return nil
}

func (s *SQLStore) processingResult(ctx context.Context, id, localPath string, cause error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if cause != nil {
		_, err = tx.ExecContext(ctx, s.rebind("UPDATE media_resources SET status='FAILED',last_error=?,retry_count=retry_count+1 WHERE id=? AND status<>'MISSING'"), cause.Error(), id)
		if err != nil {
			return err
		}
		if err = s.enqueue(ctx, tx, id); err != nil {
			return err
		}
	} else {
		_, err = tx.ExecContext(ctx, s.rebind(`UPDATE media_resources SET local_strm_path=CASE WHEN ?<>'' THEN ? ELSE local_strm_path END,status='ACTIVE',last_error='',retry_count=0 WHERE id=? AND status<>'MISSING'`), localPath, localPath, id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, s.rebind("DELETE FROM pending_materialization WHERE resource_id=?"), id); err != nil {
			return err
		}
		if err = s.refreshItemAvailability(ctx, tx, localPath); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStore) ManagedTarget(ctx context.Context, id string) (string, string, error) {
	var lib, dir string
	err := s.db.QueryRowContext(ctx, s.rebind("SELECT library_id,output_dir FROM managed_resources WHERE resource_id=?"), id).Scan(&lib, &dir)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return lib, dir, err
}

type TrackedAListRoot struct {
	ResourceID string `json:"resource_id"`
	Path       string `json:"path"`
	LibraryID  string `json:"library_id"`
	OutputDir  string `json:"output_dir"`
	Review     bool   `json:"review"`
}

func (s *SQLStore) TrackedAListRoots(ctx context.Context, review bool) ([]TrackedAListRoot, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT r.id,r.source_path,m.library_id,m.output_dir FROM alist_tracking_roots t
JOIN media_resources r ON r.id=t.resource_id JOIN managed_resources m ON m.resource_id=r.id
WHERE t.review=? ORDER BY r.source_path`), boolToInt(review))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrackedAListRoot{}
	for rows.Next() {
		v := TrackedAListRoot{Review: review}
		if err = rows.Scan(&v.ResourceID, &v.Path, &v.LibraryID, &v.OutputDir); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *SQLStore) AListSubtree(ctx context.Context, path string) ([]MediaResource, error) {
	prefix := path + "/"
	rows, err := s.db.QueryContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE origin_type='ALIST_MANUAL' AND substr(source_path,1,length(?))=?"), prefix, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var resources []MediaResource
	for rows.Next() {
		r, e := scanMediaResource(rows)
		if e != nil {
			return nil, e
		}
		resources = append(resources, r)
	}
	return resources, rows.Err()
}

func (s *Service) RecordManualFailure(ctx context.Context, path string, cause error) error {
	b, err := s.store.CreateBatch(ctx, "", path, ScanModeManualImport, false)
	if err != nil {
		return errors.Join(cause, err)
	}
	_, err = s.failBatch(b, cause)
	return err
}

func (s *SQLStore) SetAListTracking(ctx context.Context, id string, enabled bool) error {
	res, err := s.GetResource(ctx, id)
	if err != nil {
		return err
	}
	if res.OriginType != OriginAListManual || !res.IsDir {
		return errors.New("仅 AList 目录可以追更")
	}
	if enabled {
		_, err = s.db.ExecContext(ctx, s.rebind("INSERT INTO alist_tracking_roots(resource_id,review) VALUES(?,0) ON CONFLICT(resource_id) DO UPDATE SET review=0"), id)
	} else {
		_, err = s.db.ExecContext(ctx, s.rebind("DELETE FROM alist_tracking_roots WHERE resource_id=?"), id)
	}
	return err
}

// ClaimMaterializedPath reserves a path before the generator writes it. An old
// ambiguous mapping is surfaced instead of silently picking a new owner.
func (s *SQLStore) ClaimMaterializedPath(ctx context.Context, owner, candidate string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, s.rebind("SELECT local_path FROM materialized_paths WHERE owner_key=?"), owner).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if existing != "" {
		candidate = existing
	}
	if candidate == "" {
		return "", nil
	}
	var conflict bool
	err = tx.QueryRowContext(ctx, s.rebind("SELECT EXISTS(SELECT 1 FROM media_resources WHERE local_strm_path=? AND id<>?)"), candidate, owner).Scan(&conflict)
	if err != nil {
		return "", err
	}
	if conflict {
		if existing != "" {
			return "", fmt.Errorf("STRM 路径归属有冲突：%s", existing)
		}
		return "", nil
	}
	if existing == "" {
		res, err := tx.ExecContext(ctx, s.rebind("INSERT INTO materialized_paths(local_path,owner_key) VALUES(?,?) ON CONFLICT DO NOTHING"), candidate, owner)
		if err != nil {
			return "", err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", nil
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return candidate, nil
}

func (s *SQLStore) ReleaseUnusedPath(ctx context.Context, owner, p string) error {
	_, err := s.db.ExecContext(ctx, s.rebind("DELETE FROM materialized_paths WHERE owner_key=? AND local_path=? AND NOT EXISTS(SELECT 1 FROM media_resources WHERE local_strm_path=?)"), owner, p, p)
	return err
}

func (s *SQLStore) MaintenanceFiles(ctx context.Context, after string, limit int) ([]MediaResource, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE id>? AND is_dir=0 AND status='ACTIVE' ORDER BY id LIMIT ?"), after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaResource
	for rows.Next() {
		r, e := scanMediaResource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLStore) AListTrackingExclusions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT r.source_path FROM alist_tracking_roots t JOIN media_resources r ON r.id=t.resource_id WHERE t.review<>0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

func (s *SQLStore) CountSTRMPaths(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT local_strm_path) FROM media_resources WHERE lower(local_strm_path) LIKE '%.strm'").Scan(&n)
	return n, err
}
