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
	assert.FileExists(t, done.RecordPath())
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

func TestQueueCancelStopsExecution(t *testing.T) {
	fx := newMediaFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q, col := newTestQueue(ctx)
	defer q.Stop()

	plan := mediaFixturePlan(t, fx)
	b, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
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
	done := waitBatchTerminal(t, q, b.ID, 25*time.Second)
	require.Equal(t, StatusDone, done.Status)
	q.Stop() // 等消费者完成最后一次落盘

	onDisk, err := Load(done.RecordPath())
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
	_, err := q.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	assert.NoDirExists(t, fx.Out, "入队后、执行前输出目录仍不应存在")

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
	snap, _ := q.BatchCopy(batchID)
	if snap != nil {
		_ = os.Remove(snap.RecordPath())
	}
}
