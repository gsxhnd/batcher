package ffmpeg

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ffprobeBin 是读取媒体信息的可执行文件，测试中可替换。
var ffprobeBin = func() string {
	if runtime.GOOS == "windows" {
		return "ffprobe.exe"
	}
	return "ffprobe"
}()

// Duration 用 ffprobe 读取媒体总时长。ffprobe 未安装或文件没有时长信息时返回错误，
// 调用方应降级为只显示已处理时长，而不是让任务失败。
func Duration(ctx context.Context, path string) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, ffprobeBin,
		"-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", "-i", path,
	).Output()
	if err != nil {
		return 0, fmt.Errorf("读取媒体时长 %s: %w", path, err)
	}
	return parseDuration(string(out))
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(secs) || math.IsInf(secs, 0) || secs <= 0 || secs > 1e8 {
		return 0, fmt.Errorf("无效的媒体时长: %q", s)
	}
	return time.Duration(secs * float64(time.Second)), nil
}
