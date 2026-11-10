package embyserver

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type watchSuppressor struct {
	mu      sync.RWMutex
	entries map[string]time.Time
}

func (s *watchSuppressor) suppress(path string, d time.Duration) {
	if s == nil || path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]time.Time)
	}
	s.entries[filepath.Clean(path)] = time.Now().Add(d)
}

func (s *watchSuppressor) isSuppressed(path string) bool {
	if s == nil || path == "" {
		return false
	}
	clean := filepath.Clean(path)
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for target := clean; target != "" && target != "." && target != "/"; target = filepath.Dir(target) {
		if exp, exists := s.entries[target]; exists {
			if now.Before(exp) {
				return true
			}
		}
	}
	return false
}

func (s *watchSuppressor) cleanup() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for p, exp := range s.entries {
		if now.After(exp) {
			delete(s.entries, p)
		}
	}
}

// SuppressWatchPath temporarily suppresses file-system watch events for path or its children.
func (a *App) SuppressWatchPath(path string, d time.Duration) {
	a.suppressor.suppress(path, d)
}

// IsWatchSuppressed returns true if path or any of its parent directories is currently suppressed.
func (a *App) IsWatchSuppressed(path string) bool {
	return a.suppressor.isSuppressed(path)
}

func (a *App) watchDelay() int {
	var s string
	a.db.QueryRow("SELECT v FROM settings WHERE k='watch_delay_seconds'").Scan(&s)
	n, e := strconv.Atoi(s)
	if e != nil || n < 10 || n > 86400 {
		return 30
	}
	return n
}

// One event loop owns the debounce queue; a library has at most one running job.
func (a *App) watchMedia() { a.watchMediaContext(context.Background()) }
func (a *App) watchMediaContext(ctx context.Context) {
	w, e := fsnotify.NewWatcher()
	if e != nil {
		log.Printf("media watcher: %v", e)
		return
	}
	defer w.Close()
	roots := map[string]string{}
	watched := map[string]bool{}
	pending := map[string]time.Time{}
	changes := map[string]map[string]bool{}
	running := map[string]bool{}
	done := make(chan string, 32)
	addTree := func(root string) {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if !watched[p] {
				if err = w.Add(p); err != nil {
					return err
				}
				watched[p] = true
			}
			return nil
		})
		if err != nil {
			log.Printf("media watcher %s: %v", root, err)
		}
	}
	syncRoots := func() {
		next := map[string]string{}
		libs := a.libraries()
		if !a.defaultOn("watch_enabled") {
			libs = nil
			clear(pending)
			clear(changes)
		}
		for _, l := range libs {
			for _, p := range l["Locations"].([]string) {
				next[p] = l["Id"].(string)
				if _, exists := roots[p]; !exists {
					addTree(p)
				}
			}
		}
		for p := range watched {
			keep := false
			for root := range next {
				if p == root || strings.HasPrefix(p, root+"/") {
					keep = true
					break
				}
			}
			if !keep {
				w.Remove(p)
				delete(watched, p)
			}
		}
		roots = next
	}
	syncRoots()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	refresh := time.NewTicker(15 * time.Second)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			syncRoots()
			a.suppressor.cleanup()
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			// 1. 忽略隐藏文件或临时文件 (如 .DS_Store, .tmp 等)
			baseName := filepath.Base(ev.Name)
			if strings.HasPrefix(baseName, ".") {
				continue
			}

			// 2. 内部写入抑制检查 (Suppression)
			if a.IsWatchSuppressed(ev.Name) {
				continue
			}

			// 3. 区分目录与文件
			isDir := false
			if watched[ev.Name] {
				isDir = true
			} else if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
				isDir = true
			}

			// 4. 忽略非流媒体/视频文件：海报图片(.jpg,.png)与元数据(.nfo,.xml)等不触发扫描
			if !isDir && !isScannableMediaFile(ev.Name) {
				continue
			}

			for root, lib := range roots {
				if ev.Name == root || strings.HasPrefix(ev.Name, root+"/") {
					pending[lib] = time.Now()
					if changes[lib] == nil {
						changes[lib] = map[string]bool{}
					}
					target := filepath.Dir(ev.Name)
					if strings.EqualFold(filepath.Ext(ev.Name), ".strm") {
						target = ev.Name
					}
					if watched[ev.Name] {
						target = ev.Name
					} else if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						target = ev.Name
					}
					if target == root || strings.HasPrefix(target, root+"/") {
						changes[lib][target] = true
					}
					if ev.Has(fsnotify.Create) {
						if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
							addTree(ev.Name)
						}
					}
				}
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				for p := range watched {
					if p == ev.Name || strings.HasPrefix(p, ev.Name+"/") {
						w.Remove(p)
						delete(watched, p)
					}
				}
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("media watcher: %v", err)
			for _, lib := range roots {
				pending[lib] = time.Now()
				changes[lib] = nil
			}
		case lib := <-done:
			delete(running, lib)
		case now := <-tick.C:
			if !a.defaultOn("watch_enabled") {
				clear(pending)
				clear(changes)
				continue
			}
			delay := time.Duration(a.watchDelay()) * time.Second
			for lib, changed := range pending {
				if !running[lib] && now.Sub(changed) >= delay {
					delete(pending, lib)
					running[lib] = true
					scopes := []string{}
					for path := range changes[lib] {
						covered := false
						for other := range changes[lib] {
							if other != path && strings.HasPrefix(path, other+"/") {
								covered = true
								break
							}
						}
						if !covered {
							scopes = append(scopes, path)
						}
					}
					delete(changes, lib)
					go func(id string, paths []string) {
						a.scanLibraryScoped(id, false, len(paths) > 0, paths)
						select {
						case done <- id:
						case <-ctx.Done():
						}
					}(lib, scopes)
				}
			}
		}
	}
}
