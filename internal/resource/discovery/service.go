package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"xiaoya-media-server/internal/resource/xiaoya"
)

var ErrRootScanInProgress = errors.New("a scan is already running for this monitor root")

type ResourceProcessor interface {
	ProcessResource(ctx context.Context, root MonitorRoot, res MediaResource, changeType string) (localSTRMPath string, err error)
}

type Service struct {
	store     *SQLStore
	reader    atomic.Pointer[xiaoya.Reader]
	processor ResourceProcessor
	writes    sync.Mutex
	scanMu    sync.Mutex
	running   map[string]string
}

func NewService(store *SQLStore, reader *xiaoya.Reader, processor ResourceProcessor) *Service {
	s := &Service{store: store, processor: processor, running: map[string]string{}}
	s.SetReader(reader)
	return s
}
func (s *Service) SetProcessor(p ResourceProcessor) {
	s.writes.Lock()
	defer s.writes.Unlock()
	s.processor = p
}
func (s *Service) SetReader(r *xiaoya.Reader) { s.reader.Store(r) }
func (s *Service) Reader() *xiaoya.Reader     { return s.reader.Load() }
func (s *Service) Store() *SQLStore           { return s.store }
func (s *Service) RootPathBusy(path string) bool {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	for _, p := range s.running {
		if p == path || hasPathPrefix(p, path) || hasPathPrefix(path, p) {
			return true
		}
	}
	return false
}

func (s *Service) acquireRoot(root MonitorRoot) (func(), bool) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	for _, p := range s.running {
		if p == root.RootPath || hasPathPrefix(p, root.RootPath) || hasPathPrefix(root.RootPath, p) {
			return nil, false
		}
	}
	s.running[root.ID] = root.RootPath
	return func() { s.scanMu.Lock(); delete(s.running, root.ID); s.scanMu.Unlock() }, true
}

func (s *Service) RegisterMonitorRoots(ctx context.Context, paths []string, interval time.Duration) ([]MonitorRoot, error) {
	sec := int64(interval.Seconds())
	if sec <= 0 {
		sec = 600
	}
	roots := make([]MonitorRoot, 0, len(paths))
	for _, raw := range paths {
		norm, _, err := xiaoya.NormalizeMonitorPath(raw)
		if err != nil {
			return nil, fmt.Errorf("normalize monitor root %q: %w", raw, err)
		}
		root, err := s.store.SeedMonitorRoot(ctx, norm, sec)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, nil
}

func (s *Service) ScanRootFull(ctx context.Context, id string) (SyncBatch, error) {
	return s.scan(ctx, id, true)
}
func (s *Service) ScanRootIncremental(ctx context.Context, id string) (SyncBatch, error) {
	return s.scan(ctx, id, false)
}

func (s *Service) failBatch(b SyncBatch, err error) (SyncBatch, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.Status = BatchFailed
	b.ErrorMessage = err.Error()
	b.FinishedAt = time.Now().Unix()
	if e := s.store.FinishBatch(ctx, b); e != nil {
		err = errors.Join(err, e)
	}
	if b.RootID != "" {
		if e := s.store.UpdateMonitorRootStatus(ctx, b.RootID, 0, b.ID, b.Status, b.ErrorMessage, b.FinishedAt); e != nil {
			err = errors.Join(err, e)
		}
	}
	return b, err
}

func (s *Service) scan(ctx context.Context, id string, full bool) (SyncBatch, error) {
	root, err := s.store.GetMonitorRoot(ctx, id)
	if err != nil {
		return SyncBatch{}, err
	}
	release, ok := s.acquireRoot(root)
	if !ok {
		return SyncBatch{}, ErrRootScanInProgress
	}
	defer release()
	cursor, has, err := s.store.GetCursor(ctx, id)
	if err != nil {
		return SyncBatch{}, err
	}
	mode := ScanModeIncremental
	switch {
	case !has:
		mode = ScanModeBaseline
		full = true
	case full:
		mode = ScanModeFull
	}
	batch, err := s.store.CreateBatch(ctx, id, root.RootPath, mode, !has)
	if err != nil {
		return batch, err
	}
	if err = s.store.UpdateMonitorRootStatus(ctx, id, 0, batch.ID, BatchRunning, "", 0); err != nil {
		return s.failBatch(batch, err)
	}
	reader := s.Reader()
	if reader == nil {
		return s.failBatch(batch, errors.New("小雅 SQLite 未连接"))
	}

	var nodes []xiaoya.Node
	var rootNode xiaoya.Node
	var high int64
	err = reader.WithSnapshot(ctx, func(view *xiaoya.Reader) error {
		var e error
		rootNode, e = view.ResolvePath(ctx, root.RootPath)
		if e != nil {
			return e
		}
		high, e = view.MaxNodeID(ctx)
		if e != nil {
			return e
		}
		// A replaced root or source database must be reconciled before reusing its old cursor.
		if !full && (cursor.RootNodeID != rootNode.ID || high < cursor.LastMaxNodeID) {
			full = true
			batch.ScanMode = ScanModeFull
		}
		collect := func(page []xiaoya.Node) error { nodes = append(nodes, page...); return nil }
		if full {
			_, e = view.WalkSubtreeBatched(ctx, rootNode, 500, collect)
			return e
		}
		return view.WalkNewUnderRoot(ctx, rootNode, cursor.LastMaxNodeID, high, collect)
	})
	if err != nil {
		return s.failBatch(batch, err)
	}
	batch.TotalSeen = int64(len(nodes))
	batch.MaxSourceNodeID = high
	pending, err := s.commitSQLite(ctx, &batch, rootNode.ID, cursor, nodes, full)
	if err != nil {
		return s.failBatch(batch, err)
	}
	return s.finishMaterialization(ctx, batch, pending, rootNode.ID)
}

func (s *Service) commitSQLite(ctx context.Context, batch *SyncBatch, rootNode int64, cursor SyncCursor, nodes []xiaoya.Node, full bool) ([]string, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	existing, err := s.store.loadRootResources(ctx, tx, batch.RootID)
	if err != nil {
		return nil, err
	}
	exclusions, err := s.store.suppressions(ctx, tx, OriginXiaoyaSQLite)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(nodes))
	var pending []string
	now := time.Now().Unix()
	for _, n := range nodes {
		key := "sqlite:" + strconv.FormatInt(n.ID, 10)
		seen[key] = true
		old, ok := existing[key]
		if !ok {
			old, ok, err = s.store.resourceByKey(ctx, tx, key)
			if err != nil {
				return nil, err
			}
		}
		r := MediaResource{ID: StableID(key), SourceKey: key, OriginType: OriginXiaoyaSQLite, RootID: batch.RootID,
			SourceNodeID: n.ID, SourceParentID: n.ParentID, SourcePath: n.FullPath, RelPath: n.RelPath, Name: n.Name, IsDir: n.IsDir, SourceContent: n.Content}
		if suppressed(exclusions, r) {
			// Track the location of an explicitly suppressed directory by node identity.
			if _, err = tx.ExecContext(ctx, s.store.rebind("UPDATE resource_suppressions SET source_path=? WHERE resource_id=? AND source_path<>?"), r.SourcePath, r.ID, r.SourcePath); err != nil {
				return nil, err
			}
			for i := range exclusions {
				if exclusions[i].id == r.ID {
					exclusions[i].path = r.SourcePath
				}
			}
			continue
		}
		// For overlapping roots, keep one stable resource row and its existing owner when possible.
		if ok && old.RootID != batch.RootID && old.RootID != "" {
			var other string
			err = tx.QueryRowContext(ctx, s.store.rebind("SELECT root_path FROM monitor_roots WHERE id=?"), old.RootID).Scan(&other)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if other != "" && hasPathPrefix(r.SourcePath, other) {
				r.RootID = old.RootID
				// Relative mapping belongs to the owner if it already monitors this path.
				r.RelPath = r.SourcePath[len(other)+1:]
			}
		}
		queued, e := s.recordObservation(ctx, tx, batch, r, old, ok, ManageAutoMonitor, "", "", now)
		if e != nil {
			return nil, e
		}
		if queued {
			pending = append(pending, r.ID)
		}
	}
	if full {
		for k, r := range existing {
			if !seen[k] && r.OriginType == OriginXiaoyaSQLite && !suppressed(exclusions, r) {
				if err = s.store.markMissing(ctx, tx, batch, r, now); err != nil {
					return nil, err
				}
			}
		}
		cursor.LastCompleteScanAt = now
	}
	if _, err = tx.ExecContext(ctx, s.store.rebind("UPDATE sync_batches SET scan_mode=? WHERE id=?"), batch.ScanMode, batch.ID); err != nil {
		return nil, err
	}
	cursor.RootID = batch.RootID
	cursor.RootNodeID = rootNode
	cursor.LastMaxNodeID = batch.MaxSourceNodeID
	cursor.LastSuccessBatchID = batch.ID
	cursor.UpdatedAt = now
	if err = s.store.saveCursor(ctx, tx, cursor); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return pending, nil
}

func hasPathPrefix(p, base string) bool {
	return len(p) > len(base) && p[:len(base)] == base && p[len(base)] == '/'
}

func (s *Service) recordObservation(ctx context.Context, tx *sql.Tx, b *SyncBatch, r, old MediaResource, exists bool, mode, library, dir string, now int64) (bool, error) {
	h := ComputeNodeHashes(r.Name, r.SourcePath, r.SourceParentID, r.IsDir, r.SourceContent)
	r.NameHash, r.PathHash, r.ContentHash, r.SnapshotHash = h.NameHash, h.PathHash, h.ContentHash, h.SnapshotHash
	r.Status = StatusActive
	r.LastSeenAt = now
	r.LastSeenBatchID = b.ID
	var changes []string
	if !exists {
		r.FirstSeenAt = now
		r.FirstSeenBatchID = b.ID
		change := ChangeAdded
		if b.IsBaseline {
			change = ChangeBaselineAdded
		} else if mode != ManageAutoMonitor {
			change = ChangeManualImported
		}
		changes = append(changes, change)
		b.AddedCount++
	} else {
		r.ID = old.ID
		r.FirstSeenAt = old.FirstSeenAt
		r.FirstSeenBatchID = old.FirstSeenBatchID
		r.LastChangedAt = old.LastChangedAt
		r.LocalSTRMPath = old.LocalSTRMPath
		r.MediaItemID = old.MediaItemID
		r.Status, r.LastError, r.RetryCount = old.Status, old.LastError, old.RetryCount
		if old.Status == StatusMissing {
			changes = append(changes, ChangeRestored)
			b.RestoredCount++
		}
		if r.SourceParentID != old.SourceParentID || (r.SourcePath != old.SourcePath && r.Name == old.Name) {
			changes = append(changes, ChangeMoved)
			b.MovedCount++
		} else if r.Name != old.Name {
			changes = append(changes, ChangeRenamed)
			b.RenamedCount++
		}
		if r.ContentHash != old.ContentHash || r.IsDir != old.IsDir {
			changes = append(changes, ChangeModified)
			b.ModifiedCount++
		}
	}
	if len(changes) > 0 {
		r.Status = StatusActive
		r.LastError = ""
		r.RetryCount = 0
		r.LastChangedAt = now
	}
	if err := s.store.UpsertResource(ctx, tx, r); err != nil {
		return false, err
	}
	if !exists || mode != ManageAutoMonitor || r.RootID != old.RootID {
		err := s.store.UpsertManagedResource(ctx, tx, ManagedResource{ResourceID: r.ID, ManageMode: mode, RootID: r.RootID, LibraryID: library, OutputDir: dir, Enabled: true, DeletionPolicy: "keep"})
		if err != nil {
			return false, err
		}
	}
	for _, change := range changes {
		c := ResourceChange{BatchID: b.ID, RootID: b.RootID, ResourceID: r.ID, SourceNodeID: r.SourceNodeID, ChangeType: change,
			OldPath: old.SourcePath, NewPath: r.SourcePath, OldContentHash: old.ContentHash, NewContentHash: r.ContentHash,
			OldSnapshotHash: old.SnapshotHash, NewSnapshotHash: r.SnapshotHash, DetectedAt: now}
		if err := s.store.InsertChange(ctx, tx, c); err != nil {
			return false, err
		}
	}
	if old.Status == StatusMissing {
		if err := s.store.refreshItemAvailability(ctx, tx, r.LocalSTRMPath); err != nil {
			return false, err
		}
	}
	queued := !r.IsDir && len(changes) > 0
	if queued {
		if err := s.store.enqueue(ctx, tx, r.ID); err != nil {
			return false, err
		}
	}
	return queued, nil
}

func (s *Service) finishMaterialization(ctx context.Context, b SyncBatch, pending []string, rootNode int64) (SyncBatch, error) {
	b.Status = BatchSuccess
	for _, id := range pending {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		res, err := s.ReprocessResource(ctx, id)
		if err == nil && strings.EqualFold(filepath.Ext(res.LocalSTRMPath), ".strm") {
			b.MaterializedSTRMs = append(b.MaterializedSTRMs, res.LocalSTRMPath)
		}
		if err != nil && !errors.Is(err, ErrResourceUnmanaged) && !errors.Is(err, ErrResourceMissing) {
			b.FailedCount++
			if b.ErrorMessage == "" {
				b.ErrorMessage = err.Error()
			}
		}
	}
	if b.FailedCount > 0 {
		b.Status = BatchPartial
	}
	b.FinishedAt = time.Now().Unix()
	if err := s.store.FinishBatch(ctx, b); err != nil {
		return b, err
	}
	if b.RootID != "" {
		if err := s.store.UpdateMonitorRootStatus(ctx, b.RootID, rootNode, b.ID, b.Status, b.ErrorMessage, b.FinishedAt); err != nil {
			return b, err
		}
	}
	return b, nil
}

func (s *Service) ReprocessResource(ctx context.Context, id string) (MediaResource, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	res, err := s.store.GetResource(ctx, id)
	if err != nil {
		return res, err
	}
	if res.Status == StatusMissing {
		return res, ErrResourceMissing
	}
	blocked, err := s.store.IsUnmanaged(ctx, res)
	if err != nil {
		return res, err
	}
	if blocked {
		return res, ErrResourceUnmanaged
	}
	if res.IsDir || s.processor == nil {
		return res, nil
	}
	_, res.OutputDir, err = s.store.ManagedTarget(ctx, id)
	if err != nil {
		return res, err
	}
	var root MonitorRoot
	if res.RootID != "" {
		root, err = s.store.GetMonitorRoot(ctx, res.RootID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return res, err
		}
	}
	p, err := s.processor.ProcessResource(ctx, root, res, ChangeModified)
	if e := s.store.processingResult(ctx, id, p, err); e != nil {
		return res, errors.Join(err, e)
	}
	if err != nil {
		return res, err
	}
	return s.store.GetResource(ctx, id)
}

func (s *Service) SetUnmanaged(ctx context.Context, id string, confirmed bool) (CleanupImpactPreview, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	return s.store.ConfirmDeleteResource(ctx, id, confirmed)
}

func (s *Service) RestoreManagement(ctx context.Context, id string) error {
	s.writes.Lock()
	defer s.writes.Unlock()
	return s.store.RestoreManagement(ctx, id)
}

// ManualNode is one discovered AList entry. Tree membership is distinct from opting into tracking.
type ManualNode struct {
	Path, RelPath, Name, Content string
	IsDir                        bool
}

type ManualSnapshot struct {
	RootPath, Mode, LibraryID, OutputDir string
	Nodes                                []ManualNode
	Missing                              []string // verified absent paths only
	Track                                *bool    // nil preserves the tracking preference
}

func (s *Service) ApplyManualSnapshot(ctx context.Context, m ManualSnapshot) (SyncBatch, []MediaResource, error) {
	b, err := s.store.CreateBatch(ctx, "", m.RootPath, ScanModeManualImport, false)
	if err != nil {
		return b, nil, err
	}
	pending, ids, err := s.commitManual(ctx, &b, m)
	if err != nil {
		b, err = s.failBatch(b, err)
		return b, nil, err
	}
	b, err = s.finishMaterialization(ctx, b, pending, 0)
	if err != nil {
		return b, nil, err
	}
	out := make([]MediaResource, 0, len(ids))
	for _, id := range ids {
		r, e := s.store.GetResource(ctx, id)
		if e != nil {
			return b, nil, e
		}
		out = append(out, r)
	}
	return b, out, nil
}

func (s *Service) commitManual(ctx context.Context, b *SyncBatch, m ManualSnapshot) ([]string, []string, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	exclusions, err := s.store.suppressions(ctx, tx, OriginAListManual)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().Unix()
	var pending, ids []string
	for _, n := range m.Nodes {
		key := "alist:" + n.Path
		r := MediaResource{ID: StableID(key), SourceKey: key, OriginType: OriginAListManual, SourcePath: n.Path, RelPath: n.RelPath, Name: n.Name, IsDir: n.IsDir, SourceContent: n.Content}
		if suppressed(exclusions, r) {
			if n.Path == m.RootPath && m.Track != nil {
				return nil, nil, ErrResourceUnmanaged
			}
			continue
		}
		old, exists, e := s.store.resourceByKey(ctx, tx, key)
		if e != nil {
			return nil, nil, e
		}
		library, output := m.LibraryID, m.OutputDir
		if exists && m.Mode == ManageManualSeriesDir && m.Track == nil {
			library, output = "", "" // Existing children retain their previously selected target.
		}
		if exists && old.LocalSTRMPath != "" && output != "" {
			rel, pathErr := filepath.Rel(output, old.LocalSTRMPath)
			if pathErr != nil || !filepath.IsLocal(rel) {
				return nil, nil, fmt.Errorf("已入库资源的输出路径不属于所选目录：%s", n.Path)
			}
		}
		queued, e := s.recordObservation(ctx, tx, b, r, old, exists, m.Mode, library, output, now)
		if e != nil {
			return nil, nil, e
		}
		ids = append(ids, r.ID)
		b.TotalSeen++
		if queued {
			pending = append(pending, r.ID)
		}
	}
	for _, p := range m.Missing {
		r, ok, e := s.store.resourceByKey(ctx, tx, "alist:"+p)
		if e != nil {
			return nil, nil, e
		}
		if ok && !suppressed(exclusions, r) {
			if err = s.store.markMissing(ctx, tx, b, r, now); err != nil {
				return nil, nil, err
			}
		}
	}
	if m.Track != nil {
		id := StableID("alist:" + m.RootPath)
		if *m.Track {
			_, err = tx.ExecContext(ctx, s.store.rebind("INSERT INTO alist_tracking_roots(resource_id,review) VALUES(?,0) ON CONFLICT(resource_id) DO UPDATE SET review=0"), id)
		} else {
			_, err = tx.ExecContext(ctx, s.store.rebind("INSERT INTO alist_tracking_roots(resource_id,review) VALUES(?,2) ON CONFLICT(resource_id) DO UPDATE SET review=2"), id)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return pending, ids, nil
}

func (s *Service) RegisterManualResource(ctx context.Context, p, rel, name string, dir bool, content, mode, library, output string) (MediaResource, error) {
	_, rs, err := s.ApplyManualSnapshot(ctx, ManualSnapshot{RootPath: p, Mode: mode, LibraryID: library, OutputDir: output, Nodes: []ManualNode{{Path: p, RelPath: rel, Name: name, IsDir: dir, Content: content}}})
	if err != nil {
		return MediaResource{}, err
	}
	if len(rs) == 0 {
		return MediaResource{}, ErrResourceUnmanaged
	}
	return rs[0], nil
}
