//go:build !windows && !darwin && !linux

package task

func renameNoReplace(_, _ string) error {
	return errRenameNoReplaceUnsupported
}
