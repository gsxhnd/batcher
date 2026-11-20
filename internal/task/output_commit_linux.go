//go:build linux

package task

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameNoReplace(partial, output string) error {
	err := unix.Renameat2(unix.AT_FDCWD, partial, unix.AT_FDCWD, output, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return errRenameNoReplaceUnsupported
	}
	return err
}
