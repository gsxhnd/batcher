// Package ffmpeg 只负责启动 ffmpeg 进程并把输出转成事件，不含计划、预设和 UI。
package ffmpeg

import "runtime"

// ffmpegBin 是调用的可执行文件，测试中可替换。
var ffmpegBin = func() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}()
