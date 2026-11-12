package embyserver

import (
	"database/sql"
	"errors"
)

type CoverRecord struct {
	MIME string
	Data []byte
}

type coverWriter interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type CoverRepository interface {
	Exists(id string) (bool, error)
	Get(id string) (CoverRecord, error)
	Save(writer coverWriter, id, mime string, data []byte) error
	Delete(writer coverWriter, id string) error
}

type sqlCoverRepository struct {
	db *Database
}

func (r sqlCoverRepository) Exists(id string) (bool, error) {
	if r.db == nil {
		return false, errors.New("cover database is unavailable")
	}
	var exists int
	err := r.db.QueryRow("SELECT 1 FROM covers WHERE id=?", id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists != 0, err
}

func (r sqlCoverRepository) Get(id string) (CoverRecord, error) {
	if r.db == nil {
		return CoverRecord{}, errors.New("cover database is unavailable")
	}
	var cover CoverRecord
	err := r.db.QueryRow("SELECT data,mime FROM covers WHERE id=?", id).Scan(&cover.Data, &cover.MIME)
	return cover, err
}

func (r sqlCoverRepository) Save(writer coverWriter, id, mime string, data []byte) error {
	if writer == nil {
		return errors.New("cover writer is unavailable")
	}
	_, err := writer.Exec("INSERT INTO covers(id,mime,data) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET mime=excluded.mime,data=excluded.data", id, mime, data)
	return err
}

func (r sqlCoverRepository) Delete(writer coverWriter, id string) error {
	if writer == nil {
		return errors.New("cover writer is unavailable")
	}
	_, err := writer.Exec("DELETE FROM covers WHERE id=?", id)
	return err
}

func (a *App) coverStore() CoverRepository {
	if a.coverRepository != nil {
		return a.coverRepository
	}
	return sqlCoverRepository{db: a.db}
}

func (a *App) coverEntityExists(id string) (bool, error) {
	if exists, err := a.libraryStore().Exists(id); err != nil || exists {
		return exists, err
	}
	return a.mediaItems().Exists(id)
}
