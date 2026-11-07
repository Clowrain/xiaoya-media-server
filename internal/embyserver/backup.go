package embyserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

func (a *App) backup() (string, error) {
	a.backupMu.Lock()
	defer a.backupMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dir := filepath.Join(appDataDir(), "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	service, env, cleanup, err := backupServiceFile(a.databaseDSN)
	if err != nil {
		return "", err
	}
	defer cleanup()
	f, err := os.CreateTemp(dir, ".backup-*.partial")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	cmd := exec.CommandContext(ctx, "pg_dump", "--dbname=service=xiaoya_backup", "--format=custom", "--no-owner", "--no-acl", "--no-password")
	cmd.Env = append(env, "PGSERVICEFILE="+service)
	cmd.Stdout = f
	err = cmd.Run()
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return "", fmt.Errorf("pg_dump failed: %w", err)
	}
	p := filepath.Join(dir, "emby-"+time.Now().UTC().Format("20060102T150405.000000000")+".dump")
	if err = os.Rename(f.Name(), p); err != nil {
		return "", err
	}
	files, err := filepath.Glob(filepath.Join(dir, "emby-*.dump"))
	if err != nil {
		return p, err
	}
	sort.Strings(files)
	for len(files) > 14 {
		if err = os.Remove(files[0]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return p, fmt.Errorf("备份已生成，清理旧备份失败: %w", err)
		}
		files = files[1:]
	}
	return p, nil
}

func appDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("DATA_DIR")); dir != "" {
		return dir
	}
	return "/app"
}

var connOption = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z_0-9]*)[\t ]*=[\t ]*(?:'((?:[^'\\]|\\.)*)'|((?:[^\t \\'\r\n]|\\.)+))(?:[\t ]+|$)`)
var connEscape = regexp.MustCompile(`\\(.)`)

// Pass the effective connection settings through a private, short-lived libpq
// service file rather than exposing credentials in the pg_dump argument list.
func backupServiceFile(dsn string) (string, []string, func(), error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		var err error
		dsn, err = pq.ParseURL(dsn)
		if err != nil {
			return "", nil, nil, errors.New("无法解析当前数据库连接配置")
		}
	}
	options := map[string]string{}
	for remaining := strings.TrimSpace(dsn); remaining != ""; {
		m := connOption.FindStringSubmatch(remaining)
		if m == nil {
			return "", nil, nil, errors.New("无法解析当前数据库连接配置")
		}
		value := m[2]
		if value == "" {
			value = m[3]
		}
		value = connEscape.ReplaceAllString(value, "$1")
		if strings.ContainsAny(value, "\r\n") {
			return "", nil, nil, errors.New("数据库连接选项不能包含换行")
		}
		options[m[1]] = value
		remaining = strings.TrimSpace(remaining[len(m[0]):])
	}
	if len(options) == 0 {
		return "", nil, nil, errors.New("缺少运行中的数据库连接配置")
	}
	// lib/pq accepts runtime GUCs in the DSN. search_path is unnecessary for a
	// whole-database dump and is not a libpq connection option.
	delete(options, "search_path")
	if _, ok := options["connect_timeout"]; !ok {
		options["connect_timeout"] = "10"
	}

	env := os.Environ()
	if password, ok := options["password"]; ok {
		env = append(env, "PGPASSWORD="+password)
		delete(options, "password")
	}
	var data strings.Builder
	data.WriteString("[xiaoya_backup]\n")
	for key, value := range options {
		fmt.Fprintf(&data, "%s=%s\n", key, value)
	}
	f, err := os.CreateTemp("", "xiaoya-pg-service-*")
	if err != nil {
		return "", nil, nil, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	_, err = f.WriteString(data.String())
	if err = errors.Join(err, f.Close()); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return f.Name(), env, cleanup, nil
}
