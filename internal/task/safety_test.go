package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oneJobPlan 构造精确控制 ffmpeg 参数的单子任务计划。
func oneJobPlan(input, output string, args ...string) *command.BatchPlan {
	return &command.BatchPlan{Jobs: []command.Job{{Input: input, Output: output, Args: args}}}
}

func writeOldOutput(t *testing.T, output string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0o755))
	require.NoError(t, os.WriteFile(output, []byte("旧正式输出"), 0o644))
}

func assertOldOutputKept(t *testing.T, output string) {
	t.Helper()
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, "旧正式输出", string(data))
}

func assertNoTemporaryOutputs(t *testing.T, output string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(output))
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".batcher-", "失败或取消后不能残留临时输出")
	}
}

func startOverwriteAttempt(t *testing.T, q *Queue, plan *command.BatchPlan) *Batch {
	t.Helper()
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.Len(t, b.Items, 1)
	q.mu.Lock()
	b.Items[0].Overwrite = true
	q.mu.Unlock()
	require.NoError(t, q.Start(b.ID))
	return b
}

func TestOverwriteInputFailureKeepsOldOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "不存在.mp4")
	output := filepath.Join(root, "输出", "result.mkv")
	writeOldOutput(t, output)
	plan := oneJobPlan(input, output, "-nostdin", "-n", "-i", input, "-c", "copy", output)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()
	b := startOverwriteAttempt(t, q, plan)
	waitPaused(t, col)

	snap, ok := q.BatchCopy(b.ID)
	require.True(t, ok)
	require.Len(t, snap.Items, 1)
	assert.Equal(t, StatusFailed, snap.Items[0].Status)
	assert.False(t, snap.Items[0].Overwrite)
	assert.False(t, snap.Items[0].PartialOutput)
	assert.Empty(t, snap.Items[0].PartialPath)
	assertOldOutputKept(t, output)
	assertNoTemporaryOutputs(t, output)
	q.Decide(DecisionAbort)
}

func TestOverwriteStartFailureKeepsOldOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "输入.mp4")
	output := filepath.Join(root, "输出", "result.mkv")
	writeOldOutput(t, output)
	require.NoError(t, os.WriteFile(input, []byte("占位输入"), 0o644))
	plan := oneJobPlan(input, output, "-nostdin", "-n", "-i", input, output)
	t.Setenv("PATH", "") // ffprobe 与 ffmpeg 都无法启动；ffprobe 降级后，ffmpeg 启动错误必须走清理路径。

	q, col := newTestQueue(context.Background())
	defer q.Stop()
	b := startOverwriteAttempt(t, q, plan)
	waitPaused(t, col)

	snap, ok := q.BatchCopy(b.ID)
	require.True(t, ok)
	assert.Equal(t, StatusFailed, snap.Items[0].Status)
	assertOldOutputKept(t, output)
	assertNoTemporaryOutputs(t, output)
	q.Decide(DecisionAbort)
}

func TestOverwriteSuccessWithoutOutputIsFailure(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "输出", "result.mkv")
	writeOldOutput(t, output)
	// -version 会返回成功但不写文件；即使旧记录残留 -y，也不能误判成功或删除旧输出。
	plan := oneJobPlan("", output, "-nostdin", "-y", "-version", output)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()
	b := startOverwriteAttempt(t, q, plan)
	waitPaused(t, col)

	snap, ok := q.BatchCopy(b.ID)
	require.True(t, ok)
	assert.Equal(t, StatusFailed, snap.Items[0].Status)
	assert.Contains(t, snap.Items[0].Error, "非空输出")
	assertOldOutputKept(t, output)
	assertNoTemporaryOutputs(t, output)
	q.Decide(DecisionAbort)
}

func TestLegacyOverwriteArgCannotReplaceWithoutAuthorization(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "不存在.mp4")
	output := filepath.Join(root, "输出", "result.mkv")
	writeOldOutput(t, output)
	plan := oneJobPlan(input, output, "-nostdin", "-y", "-i", input, output)

	q, col := newTestQueue(context.Background())
	defer q.Stop()
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	waitPaused(t, col)

	snap, ok := q.BatchCopy(b.ID)
	require.True(t, ok)
	assert.True(t, IsOutputExists(snap.Items[0].Error))
	assertOldOutputKept(t, output)
	q.Decide(DecisionAbort)
}

func TestRecoverRequeueCleansRecordedPartial(t *testing.T) {
	plan, _ := fixturePlan(t)
	plan.Jobs = plan.Jobs[:1]
	src := NewBatch("recover-partial-success", plan, command.Selection{Convert: true}, fixedNow)
	src.Status = StatusRunning
	item := src.Items[0]
	item.Status = StatusRunning
	item.PartialOutput = true
	item.Overwrite = true
	require.NoError(t, os.MkdirAll(filepath.Dir(item.Output), 0o755))
	partial, err := os.CreateTemp(filepath.Dir(item.Output), ".recover-*.mkv")
	require.NoError(t, err)
	_, err = partial.WriteString("半成品")
	require.NoError(t, err)
	require.NoError(t, partial.Close())
	item.PartialPath = partial.Name()
	require.NoError(t, src.Save())
	require.NoError(t, RecoverStored())
	recovered, err := LoadTask(src.ID)
	require.NoError(t, err)

	q, col := newTestQueue(context.Background())
	defer q.Stop()
	require.NoError(t, q.Requeue(recovered))
	waitPaused(t, col)
	snap, ok := q.BatchCopy(src.ID)
	require.True(t, ok)
	assert.False(t, snap.Items[0].PartialOutput)
	assert.Empty(t, snap.Items[0].PartialPath)
	assert.False(t, snap.Items[0].Overwrite)
	assert.NoFileExists(t, partial.Name())
	q.Decide(DecisionAbort)
}

func TestRecoverRequeueReportsPartialCleanupFailure(t *testing.T) {
	plan, _ := fixturePlan(t)
	plan.Jobs = plan.Jobs[:1]
	src := NewBatch("recover-partial-failure", plan, command.Selection{Convert: true}, fixedNow)
	src.Status = StatusRunning
	item := src.Items[0]
	item.Status = StatusRunning
	item.PartialOutput = true
	require.NoError(t, os.MkdirAll(filepath.Dir(item.Output), 0o755))
	// 非空目录不能被 os.Remove 删除，可稳定模拟清理失败，无需依赖权限或当前用户。
	partial := filepath.Join(filepath.Dir(item.Output), ".recover-nonempty.mkv")
	require.NoError(t, os.MkdirAll(partial, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(partial, "keep"), []byte("x"), 0o644))
	item.PartialPath = partial
	require.NoError(t, src.Save())
	require.NoError(t, RecoverStored())
	recovered, err := LoadTask(src.ID)
	require.NoError(t, err)

	q, _ := newTestQueue(context.Background())
	defer q.Stop()
	err = q.Requeue(recovered)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "删除临时输出")
	_, ok := q.BatchCopy(src.ID)
	assert.False(t, ok, "清理失败的历史任务不能入队执行")
	assert.DirExists(t, partial)
	stored, loadErr := LoadTask(src.ID)
	require.NoError(t, loadErr)
	assert.True(t, stored.Items[0].PartialOutput)
	assert.Equal(t, partial, stored.Items[0].PartialPath)
}

func TestRecoverLegacyPartialDoesNotDeleteFormalOutput(t *testing.T) {
	plan, _ := fixturePlan(t)
	plan.Jobs = plan.Jobs[:1]
	src := NewBatch("recover-legacy-partial", plan, command.Selection{Convert: true}, fixedNow)
	src.Status = StatusRunning
	item := src.Items[0]
	item.Status = StatusRunning
	item.PartialOutput = true
	writeOldOutput(t, item.Output)
	require.NoError(t, src.Save())
	require.NoError(t, RecoverStored())
	recovered, err := LoadTask(src.ID)
	require.NoError(t, err)

	q, _ := newTestQueue(context.Background())
	defer q.Stop()
	err = q.Requeue(recovered)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "避免误删")
	assertOldOutputKept(t, item.Output)
}

func TestDiscardBetweenSelectionAndRunningDoesNotExecute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()
	b, err := q.Enqueue(singlePlan(t), command.Selection{Convert: true})
	require.NoError(t, err)

	// 固定重现 runPending 已选中并解锁、runBatch 尚未置 running 的交错。
	q.mu.Lock()
	q.armLocked(b.ID)
	selected := b
	delete(q.armed, b.ID)
	q.mu.Unlock()
	require.NoError(t, q.Discard(b.ID))
	assert.True(t, q.runBatch(ctx, selected))

	assert.False(t, col.has(QueueBatchStarted))
	assert.False(t, col.has(QueueItemStarted))
	_, ok := q.BatchCopy(b.ID)
	assert.False(t, ok)
	assert.Equal(t, StatusPending, selected.Status, "被移除的旧指针不能复活成运行中")
}

func TestRunItemAtomicallySkipsRemovedOrTerminalItems(t *testing.T) {
	q, col := newTestQueue(context.Background())
	defer q.Stop()
	b, err := q.Enqueue(singlePlan(t), command.Selection{Convert: true})
	require.NoError(t, err)
	item := b.Items[0]

	q.mu.Lock()
	item.Removed = true
	q.mu.Unlock()
	assert.True(t, q.runItem(context.Background(), b, item))
	assert.Zero(t, item.Attempts)

	q.mu.Lock()
	item.Removed = false
	item.Status = StatusDone
	q.mu.Unlock()
	assert.True(t, q.runItem(context.Background(), b, item))
	assert.Zero(t, item.Attempts)
	assert.False(t, col.has(QueueItemStarted))
}

type cancelAfterTwoChecks struct {
	context.Context
	checks int
}

func (c *cancelAfterTwoChecks) Err() error {
	c.checks++
	if c.checks > 2 {
		return context.Canceled
	}
	return nil
}

func TestRunItemPathCheckCancellationIsNotFailure(t *testing.T) {
	q, col := newTestQueue(context.Background())
	defer q.Stop()
	b, err := q.Enqueue(singlePlan(t), command.Selection{Convert: true})
	require.NoError(t, err)
	ctx := &cancelAfterTwoChecks{Context: context.Background()}

	assert.False(t, q.runItem(ctx, b, b.Items[0]), "路径校验阶段取消应走 canceled 收尾")
	snap, ok := q.BatchCopy(b.ID)
	require.True(t, ok)
	assert.Equal(t, StatusCanceled, snap.Items[0].Status)
	assert.False(t, snap.Items[0].PartialOutput)
	assert.False(t, col.has(QueueItemFailed))
}

func TestRunPendingCanceledContextLeavesLaterBatchesPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q, _ := newTestQueue(ctx)
	defer q.Stop()
	b, err := q.Enqueue(singlePlan(t), command.Selection{Convert: true})
	require.NoError(t, err)
	q.mu.Lock()
	q.armLocked(b.ID)
	q.mu.Unlock()
	cancel()

	assert.False(t, q.runPending(ctx))
	assert.Equal(t, StatusPending, b.Status)
	assert.Equal(t, StatusPending, b.Items[0].Status)
}

// failingStore 让每次保存都稳定失败，验证等待方会被错误唤醒而不是永久阻塞。
type failingStore struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *failingStore) SaveTask(*Batch) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.err
}

func (s *failingStore) LoadTask(id string) (*Batch, error) { return nil, errNotFound(id) }
func (s *failingStore) ListTasks() ([]*Batch, error)       { return nil, nil }
func (s *failingStore) DeleteTask(string) error            { return nil }

func (s *failingStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestStopReturnsFinalSaveFailureWithoutRetryingForever(t *testing.T) {
	saveErr := errors.New("磁盘写入失败")
	store := &failingStore{err: saveErr}
	BindStore(store)
	t.Cleanup(func() { BindStore(&memStore{}) })
	q, _ := newTestQueue(context.Background())
	b, err := q.Enqueue(singlePlan(t), command.Selection{Convert: true})
	require.NoError(t, err)

	stopped := make(chan error, 1)
	go func() { stopped <- q.Stop() }()
	select {
	case err := <-stopped:
		require.Error(t, err)
		assert.ErrorIs(t, err, saveErr)
		assert.Contains(t, err.Error(), "最终刷盘失败")
	case <-time.After(5 * time.Second):
		t.Fatal("保存失败后 Stop 永久等待")
	}
	assert.False(t, q.persisted(b.ID), "失败不能伪报 persisted")
	assert.Equal(t, 1, store.callCount(), "同一快照不无限重试")
	_, ok := q.BatchCopy(b.ID)
	assert.True(t, ok, "落盘失败仍保留内存里的最新任务状态")
}

func TestDiscardReturnsSaveFailure(t *testing.T) {
	saveErr := errors.New("磁盘写入失败")
	store := &failingStore{err: saveErr}
	BindStore(store)
	t.Cleanup(func() { BindStore(&memStore{}) })
	q, col := newTestQueue(context.Background())
	defer q.Stop()
	b, err := q.Enqueue(singlePlan(t), command.Selection{Convert: true})
	require.NoError(t, err)

	discarded := make(chan error, 1)
	go func() { discarded <- q.Discard(b.ID) }()
	select {
	case err := <-discarded:
		require.Error(t, err)
		assert.ErrorIs(t, err, saveErr)
	case <-time.After(5 * time.Second):
		t.Fatal("保存失败后 Discard 永久等待")
	}
	col.waitFor(t, QueueBatchRemoved, time.Second)
	assert.False(t, q.persisted(b.ID))
}

func TestOverwriteRealMediaCommitsTemporaryOutput(t *testing.T) {
	fx := newMediaFixture(t)
	plan := mediaFixturePlan(t, fx)
	output := plan.Jobs[0].Output
	writeOldOutput(t, output)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, _ := newTestQueue(ctx)
	defer q.Stop()
	b := startOverwriteAttempt(t, q, plan)
	done := waitBatchTerminal(t, q, b.ID, 25*time.Second)

	require.Equal(t, StatusDone, done.Status)
	require.Equal(t, StatusDone, done.Items[0].Status)
	assert.False(t, done.Items[0].Overwrite)
	assert.False(t, done.Items[0].PartialOutput)
	assert.Empty(t, done.Items[0].PartialPath)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.NotEqual(t, "旧正式输出", string(data))
	assert.NotEmpty(t, probeStreams(t, output))
	assertNoTemporaryOutputs(t, output)
	require.Contains(t, done.Items[0].PlanArgs, "-n")
	require.NotContains(t, done.Items[0].PlanArgs, "-y")
}

func TestTemporaryOutputArgsNeutralizesLegacyFlags(t *testing.T) {
	args, err := temporaryOutputArgs(
		[]string{"-nostdin", "-y", "-i", "in.mp4", "-n", "out.mkv"},
		"out.mkv", filepath.Join("dir", ".out.batcher-x.mkv"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"-y", "-nostdin", "-i", "in.mp4", filepath.Join("dir", ".out.batcher-x.mkv")}, args)
	assert.Equal(t, ".mkv", filepath.Ext(args[len(args)-1]))
	assert.NotContains(t, args[1:], "-y", "旧 -y 不得残留")
	assert.NotContains(t, args[1:], "-n", "旧 -n 不得让已创建的临时目标冲突")
}
