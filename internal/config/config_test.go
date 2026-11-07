package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
server:
  listen: "0.0.0.0:8096"
xiaoya:
  sqlite_path: "/data/strm_internal.db"
  read_only: false
monitor:
  enabled: true
  interval: 15m
  roots:
    - /每日更新/电视剧/国产剧
strm:
  output_root: /mnt/strm
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMDB_API_KEY", "unit-test-tmdb-key")
	t.Setenv("LISTEN", ":8097")

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Listen != ":8097" {
		t.Fatalf("expected LISTEN env override :8097, got %q", cfg.Server.Listen)
	}
	if !cfg.Xiaoya.ReadOnly {
		t.Fatal("Xiaoya.ReadOnly must always be enforced true")
	}
	if cfg.Monitor.Interval != 15*time.Minute {
		t.Fatalf("expected 15m interval, got %v", cfg.Monitor.Interval)
	}
	if cfg.Metadata.TMDB.APIKey != "unit-test-tmdb-key" {
		t.Fatalf("expected TMDB_API_KEY env override, got %q", cfg.Metadata.TMDB.APIKey)
	}
}

func TestResolveDatabaseDSNFromBootstrapFile(t *testing.T) {
	dir := t.TempDir()
	passFile := filepath.Join(dir, "postgres-password")
	if err := os.WriteFile(passFile, []byte("s3cr3t-p@ss\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Database.BootstrapPassFile = passFile
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_PASSWORD", "")

	dsn, err := cfg.ResolveDatabaseDSN()
	if err != nil {
		t.Fatalf("ResolveDatabaseDSN failed: %v", err)
	}
	want := "postgres://emby:s3cr3t-p%40ss@postgres:5432/emby?sslmode=disable"
	if dsn != want {
		t.Fatalf("dsn mismatch:\n got: %s\nwant: %s", dsn, want)
	}
}
