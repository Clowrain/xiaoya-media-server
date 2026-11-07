//go:build darwin

package embyserver

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplace(dir *os.File, oldBase, newBase string) error {
	return unix.RenameatxNp(int(dir.Fd()), oldBase, int(dir.Fd()), newBase, unix.RENAME_EXCL)
}
