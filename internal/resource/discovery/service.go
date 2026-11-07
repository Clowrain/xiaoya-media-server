package discovery

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"xiaoya-media-server/internal/resource/xiaoya"
)

var ErrRootScanInProgress = errors.New("a scan is already running for this monitor root")

// ResourceProcessor materializes STRM and companion attachments for a discovered file resource.
type ResourceProcessor interface {
	ProcessResource(ctx context.Context, root MonitorRoot, res MediaResource, changeType string) (localSTRMPath string, err error)
}

type Service struct {
	store     *SQLStore
	reader    *xiaoya.Reader
	processor ResourceProcessor

	locksMu   sync.Mutex
	rootLocks map[string]*sync.Mutex
}

func NewService(store *SQLStore, reader *xiaoya.Reader, processor ResourceProcessor) *Service {
	return &Service{
		store:     store,
		reader:    reader,
		processor: processor,
		rootLocks: make(map[string]*sync.Mutex),
	}
}

func (s *Service) SetProcessor(p ResourceProcessor) {
	s.processor = p
}

func (s *Service) Store() *SQLStore {
	return s.store
}

func (s *Service) tryLockRoot(rootID string) (func(), bool) {
	s.locksMu.Lock()
	mu := s.rootLocks[rootID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.rootLocks[rootID] = mu
	}
	s.locksMu.Unlock()

	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

func sqliteSourceKey(nodeID int64) string {
	return "sqlite:" + strconv.FormatInt(nodeID, 10)
}

func alistSourceKey(sourcePath string) string {
	return "alist:" + sourcePath
}

// RegisterMonitorRoots normalizes and registers initial or user-added monitor root paths.
func (s *Service) RegisterMonitorRoots(ctx context.Context, paths []string, interval time.Duration) ([]MonitorRoot, error) {
	sec := int64(interval.Seconds())
	if sec <= 0 {
		sec = 600
	}
	out := make([]MonitorRoot, 0, len(paths))
	for _, raw := range paths {
		norm, _, err := xiaoya.NormalizeMonitorPath(raw)
		if err != nil {
			return nil, fmt.Errorf("normalize monitor root %q: %w", raw, err)
		}
		mr, err := s.store.UpsertMonitorRoot(ctx, norm, true, sec)
		if err != nil {
			return nil, err
		}
		out = append(out, mr)
	}
	return out, nil
}

// ScanRootFull performs a complete subtree scan (BASELINE on first run, FULL thereafter).
// Rules enforced:
//   - Concurrent scans on the same root are rejected.
//   - Incomplete/failed scans NEVER mark resources MISSING and NEVER advance SyncCursor.
//   - Disappearing resources on a complete scan transition to MISSING under the `keep` policy.
func (s *Service) ScanRootFull(ctx context.Context, rootID string) (SyncBatch, error) {
	unlock, ok := s.tryLockRoot(rootID)
	if !ok {
		return SyncBatch{}, ErrRootScanInProgress
	}
	defer unlock()

	root, err := s.store.GetMonitorRoot(ctx, rootID)
	if err != nil {
		return SyncBatch{}, err
	}
	if s.reader == nil {
		return SyncBatch{}, errors.New("xiaoya sqlite reader is not configured")
	}

	_, hasCursor, err := s.store.GetCursor(ctx, root.ID)
	if err != nil {
		return SyncBatch{}, err
	}
	isBaseline := !hasCursor
	mode := ScanModeFull
	if isBaseline {
		mode = ScanModeBaseline
	}

	batch, err := s.store.CreateBatch(ctx, root.ID, root.RootPath, mode, isBaseline)
	if err != nil {
		return SyncBatch{}, err
	}
	_ = s.store.UpdateMonitorRootStatus(ctx, root.ID, root.SourceNodeID, batch.ID, BatchRunning, "", 0)

	failScan := func(scanErr error) (SyncBatch, error) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		batch.Status = BatchFailed
		batch.FinishedAt = time.Now().Unix()
		batch.ErrorMessage = scanErr.Error()
		_ = s.store.FinishBatch(bgCtx, batch)
		_ = s.store.UpdateMonitorRootStatus(bgCtx, root.ID, root.SourceNodeID, batch.ID, BatchFailed, scanErr.Error(), batch.FinishedAt)
		return batch, scanErr
	}

	rootNode, err := s.reader.ResolvePath(ctx, root.RootPath)
	if err != nil {
		return failScan(err)
	}

	existingByKey, err := s.store.LoadRootResourcesBySourceKey(ctx, root.ID)
	if err != nil {
		return failScan(err)
	}

	seenKeys := make(map[string]bool, len(existingByKey)+64)
	type pendingProcess struct {
		res        MediaResource
		changeType string
	}
	var toProcess []pendingProcess

	now := time.Now().Unix()
	stats, walkErr := s.reader.WalkSubtreeBatched(ctx, rootNode, 500, func(nodes []xiaoya.Node) error {
		tx, txErr := s.store.DB().BeginTx(ctx, nil)
		if txErr != nil {
			return txErr
		}
		defer tx.Rollback()

		for _, n := range nodes {
			sKey := sqliteSourceKey(n.ID)
			seenKeys[sKey] = true
			hashes := ComputeNodeHashes(n.Name, n.FullPath, n.ParentID, n.IsDir, n.Content)

			old, existed := existingByKey[sKey]
			if !existed {
				changeType := ChangeAdded
				if isBaseline {
					changeType = ChangeBaselineAdded
				}
				res := MediaResource{
					ID:               StableID(sKey),
					SourceKey:        sKey,
					OriginType:       OriginXiaoyaSQLite,
					RootID:           root.ID,
					SourceNodeID:     n.ID,
					SourceParentID:   n.ParentID,
					SourcePath:       n.FullPath,
					RelPath:          n.RelPath,
					Name:             n.Name,
					IsDir:            n.IsDir,
					SourceContent:    n.Content,
					NameHash:         hashes.NameHash,
					PathHash:         hashes.PathHash,
					ContentHash:      hashes.ContentHash,
					SnapshotHash:     hashes.SnapshotHash,
					Status:           StatusActive,
					FirstSeenBatchID: batch.ID,
					LastSeenBatchID:  batch.ID,
					FirstSeenAt:      now,
					LastSeenAt:       now,
					LastChangedAt:    now,
				}
				if err := s.store.UpsertResource(ctx, tx, res); err != nil {
					return err
				}
				if err := s.store.UpsertManagedResource(ctx, tx, ManagedResource{
					ResourceID:     res.ID,
					ManageMode:     ManageAutoMonitor,
					RootID:         root.ID,
					Enabled:        true,
					DeletionPolicy: "keep",
				}); err != nil {
					return err
				}
				if err := s.store.InsertChange(ctx, tx, ResourceChange{
					BatchID:         batch.ID,
					RootID:          root.ID,
					ResourceID:      res.ID,
					SourceNodeID:    n.ID,
					ChangeType:      changeType,
					NewPath:         n.FullPath,
					NewContentHash:  hashes.ContentHash,
					NewSnapshotHash: hashes.SnapshotHash,
					DetectedAt:      now,
				}); err != nil {
					return err
				}
				batch.AddedCount++
				existingByKey[sKey] = res
				if !res.IsDir {
					toProcess = append(toProcess, pendingProcess{res: res, changeType: changeType})
				}
				continue
			}

			// Existing resource: detect RESTORED, MOVED, RENAMED, MODIFIED, or unchanged
			var detectedChanges []string
			if old.Status == StatusMissing {
				detectedChanges = append(detectedChanges, ChangeRestored)
				batch.RestoredCount++
			}
			if old.SourceParentID != n.ParentID || (old.SourcePath != n.FullPath && old.Name == n.Name) {
				detectedChanges = append(detectedChanges, ChangeMoved)
				batch.MovedCount++
			} else if old.Name != n.Name {
				detectedChanges = append(detectedChanges, ChangeRenamed)
				batch.RenamedCount++
			}
			if old.ContentHash != hashes.ContentHash {
				detectedChanges = append(detectedChanges, ChangeModified)
				batch.ModifiedCount++
			}

			updated := old
			updated.RootID = root.ID
			updated.SourceParentID = n.ParentID
			updated.SourcePath = n.FullPath
			updated.RelPath = n.RelPath
			updated.Name = n.Name
			updated.IsDir = n.IsDir
			updated.SourceContent = n.Content
			updated.NameHash = hashes.NameHash
			updated.PathHash = hashes.PathHash
			updated.ContentHash = hashes.ContentHash
			updated.SnapshotHash = hashes.SnapshotHash
			updated.LastSeenBatchID = batch.ID
			updated.LastSeenAt = now
			if len(detectedChanges) > 0 || old.Status == StatusFailed {
				updated.Status = StatusActive
				updated.LastError = ""
				if len(detectedChanges) > 0 {
					updated.LastChangedAt = now
				}
			}

			if err := s.store.UpsertResource(ctx, tx, updated); err != nil {
				return err
			}
			for _, ct := range detectedChanges {
				if err := s.store.InsertChange(ctx, tx, ResourceChange{
					BatchID:         batch.ID,
					RootID:          root.ID,
					ResourceID:      updated.ID,
					SourceNodeID:    n.ID,
					ChangeType:      ct,
					OldPath:         old.SourcePath,
					NewPath:         n.FullPath,
					OldContentHash:  old.ContentHash,
					NewContentHash:  hashes.ContentHash,
					OldSnapshotHash: old.SnapshotHash,
					NewSnapshotHash: hashes.SnapshotHash,
					DetectedAt:      now,
				}); err != nil {
					return err
				}
			}
			existingByKey[sKey] = updated
			if !updated.IsDir && (len(detectedChanges) > 0 || old.Status == StatusFailed || updated.LocalSTRMPath == "") {
				primaryChange := ChangeModified
				if len(detectedChanges) > 0 {
					primaryChange = detectedChanges[0]
				}
				toProcess = append(toProcess, pendingProcess{res: updated, changeType: primaryChange})
			}
		}
		return tx.Commit()
	})
	if walkErr != nil {
		// Incomplete/failed scan: NEVER mark MISSING and NEVER advance cursor!
		return failScan(walkErr)
	}

	batch.TotalSeen = int64(stats.TotalNodes)
	batch.MaxSourceNodeID = stats.MaxNodeID
	if globalMax, mErr := s.reader.MaxNodeID(ctx); mErr == nil && globalMax > batch.MaxSourceNodeID {
		batch.MaxSourceNodeID = globalMax
	}

	// Complete scan succeeded: mark any previously ACTIVE/FAILED resource not seen in this subtree as MISSING (`keep` policy)
	missingTx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return failScan(err)
	}
	for sKey, prev := range existingByKey {
		if seenKeys[sKey] || prev.OriginType != OriginXiaoyaSQLite {
			continue
		}
		if prev.Status == StatusMissing {
			continue
		}
		prev.Status = StatusMissing
		prev.LastChangedAt = now
		if err := s.store.UpsertResource(ctx, missingTx, prev); err != nil {
			missingTx.Rollback()
			return failScan(err)
		}
		if err := s.store.InsertChange(ctx, missingTx, ResourceChange{
			BatchID:         batch.ID,
			RootID:          root.ID,
			ResourceID:      prev.ID,
			SourceNodeID:    prev.SourceNodeID,
			ChangeType:      ChangeMissing,
			OldPath:         prev.SourcePath,
			OldContentHash:  prev.ContentHash,
			OldSnapshotHash: prev.SnapshotHash,
			DetectedAt:      now,
		}); err != nil {
			missingTx.Rollback()
			return failScan(err)
		}
		batch.MissingCount++
		if prev.LocalSTRMPath != "" {
			s.store.SetItemAvailabilityBySTRMPath(ctx, prev.LocalSTRMPath, "MISSING")
		}
	}
	if err := missingTx.Commit(); err != nil {
		return failScan(err)
	}

	// Execute STRM & attachment materialization for newly discovered / changed / restored files
	if s.processor != nil {
		for _, item := range toProcess {
			localPath, pErr := s.processor.ProcessResource(ctx, root, item.res, item.changeType)
			if pErr != nil {
				batch.FailedCount++
				_ = s.store.UpdateResourceSTRMResult(ctx, item.res.ID, localPath, StatusFailed, pErr.Error())
				continue
			}
			_ = s.store.UpdateResourceSTRMResult(ctx, item.res.ID, localPath, StatusActive, "")
			if localPath != "" {
				s.store.SetItemAvailabilityBySTRMPath(ctx, localPath, "AVAILABLE")
			}
		}
	}

	finishedAt := time.Now().Unix()
	batch.Status = BatchSuccess
	batch.FinishedAt = finishedAt
	if err := s.store.FinishBatch(ctx, batch); err != nil {
		return batch, err
	}
	if err := s.store.SaveCursor(ctx, SyncCursor{
		RootID:             root.ID,
		RootNodeID:         rootNode.ID,
		LastMaxNodeID:      batch.MaxSourceNodeID,
		LastSuccessBatchID: batch.ID,
		LastCompleteScanAt: finishedAt,
		UpdatedAt:          finishedAt,
	}); err != nil {
		return batch, err
	}
	_ = s.store.UpdateMonitorRootStatus(ctx, root.ID, rootNode.ID, batch.ID, BatchSuccess, "", finishedAt)
	return batch, nil
}

// ScanRootIncremental uses the primary-key high-water mark (`nodes.id > last_max_node_id`)
// to quickly ingest newly appended directories and files under `rootID` without full-table scans.
// If no baseline cursor exists yet, it automatically falls back to ScanRootFull.
func (s *Service) ScanRootIncremental(ctx context.Context, rootID string) (SyncBatch, error) {
	cursor, hasCursor, err := s.store.GetCursor(ctx, rootID)
	if err != nil {
		return SyncBatch{}, err
	}
	if !hasCursor {
		return s.ScanRootFull(ctx, rootID)
	}

	unlock, ok := s.tryLockRoot(rootID)
	if !ok {
		return SyncBatch{}, ErrRootScanInProgress
	}
	defer unlock()

	root, err := s.store.GetMonitorRoot(ctx, rootID)
	if err != nil {
		return SyncBatch{}, err
	}
	if s.reader == nil {
		return SyncBatch{}, errors.New("xiaoya sqlite reader is not configured")
	}

	batch, err := s.store.CreateBatch(ctx, root.ID, root.RootPath, ScanModeIncremental, false)
	if err != nil {
		return SyncBatch{}, err
	}
	_ = s.store.UpdateMonitorRootStatus(ctx, root.ID, root.SourceNodeID, batch.ID, BatchRunning, "", 0)

	failScan := func(scanErr error) (SyncBatch, error) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		batch.Status = BatchFailed
		batch.FinishedAt = time.Now().Unix()
		batch.ErrorMessage = scanErr.Error()
		_ = s.store.FinishBatch(bgCtx, batch)
		_ = s.store.UpdateMonitorRootStatus(bgCtx, root.ID, root.SourceNodeID, batch.ID, BatchFailed, scanErr.Error(), batch.FinishedAt)
		return batch, scanErr
	}

	rootNode, err := s.reader.ResolvePath(ctx, root.RootPath)
	if err != nil {
		return failScan(err)
	}

	existingByKey, err := s.store.LoadRootResourcesBySourceKey(ctx, root.ID)
	if err != nil {
		return failScan(err)
	}

	type dirPaths struct {
		full string
		rel  string
	}
	knownDirs := map[int64]dirPaths{
		rootNode.ID: {full: strings.TrimRight(rootNode.FullPath, "/"), rel: ""},
	}
	for _, m := range existingByKey {
		if m.IsDir && m.SourceNodeID > 0 {
			knownDirs[m.SourceNodeID] = dirPaths{full: m.SourcePath, rel: m.RelPath}
		}
	}

	var toProcess []MediaResource
	now := time.Now().Unix()
	maxID, readErr := s.reader.ReadNodesAfterID(ctx, cursor.LastMaxNodeID, 1000, func(rawNodes []xiaoya.RawNode) error {
		tx, txErr := s.store.DB().BeginTx(ctx, nil)
		if txErr != nil {
			return txErr
		}
		defer tx.Rollback()

		for _, raw := range rawNodes {
			pDir, inSubtree := knownDirs[raw.ParentID]
			if !inSubtree {
				continue
			}
			fullPath := pDir.full + "/" + raw.Name
			relPath := raw.Name
			if pDir.rel != "" {
				relPath = pDir.rel + "/" + raw.Name
			}
			if raw.IsDir {
				knownDirs[raw.ID] = dirPaths{full: fullPath, rel: relPath}
			}

			sKey := sqliteSourceKey(raw.ID)
			hashes := ComputeNodeHashes(raw.Name, fullPath, raw.ParentID, raw.IsDir, raw.Content)
			res := MediaResource{
				ID:               StableID(sKey),
				SourceKey:        sKey,
				OriginType:       OriginXiaoyaSQLite,
				RootID:           root.ID,
				SourceNodeID:     raw.ID,
				SourceParentID:   raw.ParentID,
				SourcePath:       fullPath,
				RelPath:          relPath,
				Name:             raw.Name,
				IsDir:            raw.IsDir,
				SourceContent:    raw.Content,
				NameHash:         hashes.NameHash,
				PathHash:         hashes.PathHash,
				ContentHash:      hashes.ContentHash,
				SnapshotHash:     hashes.SnapshotHash,
				Status:           StatusActive,
				FirstSeenBatchID: batch.ID,
				LastSeenBatchID:  batch.ID,
				FirstSeenAt:      now,
				LastSeenAt:       now,
				LastChangedAt:    now,
			}
			if err := s.store.UpsertResource(ctx, tx, res); err != nil {
				return err
			}
			if err := s.store.UpsertManagedResource(ctx, tx, ManagedResource{
				ResourceID:     res.ID,
				ManageMode:     ManageAutoMonitor,
				RootID:         root.ID,
				Enabled:        true,
				DeletionPolicy: "keep",
			}); err != nil {
				return err
			}
			if err := s.store.InsertChange(ctx, tx, ResourceChange{
				BatchID:         batch.ID,
				RootID:          root.ID,
				ResourceID:      res.ID,
				SourceNodeID:    raw.ID,
				ChangeType:      ChangeAdded,
				NewPath:         fullPath,
				NewContentHash:  hashes.ContentHash,
				NewSnapshotHash: hashes.SnapshotHash,
				DetectedAt:      now,
			}); err != nil {
				return err
			}
			batch.TotalSeen++
			batch.AddedCount++
			if !res.IsDir {
				toProcess = append(toProcess, res)
			}
		}
		return tx.Commit()
	})
	if readErr != nil {
		return failScan(readErr)
	}

	if s.processor != nil {
		for _, res := range toProcess {
			localPath, pErr := s.processor.ProcessResource(ctx, root, res, ChangeAdded)
			if pErr != nil {
				batch.FailedCount++
				_ = s.store.UpdateResourceSTRMResult(ctx, res.ID, localPath, StatusFailed, pErr.Error())
				continue
			}
			_ = s.store.UpdateResourceSTRMResult(ctx, res.ID, localPath, StatusActive, "")
			if localPath != "" {
				s.store.SetItemAvailabilityBySTRMPath(ctx, localPath, "AVAILABLE")
			}
		}
	}

	finishedAt := time.Now().Unix()
	batch.MaxSourceNodeID = maxID
	batch.Status = BatchSuccess
	batch.FinishedAt = finishedAt
	if err := s.store.FinishBatch(ctx, batch); err != nil {
		return batch, err
	}
	if err := s.store.SaveCursor(ctx, SyncCursor{
		RootID:             root.ID,
		RootNodeID:         rootNode.ID,
		LastMaxNodeID:      maxID,
		LastSuccessBatchID: batch.ID,
		LastCompleteScanAt: cursor.LastCompleteScanAt,
		UpdatedAt:          finishedAt,
	}); err != nil {
		return batch, err
	}
	_ = s.store.UpdateMonitorRootStatus(ctx, root.ID, rootNode.ID, batch.ID, BatchSuccess, "", finishedAt)
	return batch, nil
}

// ReprocessResource re-runs STRM & attachment generation for a single resource.
func (s *Service) ReprocessResource(ctx context.Context, resourceID string) (MediaResource, error) {
	res, err := s.store.GetResource(ctx, resourceID)
	if err != nil {
		return MediaResource{}, err
	}
	if res.IsDir || s.processor == nil {
		return res, nil
	}
	var root MonitorRoot
	if res.RootID != "" {
		root, _ = s.store.GetMonitorRoot(ctx, res.RootID)
	}
	localPath, pErr := s.processor.ProcessResource(ctx, root, res, ChangeModified)
	if pErr != nil {
		_ = s.store.UpdateResourceSTRMResult(ctx, res.ID, localPath, StatusFailed, pErr.Error())
		return MediaResource{}, pErr
	}
	if err := s.store.UpdateResourceSTRMResult(ctx, res.ID, localPath, StatusActive, ""); err != nil {
		return MediaResource{}, err
	}
	return s.store.GetResource(ctx, resourceID)
}

// RegisterManualResource imports an AList file or directory into the unified `media_resources` + `managed_resources` model.
func (s *Service) RegisterManualResource(ctx context.Context, sourcePath, relPath, name string, isDir bool, content, manageMode, libraryID, outputDir string) (MediaResource, error) {
	now := time.Now().Unix()
	sKey := alistSourceKey(sourcePath)
	hashes := ComputeNodeHashes(name, sourcePath, 0, isDir, content)
	batch, err := s.store.CreateBatch(ctx, "", sourcePath, ScanModeManualImport, false)
	if err != nil {
		return MediaResource{}, err
	}

	res := MediaResource{
		ID:               StableID(sKey),
		SourceKey:        sKey,
		OriginType:       OriginAListManual,
		SourcePath:       sourcePath,
		RelPath:          relPath,
		Name:             name,
		IsDir:            isDir,
		SourceContent:    content,
		NameHash:         hashes.NameHash,
		PathHash:         hashes.PathHash,
		ContentHash:      hashes.ContentHash,
		SnapshotHash:     hashes.SnapshotHash,
		Status:           StatusActive,
		FirstSeenBatchID: batch.ID,
		LastSeenBatchID:  batch.ID,
		FirstSeenAt:      now,
		LastSeenAt:       now,
		LastChangedAt:    now,
	}

	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return MediaResource{}, err
	}
	defer tx.Rollback()

	if err := s.store.UpsertResource(ctx, tx, res); err != nil {
		return MediaResource{}, err
	}
	if err := s.store.UpsertManagedResource(ctx, tx, ManagedResource{
		ResourceID:     res.ID,
		ManageMode:     manageMode,
		LibraryID:      libraryID,
		OutputDir:      outputDir,
		Enabled:        true,
		DeletionPolicy: "keep",
	}); err != nil {
		return MediaResource{}, err
	}
	if err := s.store.InsertChange(ctx, tx, ResourceChange{
		BatchID:         batch.ID,
		ResourceID:      res.ID,
		ChangeType:      ChangeManualImported,
		NewPath:         sourcePath,
		NewContentHash:  hashes.ContentHash,
		NewSnapshotHash: hashes.SnapshotHash,
		DetectedAt:      now,
	}); err != nil {
		return MediaResource{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaResource{}, err
	}

	batch.TotalSeen = 1
	batch.AddedCount = 1
	batch.Status = BatchSuccess
	batch.FinishedAt = time.Now().Unix()

	if !res.IsDir && s.processor != nil {
		syntheticRoot := MonitorRoot{RootPath: "/"}
		localPath, pErr := s.processor.ProcessResource(ctx, syntheticRoot, res, ChangeManualImported)
		if pErr != nil {
			batch.FailedCount = 1
			_ = s.store.UpdateResourceSTRMResult(ctx, res.ID, localPath, StatusFailed, pErr.Error())
		} else {
			_ = s.store.UpdateResourceSTRMResult(ctx, res.ID, localPath, StatusActive, "")
		}
	}
	_ = s.store.FinishBatch(ctx, batch)
	return s.store.GetResource(ctx, res.ID)
}
