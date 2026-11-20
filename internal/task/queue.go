package task

import (
	"context"
	"errors"
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
	QueueFFmpeg        QueueEventType = "ffmpeg"      // 透传 ffmpeg.Event（日志/进度）
	QueueStopped       QueueEventType = "stopped"     // 队列已停止，不再消费新任务
	QueueStoreError    QueueEventType = "store_error" // 任务记录写入失败
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

	events       []QueueEvent // 待分发事件；emit 只追加，不调用回调
	eventSig     chan struct{}
	dispatchStop bool
	dispatchDone chan struct{}

	savePending map[string]saveSnap // 每个主任务只保留最新一份待写入快照
	saveSeq     map[string]uint64
	saveAck     map[string]uint64 // 最新成功写入的序号；失败不能伪装成已保存
	saveSettled map[string]uint64 // 最新已得到结果的序号，失败也会推进以唤醒等待方
	saveErr     map[string]error  // 最新一次落盘结果的错误；后续成功会清除
	saveSig     chan struct{}
	saveStop    bool
	saveExited  bool       // 落盘协程已退出；等待落盘的调用方据此不再等
	saveCond    *sync.Cond // 与 mu 配对，落盘协程每写完一份快照广播一次
	saveDone    chan struct{}
}

// saveSnap 是一次待落盘的主任务快照及其序号。
type saveSnap struct {
	gen uint64
	b   *Batch
}

// NewQueue 创建并启动队列消费者；ctx 取消时队列停止。
func NewQueue(ctx context.Context, onEvent func(QueueEvent)) *Queue {
	qctx, cancel := context.WithCancel(ctx)
	q := &Queue{
		notify:       make(chan struct{}, 1),
		decision:     make(chan PauseDecision, 1),
		cancel:       cancel,
		discardCh:    make(chan string, 1),
		done:         make(chan struct{}),
		onEvent:      onEvent,
		eventSig:     make(chan struct{}, 1),
		dispatchDone: make(chan struct{}),
		savePending:  map[string]saveSnap{},
		saveSeq:      map[string]uint64{},
		saveAck:      map[string]uint64{},
		saveSettled:  map[string]uint64{},
		saveErr:      map[string]error{},
		saveSig:      make(chan struct{}, 1),
		saveDone:     make(chan struct{}),
	}
	q.saveCond = sync.NewCond(&q.mu)
	go q.dispatch()
	go q.saveLoop()
	go q.run(qctx)
	return q
}

// Stop 停止队列，取消当前正在执行的子任务，并等待消费者、落盘和事件分发都退出。
// 返回最终刷盘时仍未成功的错误；每份待写快照只尝试一次，不会在停止时无限重试。
func (q *Queue) Stop() error {
	q.cancel()
	<-q.done
	q.mu.Lock()
	q.saveStop = true
	q.mu.Unlock()
	q.signalSave()
	<-q.saveDone
	q.mu.Lock()
	q.dispatchStop = true
	var ids []string
	for id := range q.saveSeq {
		if !q.persistedLocked(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	var errs []error
	for _, id := range ids {
		err := q.saveErr[id]
		if err == nil {
			err = errors.New("落盘协程退出前未得到写入结果")
		}
		errs = append(errs, fmt.Errorf("任务 %s 最终刷盘失败: %w", id, err))
	}
	q.mu.Unlock()
	q.signalDispatch()
	<-q.dispatchDone
	if len(errs) > 0 {
		return fmt.Errorf("停止队列: %w", errors.Join(errs...))
	}
	return nil
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
		return nil, errors.New("队列已停止，无法入队")
	}
	id := NewID(time.Now())
	b := NewBatch(id, plan, sel, time.Now())
	q.batches = append(q.batches, b)
	q.mu.Unlock()
	q.persist(b)
	q.emit(QueueEvent{Type: QueueBatchAdded, BatchID: b.ID})
	return b, nil
}

// Start 开始一个等待中的主任务。当前有任务在执行或暂停时，这一条会排到它结束之后。
func (q *Queue) Start(batchID string) error {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return errors.New("队列已停止，无法开始")
	}
	target, _ := q.findLocked(batchID)
	if target == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	if target.Status != StatusPending {
		q.mu.Unlock()
		return errors.New("任务不在等待执行")
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
		return errors.New("任务无效")
	}
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return errors.New("队列已停止，无法入队")
	}
	for _, exist := range q.batches {
		if exist.ID == src.ID {
			q.mu.Unlock()
			return q.RetryBatch(src.ID)
		}
	}
	b := copyBatch(src)
	switch b.Status {
	case StatusFailed, StatusCanceled, StatusInterrupted, StatusPaused, StatusRunning:
		if err := resetForRetry(b); err != nil {
			q.mu.Unlock()
			return err
		}
	case StatusPending:
		for _, it := range b.Items {
			if it != nil {
				it.Overwrite = false
			}
		}
	case StatusDone:
		q.mu.Unlock()
		return errors.New("任务已完成，无需重试")
	default:
		q.mu.Unlock()
		return fmt.Errorf("任务状态 %s 不可重试", b.Status)
	}
	q.batches = append(q.batches, b)
	q.armLocked(b.ID)
	q.mu.Unlock()
	q.persist(b)
	q.emit(QueueEvent{Type: QueueBatchAdded, BatchID: b.ID})
	q.wake()
	return nil
}

// BatchStatus 加锁读取主任务的当前状态。
func (q *Queue) BatchStatus(batchID string) (Status, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	b, _ := q.findLocked(batchID)
	if b == nil {
		return "", false
	}
	return b.Status, true
}

// ItemStatus 加锁读取子任务的当前状态。
func (q *Queue) ItemStatus(batchID, itemID string) (Status, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	b, _ := q.findLocked(batchID)
	it := q.findItemLocked(b, itemID)
	if it == nil {
		return "", false
	}
	return it.Status, true
}

// BatchCopy 返回指定主任务的安全只读副本（深拷贝），用于测试和 TUI 读取状态。
func (q *Queue) BatchCopy(batchID string) (*Batch, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if b, _ := q.findLocked(batchID); b != nil {
		return copyBatch(b), true
	}
	return nil, false
}

// Rename 修改主任务的展示名称并落盘。名称只是列表里的标签，
// 不影响执行、输入输出路径和子任务；任务不在队列中时返回错误。
func (q *Queue) Rename(batchID, name string) error {
	q.mu.Lock()
	target, _ := q.findLocked(batchID)
	if target == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	target.Name = strings.TrimSpace(name)
	q.persistLocked(target)
	q.mu.Unlock()
	q.signalSave()
	return nil
}

// RetryBatch 重试主任务：暂停中的批次转为重试当前失败的子任务；
// 已结束（failed/canceled/interrupted）的批次把失败过/被取消的子任务重置为 pending
// （done 与跳过的保持不变）并重新排队执行。
func (q *Queue) RetryBatch(batchID string) error {
	q.mu.Lock()
	b, _ := q.findLocked(batchID)
	if b == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	switch status := b.Status; status {
	case StatusPaused:
		// 暂停中：等同于重试当前失败的子任务。
		q.mu.Unlock()
		q.Decide(DecisionRetry)
		return nil
	case StatusFailed, StatusCanceled, StatusInterrupted:
		if err := resetForRetry(b); err != nil {
			q.mu.Unlock()
			return err
		}
	default:
		q.mu.Unlock()
		switch status {
		case StatusRunning:
			return errors.New("任务正在执行，无法重试")
		case StatusDone:
			return errors.New("任务已完成，无需重试")
		case StatusPending:
			return errors.New("任务已在队列等待执行")
		}
		return fmt.Errorf("任务状态 %s 不可重试", status)
	}
	q.armLocked(b.ID)
	q.mu.Unlock()
	q.persist(b)
	q.emit(QueueEvent{Type: QueueBatchAdded, BatchID: b.ID})
	q.wake()
	return nil
}

// resetForRetry 清理已记录的半成品，再把未完成且未删除的子任务重置为等待执行。
// 调用方需持有队列锁；任一清理失败时保留该标记并返回错误，不改成可执行状态。
func resetForRetry(b *Batch) error {
	for _, it := range b.Items {
		if it == nil {
			continue
		}
		it.Overwrite = false // 即使清理失败，重置操作也不能留下覆盖授权。
		if it.Removed || it.Status == StatusDone || it.Status == StatusSkipped {
			continue
		}
		if err := cleanupPartialOutputLocked(it); err != nil {
			return fmt.Errorf("清理子任务 %s 的半成品: %w", it.ID, err)
		}
	}
	for _, it := range b.Items {
		if it == nil {
			continue
		}
		if it.Removed || it.Status == StatusDone || it.Status == StatusSkipped {
			continue
		}
		it.Status = StatusPending
		it.Error = ""
	}
	b.Status = StatusPending
	b.FinishedAt = time.Time{}
	return nil
}

// ReplacePlan 用新计划替换主任务的配置与子任务列表（重新编辑任务详情）。
// 等待中的批次原地更新；已结束的批次替换后重置为等待，原有执行记录被覆盖；
// 运行中或暂停中的批次返回错误。批次 ID 与创建时间保持不变，
// 名称沿用计划里填写的队列名称（留空取输入文件夹名）。替换后不自动开始，按 x 才执行。
func (q *Queue) ReplacePlan(batchID string, plan *command.BatchPlan, sel command.Selection) error {
	q.mu.Lock()
	b, _ := q.findLocked(batchID)
	if b == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	switch b.Status {
	case StatusPending, StatusDone, StatusFailed, StatusCanceled, StatusInterrupted:
	default:
		q.mu.Unlock()
		return errors.New("任务正在执行或暂停中，无法编辑")
	}
	updated := NewBatch(b.ID, plan, sel, b.CreatedAt)
	// 原地改写字段而不是换指针：消费者可能已经拿到这个批次准备执行，
	// 换指针会让它按旧参数跑完。状态仍是等待时消费者按新参数执行，符合编辑预期。
	*b = *updated
	delete(q.armed, b.ID)
	q.mu.Unlock()
	q.persist(b)
	return nil
}

// Discard 从队列删除主任务：pending 与终态批次直接移除（列表消失），
// paused 批次把剩余子任务标为取消、状态记 canceled 后再移除，运行中的批次不可删除。
// pending 与终态批次会同步等待落盘并返回错误；paused 批次异步收尾，落盘错误随 QueueBatchRemoved.Err 上报。
// 任务记录文件保留，仅从队列列表中删除。
func (q *Queue) Discard(batchID string) error {
	q.mu.Lock()
	b, idx := q.findLocked(batchID)
	if b == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	if b.Status == StatusRunning {
		q.mu.Unlock()
		return errors.New("任务正在执行，无法删除")
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
	delete(q.armed, batchID)
	q.mu.Unlock()
	err := q.waitPersisted(batchID)
	q.emit(QueueEvent{Type: QueueBatchRemoved, BatchID: batchID, Err: err})
	return err
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
	q.persist(b)
	err := q.waitPersisted(b.ID)
	q.emit(QueueEvent{Type: QueueBatchRemoved, BatchID: b.ID, Err: err})
	return true
}

func copyBatch(src *Batch) *Batch {
	if src == nil {
		return nil
	}
	cp := *src
	cp.Items = make([]*Item, len(src.Items))
	for i, it := range src.Items {
		if it == nil {
			continue
		}
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
		item.PlanArgs = slices.Clone(it.PlanArgs)
		cp.Items[i] = &item
	}
	cp.Option.Subtitles = slices.Clone(src.Option.Subtitles)
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
	b, _ := q.findLocked(batchID)
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
			if it == nil {
				continue
			}
			it.Overwrite = false
			if !it.Status.Terminal() {
				it.Status = StatusCanceled
			}
		}
		b.Status = StatusCanceled
		b.FinishedAt = time.Now()
		q.mu.Unlock()
		q.emit(QueueEvent{Type: QueueBatchCanceled, BatchID: b.ID})
		q.persist(b)
		q.wake()
		return nil
	default:
		q.mu.Unlock()
		return errors.New("任务已结束，无法取消")
	}
}

// DiscardItem 从主任务中删除一个子任务。正在执行的子任务不能删；
// 暂停中的失败子任务由消费者收尾为取消并继续后续子任务；其余子任务立即从列表消失。
func (q *Queue) DiscardItem(batchID, itemID string) error {
	q.mu.Lock()
	b, _ := q.findLocked(batchID)
	it := q.findItemLocked(b, itemID)
	if b == nil || it == nil {
		q.mu.Unlock()
		return fmt.Errorf("未找到子任务 %s", itemID)
	}
	if it.Removed {
		q.mu.Unlock()
		return errors.New("子任务已删除")
	}
	if it.Status == StatusRunning {
		q.mu.Unlock()
		return errors.New("子任务正在执行，无法删除")
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
	q.persist(b)
	q.emit(QueueEvent{Type: QueueItemCanceled, BatchID: batchID, ItemID: itemID})
	return nil
}

// Copies 在一次加锁里返回每个主任务的深拷贝。
func (q *Queue) Copies() []*Batch {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Batch, len(q.batches))
	for i, b := range q.batches {
		out[i] = copyBatch(b)
	}
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

// findLocked 按 ID 查找主任务。调用方需持有 q.mu。
func (q *Queue) findLocked(id string) (*Batch, int) {
	for i, b := range q.batches {
		if b.ID == id {
			return b, i
		}
	}
	return nil, -1
}

// findItemLocked 按 ID 查找子任务。调用方需持有 q.mu。
func (q *Queue) findItemLocked(b *Batch, itemID string) *Item {
	if b == nil {
		return nil
	}
	for _, it := range b.Items {
		if it.ID == itemID {
			return it
		}
	}
	return nil
}

// emit 把事件放进内部队列后立即返回。回调由分发协程调用，避免堵住界面协程。
func (q *Queue) emit(e QueueEvent) {
	q.mu.Lock()
	q.events = coalesceEvent(q.events, e)
	q.mu.Unlock()
	q.signalDispatch()
}

func (q *Queue) signalDispatch() {
	select {
	case q.eventSig <- struct{}{}:
	default:
	}
}

// coalesceEvent 把同一子任务连续的进度事件收成最新一条，日志行全部保留。
func coalesceEvent(events []QueueEvent, e QueueEvent) []QueueEvent {
	if e.Type == QueueFFmpeg && e.FFmpegEvt != nil && e.FFmpegEvt.Type == ffmpeg.EventProgress {
		if n := len(events); n > 0 {
			last := events[n-1]
			if last.Type == QueueFFmpeg && last.BatchID == e.BatchID && last.ItemID == e.ItemID &&
				last.FFmpegEvt != nil && last.FFmpegEvt.Type == ffmpeg.EventProgress {
				events[n-1] = e
				return events
			}
		}
	}
	return append(events, e)
}

func (q *Queue) dispatch() {
	defer close(q.dispatchDone)
	for {
		q.mu.Lock()
		batch := q.events
		q.events = nil
		cb := q.onEvent
		stop := q.dispatchStop && len(batch) == 0
		q.mu.Unlock()
		if stop {
			return
		}
		if len(batch) == 0 {
			<-q.eventSig
			continue
		}
		for _, e := range batch {
			if cb != nil {
				cb(e)
			}
		}
	}
}

// persist 在锁内拍一份快照，交给单独的写入协程。同一主任务只保留最新快照。
func (q *Queue) persist(b *Batch) {
	q.mu.Lock()
	q.persistLocked(b)
	q.mu.Unlock()
	q.signalSave()
}

// persistLocked 把快照放进待写队列。调用方需持有 q.mu。
func (q *Queue) persistLocked(b *Batch) {
	if b == nil || b.ID == "" {
		return
	}
	q.saveSeq[b.ID]++
	q.savePending[b.ID] = saveSnap{gen: q.saveSeq[b.ID], b: copyBatch(b)}
}

func (q *Queue) signalSave() {
	select {
	case q.saveSig <- struct{}{}:
	default:
	}
}

// persisted 报告该主任务已排队的快照是否都已写入。
func (q *Queue) persisted(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.persistedLocked(id)
}

// persistedLocked 与 persisted 相同。调用方需持有 q.mu。
func (q *Queue) persistedLocked(id string) bool {
	_, waiting := q.savePending[id]
	return !waiting && q.saveAck[id] >= q.saveSeq[id]
}

// waitPersisted 阻塞到该主任务已排队的快照得到写入结果；失败会返回错误而不是永久等待。
// 主任务被移除前必须先等它：界面收到移除事件就会删库，
// 此时还在排队或正在写的快照会把刚删掉的记录写回去。
func (q *Queue) waitPersisted(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.persistedLocked(id) && q.saveSettled[id] < q.saveSeq[id] && !q.saveExited {
		q.saveCond.Wait()
	}
	if q.persistedLocked(id) {
		return nil
	}
	if err := q.saveErr[id]; err != nil {
		return fmt.Errorf("等待任务 %s 落盘: %w", id, err)
	}
	return fmt.Errorf("等待任务 %s 落盘: 落盘协程已退出且没有成功结果", id)
}

func (q *Queue) saveLoop() {
	defer close(q.saveDone)
	defer func() {
		q.mu.Lock()
		q.saveExited = true
		q.mu.Unlock()
		q.saveCond.Broadcast()
	}()
	for {
		q.mu.Lock()
		var id string
		var snap saveSnap
		for key, job := range q.savePending {
			id, snap = key, job
			delete(q.savePending, key)
			break
		}
		stop := q.saveStop && id == ""
		q.mu.Unlock()
		if stop {
			return
		}
		if id == "" {
			<-q.saveSig
			continue
		}
		err := snap.b.Save()
		if err != nil {
			q.emit(QueueEvent{Type: QueueStoreError, BatchID: id, Err: err})
		}
		q.mu.Lock()
		if snap.gen >= q.saveSettled[id] {
			q.saveSettled[id] = snap.gen
			if err != nil {
				q.saveErr[id] = err
			} else {
				q.saveErr[id] = nil
			}
		}
		if err == nil && snap.gen >= q.saveAck[id] {
			q.saveAck[id] = snap.gen
		}
		q.mu.Unlock()
		q.saveCond.Broadcast()
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
	if ctx.Err() != nil {
		return false
	}
	q.mu.Lock()
	if ctx.Err() != nil {
		q.mu.Unlock()
		return false
	}
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
		return ctx.Err() == nil
	}
	return q.runBatch(ctx, next)
}

// runBatch 顺序执行主任务的全部子任务，子任务失败时暂停并等待决策。
func (q *Queue) runBatch(ctx context.Context, b *Batch) bool {
	if ctx.Err() != nil {
		return false
	}
	bctx, cancelBatch := context.WithCancel(ctx)
	q.mu.Lock()
	current, _ := q.findLocked(b.ID)
	_, discarded := q.discard[b.ID]
	if ctx.Err() != nil {
		q.mu.Unlock()
		cancelBatch()
		return false
	}
	if current != b || b.Status != StatusPending || discarded {
		// 选中后、真正开跑前可能已被删除或取消；原指针不能再被复活成执行中。
		q.mu.Unlock()
		cancelBatch()
		q.wake()
		return true
	}
	q.batchCancel = cancelBatch
	b.Status = StatusRunning
	b.StartedAt = time.Now()
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		q.batchCancel = nil
		q.mu.Unlock()
	}()
	q.emit(QueueEvent{Type: QueueBatchStarted, BatchID: b.ID})
	q.persist(b)

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

	for _, it := range b.Items {
	attempts:
		for {
			if bctx.Err() != nil {
				q.cancelRemainingItems(b)
				finish(StatusCanceled)
				return true
			}
			q.mu.Lock()
			settled := it == nil || it.Removed || it.Status.Terminal()
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
			for {
				decision, cont := q.pauseForItem(bctx, b, it, finish)
				if !cont {
					return true
				}
				again, stop, resolved := q.takePauseDecision(b, it, decision)
				if stop {
					q.cancelRemainingItems(b)
					finish(StatusFailed)
					return true
				}
				if !resolved {
					// 半成品清理失败或决策无效：保留失败状态，继续等下一次明确决策。
					continue
				}
				if again {
					continue attempts
				}
				q.persist(b)
				break attempts
			}
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
	return true
}

// pauseForItem 宣布暂停并等待本次决策。调用前先丢掉执行期间多按的 r，
// 那些按键不属于这次暂停。返回 false 表示批次已经收尾，调用方应立刻返回。
func (q *Queue) pauseForItem(bctx context.Context, b *Batch, it *Item, finish func(Status)) (PauseDecision, bool) {
	q.drainDecision()
	q.drainDiscard()
	if q.completeDiscard(b) {
		q.wake()
		return "", false
	}
	q.mu.Lock()
	b.Status = StatusPaused
	itemErr := it.Error
	q.mu.Unlock()
	var err error
	if itemErr != "" {
		err = errors.New(itemErr)
	}
	q.emit(QueueEvent{Type: QueueBatchPaused, BatchID: b.ID, ItemID: it.ID, Err: err})
	q.persist(b)
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

// takePauseDecision 应用一次暂停决策。again 表示重跑，stop 表示整批结束，
// resolved 为假表示决策无效或半成品清理失败，必须继续等待而不能推进到后续子任务。
func (q *Queue) takePauseDecision(b *Batch, it *Item, decision PauseDecision) (again, stop, resolved bool) {
	if decision == DecisionAbort {
		q.mu.Lock()
		it.Overwrite = false
		q.mu.Unlock()
		return false, true, true
	}
	if decision != DecisionRetry && decision != DecisionSkip && decision != DecisionDrop {
		return false, false, false
	}

	q.mu.Lock()
	if err := cleanupPartialOutputLocked(it); err != nil {
		it.Status = StatusFailed
		it.Error = err.Error()
		it.AppendAttemptLog(it.Error)
		it.Overwrite = false
		b.Status = StatusPaused
		q.mu.Unlock()
		return false, false, false
	}
	switch decision {
	case DecisionRetry:
		it.Status = StatusPending
		it.Error = ""
		b.Status = StatusRunning
	case DecisionSkip:
		it.Status = StatusSkipped
		it.Overwrite = false
		b.Status = StatusRunning
	case DecisionDrop:
		it.Status = StatusCanceled
		it.Removed = true
		it.Overwrite = false
		b.Status = StatusRunning
	}
	q.mu.Unlock()

	switch decision {
	case DecisionRetry:
		q.emit(QueueEvent{Type: QueueItemRetry, BatchID: b.ID, ItemID: it.ID})
		return true, false, true
	case DecisionSkip:
		q.emit(QueueEvent{Type: QueueItemSkipped, BatchID: b.ID, ItemID: it.ID})
	case DecisionDrop:
		q.emit(QueueEvent{Type: QueueItemCanceled, BatchID: b.ID, ItemID: it.ID})
	}
	return false, false, true
}

// PrepareOverwrite 在确认之后，只允许这个失败子任务的下一次尝试替换已有输出。
// 不修改计划参数，也不开始执行；调用方接着发重试决策。
func (q *Queue) PrepareOverwrite(batchID, itemID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	b, _ := q.findLocked(batchID)
	if b == nil {
		return fmt.Errorf("未找到任务 %s", batchID)
	}
	if b.Status != StatusPaused {
		return errors.New("任务不在暂停，无法覆盖")
	}
	var it *Item
	for _, item := range b.Items {
		if item == nil || itemID != "" && item.ID != itemID {
			continue
		}
		if item.Status == StatusFailed && IsOutputExists(item.Error) {
			it = item
			break
		}
	}
	if it == nil {
		return errors.New("当前失败不是输出文件已存在")
	}
	it.Overwrite = true
	return nil
}

// temporaryOutputArgs 让 ffmpeg 只写本次临时文件。临时文件由本进程独占创建，
// 因此无论旧计划里残留 -n 还是 -y，都统一用 -y 写临时文件；正式输出是否可替换由提交步骤单独控制。
func temporaryOutputArgs(args []string, output, partial string) ([]string, error) {
	out := make([]string, 0, len(args)+1)
	out = append(out, "-y")
	replaced := 0
	for _, arg := range args {
		if arg == "-n" || arg == "-y" {
			continue
		}
		if arg == output {
			arg = partial
			replaced++
		}
		out = append(out, arg)
	}
	if replaced == 0 {
		return nil, errors.New("计划参数中未找到正式输出路径，已停止执行以保护已有文件")
	}
	return out, nil
}

// cleanupPartialOutputLocked 清理记录明确的半成品。调用方需持有队列锁。
// 旧记录没有 PartialPath，无法区分完整输出与半成品时宁可保留正式输出并报告。
func cleanupPartialOutputLocked(it *Item) error {
	if it == nil || !it.PartialOutput {
		return nil
	}
	partial := it.PartialPath
	if partial == "" {
		if _, err := os.Lstat(it.Output); err != nil {
			if os.IsNotExist(err) {
				it.PartialOutput = false
				return nil
			}
			return fmt.Errorf("检查旧记录的输出文件: %w", err)
		}
		return errors.New("旧记录未记录临时输出路径，为避免误删正式输出，请先手动确认")
	}
	if filepath.Clean(partial) == filepath.Clean(it.Output) || filepath.Clean(partial) == filepath.Clean(it.Input) {
		return errors.New("半成品路径与正式输入或输出重合，已拒绝删除")
	}
	if filepath.Clean(filepath.Dir(partial)) != filepath.Clean(filepath.Dir(it.Output)) {
		return errors.New("半成品不在正式输出目录内，已拒绝删除")
	}
	if err := os.Remove(partial); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除临时输出 %s: %w", partial, err)
	}
	it.PartialOutput = false
	it.PartialPath = ""
	return nil
}

// runItem 执行单个子任务；返回 false 仅当 ctx 已取消。
func (q *Queue) runItem(ctx context.Context, b *Batch, it *Item) bool {
	if ctx.Err() != nil || it == nil {
		return ctx.Err() == nil
	}
	now := time.Now()
	q.mu.Lock()
	if ctx.Err() != nil {
		q.mu.Unlock()
		return false
	}
	if it.Removed || it.Status.Terminal() || it.Status != StatusPending {
		q.mu.Unlock()
		return true
	}
	it.Status = StatusRunning
	it.Attempts++
	it.BeginAttemptLog()
	overwrite := it.Overwrite
	it.Overwrite = false // 覆盖确认只授权这一次尝试，失败或崩溃恢复都不能继续沿用。
	q.mu.Unlock()
	q.emit(QueueEvent{Type: QueueItemStarted, BatchID: b.ID, ItemID: it.ID})
	q.persist(b) // 尽快留下 running 与覆盖授权已消费的记录，崩溃恢复不会沿用旧授权。

	q.mu.Lock()
	cleanupErr := cleanupPartialOutputLocked(it)
	q.mu.Unlock()
	if cleanupErr != nil {
		q.failItem(b, it, now, cleanupErr)
		return true
	}
	if err := media.CheckPaths(ctx, []string{it.Input}, []string{it.Output}, false); err != nil {
		err = fmt.Errorf("校验输入输出路径: %w", err)
		if ctx.Err() != nil {
			return q.finishItem(ctx, b, it, now, overwrite, err)
		}
		q.failItem(b, it, now, err)
		return true
	}

	partial, err := q.prepareOutput(it.Output, overwrite)
	if err != nil {
		q.failItem(b, it, now, err)
		return true
	}
	q.mu.Lock()
	it.PartialPath = partial
	it.PartialOutput = true
	q.mu.Unlock()
	q.persist(b)
	args, err := temporaryOutputArgs(it.PlanArgs, it.Output, partial)
	if err != nil {
		q.mu.Lock()
		cleanupErr = cleanupPartialOutputLocked(it)
		q.mu.Unlock()
		if cleanupErr != nil {
			err = fmt.Errorf("%w；%w", err, cleanupErr)
		}
		q.failItem(b, it, now, err)
		return true
	}

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
	if n, convErr := strconv.Atoi(strings.TrimSpace(it.ID)); convErr == nil && n > 0 {
		idx = n - 1
	}
	call := ffmpeg.Call{
		Index:   idx,
		Total:   len(b.Items),
		Command: command.FormatCommand(args),
		Args:    args, // Runner.Run 会通过 ExecArgs 注入进度选项
		Input:   it.Input,
	}

	return q.finishItem(ctx, b, it, now, overwrite, runner.Run(ctx, call))
}

// prepareOutput 创建输出目录和本次尝试的同目录临时文件，保留原扩展名。
// 未授权覆盖时，已有正式文件直接报错且绝不删除。
func (q *Queue) prepareOutput(output string, overwrite bool) (string, error) {
	if err := media.MakeDir(filepath.Dir(output)); err != nil {
		return "", fmt.Errorf("创建输出目录: %w", err)
	}
	if !overwrite {
		if _, err := os.Lstat(output); err == nil {
			return "", fmt.Errorf("输出文件已存在: %s", output)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("检查输出文件: %w", err)
		}
	}
	// 不重复完整视频名，避免正式文件名接近 NAME_MAX 时临时文件反而超长。
	return createPartialOutput(output)
}

// finishItem 校验并提交临时输出，记录命令结果。取消时返回 false，调用方停止本批剩余子任务。
func (q *Queue) finishItem(ctx context.Context, b *Batch, it *Item, start time.Time, overwrite bool, err error) bool {
	elapsed := time.Since(start)
	if err == nil && ctx.Err() != nil {
		err = fmt.Errorf("任务已取消: %w", ctx.Err())
	}
	if err == nil {
		q.mu.Lock()
		partial := it.PartialPath
		q.mu.Unlock()
		err = commitPartialOutput(partial, it.Output, overwrite)
		if err != nil && errors.Is(err, os.ErrExist) {
			err = fmt.Errorf("输出文件已存在: %s: %w", it.Output, err)
		}
	}
	if err != nil {
		q.mu.Lock()
		cleanupErr := cleanupPartialOutputLocked(it)
		if cleanupErr != nil {
			err = fmt.Errorf("%w；%w", err, cleanupErr)
		}
		it.Overwrite = false
		q.mu.Unlock()
		if ctx.Err() != nil {
			q.mu.Lock()
			it.Status = StatusCanceled
			it.DurationMs = elapsed.Milliseconds()
			q.mu.Unlock()
			q.emit(QueueEvent{Type: QueueItemCanceled, BatchID: b.ID, ItemID: it.ID, Err: err})
			q.persist(b)
			return false
		}
		q.failItem(b, it, start, err)
		return true
	}

	q.mu.Lock()
	it.Status = StatusDone
	it.Error = ""
	it.PartialOutput = false
	it.PartialPath = ""
	it.Overwrite = false
	it.DurationMs = elapsed.Milliseconds()
	q.mu.Unlock()
	q.emit(QueueEvent{Type: QueueItemDone, BatchID: b.ID, ItemID: it.ID})
	q.persist(b)
	return true
}

func (q *Queue) failItem(b *Batch, it *Item, start time.Time, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	q.mu.Lock()
	it.Status = StatusFailed
	it.Overwrite = false
	it.Error = msg
	it.AppendAttemptLog(msg)
	it.DurationMs = time.Since(start).Milliseconds()
	q.mu.Unlock()
	q.emit(QueueEvent{Type: QueueItemFailed, BatchID: b.ID, ItemID: it.ID, Err: err})
}

func (q *Queue) cancelRemainingItems(b *Batch) {
	q.mu.Lock()
	for _, it := range b.Items {
		if it == nil {
			continue
		}
		it.Overwrite = false
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
	for _, it := range b.Items {
		if it != nil {
			it.Overwrite = false
		}
	}
	delete(q.discard, b.ID)
	q.persistLocked(b)
	q.mu.Unlock()
	q.signalSave()
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
}

// drainDiscard 丢掉不属于这次暂停的删除信号。是否删除仍以 discard 表为准。
func (q *Queue) drainDiscard() {
	for {
		select {
		case <-q.discardCh:
		default:
			return
		}
	}
}

// waitDecision 阻塞直到收到决策、对得上的删除信号，或 ctx 取消。
// 空决策和过期的删除信号会继续等待，避免跳过用户还没处理的失败。
func (q *Queue) waitDecision(ctx context.Context) PauseDecision {
	for {
		select {
		case <-ctx.Done():
			return ""
		case d := <-q.decision:
			switch d {
			case DecisionRetry, DecisionSkip, DecisionAbort, DecisionDrop:
				return d
			default:
				// 非法值不属于本次暂停的明确操作，继续等待。
				continue
			}
		case batchID := <-q.discardCh:
			if q.discardRequested(batchID) {
				return ""
			}
		}
	}
}
