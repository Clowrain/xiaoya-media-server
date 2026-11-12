package embyserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupPublishesDumpFromPgDump(t *testing.T) {
	dataDir := t.TempDir()
	pgDumpDir := t.TempDir()
	installFakePgDump(t, pgDumpDir, "printf 'fake custom dump'\n")
	configureBackupTest(t, dataDir, pgDumpDir)

	got, err := (&App{databaseDSN: "host=localhost dbname=test user=test"}).backup()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != filepath.Join(dataDir, "backups") {
		t.Fatalf("backup path = %q, want it under DATA_DIR/backups", got)
	}
	contents, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read published dump: %v", err)
	}
	if string(contents) != "fake custom dump" {
		t.Fatalf("published dump contents = %q", contents)
	}
	assertBackupFiles(t, filepath.Dir(got), []string{filepath.Base(got)})
}

func TestBackupFailureRemovesPartialDump(t *testing.T) {
	dataDir := t.TempDir()
	pgDumpDir := t.TempDir()
	installFakePgDump(t, pgDumpDir, "printf 'incomplete dump'\nexit 1\n")
	configureBackupTest(t, dataDir, pgDumpDir)

	got, err := (&App{databaseDSN: "host=localhost dbname=test user=test"}).backup()
	if err == nil {
		t.Fatalf("backup succeeded with path %q; want pg_dump failure", got)
	}
	if got != "" {
		t.Fatalf("backup path on failure = %q, want empty", got)
	}
	files := assertBackupFiles(t, filepath.Join(dataDir, "backups"), nil)
	if len(files) != 0 {
		t.Fatalf("backup files after failed dump = %v, want none", files)
	}
}

func TestBackupRetentionKeepsNewestFourteenDumps(t *testing.T) {
	dataDir := t.TempDir()
	pgDumpDir := t.TempDir()
	installFakePgDump(t, pgDumpDir, "printf 'new dump'\n")
	configureBackupTest(t, dataDir, pgDumpDir)

	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 14; i++ {
		name := filepath.Join(backupDir, "emby-20000101T000000."+strings.Repeat("0", 8)+string(rune('0'+i))+".dump")
		if err := os.WriteFile(name, []byte("old dump"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := (&App{databaseDSN: "host=localhost dbname=test user=test"}).backup()
	if err != nil {
		t.Fatal(err)
	}
	files := assertBackupFiles(t, backupDir, nil)
	if len(files) != 14 {
		t.Fatalf("retained %d dumps, want 14: %v", len(files), files)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("newly published dump missing after retention: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "emby-20000101T000000.000000000.dump")); !os.IsNotExist(err) {
		t.Fatalf("oldest dump should be removed; stat error = %v", err)
	}
}

func installFakePgDump(t *testing.T, dir, script string) {
	t.Helper()
	path := filepath.Join(dir, "pg_dump")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
}

func configureBackupTest(t *testing.T, dataDir, pgDumpDir string) {
	t.Helper()
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("PATH", pgDumpDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func assertBackupFiles(t *testing.T, dir string, want []string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) && len(want) == 0 {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
		if strings.HasSuffix(entry.Name(), ".partial") {
			t.Errorf("partial backup left behind: %s", entry.Name())
		}
	}
	if want != nil {
		if len(got) != len(want) {
			t.Fatalf("backup files = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("backup files = %v, want %v", got, want)
			}
		}
	}
	return got
}
