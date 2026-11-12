package embyserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type LibraryRecord struct {
	ID       string
	Name     string
	Path     string
	Kind     string
	Status   string
	Scanned  int64
	Error    string
	Count    int64
	Duration float64
}

type librarySettingsWriter interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type libraryQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

type LibraryRepository interface {
	Create(writer librarySettingsWriter, library LibraryRecord) error
	UpdateDetails(writer librarySettingsWriter, id, name, kind string) error
	UpdatePath(writer librarySettingsWriter, id, path string) error
	UpdateItemCount(writer librarySettingsWriter, id string) error
	Delete(writer librarySettingsWriter, id string) (sql.Result, error)
	SetOrderPosition(writer librarySettingsWriter, id string, position int) error
	DeleteOrderPosition(writer librarySettingsWriter, id string) error
	List() ([]LibraryRecord, error)
	GetByID(id string) (LibraryRecord, error)
	Exists(id string) (bool, error)
	Count() (int, error)
	CountWith(queryer libraryQueryer) (int, error)
	CountByStatus(status string) (int, error)
	Paths(id, fallback string) ([]string, error)
	SavePaths(writer librarySettingsWriter, id string, paths []string) error
	DeletePaths(writer librarySettingsWriter, id string) error
	SetScanStatus(id, status, message string) error
	MarkInterruptedScans(message string) error
	CompleteScan(id, status, message string, count int64, duration float64, scanned int64) error
	FirstByKindOrFirst(kind string) (LibraryRecord, error)
}

type sqlLibraryRepository struct {
	db *Database
}

func (r sqlLibraryRepository) Create(writer librarySettingsWriter, library LibraryRecord) error {
	if writer == nil {
		return errors.New("library writer is unavailable")
	}
	_, err := writer.Exec("INSERT INTO libraries(id,name,path,kind) VALUES(?,?,?,?)", library.ID, library.Name, library.Path, library.Kind)
	return err
}

func (r sqlLibraryRepository) UpdateDetails(writer librarySettingsWriter, id, name, kind string) error {
	if writer == nil {
		return errors.New("library writer is unavailable")
	}
	_, err := writer.Exec("UPDATE libraries SET name=?, kind=? WHERE id=?", name, kind, id)
	return err
}

func (r sqlLibraryRepository) UpdatePath(writer librarySettingsWriter, id, path string) error {
	if writer == nil {
		return errors.New("library writer is unavailable")
	}
	_, err := writer.Exec("UPDATE libraries SET path=? WHERE id=?", path, id)
	return err
}

func (r sqlLibraryRepository) UpdateItemCount(writer librarySettingsWriter, id string) error {
	if writer == nil {
		return errors.New("library writer is unavailable")
	}
	_, err := writer.Exec("UPDATE libraries SET count=(SELECT count(*) FROM items WHERE lib=? AND kind IN ('Movie','Episode')) WHERE id=?", id, id)
	return err
}

func (r sqlLibraryRepository) Delete(writer librarySettingsWriter, id string) (sql.Result, error) {
	if writer == nil {
		return nil, errors.New("library writer is unavailable")
	}
	return writer.Exec("DELETE FROM libraries WHERE id=?", id)
}

func (r sqlLibraryRepository) SetOrderPosition(writer librarySettingsWriter, id string, position int) error {
	if writer == nil {
		return errors.New("library order writer is unavailable")
	}
	_, err := writer.Exec("INSERT INTO library_order VALUES(?,?) ON CONFLICT(library_id) DO UPDATE SET position=excluded.position", id, position)
	return err
}

func (r sqlLibraryRepository) DeleteOrderPosition(writer librarySettingsWriter, id string) error {
	if writer == nil {
		return errors.New("library order writer is unavailable")
	}
	_, err := writer.Exec("DELETE FROM library_order WHERE library_id=?", id)
	return err
}

func (r sqlLibraryRepository) List() ([]LibraryRecord, error) {
	if r.db == nil {
		return nil, errors.New("library database is unavailable")
	}
	rows, err := r.db.Query(`SELECT id,name,path,kind,status,COALESCE(scanned,0),COALESCE(error,''),COALESCE(count,0),COALESCE(duration,0)
		FROM libraries
		ORDER BY COALESCE((SELECT position FROM library_order WHERE library_id=libraries.id),2147483647),name`)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "library_order") {
		// Small standalone/legacy schemas may predate the optional ordering table.
		rows, err = r.db.Query(`SELECT id,name,path,kind,status,COALESCE(scanned,0),COALESCE(error,''),COALESCE(count,0),COALESCE(duration,0)
			FROM libraries ORDER BY name`)
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "status") {
		rows, err = r.db.Query("SELECT id,name,path,kind FROM libraries ORDER BY name")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		records := make([]LibraryRecord, 0)
		for rows.Next() {
			var record LibraryRecord
			if err := rows.Scan(&record.ID, &record.Name, &record.Path, &record.Kind); err != nil {
				return nil, err
			}
			records = append(records, record)
		}
		return records, rows.Err()
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]LibraryRecord, 0)
	for rows.Next() {
		var record LibraryRecord
		if err := rows.Scan(&record.ID, &record.Name, &record.Path, &record.Kind, &record.Status, &record.Scanned, &record.Error, &record.Count, &record.Duration); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (r sqlLibraryRepository) GetByID(id string) (LibraryRecord, error) {
	if r.db == nil {
		return LibraryRecord{}, errors.New("library database is unavailable")
	}
	var record LibraryRecord
	err := r.db.QueryRow(`SELECT id,name,path,kind,status,COALESCE(scanned,0),COALESCE(error,''),COALESCE(count,0),COALESCE(duration,0)
		FROM libraries WHERE id=?`, id).
		Scan(&record.ID, &record.Name, &record.Path, &record.Kind, &record.Status, &record.Scanned, &record.Error, &record.Count, &record.Duration)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "status") {
		err = r.db.QueryRow("SELECT id,name,path,kind FROM libraries WHERE id=?", id).
			Scan(&record.ID, &record.Name, &record.Path, &record.Kind)
	}
	return record, err
}

func (r sqlLibraryRepository) Exists(id string) (bool, error) {
	if r.db == nil {
		return false, errors.New("library database is unavailable")
	}
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM libraries WHERE id=?", id).Scan(&count)
	return count > 0, err
}

func (r sqlLibraryRepository) Count() (int, error) {
	return r.CountWith(r.db)
}

func (r sqlLibraryRepository) CountWith(queryer libraryQueryer) (int, error) {
	if queryer == nil {
		return 0, errors.New("library queryer is unavailable")
	}
	var count int
	err := queryer.QueryRow("SELECT count(*) FROM libraries").Scan(&count)
	return count, err
}

func (r sqlLibraryRepository) CountByStatus(status string) (int, error) {
	if r.db == nil {
		return 0, errors.New("library database is unavailable")
	}
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM libraries WHERE status=?", status).Scan(&count)
	return count, err
}

func (r sqlLibraryRepository) Paths(id, fallback string) ([]string, error) {
	if r.db == nil {
		return []string{fallback}, errors.New("library database is unavailable")
	}
	var raw string
	err := r.db.QueryRow("SELECT v FROM settings WHERE k=?", "library-paths:"+id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{fallback}, nil
	}
	if err != nil {
		return []string{fallback}, err
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return []string{fallback}, nil
	}
	return paths, nil
}

func (r sqlLibraryRepository) SavePaths(writer librarySettingsWriter, id string, paths []string) error {
	if writer == nil {
		return errors.New("library settings writer is unavailable")
	}
	raw, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	_, err = writer.Exec("INSERT INTO settings(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", "library-paths:"+id, string(raw))
	return err
}

func (r sqlLibraryRepository) DeletePaths(writer librarySettingsWriter, id string) error {
	if writer == nil {
		return errors.New("library settings writer is unavailable")
	}
	_, err := writer.Exec("DELETE FROM settings WHERE k=?", "library-paths:"+id)
	return err
}

func (r sqlLibraryRepository) SetScanStatus(id, status, message string) error {
	if r.db == nil {
		return errors.New("library database is unavailable")
	}
	_, err := r.db.Exec("UPDATE libraries SET status=?,error=? WHERE id=?", status, message, id)
	return err
}

func (r sqlLibraryRepository) MarkInterruptedScans(message string) error {
	if r.db == nil {
		return errors.New("library database is unavailable")
	}
	_, err := r.db.Exec("UPDATE libraries SET status='interrupted',error=? WHERE status='scanning'", message)
	return err
}

func (r sqlLibraryRepository) CompleteScan(id, status, message string, count int64, duration float64, scanned int64) error {
	if r.db == nil {
		return errors.New("library database is unavailable")
	}
	_, err := r.db.Exec("UPDATE libraries SET status=?,error=?,count=?,duration=?,scanned=? WHERE id=?", status, message, count, duration, scanned, id)
	return err
}

func (r sqlLibraryRepository) FirstByKindOrFirst(kind string) (LibraryRecord, error) {
	if r.db == nil {
		return LibraryRecord{}, errors.New("library database is unavailable")
	}
	query := `SELECT id,name,path,kind,status,COALESCE(scanned,0),COALESCE(error,''),COALESCE(count,0),COALESCE(duration,0) FROM libraries`
	if kind != "" {
		library, err := scanLibraryRecord(r.db.QueryRow(query+" WHERE kind=? ORDER BY id LIMIT 1", kind))
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return library, err
		}
	}
	return scanLibraryRecord(r.db.QueryRow(query + " ORDER BY id LIMIT 1"))
}

func scanLibraryRecord(scanner interface{ Scan(...any) error }) (LibraryRecord, error) {
	var record LibraryRecord
	err := scanner.Scan(&record.ID, &record.Name, &record.Path, &record.Kind, &record.Status, &record.Scanned, &record.Error, &record.Count, &record.Duration)
	return record, err
}

func (a *App) libraryStore() LibraryRepository {
	if a.libraryRepository != nil {
		return a.libraryRepository
	}
	return sqlLibraryRepository{db: a.db}
}
