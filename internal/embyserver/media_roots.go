package embyserver

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	extraMediaRootsMu sync.RWMutex
	extraMediaRoots   = map[string]bool{}
)

// RegisterMediaRoot adds an allowed media root directory (and its resolved symlink target, if any).
func RegisterMediaRoot(root string) {
	root = filepath.Clean(strings.TrimSpace(root))
	if !filepath.IsAbs(root) || root == "/" {
		return
	}
	extraMediaRootsMu.Lock()
	defer extraMediaRootsMu.Unlock()
	extraMediaRoots[root] = true
	if real, err := filepath.EvalSymlinks(root); err == nil && filepath.IsAbs(real) && real != "/" {
		extraMediaRoots[filepath.Clean(real)] = true
	}
}

// MEDIA_ROOTS is an explicit allowlist of container media mount points.
func mediaRoots() []string {
	raw := os.Getenv("MEDIA_ROOTS")
	if raw == "" {
		raw = "/media"
	}
	roots := []string{}
	seen := map[string]bool{}
	add := func(r string) {
		r = filepath.Clean(r)
		if filepath.IsAbs(r) && r != "/" && !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	for _, root := range strings.Split(raw, ":") {
		add(root)
		if real, err := filepath.EvalSymlinks(root); err == nil {
			add(real)
		}
	}
	extraMediaRootsMu.RLock()
	for r := range extraMediaRoots {
		add(r)
	}
	extraMediaRootsMu.RUnlock()
	return roots
}
func allowedMediaPath(path string) bool {
	for _, root := range mediaRoots() {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
