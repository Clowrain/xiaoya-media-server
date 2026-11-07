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

// resolveSymlinkAllowNonExistent resolves symlinks in existing ancestor components even if leaf dirs do not exist yet.
func resolveSymlinkAllowNonExistent(p string) string {
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(real)
	}
	var tail []string
	cur := p
	for cur != "/" && cur != "." {
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = filepath.Dir(cur)
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			joined := filepath.Join(append([]string{real}, tail...)...)
			return filepath.Clean(joined)
		}
	}
	return p
}

// RegisterMediaRoot adds an allowed media root directory (and its resolved symlink target, if any).
func RegisterMediaRoot(root string) {
	root = filepath.Clean(strings.TrimSpace(root))
	if !filepath.IsAbs(root) || root == "/" {
		return
	}
	extraMediaRootsMu.Lock()
	defer extraMediaRootsMu.Unlock()
	extraMediaRoots[root] = true
	if resolved := resolveSymlinkAllowNonExistent(root); filepath.IsAbs(resolved) && resolved != "/" {
		extraMediaRoots[resolved] = true
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
