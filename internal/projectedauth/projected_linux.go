package projectedauth

import (
	"os"
	"syscall"
)

func requireReadonlyFilesystem(dir *os.File) error {
	var stat syscall.Statfs_t
	if err := syscall.Fstatfs(int(dir.Fd()), &stat); err != nil {
		return err
	}
	// Linux ST_RDONLY is bit 0. Check the filesystem of the pinned mount root.
	if stat.Flags&1 == 0 {
		return ErrUnavailable
	}
	return nil
}
