package task

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collector 收集队列事件，线程安全。
type collector struct {
	mu     sync.Mutex
	events []QueueEvent
}

func (c *collector) add(e QueueEvent) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
}

func (c *collector) types() []QueueEventType {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]QueueEventType, len(c.events))
	for i, e := range c.events {
		out[i] = e.Type
	}
	return out
}

func (c *collector) has(t QueueEventType) bool {
	return slices.Contains(c.types(), t)
}

// waitFor 轮询等待指定事件出现（事件由队列异步发出）。
func (c *collector) waitFor(t *testing.T, typ QueueEventType, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.has(typ) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待事件 %s 超时", string(typ))
}

// count 返回指定事件已出现的次数。
func (c *collector) count(typ QueueEventType) int {
	n := 0
	for _, e := range c.types() {
		if e == typ {
			n++
		}
	}
	return n
}

// waitPausedCount 等待暂停事件出现至少 n 次。
func (c *collector) waitPausedCount(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.count(QueueBatchPaused) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待第 %d 次暂停超时", n)
}

// newTestQueue 创建带事件收集的测试队列。
func newTestQueue(ctx context.Context) (*Queue, *collector) {
	col := &collector{}
	q := NewQueue(ctx, col.add)
	return q, col
}

// waitBatchTerminal 等待主任务进入终态，超时则测试失败，返回终态副本。
func waitBatchTerminal(t *testing.T, q *Queue, batchID string, timeout time.Duration) *Batch {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, ok := q.BatchCopy(batchID)
		if ok && b.Status.Terminal() {
			return b
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := q.BatchCopy(batchID)
	if b != nil {
		t.Fatalf("超时：主任务 %s 仍处于 %s", batchID, b.Status)
	}
	t.Fatalf("超时：主任务 %s 不在队列中", batchID)
	return nil
}

// mediaFixturePlan 用集成测试的真实媒体生成计划。
func mediaFixturePlan(t *testing.T, fx mediaFixture) *command.BatchPlan {
	t.Helper()
	opt := command.DefaultOption()
	opt.InputPath, opt.InputFormat = fx.input, "mkv"
	opt.OutputPath = filepath.Join(t.TempDir(), "输出")
	opt.Advance = "-c copy"
	plan, err := command.BuildPlan(context.Background(), &opt, command.Selection{Convert: true})
	require.NoError(t, err)
	return plan
}

func TestQueueRunsRealMediaToCompletion(t *testing.T) {
	fx := newMediaFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	plan := mediaFixturePlan(t, fx)
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID

	done := waitBatchTerminal(t, q, batchID, 25*time.Second)
	require.NotNil(t, done)

	assert.Equal(t, StatusDone, done.Status)
	assert.False(t, done.FinishedAt.IsZero())
	require.Len(t, done.Items, 1)
	assert.Equal(t, StatusDone, done.Items[0].Status)
	assert.False(t, done.Items[0].PartialOutput)
	assert.Greater(t, done.Items[0].DurationMs, int64(0))
	assert.FileExists(t, done.Items[0].Output)
	onDisk, err := LoadTask(done.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, onDisk.Status)
	assert.True(t, col.has(QueueBatchStarted))
	assert.True(t, col.has(QueueItemStarted))
	assert.True(t, col.has(QueueItemDone))
	assert.True(t, col.has(QueueBatchDone))
	assert.False(t, col.has(QueueBatchFailed))
}

func TestQueueSerialExecution(t *testing.T) {
	fx := newMediaFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	q, _ := newTestQueue(ctx)
	defer q.Stop()

	var mu sync.Mutex
	var peak, active int
	original := q.onEvent
	q.onEvent = func(e QueueEvent) {
		mu.Lock()
		switch e.Type {
		case QueueItemStarted:
			active++
			if active > peak {
				peak = active
			}
		case QueueItemDone, QueueItemFailed, QueueItemCanceled:
			active--
		}
		mu.Unlock()
		original(e)
	}

	plan1 := mediaFixturePlan(t, fx)
	opt2 := command.DefaultOption()
	opt2.InputPath, opt2.InputFormat = fx.input, "mkv"
	opt2.OutputPath = filepath.Join(t.TempDir(), "输出2")
	opt2.Advance = "-c copy"
	plan2, err := command.BuildPlan(context.Background(), &opt2, command.Selection{Convert: true})
	require.NoError(t, err)

	b1, err := q.Enqueue(plan1, command.Selection{Convert: true})
	require.NoError(t, err)
	b2, err := q.Enqueue(plan2, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b1.ID))
	require.NoError(t, q.Start(b2.ID))

	done1 := waitBatchTerminal(t, q, b1.ID, 50*time.Second)
	done2 := waitBatchTerminal(t, q, b2.ID, 50*time.Second)
	require.NotNil(t, done1)
	require.NotNil(t, done2)
	assert.Equal(t, StatusDone, done1.Status)
	assert.Equal(t, StatusDone, done2.Status)
	mu.Lock()
	assert.Equal(t, 1, peak, "同一时刻最多运行一个子任务")
	mu.Unlock()
}

func TestQueuePauseRetry(t *testing.T) {
	plan, _ := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID
	// 占位文件无法转码，第一个子任务必然失败
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchPaused) {
			break
		}
	}
	require.True(t, col.has(QueueBatchPaused), "子任务失败后主任务应进入暂停")

	snap, _ := q.BatchCopy(batchID)
	require.NotNil(t, snap)
	assert.Equal(t, StatusPaused, snap.Status)
	assert.Equal(t, StatusFailed, snap.Items[0].Status)
	assert.False(t, snap.Items[0].PartialOutput, "失败时必须删除半成品")
	assert.NoFileExists(t, snap.Items[0].Output, "失败时必须删除输出文件")

	// 持续发送跳过决策：4 个占位文件都会失败，每个失败都触发暂停
	go func() {
		for {
			s, ok := q.BatchStatus(batchID)
			if !ok || s.Terminal() {
				return
			}
			if s == StatusPaused {
				q.Decide(DecisionSkip)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	done := waitBatchTerminal(t, q, batchID, 10*time.Second)
	require.NotNil(t, done)
	assert.Equal(t, StatusSkipped, done.Items[0].Status)
	// 其余占位文件也会失败，最终主任务非 done
	assert.True(t, done.Status.Terminal())
}

func TestQueuePauseAbort(t *testing.T) {
	plan, _ := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchPaused) {
			break
		}
	}
	require.True(t, col.has(QueueBatchPaused))

	q.Decide(DecisionAbort)
	done := waitBatchTerminal(t, q, batchID, 5*time.Second)
	require.NotNil(t, done)
	assert.Equal(t, StatusFailed, done.Status, "中止后主任务应标为失败")
	for _, it := range done.Items {
		assert.NotEqual(t, StatusRunning, it.Status)
	}
}

func TestCancelCurrentCancelsBatchAndRunsNext(t *testing.T) {
	plan1, _ := fixturePlan(t)
	plan2, _ := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b1, err := q.Enqueue(plan1, command.Selection{Convert: true})
	require.NoError(t, err)
	b2, err := q.Enqueue(plan2, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b1.ID))
	require.NoError(t, q.Start(b2.ID))
	id1, id2 := b1.ID, b2.ID

	// 等第一个主任务失败进入暂停
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchPaused) {
			break
		}
	}
	require.True(t, col.has(QueueBatchPaused))

	// 取消当前主任务（不停止队列）：应标为 canceled 并自动开始下一个主任务
	q.CancelCurrent()
	n := 0
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		s1, ok1 := q.BatchStatus(id1)
		if ok1 && s1 == StatusCanceled {
			n = 1
			break
		}
	}
	require.Equal(t, 1, n, "当前主任务应被取消")

	// 下一个主任务继续执行并再次进入暂停（占位文件必然失败）
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		s2, ok2 := q.BatchStatus(id2)
		if ok2 && s2 == StatusRunning || ok2 && s2 == StatusPaused {
			break
		}
	}
	s2, _ := q.BatchStatus(id2)
	assert.True(t, s2 == StatusRunning || s2 == StatusPaused, "取消后队列应继续下一个主任务，got %s", s2)

	// 清理：取消第二个主任务并等待队列回空闲
	q.CancelCurrent()
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		s2, _ = q.BatchStatus(id2)
		if s2.Terminal() {
			break
		}
	}
	assert.Equal(t, StatusCanceled, s2)
}

// TestQueuePauseRetry 之上补充：批次级 RetryBatch 与 Discard。

// singlePlan 构造只有一个子任务的转码计划（占位文件，ffmpeg 必然失败）。
func singlePlan(t *testing.T) *command.BatchPlan {
	t.Helper()
	plan, _ := fixturePlan(t)
	plan.Jobs = plan.Jobs[:1]
	plan.Jobs[0].Output = filepath.Join(t.TempDir(), "输出", "a.mkv")
	return plan
}

// waitPaused 等待队列出现暂停事件。
func waitPaused(t *testing.T, col *collector) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchPaused) {
			return
		}
	}
	t.Fatal("等待批次暂停超时")
}

func TestRetryBatchOnPausedRetriesFailedItem(t *testing.T) {
	plan := singlePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID
	waitPaused(t, col)

	// r：暂停中重试当前失败的子任务，再次失败又暂停
	require.NoError(t, q.RetryBatch(batchID))
	col.waitPausedCount(t, 2, 10*time.Second)
	assert.True(t, col.has(QueueItemRetry))
	snap, _ := q.BatchCopy(batchID)
	require.NotNil(t, snap)
	assert.Equal(t, StatusPaused, snap.Status)
	assert.Equal(t, StatusFailed, snap.Items[0].Status)
	assert.Equal(t, 2, snap.Items[0].Attempts, "第二次失败应累计尝试次数")

	// s：跳过后批次收尾（无失败项 → done）
	q.Decide(DecisionSkip)
	done := waitBatchTerminal(t, q, batchID, 10*time.Second)
	require.NotNil(t, done)
	assert.Equal(t, StatusDone, done.Status, "跳过唯一失败项后批次应完成")
	assert.Equal(t, StatusSkipped, done.Items[0].Status)
}

// TestRetryAgainDoesNotSkip 连续重试失败的子任务时，每一次 r 都再跑一次，不能改成跳过并进入下一项。
func TestRetryAgainDoesNotSkip(t *testing.T) {
	plan := singlePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	waitPaused(t, col)

	require.NoError(t, q.RetryBatch(b.ID))
	col.waitPausedCount(t, 2, 10*time.Second)
	// 第二次暂停后再重试一次：必须仍停在这个子任务，不能被改成跳过。
	require.NoError(t, q.RetryBatch(b.ID))
	col.waitPausedCount(t, 3, 10*time.Second)

	snap, ok := q.BatchCopy(b.ID)
	require.True(t, ok)
	assert.Equal(t, StatusPaused, snap.Status, "多次重试后仍应停在当前子任务")
	require.Len(t, snap.Items, 1)
	assert.Equal(t, StatusFailed, snap.Items[0].Status)
	assert.Equal(t, 3, snap.Items[0].Attempts)

	q.Decide(DecisionSkip)
	done := waitBatchTerminal(t, q, b.ID, 10*time.Second)
	require.NotNil(t, done)
	assert.Equal(t, StatusSkipped, done.Items[0].Status)
}

func TestRetryBatchAfterAbortRequeuesWholeBatch(t *testing.T) {
	plan := singlePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID
	waitPaused(t, col)

	// a：中止 → 批次失败（终态）
	q.Decide(DecisionAbort)
	failedBatch := waitBatchTerminal(t, q, batchID, 10*time.Second)
	require.Equal(t, StatusFailed, failedBatch.Status)

	// r：重试整个任务 → failed 子任务重置 pending，批次重新排队 → 再次失败暂停
	require.NoError(t, q.RetryBatch(batchID))
	requeued, _ := q.BatchCopy(batchID)
	require.NotNil(t, requeued)
	assert.Equal(t, StatusPending, requeued.Status, "批次应重新排队")
	assert.Equal(t, StatusPending, requeued.Items[0].Status)
	col.waitPausedCount(t, 2, 10*time.Second)

	// s：跳过收尾
	q.Decide(DecisionSkip)
	done := waitBatchTerminal(t, q, batchID, 10*time.Second)
	require.NotNil(t, done)
	assert.Equal(t, StatusDone, done.Status)
	assert.Equal(t, 2, done.Items[0].Attempts)
}

func TestDiscardPausesBatchMarksCanceledAndRemoves(t *testing.T) {
	plan := singlePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID
	waitPaused(t, col)

	require.NoError(t, q.Discard(batchID))

	// 已从队列移除，且发出移除事件；剩余子任务与批次记为 canceled。
	col.waitFor(t, QueueBatchRemoved, 5*time.Second)
	assert.True(t, col.has(QueueBatchRemoved))
	_, ok := q.BatchCopy(batchID)
	assert.False(t, ok, "删除后不应再在队列中")
	snapshot := q.Snapshot()
	for _, s := range snapshot {
		assert.NotEqual(t, batchID, s.ID)
	}
	onDisk, err := LoadTask(batchID)
	require.NoError(t, err)
	assert.Equal(t, StatusCanceled, onDisk.Status)
	for _, it := range onDisk.Items {
		assert.True(t, it.Status.Terminal(), "删除后不应有子任务卡在非终态")
	}
}

func TestDiscardPendingAndTerminalBatches(t *testing.T) {
	plan1 := singlePlan(t)
	plan2 := singlePlan(t)
	plan3 := singlePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b1, err := q.Enqueue(plan1, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b1.ID))
	b2, err := q.Enqueue(plan2, command.Selection{Convert: true})
	require.NoError(t, err)
	_, err = q.Enqueue(plan3, command.Selection{Convert: true})
	require.NoError(t, err)

	// 等第一个暂停，此时 b2 大概率 pending、b3 pending
	waitPaused(t, col)
	if s, _ := q.BatchStatus(b2.ID); s == StatusPending {
		require.NoError(t, q.Discard(b2.ID))
		_, ok := q.BatchCopy(b2.ID)
		assert.False(t, ok, "pending 批次删除后应直接移除")
		assert.True(t, col.has(QueueBatchRemoved))
	}

	// 删除不存在/未知的批次报错
	assert.Error(t, q.Discard("不存在的批次"))
	_ = col
}

func TestQueueCancelStopsExecution(t *testing.T) {

	fx := newMediaFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	plan := mediaFixturePlan(t, fx)
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID

	// 等到子任务开始后取消
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueItemStarted) {
			break
		}
	}
	require.True(t, col.has(QueueItemStarted))
	q.Stop()

	done := waitBatchTerminal(t, q, batchID, 10*time.Second)
	require.NotNil(t, done)
	assert.Equal(t, StatusCanceled, done.Status)
	assert.True(t, col.has(QueueItemCanceled))
	assert.NoFileExists(t, done.Items[0].Output, "取消时必须删除半成品")
}

func TestQueueRemovePending(t *testing.T) {
	plan1, _ := fixturePlan(t)
	plan2, _ := fixturePlan(t)
	ctx := context.Background()
	q, _ := newTestQueue(ctx)
	defer q.Stop()

	b1, err := q.Enqueue(plan1, command.Selection{Convert: true})
	require.NoError(t, err)
	b2, err := q.Enqueue(plan2, command.Selection{Convert: true})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	s2, _ := q.BatchStatus(b2.ID)
	if s2 == StatusPending {
		require.NoError(t, q.RemovePending(b2.ID))
		snap := q.Snapshot()
		for _, b := range snap {
			assert.NotEqual(t, b2.ID, b.ID, "移除后不应再出现在快照里")
		}
	}

	err = q.RemovePending("不存在的ID")
	assert.Error(t, err)
	_ = b1
}

func TestQueueEnqueueAfterStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q, _ := newTestQueue(ctx)
	cancel()
	q.Stop() // 等待消费者退出
	plan, _ := fixturePlan(t)
	_, err := q.Enqueue(plan, command.Selection{Convert: true})
	assert.Error(t, err)
}

func TestQueueFFmpegEventsForwarded(t *testing.T) {
	fx := newMediaFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	plan := mediaFixturePlan(t, fx)
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	done := waitBatchTerminal(t, q, b.ID, 25*time.Second)
	require.Equal(t, StatusDone, done.Status)

	col.mu.Lock()
	var logs, progressEvents int
	for _, e := range col.events {
		if e.Type == QueueFFmpeg && e.FFmpegEvt != nil {
			switch e.FFmpegEvt.Type {
			case ffmpeg.EventLog:
				logs++
			case ffmpeg.EventProgress:
				progressEvents++
			}
		}
	}
	col.mu.Unlock()
	assert.Greater(t, logs, 0, "应透传日志事件")
	assert.Greater(t, progressEvents, 0, "应透传进度事件")
}

func TestQueuePartialOutputCleanup(t *testing.T) {
	plan, _ := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchPaused) {
			break
		}
	}
	require.True(t, col.has(QueueBatchPaused))

	snap, _ := q.BatchCopy(batchID)
	require.NotNil(t, snap)
	assert.False(t, snap.Items[0].PartialOutput, "失败后 PartialOutput 应置回 false")
	assert.NoFileExists(t, snap.Items[0].Output)

	q.Decide(DecisionAbort)
}

func TestQueueRecordSavedAfterEachItem(t *testing.T) {
	fx := newMediaFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, _ := newTestQueue(ctx)
	defer q.Stop()

	plan := mediaFixturePlan(t, fx)
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	done := waitBatchTerminal(t, q, b.ID, 25*time.Second)
	require.Equal(t, StatusDone, done.Status)
	q.Stop() // 等消费者完成最后一次落盘

	onDisk, err := LoadTask(done.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, onDisk.Status)
	assert.Equal(t, StatusDone, onDisk.Items[0].Status)
}

func TestQueueStoppedEventEmitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q, col := newTestQueue(ctx)
	_ = q
	cancel()
	q.Stop()
	assert.True(t, col.has(QueueStopped))
}

func TestBatchNameDeduplication(t *testing.T) {
	plan1, fx := fixturePlan(t)
	opt2 := command.DefaultOption()
	opt2.InputPath, opt2.InputFormat = fx.Root, "mp4"
	opt2.OutputPath = filepath.Join(t.TempDir(), "out2")
	plan2, err := command.BuildPlan(context.Background(), &opt2, command.Selection{Convert: true})
	require.NoError(t, err)

	ctx := context.Background()
	q, _ := newTestQueue(ctx)
	defer q.Stop()

	b1, _ := q.Enqueue(plan1, command.Selection{Convert: true})
	b2, _ := q.Enqueue(plan2, command.Selection{Convert: true})

	// 同一输入文件夹，Name 相同；ID 必须不同
	snap1, _ := q.BatchCopy(b1.ID)
	snap2, _ := q.BatchCopy(b2.ID)
	require.NotNil(t, snap1)
	require.NotNil(t, snap2)
	assert.Equal(t, snap1.Name, snap2.Name, "主任务名称取文件夹名，可重复")
	assert.NotEqual(t, snap1.ID, snap2.ID)
}

func TestQueueSkipsDoneItems(t *testing.T) {
	plan, _ := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	batchID := b.ID
	itemID := b.Items[0].ID

	// 把第一个子任务预先标为 done，消费者应跳过它
	q.setItemStatus(batchID, itemID, StatusDone)
	require.NoError(t, q.Start(b.ID))

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchPaused) || func() bool { s, _ := q.BatchStatus(batchID); return s.Terminal() }() {
			break
		}
	}
	// 第一个子任务状态不能被重置
	s, _ := q.ItemStatus(batchID, itemID)
	assert.Equal(t, StatusDone, s)
}

func TestQueueOutputDirCreatedOnlyOnExecution(t *testing.T) {
	plan, fx := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	assert.NoDirExists(t, fx.Out, "入队前输出目录不应存在")
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	assert.NoDirExists(t, fx.Out, "入队后、执行前输出目录仍不应存在")
	require.NoError(t, q.Start(b.ID))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchStarted) {
			break
		}
	}
	require.True(t, col.has(QueueBatchStarted))
	assert.DirExists(t, fx.Out, "执行后输出目录必须已创建")

	q.Decide(DecisionAbort)
}

func TestQueueLogStderrTailOnFailure(t *testing.T) {
	plan, _ := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))
	batchID := b.ID
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if col.has(QueueBatchPaused) {
			break
		}
	}
	require.True(t, col.has(QueueBatchPaused))

	snap, _ := q.BatchCopy(batchID)
	require.NotNil(t, snap)
	assert.NotEmpty(t, snap.Items[0].Error)

	q.Decide(DecisionAbort)
}

func TestEnqueueDoesNotRunUntilStart(t *testing.T) {
	plan, _ := fixturePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	time.Sleep(80 * time.Millisecond)
	s, ok := q.BatchStatus(b.ID)
	require.True(t, ok)
	assert.Equal(t, StatusPending, s)
	assert.False(t, col.has(QueueBatchStarted))

	require.NoError(t, q.Start(b.ID))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if col.has(QueueBatchStarted) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("手动开始后应执行")
}

func TestExistingOutputFailsWithoutDeletingAndRetryCounts(t *testing.T) {
	plan, _ := fixturePlan(t)
	out := plan.Jobs[0].Output
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	require.NoError(t, os.WriteFile(out, []byte("keep"), 0o644))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	q, _ := newTestQueue(ctx)
	defer q.Stop()

	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, q.Start(b.ID))

	waitAttempt := func(n int) *Batch {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			snap, ok := q.BatchCopy(b.ID)
			if ok && snap.Status == StatusPaused && snap.Items[0].Attempts >= n && snap.Items[0].Status == StatusFailed {
				return snap
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("未在暂停中看到第 %d 次失败", n)
		return nil
	}

	snap := waitAttempt(1)
	assert.True(t, IsOutputExists(snap.Items[0].Error))
	assert.Equal(t, 1, snap.Items[0].Attempts)
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))

	q.Decide(DecisionRetry)
	snap = waitAttempt(2)
	assert.Equal(t, 2, snap.Items[0].Attempts, "r 重试应记成第 2 次")
	assert.True(t, IsOutputExists(snap.Items[0].Error))
	data, err = os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data), "未确认覆盖时保留已有文件")

	require.NoError(t, q.PrepareOverwrite(b.ID, snap.Items[0].ID))
	snap, ok := q.BatchCopy(b.ID)
	require.True(t, ok)
	assert.True(t, snap.Items[0].Overwrite)
	assert.Contains(t, snap.Items[0].PlanArgs, "-y")
	assert.NotContains(t, snap.Items[0].PlanArgs, "-n")

	q.Decide(DecisionAbort)
}

func TestQueueCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q, _ := newTestQueue(ctx)
	plan, _ := fixturePlan(t)
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	if err != nil {
		return // 队列已停止，入队失败，测试通过
	}
	q.Stop()
	batchID := b.ID
	s, ok := q.BatchStatus(batchID)
	if ok {
		assert.NotEqual(t, StatusRunning, s)
	}
	_, _ = q.BatchCopy(batchID)
}
