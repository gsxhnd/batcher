package task

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)

// fixturePlan 用占位文件生成一份四个子任务的转码计划，输出目录尚未创建。
func fixturePlan(t *testing.T) (*command.BatchPlan, testutil.Fixture) {
	t.Helper()
	fx := testutil.NewFixture(t)
	opt := command.DefaultOption()
	opt.InputPath, opt.InputFormat, opt.OutputPath = fx.Root, "mp4", fx.Out
	plan, err := command.BuildPlan(context.Background(), &opt, command.Selection{Convert: true})
	require.NoError(t, err)
	require.Len(t, plan.Jobs, len(fx.MP4s))
	return plan, fx
}

func TestNewBatchFromPlan(t *testing.T) {
	plan, fx := fixturePlan(t)
	sel := command.Selection{Convert: true}
	b := NewBatch("batch-1", plan, sel, fixedNow)

	assert.Equal(t, recordVersion, b.Version)
	assert.Equal(t, "batch-1", b.ID)
	assert.Equal(t, filepath.Base(fx.Root), b.Name, "主任务名称取输入文件夹名")
	assert.Equal(t, fx.Root, b.InputDir)
	assert.Equal(t, fx.Out, b.OutputDir)
	assert.Equal(t, StatusPending, b.Status)
	assert.Equal(t, fixedNow, b.CreatedAt)
	assert.True(t, b.StartedAt.IsZero())
	assert.Equal(t, sel, b.Selection)
	require.Len(t, b.Items, len(plan.Jobs))
	for i, it := range b.Items {
		job := plan.Jobs[i]
		assert.Equal(t, fmt.Sprintf("%03d", i+1), it.ID)
		assert.Equal(t, job.Input, it.Input)
		assert.Equal(t, job.Output, it.Output)
		assert.Equal(t, StatusPending, it.Status)
		assert.Zero(t, it.Attempts)
		assert.False(t, it.PartialOutput)
		assert.Equal(t, job.Args, it.PlanArgs)
		require.Greater(t, len(it.Args), 3)
		assert.Equal(t, []string{"-progress", "pipe:1", "-nostats"}, it.Args[:3], "记录实际执行的参数，含注入的进度选项")
		assert.Equal(t, job.Args, it.Args[3:])
	}
}

func TestNewBatchMakesInputOutputAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	in := filepath.Join("src", "a.mp4")
	out := filepath.Join("dst", "a.mkv")
	plan := &command.BatchPlan{Jobs: []command.Job{{
		Input:  in,
		Output: out,
		Args:   []string{"-i", in, "-c", "copy", out},
	}}}
	b := NewBatch("batch-abs", plan, command.Selection{Convert: true}, fixedNow)
	absIn, err := filepath.Abs(in)
	require.NoError(t, err)
	absOut, err := filepath.Abs(out)
	require.NoError(t, err)
	require.Len(t, b.Items, 1)
	assert.Equal(t, absIn, b.Items[0].Input)
	assert.Equal(t, absOut, b.Items[0].Output)
	assert.Equal(t, []string{"-i", absIn, "-c", "copy", absOut}, b.Items[0].PlanArgs)
	assert.Equal(t, []string{"-i", in, "-c", "copy", out}, plan.Jobs[0].Args, "原计划保持调用方传入的路径")
}

func TestNewBatchCopiesPlan(t *testing.T) {
	plan, _ := fixturePlan(t)
	b := NewBatch("batch-1", plan, command.Selection{Convert: true}, fixedNow)
	b.Items[0].PlanArgs[0] = "调用方修改副本"
	assert.Equal(t, "-nostdin", plan.Jobs[0].Args[0], "记录不能与计划快照共享切片")
	b.Option.InputFormat = "改"
	assert.Equal(t, "mp4", plan.Option().InputFormat)
}

func TestBatchCounts(t *testing.T) {
	b := &Batch{Items: []*Item{
		{Status: StatusDone}, {Status: StatusDone}, {Status: StatusFailed}, {Status: StatusSkipped},
		{Status: StatusRunning}, {Status: StatusPending}, {Status: StatusCanceled}, {Status: StatusInterrupted},
	}}
	assert.Equal(t, Counts{Total: 8, Pending: 1, Running: 1, Done: 2, Failed: 1, Skipped: 1, Canceled: 1, Interrupted: 1}, b.Counts())
}

func TestStatusTerminal(t *testing.T) {
	for _, s := range []Status{StatusDone, StatusFailed, StatusSkipped, StatusCanceled, StatusInterrupted} {
		assert.True(t, s.Terminal(), string(s))
	}
	for _, s := range []Status{StatusPending, StatusRunning, StatusPaused} {
		assert.False(t, s.Terminal(), string(s))
	}
}

func TestItemAddStderrKeepsTail(t *testing.T) {
	it := &Item{}
	for i := range 60 {
		it.AddStderr(fmt.Sprintf("l%d", i))
	}
	require.Len(t, it.StderrTail, maxStderrTail)
	assert.Equal(t, "l10", it.StderrTail[0])
	assert.Equal(t, "l59", it.StderrTail[len(it.StderrTail)-1])
}

func TestNewIDIsUniqueAndSortable(t *testing.T) {
	seen := make(map[string]bool)
	for range 50 {
		id := NewID(fixedNow)
		assert.True(t, strings.HasPrefix(id, "20261110-080000-"), id)
		assert.Len(t, id, 24)
		assert.False(t, seen[id], "ID 不能重复: %s", id)
		seen[id] = true
	}
	assert.Less(t, NewID(fixedNow), NewID(fixedNow.Add(time.Second)))
}
