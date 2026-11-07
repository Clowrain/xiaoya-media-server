package xiaoya

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

// BuildReadOnlyDSN constructs a strict read-only SQLite URI for modernc.org/sqlite.
// It enables query_only=1 and busy_timeout=5000 while preserving WAL visibility when -wal/-shm exist.
func BuildReadOnlyDSN(sqlitePath string) (string, error) {
	p := strings.TrimSpace(sqlitePath)
	if p == "" {
		return "", fmt.Errorf("xiaoya sqlite_path cannot be empty")
	}
	u := &url.URL{
		Scheme: "file",
		Opaque: p,
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	return u.String() + "?" + q.Encode(), nil
}

// OpenReadOnly opens a read-only *sql.DB connection to Xiaoya's strm_internal.db.
func OpenReadOnly(sqlitePath string) (*sql.DB, error) {
	dsn, err := BuildReadOnlyDSN(sqlitePath)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	return db, nil
}
