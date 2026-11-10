package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		for _, item := range []string{"最后一个内置项", "手动输入"} {
			for _, key := range []string{"e", "d", "J", "K"} {
				t.Run(fmt.Sprintf("%d/%s/%s", count, item, key), func(t *testing.T) {
					isolatePresets(t)
					m := newModel()
					m.ops.Convert, m.focus = true, panelPresets
					if count > 0 {
						m.presets = []presets.Preset{{Args: "a"}, {Args: "b"}}
					}
					m.presetCursor = m.presetUserOffset() - 1
					if item == "手动输入" {
						m.presetCursor = m.presetCount() - 1
					}
					before := append([]presets.Preset(nil), m.presets...)
					require.NotPanics(t, func() { press(&m, key) })
					assert.Equal(t, before, m.presets)
					assert.Nil(t, m.modal)
				})
			}
		}
	}
}

func TestPresetPersistenceFailureDoesNotCommit(t *testing.T) {
	for _, action := range []string{"新增", "编辑", "删除", "排序", "另存"} {
		t.Run(action, func(t *testing.T) {
			isolatePresets(t)
			m := newModel()
			m.ops.Convert = true
			m.rebuildFields()
			m.presets = []presets.Preset{{Args: "-c copy"}, {Args: "-c:v libx264"}}
			before := append([]presets.Preset(nil), m.presets...)
			m.presetCursor = m.presetUserOffset()
			m.manualAdvance = "-c copy"
			m.applyAdvanceToForm()
			blocked := filepath.Join(t.TempDir(), "不是目录")
			require.NoError(t, os.WriteFile(blocked, []byte("保留"), 0o644))
			t.Setenv("BATCHER_PRESETS_DIR", blocked)
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
			case "另存":
				field, ok := findField(&m.fields, "advance")
				require.True(t, ok)
				field.input.SetValue("-c:v mpeg4")
				m.saveAdvancePreset()
			}
			assert.Equal(t, before, m.presets)
			assert.Equal(t, "-c copy", m.manualAdvance)
			assert.Contains(t, m.statusMsg, "失败")
		})
	}
}

func TestPasteReachesOnlyActiveInput(t *testing.T) {
	for _, target := range []string{"主表单", "预设弹窗", "字幕弹窗"} {
		t.Run(target, func(t *testing.T) {
			isolatePresets(t)
			m := newModel()
			m.ops.Convert, m.focus = true, panelForm
			m.rebuildFields()
			switch target {
			case "主表单":
				m.beginFormEdit()
				m.fields[0].input.SetValue("")
			case "预设弹窗":
				m.openPresetModal(-1, presets.Preset{})
			case "字幕弹窗":
				m.openSubModal(-1)
			}
			text := "1xqjk 中文 路径"
			next, _ := m.Update(tea.PasteMsg{Content: text})
			m = next.(model)
			assert.False(t, m.running)
			assert.Equal(t, panelForm, m.focus)
			if m.modal != nil {
				assert.Equal(t, text, m.modal.fields[0].input.Value())
				assert.False(t, m.planLoading, "弹窗草稿不影响主配置")
			} else {
				assert.Equal(t, text, m.fields[0].input.Value())
				assert.True(t, m.planLoading)
			}
		})
	}
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
		case <-time.After(deadline.Sub(time.Now()) / 2):
			t.Fatalf("等待队列事件 %s 超时", typ)
		}
	}
	t.Fatalf("等待队列事件 %s 超时", typ)
	return queueEventMsg{}
}

func TestExecutionRequiresLatestPreview(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops.Convert = true
	m.rebuildFields()
	testQueue(t, &m)
	m.plan, m.previewDone, m.planLoading = testPlan("a.mkv"), true, true
	press(&m, "x")
	assert.Empty(t, m.queue.Snapshot(), "预览未就绪不能入队")
	m.planLoading = false
	next, _ := m.Update(planMsg{seq: m.planSeq, plan: testPlanOut(t, "a.mkv")})
	m = next.(model)
	press(&m, "x")
	batches := m.queue.Snapshot()
	require.Len(t, batches, 1, "最新预览生成后 x 入队")
	seq := m.planSeq
	next, _ = m.Update(planMsg{seq: seq, plan: testPlanOut(t, "不应替换.mkv")})
	m = next.(model)
	assert.Equal(t, "a.mkv", batches[0].Items[0].Input, "入队复制的快照不受后续计划影响")
}

// TestRosterShowsBatchesAndItemStates 验证任务面板的队列视图：
// 主任务行显示状态与进度概要，展开后可见每个子任务的状态；光标所在子任务可查看完整命令。
func TestRosterShowsBatchesAndItemStates(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{Convert: true}
	m.rebuildFields()
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a", "b"), command.Selection{Convert: true})
	require.NoError(t, err)
	batchID := batch.ID
	_ = batchID
	waitBatchEvent(t, &m, task.QueueItemStarted)
	waitBatchEvent(t, &m, task.QueueBatchPaused) // 占位文件无法转码：首个子任务失败后暂停
	require.True(t, m.running)
	require.True(t, m.queueViewActive(), "入队后应显示队列视图")

	// 默认展开运行中的批次：1 批次行 + 2 子任务行
	require.Len(t, m.roster, 3, "1 批次行 + 2 子任务行")
	assert.Empty(t, m.roster[0].itemID, "第一行是批次行")
	assert.Equal(t, fileRunning, m.roster[0].state, "暂停中的批次按运行态展示")
	assert.Equal(t, fileFailed, m.roster[1].state, "失败的子任务应可见")
	assert.Equal(t, filePending, m.roster[2].state, "未开始的子任务应可见")
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

	title := m.panelTitle(panelRun)
	assert.Contains(t, title, "任务（1 批次）")
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

// TestQueueEventsFlowWhenIdle 验证队列事件监听链常驻：
// 程序启动即由 Init 挂上监听，空闲（未在执行）时入队也能立刻进入执行。
// 此前的缺陷是监听只在 eventing=true 时重挂，空闲时第一条事件后即断链，入队永远不执行。
func TestQueueEventsFlowWhenIdle(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{Convert: true}
	m.rebuildFields()
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)
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

func TestStalesPlanMessagesDuringRun(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops.Convert = true
	m.rebuildFields()
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), queueSelection())
	require.NoError(t, err)
	waitBatchEvent(t, &m, task.QueueBatchStarted)
	require.True(t, m.running)
	require.Len(t, m.files, 1)
	m.statusMsg = ""

	// 执行中生成的新计划先暂存：批次结束后再应用，避免与执行中文件列表下标冲突。
	next, _ := m.Update(planMsg{seq: m.planSeq, plan: testPlan("新配置.mkv")})
	m = next.(model)
	require.NotNil(t, m.pendingPlan, "运行期间的新计划应暂存")
	assert.Equal(t, "新配置.mkv", m.pendingPlan.Jobs[0].Input)
	assert.Contains(t, m.statusMsg, "配置已更新")
	_ = batch
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
	m.plan, m.previewDone, m.startAt = plan, true, time.Now()
	m.files = buildFileItems(plan.Jobs)
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(plan, queueSelection())
	require.NoError(t, err)
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
	_, err := m.queue.Enqueue(plan, queueSelection())
	require.NoError(t, err)
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

func TestHiddenAndEmptyFieldValuesSurviveRebuild(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{Convert: true, AddFonts: true}
	m.rebuildFields()
	values := map[string]string{"advance": "-c:v mpeg4", "fonts_path": "字体 路径", "input_path": ""}
	for key, value := range values {
		field, ok := findField(&m.fields, key)
		require.True(t, ok)
		field.input.SetValue(value)
	}
	m.ops = command.Selection{}
	m.rebuildFields()
	m.ops = command.Selection{Convert: true, AddFonts: true}
	m.rebuildFields()
	for key, value := range values {
		field, ok := findField(&m.fields, key)
		require.True(t, ok)
		assert.Equal(t, value, field.input.Value())
	}
}

func TestOnlyOneDefaultSubtitle(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops.AddSubtitle, m.focus = true, panelSubs
	m.subs = []command.SubtitleOption{{Suffix: "chi.ass", Default: true}, {Suffix: "eng.ass"}}
	m.subCursor = 1
	press(&m, "t")
	assert.False(t, m.subs[0].Default)
	assert.True(t, m.subs[1].Default)
	m.openSubModal(0)
	m.modal.fields[1].input.SetValue("chi")
	m.modal.fields[3].checked = true
	m.saveSubModal()
	assert.True(t, m.subs[0].Default)
	assert.False(t, m.subs[1].Default)
}

func TestCopyIncludesOnlyCommandsAndResultsKeepLogs(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.plan, m.previewDone = testPlan("a.mkv", "b.mkv"), true
	m.files = buildFileItems(m.plan.Jobs)
	m.previewSummary = "预览说明，不得复制"
	m.files[0].logs = []string{"执行输出，不得复制"}
	m.logs = []string{"汇总日志，不得复制"}
	m.fileCursor = 1
	cmd := m.copyShownCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, m.files[0].cmd, fmt.Sprint(cmd()))
	assert.Contains(t, m.previewContent(), "执行输出，不得复制")
	m.fileCursor = 0
	cmd = m.copyShownCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, m.files[0].cmd+"\n"+m.files[1].cmd, fmt.Sprint(cmd()))
	assert.Contains(t, m.previewContent(), "汇总日志，不得复制")
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
	m.plan = testPlan("a.mkv", "b.mkv", "c.mkv")
	m.files = buildFileItems(m.plan.Jobs)
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

func TestShortSidebarAllocatesOnlyAvailableRows(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	for _, height := range []int{13, 16, 20} {
		m.height = height
		for focus := panel(0); focus < panelCount; focus++ {
			m.focus = focus
			sum := 0
			for _, rows := range m.panelHeights() {
				assert.GreaterOrEqual(t, rows, 0)
				sum += rows
			}
			assert.Equal(t, m.bodyH()-int(panelCount)*2, sum)
		}
	}
	box := drawBox(20, "折叠", false, true, "不应显示", 0)
	assert.Equal(t, 2, lipgloss.Height(box), "零行内容只保留上下边框")
}

func TestInvalidOrEmptyPreviewNeverExecutes(t *testing.T) {
	for _, invalid := range []bool{true, false} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			isolatePresets(t)
			m := newModel()
			m.ops.Convert, m.previewDone = true, true
			m.rebuildFields()
			m.plan = testPlan()
			if invalid {
				m.planErr = fmt.Errorf("参数无效")
			}
			press(&m, "x")
			assert.False(t, m.running)
			assert.Equal(t, panelRun, m.focus)
			assert.NotEmpty(t, m.statusMsg)
		})
	}
}

func TestReschedulingCancelsPreviousPlan(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.planCancel, m.plan = cancel, testPlan("旧计划")
	cmd := m.schedulePlan(0)
	require.NotNil(t, cmd)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.Nil(t, m.planCancel)
	assert.Nil(t, m.plan)
	assert.True(t, m.planLoading)
}

func TestPlanCommandReturnsReadOnlySnapshot(t *testing.T) {
	isolatePresets(t)
	opt := command.DefaultOption()
	opt.InputPath = t.TempDir()
	opt.OutputPath = filepath.Join(t.TempDir(), "尚未创建")
	input := filepath.Join(opt.InputPath, "a.mp4")
	require.NoError(t, os.WriteFile(input, nil, 0o644))
	msg := planCmd(context.Background(), &opt, command.Selection{Convert: true}, 7)().(planMsg)
	require.NoError(t, msg.err)
	assert.Equal(t, 7, msg.seq)
	require.NotNil(t, msg.plan)
	require.Len(t, msg.plan.Jobs, 1)
	assert.Equal(t, input, msg.plan.Jobs[0].Input)
	assert.NoDirExists(t, opt.OutputPath)
}
