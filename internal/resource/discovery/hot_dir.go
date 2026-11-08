package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const hotDirSchema = `
CREATE TABLE IF NOT EXISTS hot_directories (
    id TEXT PRIMARY KEY,
    root_id TEXT NOT NULL,
    dir_path TEXT NOT NULL,
    source_node_id BIGINT NOT NULL DEFAULT 0,
    interval_sec BIGINT NOT NULL DEFAULT 1800,
    consecutive_unchanged BIGINT NOT NULL DEFAULT 0,
    last_scanned_at BIGINT NOT NULL DEFAULT 0,
    next_due_at BIGINT NOT NULL DEFAULT 0,
    enabled BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    UNIQUE(root_id, dir_path)
);
CREATE INDEX IF NOT EXISTS idx_hot_directories_due ON hot_directories(enabled, next_due_at);
`

// TouchHotDirectory registers or updates an actively tracking series/media directory.
// When changes are detected, interval resets to 30m (1800s); when consecutive scans
// yield no changes, backoff extends the interval: 30m -> 60m -> 120m -> 360m (max 6h).
func (s *SQLStore) TouchHotDirectory(ctx context.Context, rootID, dirPath string, sourceNodeID int64, hasChanges bool) error {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" || rootID == "" {
		return nil
	}
	id := StableID("hot:" + rootID + ":" + dirPath)
	now := time.Now().Unix()

	var existingInterval, consecutive int64
	qSelect := s.rebind("SELECT interval_sec, consecutive_unchanged FROM hot_directories WHERE id=?")
	err := s.db.QueryRowContext(ctx, qSelect, id).Scan(&existingInterval, &consecutive)

	var nextInterval int64
	var nextConsecutive int64

	if errors.Is(err, sql.ErrNoRows) {
		nextInterval = 1800 // initial 30 minutes
		nextConsecutive = 0
	} else if err != nil {
		return err
	} else if hasChanges {
		nextInterval = 1800 // reset back to 30 minutes on changes
		nextConsecutive = 0
	} else {
		nextConsecutive = consecutive + 1
		switch {
		case nextConsecutive == 1:
			nextInterval = 3600 // 60 minutes
		case nextConsecutive == 2:
			nextInterval = 7200 // 120 minutes
		default:
			nextInterval = 21600 // 360 minutes (6 hours max)
		}
	}

	nextDue := now + nextInterval
	qUpsert := s.rebind(`INSERT INTO hot_directories(id, root_id, dir_path, source_node_id, interval_sec, consecutive_unchanged, last_scanned_at, next_due_at, enabled, created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,1,?,?)
ON CONFLICT(root_id, dir_path) DO UPDATE SET
  source_node_id=CASE WHEN excluded.source_node_id>0 THEN excluded.source_node_id ELSE hot_directories.source_node_id END,
  interval_sec=excluded.interval_sec,
  consecutive_unchanged=excluded.consecutive_unchanged,
  last_scanned_at=excluded.last_scanned_at,
  next_due_at=excluded.next_due_at,
  enabled=1,
  updated_at=excluded.updated_at`)

	_, err = s.db.ExecContext(ctx, qUpsert, id, rootID, dirPath, sourceNodeID, nextInterval, nextConsecutive, now, nextDue, now, now)
	return err
}

func (s *SQLStore) GetDueHotDirectories(ctx context.Context, now int64, limit int) ([]HotDirectory, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := s.rebind("SELECT id, root_id, dir_path, source_node_id, interval_sec, consecutive_unchanged, last_scanned_at, next_due_at, enabled, created_at, updated_at FROM hot_directories WHERE enabled=1 AND next_due_at<=? ORDER BY next_due_at ASC LIMIT ?")
	rows, err := s.db.QueryContext(ctx, q, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []HotDirectory
	for rows.Next() {
		var h HotDirectory
		var en int64
		if err := rows.Scan(&h.ID, &h.RootID, &h.DirPath, &h.SourceNodeID, &h.IntervalSec, &h.ConsecutiveUnchanged, &h.LastScannedAt, &h.NextDueAt, &en, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.Enabled = en != 0
		list = append(list, h)
	}
	return list, rows.Err()
}

func (s *SQLStore) GetHotDirectory(ctx context.Context, id string) (HotDirectory, error) {
	q := s.rebind("SELECT id, root_id, dir_path, source_node_id, interval_sec, consecutive_unchanged, last_scanned_at, next_due_at, enabled, created_at, updated_at FROM hot_directories WHERE id=?")
	var h HotDirectory
	var en int64
	err := s.db.QueryRowContext(ctx, q, id).Scan(&h.ID, &h.RootID, &h.DirPath, &h.SourceNodeID, &h.IntervalSec, &h.ConsecutiveUnchanged, &h.LastScannedAt, &h.NextDueAt, &en, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return HotDirectory{}, err
	}
	h.Enabled = en != 0
	return h, nil
}

func (s *SQLStore) ListHotDirectories(ctx context.Context) ([]HotDirectory, error) {
	q := s.rebind("SELECT id, root_id, dir_path, source_node_id, interval_sec, consecutive_unchanged, last_scanned_at, next_due_at, enabled, created_at, updated_at FROM hot_directories ORDER BY dir_path ASC")
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []HotDirectory
	for rows.Next() {
		var h HotDirectory
		var en int64
		if err := rows.Scan(&h.ID, &h.RootID, &h.DirPath, &h.SourceNodeID, &h.IntervalSec, &h.ConsecutiveUnchanged, &h.LastScannedAt, &h.NextDueAt, &en, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.Enabled = en != 0
		list = append(list, h)
	}
	return list, rows.Err()
}

func (s *SQLStore) EnsureHotDirectoriesSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, hotDirSchema); err != nil {
		return fmt.Errorf("ensure hot_directories schema: %w", err)
	}
	return nil
}
