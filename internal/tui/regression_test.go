package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresetBoundaryActions(t *testing.T) {
	for _, count := range []int{0, 2} {
		for _, key := range []string{"e", "d", "J", "K"} {
			t.Run(fmt.Sprintf("%d/最后一个内置项/%s", count, key), func(t *testing.T) {
				isolatePresets(t)
				m := newModel()
				if count > 0 {
					require.NoError(t, presets.Save([]presets.Preset{{Args: "a"}, {Args: "b"}}))
					m.presets = []presets.Preset{{Args: "a"}, {Args: "b"}}
				}
				m.openPresetList()
				m.presetCursor = m.presetUserOffset() - 1
				m.modal.focus = m.presetCursor
				before := slices.Clone(m.presets)
				require.NotPanics(t, func() { press(&m, key) })
				assert.Equal(t, before, m.presets)
				require.NotNil(t, m.modal)
				assert.Equal(t, modalPresetList, m.modal.kind)
			})
		}
	}
}

type failPresetStore struct{}

func (failPresetStore) LoadPresets() ([]presets.Preset, error) { return nil, nil }
func (failPresetStore) SavePresets([]presets.Preset) error {
	return errors.New("写入失败")
}

func TestPresetPersistenceFailureDoesNotCommit(t *testing.T) {
	for _, action := range []string{"新增", "编辑", "删除", "排序"} {
		t.Run(action, func(t *testing.T) {
			isolatePresets(t)
			m := newModel()
			m.presets = []presets.Preset{{Args: "-c copy"}, {Args: "-c:v libx264"}}
			before := append([]presets.Preset(nil), m.presets...)
			m.presetCursor = 0
			presets.Bind(failPresetStore{})
			switch action {
			case "新增", "编辑":
				index := -1
				if action == "编辑" {
					index = 0
				}
				m.openPresetModal(index, presets.Preset{Args: "-c:v mpeg4"})
				m.savePresetModal()
				require.NotNil(t, m.modal)
				assert.Contains(t, m.modal.err, "保存失败")
			case "删除":
				m.deletePreset(0)
			case "排序":
				m.movePreset(1)
			}
			assert.Equal(t, before, m.presets)
			assert.Contains(t, m.statusMsg, "失败")
		})
	}
}

func TestPasteReachesOnlyActiveInput(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.openPresetModal(-1, presets.Preset{})
	text := "1xqjk 中文 路径"
	next, _ := m.Update(tea.PasteMsg{Content: text})
	m = next.(model)
	require.NotNil(t, m.modal)
	assert.Equal(t, text, m.modal.fields[0].input.Value())
	assert.False(t, m.running)
}

// testQueue 为模型接上一个真实队列并注册清理；事件由测试自行从 m.evCh 读取。
func testQueue(t *testing.T, m *model) {
	t.Helper()
	m.evCh = make(chan tea.Msg, 256)
	m.initQueue(m.evCh)
	t.Cleanup(m.queue.Stop)
}

// queueSelection 返回队列测试使用的选择组合。
func queueSelection() command.Selection {
	return command.Selection{Convert: true}
}

// waitBatchEvent 从模型事件通道读消息并喂给 Update，直到出现指定类型的队列事件或超时。
func waitBatchEvent(t *testing.T, m *model, typ task.QueueEventType) queueEventMsg {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-m.evCh:
			next, _ := m.Update(msg)
			*m = next.(model)
			if e, ok := msg.(queueEventMsg); ok && e.evt.Type == typ {
				return e
			}
		case <-time.After(time.Until(deadline) / 2):
			t.Fatalf("等待队列事件 %s 超时", typ)
		}
	}
	t.Fatalf("等待队列事件 %s 超时", typ)
	return queueEventMsg{}
}

func TestExecutionRequiresLatestPreview(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	press(&m, "x")
	assert.Empty(t, m.queue.Snapshot(), "按 x 不入队")
	press(&m, "enter")
	assert.Empty(t, m.queue.Snapshot(), "回车不入队")
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	assert.Empty(t, m.queue.Snapshot(), "打开创建弹窗还不入队")
	batch, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)
	require.Len(t, batch.Items, 1)
	absIn, err := filepath.Abs("a.mkv")
	require.NoError(t, err)
	assert.Equal(t, absIn, batch.Items[0].Input, "入队时输入改为绝对路径")
}

// TestRosterShowsBatchesAndItemStates 验证任务面板的队列视图：
// 主任务行显示状态与进度概要，展开后可见每个子任务的状态；光标所在子任务可查看完整命令。
func TestRosterShowsBatchesAndItemStates(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a", "b"), command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, m.queue.Start(batch.ID))
	batchID := batch.ID
	_ = batchID
	waitBatchEvent(t, &m, task.QueueItemStarted)
	waitBatchEvent(t, &m, task.QueueBatchPaused) // 占位文件无法转码：首个子任务失败后暂停
	require.True(t, m.running)
	require.True(t, m.queueViewActive(), "入队后应显示队列视图")

	// 默认展开暂停中的批次，失败的子任务再展开次数。
	require.GreaterOrEqual(t, len(m.roster), 4, "批次 + 失败子任务 + 第 1 次 + 未开始子任务")
	assert.Empty(t, m.roster[0].itemID, "第一行是批次行")
	assert.Equal(t, filePaused, m.roster[0].state, "暂停中的批次应显示为暂停")
	assert.Equal(t, fileFailed, m.roster[1].state, "失败的子任务应可见")
	assert.Equal(t, 1, m.roster[2].attempt, "失败子任务下应挂出第 1 次")
	assert.Equal(t, m.roster[1].itemID, m.roster[2].itemID)
	var pending bool
	for _, row := range m.roster {
		if row.attempt == 0 && row.itemID != "" && row.state == filePending {
			pending = true
		}
	}
	assert.True(t, pending, "未开始的子任务应可见")
	assert.True(t, strings.HasPrefix(m.roster[1].cmd, "ffmpeg"), "子任务行携带完整命令")

	// 光标停在失败子任务上：置顶显示该子任务的完整命令（与正在执行与否无关）
	idx := -1
	for i, row := range m.roster {
		if row.state == fileFailed {
			idx = i
			break
		}
	}
	require.GreaterOrEqual(t, idx, 0)
	m.fileCursor = idx
	m.pinRosterCursor()
	assert.Equal(t, m.roster[idx].cmd, m.pinnedCmdText(), "光标子任务的命令置顶显示")

	// 批次行：pinned 无命令，y 复制该批次全部命令
	batchIdx := 0
	for i, row := range m.roster {
		if row.itemID == "" {
			batchIdx = i
			break
		}
	}
	m.fileCursor = batchIdx
	m.pinRosterCursor()
	assert.Empty(t, m.pinnedCmdText(), "批次行不显示单条命令")
	cmds := m.batchCommands(batch.ID)
	require.NotEmpty(t, cmds)
	cmd := m.copyShownCommand()
	require.NotNil(t, cmd)
	assert.Contains(t, fmt.Sprint(cmd()), cmds[0])
}

// TestPausedTimerFreezesElapsed 验证暂停等待决策期间计时冻结，恢复后扣除暂停时长。
func TestPausedTimerFreezesElapsed(t *testing.T) {
	m := newModel()
	m.running = true
	m.startAt = time.Now().Add(-time.Minute)
	m.pausedBatch = "b"
	m.pauseTimer()
	paused := m.elapsedNow()
	assert.InDelta(t, float64(time.Minute), float64(paused), float64(2*time.Second),
		"暂停后耗时应冻结在暂停时刻")
	e1 := m.elapsedNow()
	time.Sleep(20 * time.Millisecond)
	e2 := m.elapsedNow()
	assert.Less(t, e2-e1, time.Duration(5*time.Millisecond), "冻结期间不再显著增长")

	m.resumeTimer()
	assert.InDelta(t, float64(time.Minute), float64(m.elapsedNow()), float64(2*time.Second),
		"恢复后耗时不含暂停时段")
}

func TestFinishedElapsedStaysPut(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	m.total, m.done = 1, 1
	m.startAt = time.Now().Add(-3 * time.Second)
	m.elapsed = 3 * time.Second
	m.running = false
	first := m.progressBlock()
	time.Sleep(1100 * time.Millisecond)
	second := m.progressBlock()
	assert.Equal(t, first, second, "任务结束后耗时应停在结束时的值")
	assert.Contains(t, first, "3s")
}

func TestRetryAddsAttemptRow(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 36
	m.resizePanes()
	m.focus = panelRun
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, m.queue.Start(batch.ID))
	waitBatchEvent(t, &m, task.QueueBatchPaused)
	require.Equal(t, 1, attemptRows(&m))
	frozen := m.progressHint()
	time.Sleep(1100 * time.Millisecond)
	assert.Equal(t, frozen, m.progressHint(), "暂停等待决策时耗时要停住")

	// 停在失败的这一次上再按 r，和手动查看后重试一样。
	for i, row := range m.roster {
		if row.attempt > 0 {
			m.fileCursor = i
			m.pinRosterCursor()
			m.fileFollow = false
		}
	}
	press(&m, "r")
	waitBatchEvent(t, &m, task.QueueItemStarted)
	assert.Equal(t, 2, attemptRows(&m), "重试后应出现第 2 次")
	assert.Contains(t, m.subtaskStatusLine(), "第 2 次")
}

func attemptRows(m *model) int {
	n := 0
	for _, row := range m.roster {
		if row.attempt > 0 {
			n++
		}
	}
	return n
}

// TestQueueEventsFlowWhenIdle 验证队列事件监听链常驻：
// 程序启动即由 Init 挂上监听，空闲时手动开始也能把执行事件送进界面。
// 此前的缺陷是监听只在 eventing=true 时重挂，空闲时第一条事件后即断链，开始后永远不执行。
func TestQueueEventsFlowWhenIdle(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, m.queue.Start(batch.ID))
	batchID := batch.ID
	_ = batchID

	cur := m
	cmd := cur.Init()
	deadline := time.Now().Add(8 * time.Second)
	sawStarted := false
	for time.Now().Before(deadline) {
		if cmd == nil {
			t.Fatal("监听链提前断开：Update 未重挂监听，空闲时队列事件无法送达界面")
		}
		msg := cmd()
		require.NotNil(t, msg)
		next, c := cur.Update(msg)
		cur, cmd = next.(model), c
		if e, ok := msg.(queueEventMsg); ok && e.evt.Type == task.QueueBatchStarted {
			sawStarted = true
		}
		if sawStarted {
			cur.queue.Stop()
			assert.True(t, cur.running, "收到 BatchStarted 后界面应处于执行态")
			return
		}
	}
	t.Fatal("等待 QueueBatchStarted 超时：入队后事件未送达")
}

func TestOldRunMessagesAreIgnored(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.running, m.eventing, m.runID = true, true, 2
	for _, msg := range []tea.Msg{
		eventMsg{runID: 1, event: ffmpeg.Event{Type: ffmpeg.EventCommandDone}},
		doneMsg{runID: 1}, tickMsg{runID: 1}, logRefreshMsg{runID: 1},
	} {
		next, cmd := m.Update(msg)
		m = next.(model)
		assert.Nil(t, cmd)
		assert.True(t, m.running)
		assert.Zero(t, m.done)
	}
}

func TestBatchMessagesDrainBeforeCompletion(t *testing.T) {
	isolatePresets(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan := testPlan("a", "b", "c")
	for i := range plan.Jobs {
		plan.Jobs[i].Output = filepath.Join(t.TempDir(), "输出")
		plan.Jobs[i].Args = []string{"-version"}
	}
	// 第一条失败触发暂停，决策为中止：后续子任务取消，批次失败收尾。
	plan.Jobs[0].Args = []string{"-batcher-invalid"}
	m := newModel()
	m.ctx = ctx
	m.startAt = time.Now()
	m.files = buildFileItems(plan.Jobs)
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(plan, queueSelection())
	require.NoError(t, err)
	require.NoError(t, m.queue.Start(batch.ID))
	// 暂停后立刻决策中止，驱动批次快速收尾；捕获队列指针，避免与 Update 的值拷贝竞态。
	q := m.queue
	go func() {
		for {
			s, ok := q.BatchStatus(batch.ID)
			if !ok || s.Terminal() {
				return
			}
			if s == task.StatusPaused {
				q.Decide(task.DecisionAbort)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	deadline := time.Now().Add(9 * time.Second)
	for {
		select {
		case msg := <-m.evCh:
			next, _ := m.Update(msg)
			m = next.(model)
			e, ok := msg.(queueEventMsg)
			if !ok || e.evt.Type != task.QueueBatchFailed {
				if time.Now().After(deadline) {
					t.Fatal("等待批次结束事件超时")
				}
				continue
			}
			// 中止后：第一条失败，其余子任务取消，批次失败收尾。
			assert.Equal(t, 0, m.done)
			assert.Equal(t, 1, m.failed)
			assert.Equal(t, 2, m.canceled)
			assert.NotEmpty(t, m.logs)
			assert.False(t, m.running)
			assert.Equal(t, fileFailed, m.files[0].state)
			assert.Equal(t, fileCanceled, m.files[1].state)
			assert.Equal(t, fileCanceled, m.files[2].state)
			return
		case <-ctx.Done():
			t.Fatal("等待批处理事件超时")
		}
	}
}

func TestCancelledQueueDropsEventsFast(t *testing.T) {
	isolatePresets(t)
	ctx, cancel := context.WithCancel(context.Background())
	plan := testPlan("a")
	plan.Jobs[0].Output = filepath.Join(t.TempDir(), "输出")
	plan.Jobs[0].Args = []string{"-version"}
	m := newModel()
	m.ctx = ctx
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(plan, queueSelection())
	require.NoError(t, err)
	require.NoError(t, m.queue.Start(batch.ID))
	waitBatchEvent(t, &m, task.QueueItemStarted)
	cancel()
	stopped := make(chan struct{})
	go func() { m.queue.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("退出后事件背压不应阻塞队列回收")
	}
}

func TestCanceledRunMarksPendingTasks(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.running, m.runID, m.done, m.canceled = true, 1, 1, 1
	m.startAt = time.Now()
	m.files = []fileItem{{state: fileDone}, {state: fileCanceled}, {state: filePending}}
	next, _ := m.Update(doneMsg{runID: 1, err: context.Canceled})
	m = next.(model)
	assert.Equal(t, 2, m.canceled)
	assert.Equal(t, fileCanceled, m.files[2].state)
	assert.Zero(t, m.failed)
}

func TestProgressAndCoalescedLogRefresh(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	m.running, m.total, m.done = true, 4, 2
	m.files = []fileItem{{}}
	m.startAt = time.Now()
	assert.Contains(t, m.runBody(), "50%", "进度无需依赖动画帧即可更新")
	cmd := m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventLog, Message: "第一行"})
	require.NotNil(t, cmd)
	for range 100 {
		assert.Nil(t, m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventLog, Message: "更多日志"}))
	}
	assert.True(t, m.logDirty)
	next, _ := m.Update(logRefreshMsg{runID: m.runID})
	m = next.(model)
	assert.False(t, m.logDirty)
	assert.False(t, m.logRefreshScheduled)
	assert.Contains(t, m.logVp.View(), "更多日志")
}

func TestOnlyOneDefaultSubtitle(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	m.modal.focus = 1
	press(&m, "space")
	press(&m, "ctrl+n")
	defaults := 0
	for i := range m.modal.fields {
		if m.modal.fields[i].def.key == "sub_default" {
			m.modal.fields[i].checked = true
			defaults++
		}
	}
	require.Equal(t, 2, defaults)
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal)
	assert.Contains(t, m.statusMsg, "默认字幕", "校验失败用右上角 toast 提示")

	seen := false
	for i := range m.modal.fields {
		if m.modal.fields[i].def.key == "sub_default" && !seen {
			m.modal.fields[i].checked = false
			seen = true
		}
	}
	press(&m, "ctrl+s")
	assert.NotContains(t, m.statusMsg, "默认字幕")
	assert.True(t, m.creating, "只保留一条默认字幕时应开始创建")
}

func TestHelpShowsInjectedBuildInformation(t *testing.T) {
	previousVersion, previousDate, previousCommit := version, buildDate, gitCommit
	t.Cleanup(func() { version, buildDate, gitCommit = previousVersion, previousDate, previousCommit })
	version, buildDate, gitCommit = "test-version", "test-date", "test-commit"
	text := helpText()
	assert.Contains(t, text, "构建信息")
	assert.Contains(t, text, version)
	assert.Contains(t, text, buildDate)
	assert.Contains(t, text, gitCommit)
}

func TestNarrowRunLayoutKeepsLatestLogVisible(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height, m.focus = 70, 24, panelRun
	plan := testPlan("a.mkv", "b.mkv", "c.mkv")
	m.files = buildFileItems(plan.Jobs)
	m.running, m.total, m.current, m.logFollow = true, 3, m.files[0].cmd, true
	m.startAt = time.Now()
	m.resizePanes()
	for range 30 {
		m.appendLog("历史日志", logLineDefaultStyle)
	}
	m.appendLog("末尾日志必须可见", logLineDefaultStyle)
	m.syncLogPane()
	assert.Equal(t, m.width-2, m.logVp.Width())
	out := m.render()
	assert.Contains(t, out, "末尾日志必须可见")
	assert.NotContains(t, out, "行已折叠")
	assert.Equal(t, m.height, lipgloss.Height(out))
	for _, line := range strings.Split(out, "\n") {
		assert.Equal(t, m.width, lipgloss.Width(line))
	}
}

func TestDrawBoxZeroContentKeepsBorders(t *testing.T) {
	box := drawBox(20, "折叠", false, true, "不应显示", 0)
	assert.Equal(t, 2, lipgloss.Height(box), "零行内容只保留上下边框")
}

func TestRunningFooterShowsTaskKeys(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 140, 30
	m.running = true
	m.roster = []rosterRow{{batchID: "b", title: "任务"}}
	text := m.footer()
	assert.Contains(t, text, "重试")
	assert.Contains(t, text, "删除")
	assert.Contains(t, text, "取消")
}

func TestRetryDoesNotAdvanceSettledProgress(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.files = []fileItem{{}, {}, {}}
	m.total = 3
	m.running = true

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandFailed, Index: 0, Total: 3, Message: "失败"})
	settled, failed, _, _, total, _ := m.progressNumbers()
	assert.Equal(t, 0, settled, "失败还没结束，不应算进完成进度")
	assert.Equal(t, 1, failed)
	assert.Equal(t, 3, total)

	m.recordLine(0, "重试", fileRunning, logLineInfoStyle)
	settled, failed, _, _, _, _ = m.progressNumbers()
	assert.Equal(t, 0, settled, "重试不应增加进度")
	assert.Equal(t, 0, failed)

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandFailed, Index: 0, Total: 3, Message: "再次失败"})
	settled, failed, _, _, _, _ = m.progressNumbers()
	assert.Equal(t, 0, settled, "同一子任务再次失败不应把进度往前推")
	assert.Equal(t, 1, failed)

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandDone, Index: 0, Total: 3})
	settled, failed, _, _, _, _ = m.progressNumbers()
	assert.Equal(t, 1, settled)
	assert.Equal(t, 0, failed)
}

func TestBatchRowHidesSubtaskLogs(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	m.roster = []rosterRow{{batchID: "b", title: "主任务"}}
	m.fileCursor = 0
	m.logs = []string{"ffmpeg-detail-UNIQUE"}
	m.files = []fileItem{{logs: []string{"child-output-UNIQUE"}}}
	m.syncLogPane()
	view := m.logVp.View()
	assert.NotContains(t, view, "ffmpeg-detail-UNIQUE")
	assert.NotContains(t, view, "child-output-UNIQUE")
	assert.Contains(t, view, "暂无子任务")
}

func TestSubtaskProgressRendersSeparatelyFromBatch(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 36
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.total = 2
	m.activeBatch = "b"
	m.filesBatch = "b"
	m.startAt = time.Now()
	m.files = []fileItem{
		{input: "a.mkv", state: fileRunning, cmd: "ffmpeg -i a.mkv o.mkv", attempts: []attemptLog{{n: 1, status: fileRunning}}},
		{input: "b.mkv"},
	}
	m.runIdx = 0
	m.roster = []rosterRow{
		{batchID: "b", title: "主任务", state: fileRunning},
		{batchID: "b", itemID: "001", title: "a.mkv", state: fileRunning, cmd: "ffmpeg -i a.mkv o.mkv"},
		{batchID: "b", itemID: "001", attempt: 1, title: "第 1 次", state: fileRunning, cmd: "ffmpeg -i a.mkv o.mkv"},
		{batchID: "b", itemID: "002", title: "b.mkv", state: filePending},
	}
	m.fileCursor = 2

	cmd := m.handleQueueEvent(task.QueueEvent{
		Type: task.QueueFFmpeg, BatchID: "b", ItemID: "001",
		FFmpegEvt: &ffmpeg.Event{
			Type:  ffmpeg.EventProgress,
			Index: 9,
			Progress: &ffmpeg.Progress{
				OutTime:  30 * time.Second,
				Duration: 2 * time.Minute,
				Speed:    1.5,
				FPS:      24,
			},
		},
	})
	assert.Nil(t, cmd, "进度不触发日志刷新")
	assert.False(t, m.logDirty)
	assert.Empty(t, m.files[0].logs, "进度不能写进子任务日志")
	assert.True(t, m.files[0].hasProgress)
	assert.False(t, m.files[1].hasProgress, "应按子任务 ID 写入，而不是事件里的下标")

	body := m.runBody()
	assert.Contains(t, body, "25%")
	assert.Contains(t, body, "00:30 / 02:00")
	assert.Contains(t, body, "1.5x")
	assert.Contains(t, body, "24 fps")
	assert.Contains(t, m.filePane(8), "25%", "左侧正在执行的子任务要标出自己的进度")
	assert.Equal(t, 2, m.runBarH())

	m.fileCursor = 0
	batch := m.runBody()
	assert.Contains(t, batch, "进度: 0/2", "主任务仍显示按个数计算的总进度")
	assert.NotContains(t, batch, "1.5x", "总进度条不混入当前子任务的速度")

	m.fileCursor = 2
	m.files[0].progress = ffmpeg.Progress{OutTime: 12 * time.Second, Speed: 2}
	unknown := m.runBody()
	assert.Contains(t, unknown, "已处理 00:12")
	assert.Contains(t, unknown, "2x")
	assert.NotContains(t, unknown, "%", "没有总时长时不显示百分比")
	assert.Contains(t, m.filePane(8), "00:12")

	m.files[0].state = fileDone
	assert.NotContains(t, m.runBody(), "2x", "子任务结束后不再显示这一次的实时进度")

	m.files[0].state = fileRunning
	m.files[0].hasProgress = true
	m.files[0].progress = ffmpeg.Progress{OutTime: time.Second, Duration: 4 * time.Second, Speed: 1.25}
	m.fileCursor = 1
	assert.Contains(t, m.runBody(), "25%")
	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandStart, Index: 0, Total: 2, Command: "ffmpeg -i a.mkv o.mkv"})
	assert.False(t, m.files[0].hasProgress, "下一次开始要清掉上一次的进度")
}

func TestFFmpegLogUsesItemID(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.running = true
	m.activeBatch = "b"
	m.filesBatch = "b"
	m.total = 2
	m.files = []fileItem{{input: "a.mkv"}, {input: "b.mkv"}}
	evt := ffmpeg.Event{Type: ffmpeg.EventLog, Index: 0, Message: "second-output"}
	m.handleQueueEvent(task.QueueEvent{
		Type: task.QueueFFmpeg, BatchID: "b", ItemID: "002", FFmpegEvt: &evt,
	})
	assert.NotContains(t, strings.Join(m.files[0].logs, "\n"), "second-output")
	assert.Contains(t, strings.Join(m.files[1].logs, "\n"), "second-output")
}

func TestEnterSubtaskScrollsOutput(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	m.focus = panelRun
	m.filesBatch = "b"
	m.roster = []rosterRow{{batchID: "b", itemID: "001", attempt: 1, title: "第 1 次"}}
	logs := make([]string, 0, 20)
	for i := range 20 {
		logs = append(logs, fmt.Sprintf("输出行 %02d", i+1))
	}
	m.files = []fileItem{{input: "a.mkv", attempts: []attemptLog{{n: 1, status: fileRunning, lines: logs}}}}
	m.logVp.SetHeight(4)

	press(&m, "enter")
	assert.True(t, m.outputFocus, "子任务回车应进入输出")
	assert.Equal(t, 0, m.fileCursor)
	before := m.logVp.YOffset()
	require.Positive(t, before, "输出应能向下滚出视口")

	press(&m, "up")
	assert.Less(t, m.logVp.YOffset(), before, "上下键应滚动输出")
	assert.Equal(t, 0, m.fileCursor, "查看输出时不应改动左侧光标")

	press(&m, "q")
	assert.True(t, m.outputFocus, "输出里按 q 不返回上一级")

	press(&m, "esc")
	assert.False(t, m.outputFocus)
}

func TestBatchBodyHasNoOutputPane(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 34
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.total = 2
	m.roster = []rosterRow{{batchID: "b", title: "主任务"}}
	m.fileCursor = 0
	m.refreshRunHeights()
	m.syncLogPane()

	body := m.runBody()
	assert.Contains(t, body, "子任务")
	assert.NotContains(t, body, "固定 · 当前任务完整命令")
	assert.NotContains(t, body, "── 输出 ──")
	assert.NotContains(t, body, "ffmpeg-detail-UNIQUE")
}

func TestRetryAttemptsAreNumberedAndFoldable(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 34
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.total = 1
	m.filesBatch = "b"
	m.files = []fileItem{{input: "a.mkv", cmd: "ffmpeg -i a.mkv o.mkv"}}
	m.roster = []rosterRow{{batchID: "b", itemID: "001", title: "a.mkv", cmd: "ffmpeg -i a.mkv o.mkv"}}
	m.fileCursor = 0

	start := func(msg string) {
		m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandStart, Index: 0, Total: 1, Command: "ffmpeg -i a.mkv o.mkv"})
		m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventLog, Index: 0, Message: msg})
		m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandFailed, Index: 0, Total: 1, Message: "失败"})
	}
	start("first-output-UNIQUE")
	start("second-output-UNIQUE")

	require.Len(t, m.files[0].attempts, 2)
	assert.Equal(t, 1, m.files[0].attempts[0].n)
	assert.Equal(t, 2, m.files[0].attempts[1].n)
	assert.NotContains(t, strings.Join(m.files[0].logs, "\n"), "[1/1] 重试")

	m.syncLogPane()
	parent := m.subtaskBody()
	assert.Contains(t, parent, "次数")
	assert.Contains(t, parent, "第 1 次")
	assert.Contains(t, parent, "第 2 次")
	assert.Contains(t, parent, "first-output-UNIQUE", "失败次数里应直接看到该次输出")
	assert.Contains(t, parent, "second-output-UNIQUE")
	assert.NotContains(t, parent, "── 输出 ──")

	m.roster = []rosterRow{
		{batchID: "b", itemID: "001", title: "a.mkv", cmd: "ffmpeg -i a.mkv o.mkv"},
		{batchID: "b", itemID: "001", attempt: 1, title: "第 1 次", state: fileFailed, cmd: "ffmpeg -i a.mkv o.mkv"},
		{batchID: "b", itemID: "001", attempt: 2, title: "第 2 次", state: fileFailed, cmd: "ffmpeg -i a.mkv o.mkv"},
	}
	m.fileCursor = 1
	m.logFollow = false
	m.syncLogPane()
	assert.Contains(t, m.logVp.View(), "first-output-UNIQUE")
	assert.NotContains(t, m.logVp.View(), "second-output-UNIQUE")

	m.fileCursor = 2
	m.syncLogPane()
	assert.Contains(t, m.logVp.View(), "second-output-UNIQUE")
	assert.NotContains(t, m.logVp.View(), "first-output-UNIQUE")
}

func TestTaskPanelXDoesNotEnqueue(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	m.focus = panelRun
	m.roster = []rosterRow{{batchID: "existing", title: "已有任务"}}

	press(&m, "x")
	assert.Empty(t, m.queue.Snapshot(), "任务列表里按 x 不应新建主任务")
	assert.Nil(t, m.modal)

	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	assert.Empty(t, m.queue.Snapshot(), "打开创建弹窗还不入队")
	press(&m, "esc")
	assert.Nil(t, m.modal)
	assert.Empty(t, m.queue.Snapshot(), "取消创建不入队")
}

func TestRunBodySplitsFixedAndOutput(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 34
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.total = 2
	m.current = "ffmpeg -i a.mkv -c copy out/a.mkv"
	m.runIdx = 0
	m.fileCursor = 1
	m.files = []fileItem{{
		input: "a.mkv",
		cmd:   m.current,
		state: fileRunning,
		logs:  []string{"ffmpeg-scroll-UNIQUE"},
	}}
	m.refreshRunHeights()
	m.syncLogPane()

	body := m.runBody()
	cmdAt := strings.Index(body, "固定 · 当前任务完整命令")
	splitAt := strings.Index(body, "输出")
	logAt := strings.Index(body, "ffmpeg-scroll-UNIQUE")
	require.Greater(t, cmdAt, -1)
	require.Greater(t, splitAt, cmdAt, "分隔线应在固定内容之后")
	require.Greater(t, logAt, splitAt, "输出应在分隔线之后")
	assert.Contains(t, body, "状态：执行中")
	assert.NotContains(t, m.logVp.View(), "固定 · 当前任务完整命令", "固定内容不应放进可滚动输出")
	assert.Contains(t, m.logVp.View(), "ffmpeg-scroll-UNIQUE")

	out := m.render()
	for i, ln := range strings.Split(out, "\n") {
		assert.Equal(t, 120, lipgloss.Width(ln), "第 %d 行宽度应为 120", i+1)
	}
}
