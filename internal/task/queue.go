package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/media"
)

// QueueEvent 是队列层面的状态变化，供 TUI 更新界面。
type QueueEvent struct {
	Type      QueueEventType
	BatchID   string
	ItemID    string        // 仅子任务事件有值
	FFmpegEvt *ffmpeg.Event // 仅 QueueFFmpeg 有值
	Err       error
}

// QueueEventType 描述队列事件的种类。
type QueueEventType string

const (
	QueueBatchAdded    QueueEventType = "batch_added"
	QueueBatchStarted  QueueEventType = "batch_started"
	QueueBatchPaused   QueueEventType = "batch_paused" // 子任务失败，等待决策
	QueueBatchDone     QueueEventType = "batch_done"
	QueueBatchFailed   QueueEventType = "batch_failed"
	QueueBatchCanceled QueueEventType = "batch_canceled"
	QueueBatchRemoved  QueueEventType = "batch_removed" // 批次已从队列移除（删除任务）
	QueueItemStarted   QueueEventType = "item_started"
	QueueItemDone      QueueEventType = "item_done"
	QueueItemRetry     QueueEventType = "item_retry"
	QueueItemFailed    QueueEventType = "item_failed"
	QueueItemSkipped   QueueEventType = "item_skipped"
	QueueItemCanceled  QueueEventType = "item_canceled"
	QueueFFmpeg        QueueEventType = "ffmpeg"  // 透传 ffmpeg.Event（日志/进度）
	QueueStopped       QueueEventType = "stopped" // 队列已停止，不再消费新任务
)

// PauseDecision 是子任务失败后的处理决策。
type PauseDecision string

const (
	DecisionRetry PauseDecision = "retry"
	DecisionSkip  PauseDecision = "skip"
	DecisionAbort PauseDecision = "abort" // 中止当前主任务，转入下一个
	DecisionDrop  PauseDecision = "drop"  // 删除当前失败的子任务，主任务继续
)

// Queue 是串行任务队列：同一时刻只运行一个 ffmpeg 子任务，子任务失败时暂停等待决策。
type Queue struct {
	mu          sync.Mutex
	batches     []*Batch
	notify      chan struct{}      // 有新任务或决策时通知消费者
	decision    chan PauseDecision // 暂停期间发送决策
	cancel      context.CancelFunc
	batchCancel context.CancelFunc  // 取消当前正在执行的主任务（不影响队列本身）
	discard     map[string]struct{} // 用户要求删除的主任务 ID：消费者收尾时直接取消并移除
	armed       map[string]struct{} // 用户按了开始的主任务；未标记的 pending 不会被拉起
	discardCh   chan string         // 删除信号：正在暂停等待决策的批次被删除时通知消费者收尾
	done        chan struct{}       // 消费者退出时关闭
	onEvent     func(QueueEvent)
	stopped     bool
}

// NewQueue 创建并启动队列消费者；ctx 取消时队列停止。
func NewQueue(ctx context.Context, onEvent func(QueueEvent)) *Queue {
	qctx, cancel := context.WithCancel(ctx)
	q := &Queue{
		notify:    make(chan struct{}, 1),
		decision:  make(chan PauseDecision, 1),
		cancel:    cancel,
		discardCh: make(chan string, 1),
		done:      make(chan struct{}),
		onEvent:   onEvent,
	}
	go q.run(qctx)
	return q
}

// Stop 停止队列，取消当前正在执行的子任务并等待消费者完全退出（包括最后一次记录落盘）。
func (q *Queue) Stop() {
	q.cancel()
	<-q.done
}

// CancelCurrent 取消当前正在执行的主任务；主任务标为 canceled。
// 已经手动开始、还在等待的后续任务会接着执行。不在执行任何任务时为空操作。
func (q *Queue) CancelCurrent() {
	q.mu.Lock()
	cancel := q.batchCancel
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Enqueue 把一个已规划的批处理加入队列末尾，状态为等待，不开始执行。
// 队列已停止时返回错误。要跑起来需再调用 Start。
func (q *Queue) Enqueue(plan *command.BatchPlan, sel command.Selection) (*Batch, error) {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return nil, fmt.Errorf("队列已停止，无法入队")
	}
	id := NewID(time.Now())
	b := NewBatch(id, plan, sel, time.Now())
	q.batches = append(q.batches, b)
	q.mu.Unlock()
	_ = b.Save()
	q.emit(QueueEvent{Type: QueueBatchAdded, BatchID: b.ID})
	return b, nil
}

// Start 开始一个等待中的主任务。当前有任务在执行或暂停时，这一条会排到它结束之后。
func (q *Queue) Start(batchID string) error {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return fmt.Errorf("队列已停止，无法开始")
	}
	var target *Batch
	for _, b := range q.batches {
		if b.ID == batchID {
			target = b
			break
		}
	}
	if target == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	if target.Status != StatusPending {
		q.mu.Unlock()
		return fmt.Errorf("任务不在等待执行")
	}
	q.armLocked(batchID)
	q.mu.Unlock()
	q.wake()
	return nil
}

// Requeue 把不在队列里的历史任务重新放回队尾。
// 已完成的子任务保持原样，失败、取消或中断的子任务重置为 pending。
func (q *Queue) Requeue(src *Batch) error {
	if src == nil || src.ID == "" {
		return fmt.Errorf("任务无效")
	}
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return fmt.Errorf("队列已停止，无法入队")
	}
	for _, exist := range q.batches {
		if exist.ID == src.ID {
			q.mu.Unlock()
			return q.RetryBatch(src.ID)
		}
	}
	b, err := CloneBatch(src)
	if err != nil {
		q.mu.Unlock()
		return err
	}
	switch b.Status {
	case StatusFailed, StatusCanceled, StatusInterrupted, StatusPaused, StatusRunning:
		for _, it := range b.Items {
			if it.Removed || it.Status == StatusDone || it.Status == StatusSkipped {
				continue
			}
			it.Status = StatusPending
			it.PartialOutput = false
			it.Error = ""
		}
		b.Status = StatusPending
		b.FinishedAt = time.Time{}
	case StatusPending:
	case StatusDone:
		q.mu.Unlock()
		return fmt.Errorf("任务已完成，无需重试")
	default:
		q.mu.Unlock()
		return fmt.Errorf("任务状态 %s 不可重试", b.Status)
	}
	q.batches = append(q.batches, b)
	q.armLocked(b.ID)
	q.mu.Unlock()
	_ = b.Save()
	q.emit(QueueEvent{Type: QueueBatchAdded, BatchID: b.ID})
	q.wake()
	return nil
}

// BatchStatus 加锁读取主任务的当前状态。
func (q *Queue) BatchStatus(batchID string) (Status, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, b := range q.batches {
		if b.ID == batchID {
			return b.Status, true
		}
	}
	return "", false
}

// ItemStatus 加锁读取子任务的当前状态。
func (q *Queue) ItemStatus(batchID, itemID string) (Status, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, b := range q.batches {
		if b.ID == batchID {
			for _, it := range b.Items {
				if it.ID == itemID {
					return it.Status, true
				}
			}
		}
	}
	return "", false
}

// setItemStatusLocked 仅供测试用：在持锁的情况下预设某个子任务的状态。
func (q *Queue) setItemStatus(batchID, itemID string, s Status) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, b := range q.batches {
		if b.ID == batchID {
			for _, it := range b.Items {
				if it.ID == itemID {
					it.Status = s
				}
			}
		}
	}
}

// BatchCopy 返回指定主任务的安全只读副本（深拷贝），用于测试和 TUI 读取状态。
func (q *Queue) BatchCopy(batchID string) (*Batch, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, b := range q.batches {
		if b.ID == batchID {
			return copyBatch(b), true
		}
	}
	return nil, false
}

// RetryBatch 重试主任务：暂停中的批次转为重试当前失败的子任务；
// 已结束（failed/canceled/interrupted）的批次把失败过/被取消的子任务重置为 pending
// （done 与跳过的保持不变）并重新排队执行。
func (q *Queue) RetryBatch(batchID string) error {
	q.mu.Lock()
	idx := -1
	for i, b := range q.batches {
		if b.ID == batchID {
			idx = i
			break
		}
	}
	if idx < 0 {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	status := q.batches[idx].Status
	q.mu.Unlock()

	if status == StatusPaused {
		// 暂停中：等同于重试当前失败的子任务。
		q.Decide(DecisionRetry)
		return nil
	}

	q.mu.Lock()
	b := q.batches[idx]
	switch status {
	case StatusFailed, StatusCanceled, StatusInterrupted:
		for _, it := range b.Items {
			if it.Status == StatusFailed || it.Status == StatusCanceled || it.Status == StatusInterrupted {
				it.Status = StatusPending
				it.PartialOutput = false
			}
		}
		b.Status = StatusPending
		b.FinishedAt = time.Time{}
	default:
		q.mu.Unlock()
		if status == StatusRunning {
			return fmt.Errorf("任务正在执行，无法重试")
		}
		if status == StatusDone {
			return fmt.Errorf("任务已完成，无需重试")
		}
		if status == StatusPending {
			return fmt.Errorf("任务已在队列等待执行")
		}
		return fmt.Errorf("任务状态 %s 不可重试", status)
	}
	q.armLocked(b.ID)
	q.mu.Unlock()
	_ = b.Save()
	q.emit(QueueEvent{Type: QueueBatchAdded, BatchID: b.ID})
	q.wake()
	return nil
}

// Discard 从队列删除主任务：pending 与终态批次直接移除（列表消失），
// paused 批次把剩余子任务标为取消、状态记 canceled 后再移除，运行中的批次不可删除。
// 任务记录文件保留，仅从队列列表中删除。
func (q *Queue) Discard(batchID string) error {
	q.mu.Lock()
	idx := -1
	for i, b := range q.batches {
		if b.ID == batchID {
			idx = i
			break
		}
	}
	if idx < 0 {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	b := q.batches[idx]
	if b.Status == StatusRunning {
		q.mu.Unlock()
		return fmt.Errorf("任务正在执行，无法删除")
	}
	if b.Status == StatusPaused {
		// 交给消费者收尾：waitDecision 会收到删除信号，completeDiscard 统一收尾并移除。
		if q.discard == nil {
			q.discard = make(map[string]struct{})
		}
		q.discard[batchID] = struct{}{}
		q.mu.Unlock()
		select {
		case q.discardCh <- batchID:
		default:
		}
		return nil
	}
	q.batches = append(q.batches[:idx], q.batches[idx+1:]...)
	q.mu.Unlock()
	q.emit(QueueEvent{Type: QueueBatchRemoved, BatchID: batchID})
	return nil
}

// discardRequested 报告用户是否删除了该批次。
func (q *Queue) discardRequested(batchID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.discard[batchID]
	return ok
}

// completeDiscard 把被删除的暂停批次收尾：剩余子任务取消、状态记 canceled、从队列移除。
// 返回是否发生了删除（调用方应随即结束该批次处理）。
func (q *Queue) completeDiscard(b *Batch) bool {
	if !q.discardRequested(b.ID) {
		return false
	}
	q.cancelRemainingItems(b)
	q.mu.Lock()
	b.Status = StatusCanceled
	b.FinishedAt = time.Now()
	for i, other := range q.batches {
		if other.ID == b.ID {
			q.batches = append(q.batches[:i], q.batches[i+1:]...)
			break
		}
	}
	delete(q.discard, b.ID)
	q.mu.Unlock()
	_ = b.Save()
	q.emit(QueueEvent{Type: QueueBatchRemoved, BatchID: b.ID})
	return true
}

func copyBatch(src *Batch) *Batch {
	if src == nil {
		return nil
	}
	cp := *src
	cp.Items = make([]*Item, len(src.Items))
	for i, it := range src.Items {
		item := *it
		if it.StderrTail != nil {
			item.StderrTail = make([]string, len(it.StderrTail))
			copy(item.StderrTail, it.StderrTail)
		}
		if it.AttemptLogs != nil {
			item.AttemptLogs = make([][]string, len(it.AttemptLogs))
			for i, lines := range it.AttemptLogs {
				item.AttemptLogs[i] = slices.Clone(lines)
			}
		}
		cp.Items[i] = &item
	}
	return &cp
}

func (q *Queue) Decide(d PauseDecision) {
	select {
	case q.decision <- d:
		return
	default:
	}
	// 通道里已有一条还没被取走的决策时，保留最新的一次按键。
	select {
	case <-q.decision:
	default:
	}
	select {
	case q.decision <- d:
	default:
	}
}

// drainDecision 丢掉执行期间多按留下的决策。
// 只在宣布下一次暂停之前调用，避免把用户针对本次暂停的按键清掉。
func (q *Queue) drainDecision() {
	select {
	case <-q.decision:
	default:
	}
}

// CancelBatch 取消指定主任务。正在执行或暂停的批次停止当前 ffmpeg，剩余子任务记为取消；
// 尚未开始的批次直接标为取消，不再执行。已结束的批次返回错误。
func (q *Queue) CancelBatch(batchID string) error {
	q.mu.Lock()
	var b *Batch
	for _, other := range q.batches {
		if other.ID == batchID {
			b = other
			break
		}
	}
	if b == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	switch b.Status {
	case StatusRunning, StatusPaused:
		cancel := q.batchCancel
		q.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil
	case StatusPending:
		for _, it := range b.Items {
			if !it.Status.Terminal() {
				it.Status = StatusCanceled
			}
		}
		b.Status = StatusCanceled
		b.FinishedAt = time.Now()
		q.mu.Unlock()
		q.emit(QueueEvent{Type: QueueBatchCanceled, BatchID: b.ID})
		_ = b.Save()
		q.wake()
		return nil
	default:
		q.mu.Unlock()
		return fmt.Errorf("任务已结束，无法取消")
	}
}

// DiscardItem 从主任务中删除一个子任务。正在执行的子任务不能删；
// 暂停中的失败子任务由消费者收尾为取消并继续后续子任务；其余子任务立即从列表消失。
func (q *Queue) DiscardItem(batchID, itemID string) error {
	q.mu.Lock()
	var b *Batch
	var it *Item
	for _, other := range q.batches {
		if other.ID != batchID {
			continue
		}
		b = other
		for _, item := range other.Items {
			if item.ID == itemID {
				it = item
				break
			}
		}
		break
	}
	if b == nil || it == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到子任务 %s", itemID)
	}
	if it.Removed {
		q.mu.Unlock()
		return fmt.Errorf("子任务已删除")
	}
	if it.Status == StatusRunning {
		q.mu.Unlock()
		return fmt.Errorf("子任务正在执行，无法删除")
	}
	if b.Status == StatusPaused && it.Status == StatusFailed {
		q.mu.Unlock()
		q.Decide(DecisionDrop)
		return nil
	}
	it.Removed = true
	if !it.Status.Terminal() {
		it.Status = StatusCanceled
	}
	q.mu.Unlock()
	_ = b.Save()
	q.emit(QueueEvent{Type: QueueItemCanceled, BatchID: batchID, ItemID: itemID})
	return nil
}

// RemovePending 从队列中移除一个仍处于 pending 状态的主任务；已启动的任务不可移除。
func (q *Queue) RemovePending(batchID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, b := range q.batches {
		if b.ID == batchID {
			if b.Status != StatusPending {
				return fmt.Errorf("任务 %s 已开始执行，无法移除", batchID)
			}
			q.batches = append(q.batches[:i], q.batches[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("未找到任务 %s", batchID)
}

// Snapshot 返回当前队列内容的只读快照（切片副本，元素指针共享）。
func (q *Queue) Snapshot() []*Batch {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Batch, len(q.batches))
	copy(out, q.batches)
	return out
}

// armLocked 把主任务标成可以执行。调用方需持有 q.mu。
func (q *Queue) armLocked(batchID string) {
	if q.armed == nil {
		q.armed = map[string]struct{}{}
	}
	q.armed[batchID] = struct{}{}
}

func (q *Queue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *Queue) emit(e QueueEvent) {
	if q.onEvent != nil {
		q.onEvent(e)
	}
}

// run 是消费者主循环，在独立 goroutine 里运行。
func (q *Queue) run(ctx context.Context) {
	defer close(q.done)
	defer func() {
		q.mu.Lock()
		q.stopped = true
		q.mu.Unlock()
		q.emit(QueueEvent{Type: QueueStopped})
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.notify:
			if !q.runPending(ctx) {
				return
			}
		}
	}
}

// runPending 取出下一个已经手动开始的 pending 主任务并执行；返回 false 表示应退出。
func (q *Queue) runPending(ctx context.Context) bool {
	q.mu.Lock()
	var next *Batch
	for _, b := range q.batches {
		if b.Status != StatusPending {
			continue
		}
		if _, ok := q.armed[b.ID]; !ok {
			continue
		}
		next = b
		delete(q.armed, b.ID)
		break
	}
	q.mu.Unlock()
	if next == nil {
		return true
	}
	return q.runBatch(ctx, next)
}

// runBatch 顺序执行主任务的全部子任务，子任务失败时暂停并等待决策。
func (q *Queue) runBatch(ctx context.Context, b *Batch) bool {
	bctx, cancelBatch := context.WithCancel(ctx)
	q.mu.Lock()
	q.batchCancel = cancelBatch
	if b.Status != StatusPending {
		// 入队后、真正开跑前被取消：不要覆盖成执行中。
		q.batchCancel = nil
		q.mu.Unlock()
		cancelBatch()
		q.wake()
		return true
	}
	b.Status = StatusRunning
	b.StartedAt = time.Now()
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		q.batchCancel = nil
		q.mu.Unlock()
	}()
	q.emit(QueueEvent{Type: QueueBatchStarted, BatchID: b.ID})
	_ = b.Save()

	finish := func(status Status) {
		if bctx.Err() != nil && status == StatusFailed {
			// 批次被取消时不应误记为失败。
			status = StatusCanceled
		}
		q.finishBatch(b, status)
		q.wake() // 空闲后继续已经手动开始的下一个主任务
	}

	continueItem := func(it *Item) bool {
		// runItem 只在批次被取消时返回 false。
		if q.runItem(bctx, b, it) {
			return true
		}
		q.cancelRemainingItems(b)
		finish(StatusCanceled)
		return false
	}

	// pauseFor 宣布暂停并等待本次决策。调用前先丢掉执行期间多按的 r，
	// 那些按键不属于这次暂停。返回 false 表示批次已经收尾，调用方应立刻返回。
	pauseFor := func(it *Item) (PauseDecision, bool) {
		q.drainDecision()
		q.mu.Lock()
		b.Status = StatusPaused
		itemErr := it.Error
		q.mu.Unlock()
		var err error
		if itemErr != "" {
			err = fmt.Errorf("%s", itemErr)
		}
		q.emit(QueueEvent{Type: QueueBatchPaused, BatchID: b.ID, ItemID: it.ID, Err: err})
		_ = b.Save()
		decision := q.waitDecision(bctx)
		if q.completeDiscard(b) {
			q.wake()
			return "", false
		}
		if bctx.Err() != nil {
			// 等待期间批次被取消，剩余子任务标 canceled，主任务不应记为失败。
			q.cancelRemainingItems(b)
			finish(StatusCanceled)
			return "", false
		}
		return decision, true
	}

	for _, it := range b.Items {
		for {
			if bctx.Err() != nil {
				q.cancelRemainingItems(b)
				finish(StatusCanceled)
				return true
			}
			q.mu.Lock()
			settled := it.Removed || it.Status.Terminal()
			q.mu.Unlock()
			if settled {
				break
			}
			if !continueItem(it) {
				return true
			}
			q.mu.Lock()
			failed := it.Status == StatusFailed
			q.mu.Unlock()
			if !failed {
				break
			}
			decision, cont := pauseFor(it)
			if !cont {
				return true
			}
			switch decision {
			case DecisionRetry:
				q.mu.Lock()
				it.Status = StatusPending
				it.PartialOutput = false
				it.Error = ""
				b.Status = StatusRunning
				q.mu.Unlock()
				q.emit(QueueEvent{Type: QueueItemRetry, BatchID: b.ID, ItemID: it.ID})
				continue
			case DecisionSkip:
				q.mu.Lock()
				it.Status = StatusSkipped
				b.Status = StatusRunning
				q.mu.Unlock()
				q.emit(QueueEvent{Type: QueueItemSkipped, BatchID: b.ID, ItemID: it.ID})
			case DecisionDrop:
				q.mu.Lock()
				it.Status = StatusCanceled
				it.Removed = true
				it.PartialOutput = false
				b.Status = StatusRunning
				q.mu.Unlock()
				q.emit(QueueEvent{Type: QueueItemCanceled, BatchID: b.ID, ItemID: it.ID})
			case DecisionAbort:
				q.cancelRemainingItems(b)
				finish(StatusFailed)
				return true
			default:
				// 未识别的决策：保留失败状态，继续后面的子任务。
			}
			_ = b.Save()
			break
		}
	}
	q.mu.Lock()
	c := b.Counts()
	q.mu.Unlock()
	finalStatus := StatusDone
	if c.Failed > 0 {
		finalStatus = StatusFailed
	}
	finish(finalStatus)
	// 主任务结束后继续已经手动开始的下一个主任务。
	q.wake()
	return true
}

// PrepareOverwrite 在两次确认之后，允许这个失败的子任务替换已有输出。
// 只改参数，不开始执行；调用方接着发重试决策。
func (q *Queue) PrepareOverwrite(batchID, itemID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	var b *Batch
	for _, other := range q.batches {
		if other.ID == batchID {
			b = other
			break
		}
	}
	if b == nil {
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	if b.Status != StatusPaused {
		return fmt.Errorf("任务不在暂停，无法覆盖")
	}
	var it *Item
	for _, item := range b.Items {
		if itemID != "" && item.ID != itemID {
			continue
		}
		if item.Status == StatusFailed && IsOutputExists(item.Error) {
			it = item
			break
		}
	}
	if it == nil {
		return fmt.Errorf("当前失败不是输出文件已存在")
	}
	it.Overwrite = true
	it.PlanArgs = overwriteArgs(it.PlanArgs)
	it.Args = ffmpeg.ExecArgs(it.PlanArgs)
	return nil
}

// overwriteArgs 把禁止覆盖的 -n 换成 -y。没有 -n 时在参数开头补上 -y。
func overwriteArgs(args []string) []string {
	out := slices.Clone(args)
	for i, arg := range out {
		if arg == "-n" {
			out[i] = "-y"
			return out
		}
	}
	if slices.Contains(out, "-y") {
		return out
	}
	return append([]string{"-y"}, out...)
}

// runItem 执行单个子任务；返回 false 仅当 ctx 已取消。
func (q *Queue) runItem(ctx context.Context, b *Batch, it *Item) bool {
	now := time.Now()
	q.mu.Lock()
	it.Status = StatusRunning
	it.Attempts++
	it.BeginAttemptLog()
	overwrite := it.Overwrite
	args := slices.Clone(it.PlanArgs)
	q.mu.Unlock()
	if overwrite {
		args = overwriteArgs(args)
	}
	q.emit(QueueEvent{Type: QueueItemStarted, BatchID: b.ID, ItemID: it.ID})
	_ = b.Save()

	runner := &ffmpeg.Runner{OnEvent: func(e ffmpeg.Event) {
		evt := e
		if evt.Type == ffmpeg.EventLog && evt.Message != "" {
			q.mu.Lock()
			it.AppendAttemptLog(evt.Message)
			q.mu.Unlock()
		}
		q.emit(QueueEvent{Type: QueueFFmpeg, BatchID: b.ID, ItemID: it.ID, FFmpegEvt: &evt})
	}}
	idx := 0
	if n, err := strconv.Atoi(strings.TrimSpace(it.ID)); err == nil && n > 0 {
		idx = n - 1
	}
	call := ffmpeg.Call{
		Index:   idx,
		Total:   len(b.Items),
		Command: command.FormatCommand(args),
		Args:    args, // Runner.Run 会通过 ExecArgs 注入进度选项
		Input:   it.Input,
	}

	// 确保输出目录存在
	if err := media.MakeDir(filepath.Dir(it.Output)); err != nil {
		q.failItem(b, it, now, fmt.Sprintf("创建输出目录: %v", err))
		return true
	}
	// 已有输出默认失败并暂停。这里不能删文件：它是上一次的结果，不是这次的半成品。
	if !overwrite {
		if _, err := os.Lstat(it.Output); err == nil {
			q.failItem(b, it, now, fmt.Sprintf("输出文件已存在: %s", it.Output))
			return true
		} else if err != nil && !os.IsNotExist(err) {
			q.failItem(b, it, now, fmt.Sprintf("检查输出文件: %v", err))
			return true
		}
	}

	q.mu.Lock()
	it.PartialOutput = true
	q.mu.Unlock()
	_ = b.Save()

	err := runner.Run(ctx, call)
	elapsed := time.Since(now)

	if err != nil {
		if ctx.Err() != nil {
			// 取消：删除半成品
			_ = os.Remove(it.Output)
			q.mu.Lock()
			it.Status = StatusCanceled
			it.PartialOutput = false
			it.DurationMs = elapsed.Milliseconds()
			q.mu.Unlock()
			q.emit(QueueEvent{Type: QueueItemCanceled, BatchID: b.ID, ItemID: it.ID})
			_ = b.Save()
			return false
		}
		// 输出已存在时保留原文件。其余失败删除这次写出的半成品。
		if !IsOutputExists(err.Error()) {
			_ = os.Remove(it.Output)
		}
		q.failItem(b, it, now, err.Error())
		return true
	}

	q.mu.Lock()
	it.Status = StatusDone
	it.Error = ""
	it.PartialOutput = false
	it.DurationMs = elapsed.Milliseconds()
	q.mu.Unlock()
	q.emit(QueueEvent{Type: QueueItemDone, BatchID: b.ID, ItemID: it.ID})
	_ = b.Save()
	return true
}

func (q *Queue) failItem(b *Batch, it *Item, start time.Time, msg string) {
	q.mu.Lock()
	it.Status = StatusFailed
	it.PartialOutput = false
	it.Error = msg
	it.AppendAttemptLog(msg)
	it.DurationMs = time.Since(start).Milliseconds()
	q.mu.Unlock()
	q.emit(QueueEvent{Type: QueueItemFailed, BatchID: b.ID, ItemID: it.ID, Err: fmt.Errorf("%s", msg)})
	_ = b.Save()
}

func (q *Queue) cancelRemainingItems(b *Batch) {
	q.mu.Lock()
	for _, it := range b.Items {
		if it.Status == StatusPending {
			it.Status = StatusCanceled
		}
	}
	q.mu.Unlock()
}

func (q *Queue) finishBatch(b *Batch, status Status) {
	q.mu.Lock()
	b.Status = status
	b.FinishedAt = time.Now()
	q.mu.Unlock()
	var evtType QueueEventType
	switch status {
	case StatusDone:
		evtType = QueueBatchDone
	case StatusFailed:
		evtType = QueueBatchFailed
	default:
		evtType = QueueBatchCanceled
	}
	q.emit(QueueEvent{Type: evtType, BatchID: b.ID})
	_ = b.Save()
}

// waitDecision 阻塞直到收到决策指令、删除信号、或 ctx 取消；取消时返回空字符串。
func (q *Queue) waitDecision(ctx context.Context) PauseDecision {
	select {
	case <-ctx.Done():
		return ""
	case d := <-q.decision:
		return d
	case batchID := <-q.discardCh:
		_ = batchID // 调用方通过 completeDiscard 收尾
		return ""
	}
}
