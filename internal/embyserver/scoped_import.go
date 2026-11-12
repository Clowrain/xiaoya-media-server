package embyserver

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && filepath.IsLocal(rel)
}

func (a *App) mediaLibraryLocations() (map[string][]string, error) {
	libraries, err := a.libraryStore().List()
	if err != nil {
		return nil, err
	}
	locations := make(map[string][]string, len(libraries))
	for _, library := range libraries {
		locations[library.ID] = a.libraryPaths(library.ID, library.Path)
	}
	return locations, nil
}

func (a *App) UnmatchedLibraryPaths(paths []string) []string {
	locations, err := a.mediaLibraryLocations()
	if err != nil {
		return paths
	}
	var roots []string
	for _, rs := range locations {
		for _, r := range rs {
			roots = append(roots, resolveSymlinkAllowNonExistent(r))
		}
	}
	var unmatched []string
	for _, p := range paths {
		resolved := resolveSymlinkAllowNonExistent(p)
		matched := false
		for _, r := range roots {
			if pathWithin(r, resolved) {
				matched = true
				break
			}
		}
		if !matched {
			unmatched = append(unmatched, p)
		}
	}
	return unmatched
}

// ResolveImportTarget checks the effective output path before we start contacting AList.
func (a *App) ResolveImportTarget(id, dir string) (string, error) {
	id, dir = strings.TrimSpace(id), strings.TrimSpace(dir)
	if id == "" && dir == "" {
		return "", nil
	}
	var roots []string
	if id != "" {
		library, err := a.libraryStore().GetByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("媒体库不存在")
			}
			return "", err
		}
		roots = a.libraryPaths(id, library.Path)
		if len(roots) == 0 {
			return "", fmt.Errorf("媒体库尚未配置目录")
		}
		if dir == "" {
			dir = roots[0]
		}
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("输出目录必须为绝对路径")
	}
	dir = resolveSymlinkAllowNonExistent(dir)
	if !allowedMediaPath(dir) {
		return "", fmt.Errorf("输出目录不在已授权的媒体路径内")
	}
	if id == "" {
		return dir, nil
	}
	for _, r := range roots {
		if pathWithin(resolveSymlinkAllowNonExistent(r), dir) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("输出目录不在所选媒体库内")
}

func (a *App) ScanScopedPaths(paths []string)        { a.scanScopedPaths(paths, false) }
func (a *App) TriggerScopedPathScans(paths []string) { a.scanScopedPaths(paths, true) }
func (a *App) scanScopedPaths(paths []string, async bool) {
	if a == nil || a.db == nil || len(paths) == 0 {
		return
	}
	locations, err := a.mediaLibraryLocations()
	if err != nil {
		return
	}
	for id, roots := range locations {
		var targets []string
		for _, p := range paths {
			p = resolveSymlinkAllowNonExistent(p)
			for _, r := range roots {
				r = resolveSymlinkAllowNonExistent(r)
				if pathWithin(r, p) {
					targets = append(targets, p)
					break
				}
			}
		}
		// A parent scan covers all descendant targets; don't walk them twice.
		unique := []string{}
		for i, p := range targets {
			covered := false
			for j, t := range targets {
				if i != j && pathWithin(t, p) && (p != t || j < i) {
					covered = true
					break
				}
			}
			if !covered {
				unique = append(unique, p)
			}
		}
		if len(unique) > 0 {
			if async {
				go a.scanLibraryScoped(id, true, false, unique)
			} else {
				a.scanLibraryScoped(id, true, false, unique)
			}
		}
	}
}
