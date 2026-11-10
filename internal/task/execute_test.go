package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobsPlan 构造只含给定参数的计划：输入输出都在临时目录内，输出尚不存在。
func jobsPlan(t *testing.T, args ...[]string) *command.BatchPlan {
	t.Helper()
	dir := t.TempDir()
	plan := &command.BatchPlan{}
	for i, a := range args {
		plan.Jobs = append(plan.Jobs, command.Job{
			Input:  filepath.Join(dir, fmt.Sprintf("in%d.mp4", i)),
			Output: filepath.Join(dir, fmt.Sprintf("out%d.mkv", i)),
			Args:   a,
		})
	}
	return plan
}

func TestExecuteContinuesAfterFailure(t *testing.T) {
	var started, done, failed, active, peak int
	onEvent := func(e ffmpeg.Event) {
		switch e.Type {
		case ffmpeg.EventCommandStart:
			started++
			active++
			peak = max(peak, active)
		case ffmpeg.EventCommandDone:
			done++
			active--
		case ffmpeg.EventCommandFailed:
			failed++
			active--
		}
	}
	plan := jobsPlan(t, []string{"-batcher-invalid"}, []string{"-version"}, []string{"-version"}, []string{"-version"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.Error(t, Execute(ctx, plan, onEvent))
	assert.Equal(t, 4, started)
	assert.Equal(t, 3, done)
	assert.Equal(t, 1, failed)
	assert.Zero(t, active)
	assert.Equal(t, 1, peak, "任务必须串行执行")
}

func TestExecuteEventOrder(t *testing.T) {
	var events []ffmpeg.Event
	plan := jobsPlan(t, []string{"-version"})
	require.NoError(t, Execute(context.Background(), plan, func(e ffmpeg.Event) { events = append(events, e) }))

	types := make([]ffmpeg.EventType, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}
	require.NotEmpty(t, types)
	assert.Equal(t, ffmpeg.EventBatchStart, types[0])
	assert.Equal(t, 1, events[0].Total)
	assert.Contains(t, types, ffmpeg.EventCommandStart)
	assert.Contains(t, types, ffmpeg.EventLog)
	assert.Equal(t, ffmpeg.EventCommandDone, types[len(types)-1])
}

func TestExecuteCancellationStopsQueuedCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, canceled := 0, 0
	onEvent := func(e ffmpeg.Event) {
		switch e.Type {
		case ffmpeg.EventCommandStart:
			started++
			cancel()
		case ffmpeg.EventCommandCanceled:
			canceled++
		}
	}
	plan := jobsPlan(t, []string{"-version"}, []string{"-version"}, []string{"-version"})

	err := Execute(ctx, plan, onEvent)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, started, "取消后不得再启动排队中的命令")
	assert.Equal(t, started, canceled)
}

func TestExecuteCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := Execute(ctx, jobsPlan(t, []string{"-version"}), func(ffmpeg.Event) { called = true })
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, called)
}

func TestExecuteRejectsExistingOutput(t *testing.T) {
	plan := jobsPlan(t, []string{"-version"})
	require.NoError(t, os.WriteFile(plan.Jobs[0].Output, nil, 0o644))
	called := false
	err := Execute(context.Background(), plan, func(ffmpeg.Event) { called = true })
	assert.ErrorIs(t, err, os.ErrExist)
	assert.False(t, called, "检查失败时不得启动任何命令")
}

func TestExecuteEmptyPlan(t *testing.T) {
	called := false
	err := Execute(context.Background(), &command.BatchPlan{}, func(ffmpeg.Event) { called = true })
	assert.NoError(t, err)
	assert.False(t, called)
}

func TestExecuteCreatesOutputDirOnlyWhenRunning(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := command.DefaultOption()
	opt.InputPath, opt.InputFormat, opt.OutputPath = fx.Root, "mp4", fx.Out
	plan, err := command.BuildPlan(context.Background(), &opt, command.Selection{Convert: true})
	require.NoError(t, err)
	assert.NoDirExists(t, fx.Out, "规划不得创建输出目录")

	// 占位文件不是真实媒体，ffmpeg 会逐条失败，但输出目录此时必须已经创建。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	assert.Error(t, Execute(ctx, plan, func(ffmpeg.Event) {}))
	assert.DirExists(t, fx.Out)
}
