//go:build windows

package task

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// replacePartialOutput 使用 Windows 原子移动语义。授权覆盖时直接替换，
// 绝不先删除旧正式文件；未授权时 MOVEFILE_REPLACE_EXISTING 不置位，目标存在会失败。
func replacePartialOutput(partial, output string, overwrite bool) error {
	from, err := windows.UTF16PtrFromString(partial)
	if err != nil {
		return fmt.Errorf("转换临时输出路径: %w", err)
	}
	to, err := windows.UTF16PtrFromString(output)
	if err != nil {
		return fmt.Errorf("转换正式输出路径: %w", err)
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if overwrite {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if err := windows.MoveFileEx(from, to, flags); err != nil {
		return fmt.Errorf("移动临时输出: %w", err)
	}
	return nil
}
