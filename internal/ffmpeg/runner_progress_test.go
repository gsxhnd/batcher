package ffmpeg

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSource 运行时生成一个 2 秒的微型视频，不使用仓库内 testdata。
func makeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "源 视频.mkv")
	var logs []string
	r := &Runner{OnEvent: func(e Event) {
		if e.Type == EventLog {
			logs = append(logs, e.Message)
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := r.Run(ctx, Call{Total: 1, Args: []string{
		"-nostdin", "-n", "-f", "lavfi", "-i", "color=c=black:s=32x32:r=10:d=2", "-c:v", "mpeg4", src,
	}})
	require.NoError(t, err, strings.Join(logs, "\n"))
	return src
}

// useMissingFFprobe 模拟没有安装 ffprobe。
func useMissingFFprobe(t *testing.T) {
	t.Helper()
	prev := ffprobeBin
	ffprobeBin = "batcher-ffprobe-not-installed"
	t.Cleanup(func() { ffprobeBin = prev })
}

func copyCall(src, out string) Call {
	return Call{Total: 1, Input: src, Args: []string{"-nostdin", "-n", "-i", src, "-c", "copy", out}}
}

func TestRunnerReportsProgress(t *testing.T) {
	src := makeSource(t)
	out := filepath.Join(t.TempDir(), "out.mkv")
	var progress []Progress
	var logs []string
	r := &Runner{OnEvent: func(e Event) {
		switch e.Type {
		case EventProgress:
			progress = append(progress, *e.Progress)
		case EventLog:
			logs = append(logs, e.Message)
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx, copyCall(src, out)), strings.Join(logs, "\n"))

	require.NotEmpty(t, progress)
	last := progress[len(progress)-1]
	assert.True(t, last.Done)
	assert.Greater(t, last.Duration, time.Duration(0))
	_, ok := last.Percent()
	assert.True(t, ok)
	for _, line := range logs {
		assert.NotContains(t, line, "progress=", "进度键值不能混入日志")
		if strings.HasPrefix(line, "frame=") {
			// -nostats 只关闭周期性统计；ffmpeg 结束时仍会输出一行带 Lsize 的汇总。
			assert.Contains(t, line, "Lsize=", "-nostats 之后 stderr 只允许出现结束时的汇总行")
		}
	}
}

func TestRunnerWithoutFFprobeStillReportsProgress(t *testing.T) {
	src := makeSource(t)
	useMissingFFprobe(t)
	out := filepath.Join(t.TempDir(), "out.mkv")
	var progress []Progress
	r := &Runner{OnEvent: func(e Event) {
		if e.Type == EventProgress {
			progress = append(progress, *e.Progress)
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, r.Run(ctx, copyCall(src, out)))

	require.NotEmpty(t, progress)
	last := progress[len(progress)-1]
	assert.True(t, last.Done)
	assert.Zero(t, last.Duration)
	_, ok := last.Percent()
	assert.False(t, ok, "没有 ffprobe 时只显示已处理时长")
}

func TestExecArgsKeepsPlanUntouched(t *testing.T) {
	planned := []string{"-nostdin", "-n", "-i", "a.mp4", "b.mkv"}
	got := ExecArgs(planned)
	assert.Equal(t, []string{"-progress", "pipe:1", "-nostats", "-nostdin", "-n", "-i", "a.mp4", "b.mkv"}, got)
	assert.Equal(t, []string{"-nostdin", "-n", "-i", "a.mp4", "b.mkv"}, planned)
	got[0] = "changed"
	assert.Equal(t, "-progress", progressArgs[0], "注入的选项模板不能被调用方修改")
}

func TestRunnerCancelInterruptsRunningProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不能向子进程发送中断信号")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := filepath.Join(t.TempDir(), "out.mkv")
	var canceled bool
	r := &Runner{OnEvent: func(e Event) {
		switch e.Type {
		case EventProgress:
			cancel()
		case EventCommandCanceled:
			canceled = true
		}
	}}
	start := time.Now()
	err := r.Run(ctx, Call{Total: 1, Args: []string{
		"-nostdin", "-n", "-re", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=10:duration=60", "-c:v", "mpeg4", out,
	}})
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, canceled)
	assert.Less(t, time.Since(start), cancelGrace, "ffmpeg 应在收到中断信号后正常退出，而不是等到强制结束")
}
