//go:build darwin || linux

package localmodel

import (
	"fmt"
	"syscall"
)

// checkDiskSpace fails if `dir`'s filesystem has less than need bytes free
// (plus a small margin), so a download aborts before filling the disk.
func checkDiskSpace(dir string, need int64) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return nil // can't tell — don't block the download
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	if free < need+(64<<20) { // 64 MB margin
		return fmt.Errorf("not enough disk space: need %d MB, %d MB free", need>>20, free>>20)
	}
	return nil
}
