package embyserver

import (
	"context"
	"fmt"
	"time"

	"xiaoya-media-server/internal/resource/discovery"
)

type schemaMigration struct {
	version int64
	name    string
	apply   func() error
}

func runSchemaMigrations(db *Database, migrations []schemaMigration) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS app_schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at BIGINT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create app schema migration ledger: %w", err)
	}

	var current int64
	if err := db.QueryRow("SELECT COALESCE(MAX(version),0) FROM app_schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read app schema migration version: %w", err)
	}
	if len(migrations) > 0 && current > migrations[len(migrations)-1].version {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, migrations[len(migrations)-1].version)
	}

	for _, migration := range migrations {
		if migration.version <= current {
			continue
		}
		if migration.version != current+1 {
			return fmt.Errorf("app schema migration sequence has a gap after version %d", current)
		}
		if migration.apply == nil {
			return fmt.Errorf("app schema migration %d (%s) has no implementation", migration.version, migration.name)
		}
		if err := migration.apply(); err != nil {
			return fmt.Errorf("apply app schema migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := db.Exec("INSERT INTO app_schema_migrations(version,name,applied_at) VALUES(?,?,?)", migration.version, migration.name, time.Now().Unix()); err != nil {
			return fmt.Errorf("record app schema migration %d (%s): %w", migration.version, migration.name, err)
		}
		current = migration.version
	}
	return nil
}

func (a *App) ensureSchema() error {
	return a.withSchemaLock(func() error {
		migrations := []schemaMigration{{
			version: 1,
			name:    "legacy-app-schema-bootstrap",
			apply:   a.ensureApplicationSchema,
		}}
		if err := runSchemaMigrations(a.db, migrations); err != nil {
			return err
		}
		if err := discovery.NewSQLStore(a.db.DB, "postgres").EnsureSchema(context.Background()); err != nil {
			return err
		}
		return a.db.QueryRow("SELECT v FROM settings WHERE k='image_secret'").Scan(&a.cursorSecret)
	})
}

func (a *App) withSchemaLock(apply func() error) (err error) {
	conn, err := a.db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()

	const lockNamespace int32 = 0x584d5343
	const lockID int32 = 1
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1,$2)", lockNamespace, lockID); err != nil {
		return fmt.Errorf("acquire app schema lock: %w", err)
	}
	defer func() {
		if _, unlockErr := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1,$2)", lockNamespace, lockID); err == nil && unlockErr != nil {
			err = fmt.Errorf("release app schema lock: %w", unlockErr)
		}
	}()
	return apply()
}

func (a *App) ensureApplicationSchema() error {
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS settings(k TEXT PRIMARY KEY,v TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL ,hash TEXT NOT NULL,admin BIGINT NOT NULL DEFAULT 0,first_admin BIGINT NOT NULL DEFAULT 0,max_devices BIGINT NOT NULL DEFAULT 2);
 CREATE TABLE IF NOT EXISTS tokens(hash TEXT PRIMARY KEY,user_id TEXT REFERENCES users(id) ON DELETE CASCADE,device TEXT NOT NULL,expires BIGINT NOT NULL);
 CREATE TABLE IF NOT EXISTS api_keys(id TEXT PRIMARY KEY,name TEXT NOT NULL,hash TEXT UNIQUE NOT NULL,created BIGINT NOT NULL);
 CREATE TABLE IF NOT EXISTS libraries(id TEXT PRIMARY KEY,name TEXT NOT NULL,path TEXT UNIQUE NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'idle',scanned BIGINT NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',count BIGINT NOT NULL DEFAULT 0,duration DOUBLE PRECISION NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS items(id TEXT PRIMARY KEY,lib TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,parent TEXT NOT NULL,name TEXT NOT NULL,kind TEXT NOT NULL,path TEXT UNIQUE NOT NULL,url TEXT NOT NULL DEFAULT '',overview TEXT NOT NULL DEFAULT '',poster TEXT NOT NULL DEFAULT '',year BIGINT NOT NULL DEFAULT 0,season BIGINT NOT NULL DEFAULT 0,episode BIGINT NOT NULL DEFAULT 0,mtime BIGINT NOT NULL DEFAULT 0,size BIGINT NOT NULL DEFAULT 0,seen TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS items_lib_name ON items(lib,name,id);
 CREATE INDEX IF NOT EXISTS items_parent_name ON items(parent,name,id);
 CREATE INDEX IF NOT EXISTS items_kind ON items(kind);
 CREATE INDEX IF NOT EXISTS items_seen ON items(lib,seen);
 CREATE TABLE IF NOT EXISTS plays(user_id TEXT REFERENCES users(id) ON DELETE CASCADE,device TEXT NOT NULL,item TEXT NOT NULL,updated BIGINT NOT NULL,PRIMARY KEY(user_id,device));
 CREATE TABLE IF NOT EXISTS userdata(user_id TEXT REFERENCES users(id) ON DELETE CASCADE,item TEXT REFERENCES items(id) ON DELETE CASCADE,position BIGINT NOT NULL DEFAULT 0,played BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(user_id,item));
 CREATE TABLE IF NOT EXISTS user_favorites(user_id TEXT REFERENCES users(id) ON DELETE CASCADE,item TEXT REFERENCES items(id) ON DELETE CASCADE,created_at BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(user_id,item));
 CREATE INDEX IF NOT EXISTS user_favorites_user ON user_favorites(user_id);`); err != nil {
		return err
	}
	if err := a.extraSchema(); err != nil {
		return err
	}
	if err := a.browseSchema(); err != nil {
		return err
	}
	if err := a.postgresSchema(); err != nil {
		return err
	}
	if err := a.versionsSchema(); err != nil {
		return err
	}
	if err := a.initialsSchema(); err != nil {
		return err
	}
	if err := a.tmdbSchema(); err != nil {
		return err
	}
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS user_logins(user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, last_login BIGINT NOT NULL)`); err != nil {
		return err
	}
	return nil
}
