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
	versionMu sync.Mutex
	dataVers  map[string]int64
}

func NewService(store *SQLStore, reader *xiaoya.Reader, processor ResourceProcessor) *Service {
	s := &Service{store: store, processor: processor, running: map[string]string{}, dataVers: map[string]int64{}}
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

	var rootNode xiaoya.Node
	var high int64
	var dataVersion int64
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
		dataVersion, e = view.DataVersion(ctx)
		if e != nil {
			return e
		}
		// A replaced root or source database must be reconciled before reusing its old cursor.
		if !full && (cursor.RootNodeID != rootNode.ID || high < cursor.LastMaxNodeID) {
			full = true
			batch.ScanMode = ScanModeFull
		}
		if !full && has && dataVersion > 0 {
			s.versionMu.Lock()
			unchanged := s.dataVers[id] == dataVersion
			s.versionMu.Unlock()
			if unchanged {
				batch.Status = BatchSuccess
				batch.FinishedAt = time.Now().Unix()
				return nil
			}
		}
		collect := func(page []xiaoya.Node) error {
			batch.TotalSeen += int64(len(page))
			pending, chunkErr := s.commitSQLiteChunk(ctx, &batch, rootNode.ID, page)
			if chunkErr != nil {
				return chunkErr
			}
			return s.materializePending(ctx, &batch, pending)
		}
		if full {
			_, e = view.WalkSubtreeBatched(ctx, rootNode, 500, collect)
			return e
		}
		return view.WalkNewUnderRoot(ctx, rootNode, cursor.LastMaxNodeID, high, collect)
	})
	if err != nil {
		return s.failBatch(batch, err)
	}
	if batch.Status == BatchSuccess && batch.TotalSeen == 0 && !full {
		batch.MaxSourceNodeID = high
		if err := s.store.FinishBatch(ctx, batch); err != nil {
			return s.failBatch(batch, err)
		}
		if err := s.store.UpdateMonitorRootStatus(ctx, id, high, batch.ID, BatchSuccess, "", batch.FinishedAt); err != nil {
			return s.failBatch(batch, err)
		}
		return batch, nil
	}
	batch.MaxSourceNodeID = high
	err = s.finalizeSQLiteScan(ctx, &batch, rootNode.ID, cursor, full)
	if err != nil {
		return s.failBatch(batch, err)
	}
	s.versionMu.Lock()
	s.dataVers[id] = dataVersion
	s.versionMu.Unlock()
	return s.finishMaterialization(ctx, batch, nil, rootNode.ID)
}

// commitSQLiteChunk applies one bounded reader page. It intentionally looks up
// prior rows by the stable SQLite node key instead of loading the entire root
// into a Go map.
func (s *Service) commitSQLiteChunk(ctx context.Context, batch *SyncBatch, rootNode int64, nodes []xiaoya.Node) ([]string, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	exclusions, err := s.store.suppressions(ctx, tx, OriginXiaoyaSQLite)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var pending []string
	for _, n := range nodes {
		key := "sqlite:" + strconv.FormatInt(n.ID, 10)
		old, exists, lookupErr := s.store.resourceByKey(ctx, tx, key)
		if lookupErr != nil {
			return nil, lookupErr
		}
		r := MediaResource{ID: StableID(key), SourceKey: key, OriginType: OriginXiaoyaSQLite, RootID: batch.RootID,
			SourceNodeID: n.ID, SourceParentID: n.ParentID, SourcePath: n.FullPath, RelPath: n.RelPath, Name: n.Name, IsDir: n.IsDir, SourceContent: n.Content}
		if suppressed(exclusions, r) {
			if _, err = tx.ExecContext(ctx, s.store.rebind("UPDATE resource_suppressions SET source_path=? WHERE resource_id=? AND source_path<>?"), r.SourcePath, r.ID, r.SourcePath); err != nil {
				return nil, err
			}
			continue
		}
		if exists && old.RootID != batch.RootID && old.RootID != "" {
			var other string
			err = tx.QueryRowContext(ctx, s.store.rebind("SELECT root_path FROM monitor_roots WHERE id=?"), old.RootID).Scan(&other)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if other != "" && hasPathPrefix(r.SourcePath, other) {
				r.RootID = old.RootID
				r.RelPath = r.SourcePath[len(other)+1:]
			}
		}
		queued, err := s.recordObservation(ctx, tx, batch, r, old, exists, ManageAutoMonitor, "", "", now)
		if err != nil {
			return nil, err
		}
		if queued {
			pending = append(pending, r.ID)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	_ = rootNode // retained in the signature to document the scan ownership.
	return pending, nil
}

func (s *Service) finalizeSQLiteScan(ctx context.Context, batch *SyncBatch, rootNode int64, cursor SyncCursor, full bool) error {
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if full {
		exclusions, e := s.store.suppressions(ctx, tx, OriginXiaoyaSQLite)
		if e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, s.store.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE root_id=? AND origin_type=? AND status=? AND last_seen_batch_id<>? ORDER BY id"), batch.RootID, OriginXiaoyaSQLite, StatusActive, batch.ID)
		if e != nil {
			return e
		}
		for rows.Next() {
			r, scanErr := scanMediaResource(rows)
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			if suppressed(exclusions, r) {
				continue
			}
			if e = s.store.markMissing(ctx, tx, batch, r, now); e != nil {
				rows.Close()
				return e
			}
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		rows.Close()
		cursor.LastCompleteScanAt = now
	}
	if _, err = tx.ExecContext(ctx, s.store.rebind("UPDATE sync_batches SET scan_mode=? WHERE id=?"), batch.ScanMode, batch.ID); err != nil {
		return err
	}
	cursor.RootID = batch.RootID
	cursor.RootNodeID = rootNode
	cursor.LastMaxNodeID = batch.MaxSourceNodeID
	cursor.LastSuccessBatchID = batch.ID
	cursor.UpdatedAt = now
	if err = s.store.saveCursor(ctx, tx, cursor); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) materializePending(ctx context.Context, batch *SyncBatch, pending []string) error {
	for _, id := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := s.ReprocessResource(ctx, id)
		if err == nil {
			if identityErr := s.PersistV2Identity(ctx, res); identityErr != nil {
				batch.FailedCount++
				if batch.ErrorMessage == "" {
					batch.ErrorMessage = identityErr.Error()
				}
			}
		}
		if err == nil && strings.EqualFold(filepath.Ext(res.LocalSTRMPath), ".strm") {
			batch.MaterializedSTRMs = append(batch.MaterializedSTRMs, res.LocalSTRMPath)
		}
		if err != nil && !errors.Is(err, ErrResourceUnmanaged) && !errors.Is(err, ErrResourceMissing) {
			batch.FailedCount++
			if batch.ErrorMessage == "" {
				batch.ErrorMessage = err.Error()
			}
		}
	}
	return nil
}

// PersistV2Identity stores the deterministic identity and playback version
// produced from one SQLite resource. Provider matching can update the same
// rows later without changing the source key or output path.
func (s *Service) PersistV2Identity(ctx context.Context, res MediaResource) error {
	identity := ParseMediaIdentity(res)
	if identity.MediaType == "" {
		return nil
	}
	now := time.Now().Unix()
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	mediaID := identity.ParentID
	if identity.MediaType == "MOVIE" {
		mediaID = StableID("media:MOVIE:" + identity.Title + ":" + strconv.Itoa(identity.Year))
	} else {
		if _, err = tx.ExecContext(ctx, s.store.rebind(`INSERT INTO media_items_v2(id,media_type,title,sort_title,year,state,revision,created_at,updated_at)
VALUES(?,?,?,?,?,'ACTIVE',1,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,year=excluded.year,updated_at=excluded.updated_at`),
			identity.ParentID, "SERIES", identity.Title, identity.Title, identity.Year, now, now); err != nil {
			return err
		}
	}
	if identity.MediaType == "MOVIE" {
		if _, err = tx.ExecContext(ctx, s.store.rebind(`INSERT INTO media_items_v2(id,media_type,title,sort_title,year,parent_id,season_number,episode_number,state,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?, 'ACTIVE',1,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,year=excluded.year,updated_at=excluded.updated_at`),
			mediaID, identity.MediaType, identity.Title, identity.Title, identity.Year, "", 0, 0, now, now); err != nil {
			return err
		}
	} else {
		mediaID = StableID("media:EPISODE:" + identity.ParentID + ":" + strconv.Itoa(identity.SeasonNumber) + ":" + strconv.Itoa(identity.EpisodeNumber))
		if _, err = tx.ExecContext(ctx, s.store.rebind(`INSERT INTO media_items_v2(id,media_type,title,sort_title,year,parent_id,season_number,episode_number,state,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?, 'ACTIVE',1,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,parent_id=excluded.parent_id,season_number=excluded.season_number,episode_number=excluded.episode_number,updated_at=excluded.updated_at`),
			mediaID, "EPISODE", identity.Title, identity.Title, identity.Year, identity.ParentID, identity.SeasonNumber, identity.EpisodeNumber, now, now); err != nil {
			return err
		}
	}
	recID := StableID("recognition:" + res.ID)
	if _, err = tx.ExecContext(ctx, s.store.rebind(`INSERT INTO recognition_results(id,source_resource_id,media_type,title,year,season_number,episode_number,evidence,state,revision,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_resource_id) DO UPDATE SET media_type=excluded.media_type,title=excluded.title,year=excluded.year,season_number=excluded.season_number,episode_number=excluded.episode_number,evidence=excluded.evidence,state=excluded.state,revision=recognition_results.revision+1,updated_at=excluded.updated_at`),
		recID, res.ID, identity.MediaType, identity.Title, identity.Year, identity.SeasonNumber, identity.EpisodeNumber, identity.Evidence, identity.State, 1, now); err != nil {
		return err
	}
	versionID := StableID("version:" + mediaID + ":" + res.ID)
	if _, err = tx.ExecContext(ctx, s.store.rebind(`INSERT INTO media_versions_v2(id,media_id,source_resource_id,label,stream_uri,revision,updated_at)
VALUES(?,?,?,?,?,1,?) ON CONFLICT(media_id,source_resource_id) DO UPDATE SET stream_uri=excluded.stream_uri,revision=media_versions_v2.revision+1,updated_at=excluded.updated_at`),
		versionID, mediaID, res.ID, res.Name, res.SourceContent, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_, err = s.store.EnqueueV2Job(ctx, "metadata_match", res.ID, res.LastChangedAt, `{"resource_id":"`+res.ID+`"}`, now)
	return err
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
	if exists && len(changes) == 0 && old.Status == StatusActive && old.NameHash == r.NameHash && old.PathHash == r.PathHash && old.ContentHash == r.ContentHash && old.SnapshotHash == r.SnapshotHash {
		// A successful observation still advances the batch proof used by full
		// scans, but an unchanged resource does not need a full-row upsert.
		_, err := tx.ExecContext(ctx, s.store.rebind("UPDATE media_resources SET last_seen_batch_id=?, last_seen_at=? WHERE id=?"), b.ID, now, old.ID)
		return false, err
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
	SourceNodeID, SourceParentID int64
}

type ManualSnapshot struct {
	RootPath, Mode, LibraryID, OutputDir string
	// OriginType selects the authoritative source for this snapshot.  Existing
	// callers default to AList manual import; SQLite-resolved imports set
	// OriginXiaoyaSQLite so downstream keys and suppression rules stay aligned
	// with the sole resource source policy.
	OriginType string
	Nodes      []ManualNode
	Missing    []string // verified absent paths only
	Track      *bool    // nil preserves the tracking preference
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
	origin := m.OriginType
	if origin == "" {
		origin = OriginAListManual
	}
	keyPrefix := "alist:"
	if origin == OriginXiaoyaSQLite {
		keyPrefix = "xiaoya:"
	}
	exclusions, err := s.store.suppressions(ctx, tx, origin)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().Unix()
	var pending, ids []string
	for _, n := range m.Nodes {
		key := keyPrefix + n.Path
		sourceNodeID, sourceParentID := int64(0), int64(0)
		if origin == OriginXiaoyaSQLite && n.SourceNodeID > 0 {
			key = "sqlite:" + strconv.FormatInt(n.SourceNodeID, 10)
			sourceNodeID, sourceParentID = n.SourceNodeID, n.SourceParentID
		}
		r := MediaResource{ID: StableID(key), SourceKey: key, OriginType: origin, SourceNodeID: sourceNodeID, SourceParentID: sourceParentID, SourcePath: n.Path, RelPath: n.RelPath, Name: n.Name, IsDir: n.IsDir, SourceContent: n.Content}
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
		r, ok, e := s.store.resourceByKey(ctx, tx, keyPrefix+p)
		if e != nil {
			return nil, nil, e
		}
		if ok && !suppressed(exclusions, r) {
			if err = s.store.markMissing(ctx, tx, b, r, now); err != nil {
				return nil, nil, err
			}
		}
	}
	if m.Track != nil && origin == OriginAListManual {
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
