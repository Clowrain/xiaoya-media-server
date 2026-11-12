package embyserver

import (
	"database/sql"
	"errors"
)

type PlaybackSessionRepository interface {
	CurrentItem(userID, deviceID string) (string, error)
	Reserve(userID, deviceID, itemID string, maxOtherDevices int, now int64) (limitReached bool, err error)
	Release(userID, deviceID string) error
	ReleaseUser(userID string) error
	ActiveCountSince(updatedAfter int64) (int, error)
	ActiveItemPaths(updatedAfter int64) ([]string, error)
	TouchResume(userID, itemID string, updated int64) error
}

type sqlPlaybackSessionRepository struct {
	db *Database
}

func (r sqlPlaybackSessionRepository) CurrentItem(userID, deviceID string) (string, error) {
	if r.db == nil {
		return "", nil
	}
	var itemID string
	err := r.db.QueryRow("SELECT item FROM plays WHERE user_id=? AND device=?", userID, deviceID).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return itemID, err
}

func (r sqlPlaybackSessionRepository) Reserve(userID, deviceID, itemID string, maxOtherDevices int, now int64) (bool, error) {
	if r.db == nil {
		return false, errors.New("playback session database is unavailable")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM plays WHERE updated<?", now-180); err != nil {
		return false, err
	}
	var otherDevices int
	if err := tx.QueryRow("SELECT count(*) FROM plays WHERE user_id=? AND device<>?", userID, deviceID).Scan(&otherDevices); err != nil {
		return false, err
	}
	if otherDevices >= maxOtherDevices {
		return true, nil
	}
	if _, err := tx.Exec("INSERT INTO plays VALUES(?,?,?,?) ON CONFLICT(user_id,device) DO UPDATE SET item=excluded.item,updated=excluded.updated", userID, deviceID, itemID, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func (r sqlPlaybackSessionRepository) Release(userID, deviceID string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.Exec("DELETE FROM plays WHERE user_id=? AND device=?", userID, deviceID)
	return err
}

func (r sqlPlaybackSessionRepository) ReleaseUser(userID string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.Exec("DELETE FROM plays WHERE user_id=?", userID)
	return err
}

func (r sqlPlaybackSessionRepository) ActiveCountSince(updatedAfter int64) (int, error) {
	if r.db == nil {
		return 0, errors.New("playback session database is unavailable")
	}
	var active int
	err := r.db.QueryRow("SELECT count(*) FROM plays WHERE updated>?", updatedAfter).Scan(&active)
	return active, err
}

func (r sqlPlaybackSessionRepository) ActiveItemPaths(updatedAfter int64) ([]string, error) {
	if r.db == nil {
		return nil, errors.New("playback session database is unavailable")
	}
	rows, err := r.db.Query("SELECT i.path FROM plays p JOIN items i ON i.id=p.item WHERE p.updated>?", updatedAfter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

func (r sqlPlaybackSessionRepository) TouchResume(userID, itemID string, updated int64) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.Exec("INSERT INTO resume_activity VALUES(?,?,?) ON CONFLICT(user_id,item) DO UPDATE SET updated=excluded.updated", userID, itemID, updated)
	return err
}

func (a *App) playbackSessionStore() PlaybackSessionRepository {
	if a.playbackSessionRepository != nil {
		return a.playbackSessionRepository
	}
	return sqlPlaybackSessionRepository{db: a.db}
}
