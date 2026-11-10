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
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
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

func TestExecutionRequiresLatestPreviewAndLocksPlanning(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops.Convert = true
	m.rebuildFields()
	m.plan, m.previewDone, m.planLoading = testPlan("a.mkv"), true, true
	press(&m, "x")
	assert.False(t, m.running, "过期预览不能执行")
	m.planLoading = false
	next, _ := m.Update(planMsg{seq: m.planSeq, plan: testPlan("a.mkv")})
	m = next.(model)
	press(&m, "x")
	t.Cleanup(m.quitCleanup)
	require.True(t, m.running)
	assert.Equal(t, 1, m.total)
	assert.Equal(t, "a.mkv", m.files[0].input)
	seq := m.planSeq
	next, cmd := m.Update(planDebounceMsg{seq: seq})
	m = next.(model)
	assert.Nil(t, cmd)
	next, _ = m.Update(planMsg{seq: seq, plan: testPlan("不应替换.mkv")})
	m = next.(model)
	assert.Equal(t, "a.mkv", m.files[0].input)
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
	plan.Jobs[1].Args = []string{"-batcher-invalid"}
	m := newModel()
	m.ctx, m.runID, m.running, m.eventing = ctx, 7, true, true
	m.plan, m.previewDone, m.startAt = plan, true, time.Now()
	m.files = buildFileItems(plan.Jobs)
	ch := make(chan tea.Msg, 1)
	started, finished := make(chan struct{}), make(chan struct{})
	go startBatchCmd(ctx, ctx, plan, m.runID, ch, started, finished)()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("等待批处理事件超时")
		case msg := <-ch:
			require.NotNil(t, msg)
			_, ending := msg.(doneMsg)
			if ending {
				assert.Equal(t, 2, m.done, "结束结果前必须已经消费完成事件")
				assert.Equal(t, 1, m.failed)
				assert.NotEmpty(t, m.logs)
			}
			next, _ := m.Update(msg)
			m = next.(model)
			if ending {
				assert.False(t, m.running)
				assert.Equal(t, fileDone, m.files[0].state)
				assert.Equal(t, fileFailed, m.files[1].state)
				assert.Equal(t, fileDone, m.files[2].state)
				<-finished
				return
			}
		}
	}
}

func TestApplicationCancellationUnblocksEventBackpressure(t *testing.T) {
	isolatePresets(t)
	ctx, cancel := context.WithCancel(context.Background())
	plan := testPlan("a")
	plan.Jobs[0].Output = filepath.Join(t.TempDir(), "输出")
	plan.Jobs[0].Args = []string{"-version"}
	ch := make(chan tea.Msg, 1)
	started, finished := make(chan struct{}), make(chan struct{})
	go startBatchCmd(ctx, ctx, plan, 1, ch, started, finished)()
	<-started
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("退出后事件背压不应阻塞批处理回收")
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
	m.ops = ffmpeg.Selection{Convert: true, AddFonts: true}
	m.rebuildFields()
	values := map[string]string{"advance": "-c:v mpeg4", "fonts_path": "字体 路径", "input_path": ""}
	for key, value := range values {
		field, ok := findField(&m.fields, key)
		require.True(t, ok)
		field.input.SetValue(value)
	}
	m.ops = ffmpeg.Selection{}
	m.rebuildFields()
	m.ops = ffmpeg.Selection{Convert: true, AddFonts: true}
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
	m.subs = []ffmpeg.SubtitleOption{{Suffix: "chi.ass", Default: true}, {Suffix: "eng.ass"}}
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
	opt := ffmpeg.DefaultOption()
	opt.InputPath = t.TempDir()
	opt.OutputPath = filepath.Join(t.TempDir(), "尚未创建")
	input := filepath.Join(opt.InputPath, "a.mp4")
	require.NoError(t, os.WriteFile(input, nil, 0o644))
	msg := planCmd(context.Background(), &opt, ffmpeg.Selection{Convert: true}, 7)().(planMsg)
	require.NoError(t, msg.err)
	assert.Equal(t, 7, msg.seq)
	require.NotNil(t, msg.plan)
	require.Len(t, msg.plan.Jobs, 1)
	assert.Equal(t, input, msg.plan.Jobs[0].Input)
	assert.NoDirExists(t, opt.OutputPath)
}
