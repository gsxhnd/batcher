//go:build !windows

package task

import (
	"errors"
	"fmt"
	"os"
)

var errRenameNoReplaceUnsupported = errors.New("当前系统不支持 no-replace rename")

// 测试中可注入清理失败，验证异常路径不会误删正式输出。
var removePartialFile = os.Remove

// replacePartialOutput 在同目录内提交临时文件。覆盖时 rename 原子替换；
// 不覆盖时优先用系统原生 no-replace rename，不支持时才回退到硬链接。
func replacePartialOutput(partial, output string, overwrite bool) error {
	if overwrite {
		return os.Rename(partial, output)
	}
	err := renameNoReplace(partial, output)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errRenameNoReplaceUnsupported) {
		return err
	}
	return linkPartialOutput(partial, output)
}

// linkPartialOutput 用硬链接保证目标一旦存在就失败，不能用普通 rename 意外覆盖并发创建的文件。
func linkPartialOutput(partial, output string) error {
	if err := os.Link(partial, output); err != nil {
		return err
	}
	if err := removePartialFile(partial); err != nil {
		// 正式输出此刻对其他进程可见，不能证明路径仍属于本次提交，绝不能按路径回滚删除。
		return fmt.Errorf("清理临时文件: %w；正式输出已保留", err)
	}
	return nil
}
