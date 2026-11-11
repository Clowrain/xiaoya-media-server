package embyserver

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"xiaoya-media-server/internal/metadata/tmdb"
)

type fileStamp struct{ Size, Mtime int64 }
type fileSnapshot map[string]fileStamp

func sidecarFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".nfo", ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}
func changedSidecars(old, current fileSnapshot) map[string]bool {
	dirs := map[string]bool{}
	for p, v := range current {
		if sidecarFile(p) && old[p] != v {
			dirs[filepath.Dir(p)] = true
		}
	}
	for p := range old {
		if sidecarFile(p) {
			if _, ok := current[p]; !ok {
				dirs[filepath.Dir(p)] = true
			}
		}
	}
	return dirs
}
func dependencyChanged(path string, dirs map[string]bool) bool {
	for d := filepath.Dir(path); d != "/" && d != "."; d = filepath.Dir(d) {
		if dirs[d] {
			return true
		}
	}
	return false
}

var seasonDirPattern = regexp.MustCompile(`(?i)^season[ _.-]*0*(\d+)$`)

func seriesDirectory(root, path string) string {
	return seriesDirectoryWithCache(root, path, nil)
}

func seriesDirectoryWithCache(root, path string, cache *localInfoCache) string {
	dir := filepath.Dir(path)
	for p := dir; p != root && p != "/" && p != "."; p = filepath.Dir(p) {
		if seasonDirPattern.MatchString(filepath.Base(p)) {
			cand := filepath.Dir(p)
			for cand != root && cand != "/" && cand != "." && tmdb.IsPureMetadataTagDir(filepath.Base(cand)) {
				cand = filepath.Dir(cand)
			}
			return cand
		}
		nfoPath := filepath.Join(p, "tvshow.nfo")
		found := false
		if cache != nil {
			found = cache.posterExists(nfoPath, true)
		} else if st, e := os.Stat(nfoPath); e == nil && !st.IsDir() {
			found = true
		}
		if found {
			cand := p
			for cand != root && cand != "/" && cand != "." && tmdb.IsPureMetadataTagDir(filepath.Base(cand)) {
				cand = filepath.Dir(cand)
			}
			return cand
		}
	}
	rel, _ := filepath.Rel(root, dir)
	return filepath.Join(root, strings.Split(rel, string(filepath.Separator))[0])
}

func seasonPoster(path string, n int) string {
	return seasonPosterWithCache(path, n, nil)
}

func seasonPosterWithCache(path string, n int, cache *localInfoCache) string {
	candidates := []string{}
	parentDir := filepath.Dir(path)
	grandDir := ""
	if tmdb.IsPureMetadataTagDir(filepath.Base(parentDir)) {
		grandDir = filepath.Dir(parentDir)
	}
	for _, ext := range []string{".jpg", ".png", ".webp", ".jpeg"} {
		candidates = append(candidates,
			filepath.Join(path, "poster"+ext),
			filepath.Join(path, "folder"+ext),
			filepath.Join(parentDir, fmt.Sprintf("season%02d-poster%s", n, ext)),
			filepath.Join(parentDir, fmt.Sprintf("season%d-poster%s", n, ext)),
		)
		if grandDir != "" {
			candidates = append(candidates,
				filepath.Join(grandDir, fmt.Sprintf("season%02d-poster%s", n, ext)),
				filepath.Join(grandDir, fmt.Sprintf("season%d-poster%s", n, ext)),
			)
		}
	}
	for _, p := range candidates {
		if cache != nil {
			if cache.regularPosterExists(p) {
				return p
			}
		} else if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func withinScanScopes(path string, scopes []string) bool {
	for _, p := range scopes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
func seriesFallbackPoster(path string) string {
	return seriesFallbackPosterWithCache(path, nil)
}

func seriesFallbackPosterWithCache(path string, cache *localInfoCache) string {
	var entries []os.DirEntry
	var err error
	if cache != nil {
		entries, err = cache.readDir(path)
	} else {
		entries, err = os.ReadDir(path)
	}
	if err != nil {
		return ""
	}
	for _, d := range entries {
		if d.IsDir() {
			if m := seasonDirPattern.FindStringSubmatch(d.Name()); len(m) == 2 {
				n, _ := strconv.Atoi(m[1])
				if p := seasonPosterWithCache(filepath.Join(path, d.Name()), n, cache); p != "" {
					return p
				}
			}
			if tmdb.IsPureMetadataTagDir(d.Name()) {
				if p := seriesFallbackPosterWithCache(filepath.Join(path, d.Name()), cache); p != "" {
					return p
				}
			}
		}
	}
	return ""
}
