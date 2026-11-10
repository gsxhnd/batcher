package command

import (
	"runtime"

	"github.com/gsxhnd/batcher/internal/commandline"
)

// FormatCommand 将命令参数引用为可复制的命令；Windows 使用 PowerShell，其他系统使用 POSIX shell。
func FormatCommand(args []string) string {
	if runtime.GOOS == "windows" {
		return "ffmpeg " + commandline.JoinPowerShell(args)
	}
	return "ffmpeg " + commandline.Join(args)
}
