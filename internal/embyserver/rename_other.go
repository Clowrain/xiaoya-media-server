//go:build !linux && !darwin

package embyserver

import (
	"io/fs"
	"os"
	"path/filepath"
)

func renameNoReplace(dir *os.File, oldBase, newBase string) error {
	parent := dir.Name()
	dst := filepath.Join(parent, newBase)
	if _, err := os.Lstat(dst); err == nil {
		return fs.ErrExist
	}
	return os.Rename(filepath.Join(parent, oldBase), dst)
}
