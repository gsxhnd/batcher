//go:build darwin

package task

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameNoReplace(partial, output string) error {
	err := unix.RenamexNp(partial, output, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) {
		return errRenameNoReplaceUnsupported
	}
	return err
}
