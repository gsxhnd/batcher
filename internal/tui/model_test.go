package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/store"
	"github.com/gsxhnd/batcher/internal/task"
	"github.com/gsxhnd/batcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolatePresets 把任务和预设指到临时数据库，避免测试读写可执行文件旁边的 batcher.db。
func isolatePresets(t *testing.T) {
	t.Helper()
	db, err := store.OpenDir(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		task.BindStore(nil)
		presets.Bind(nil)
	})
	task.BindStore(db)
	presets.Bind(db)
}

func press(m *model, s string) {
	_ = pressCmd(m, s)
}

func pressCmd(m *model, s string) tea.Cmd {
	var key tea.KeyPressMsg
	switch s {
	case "enter":
		key = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		key = tea.KeyPressMsg{Code: tea.KeyEsc}
	case "space":
		key = tea.KeyPressMsg{Code: tea.KeySpace}
	case "tab":
		key = tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		key = tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		key = tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		key = tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		key = tea.KeyPressMsg{Code: tea.KeyRight}
	case "pgup":
		key = tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		key = tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "end":
		key = tea.KeyPressMsg{Code: tea.KeyEnd}
	default:
		key = tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
	}
	next, cmd := m.Update(key)
	*m = next.(model)
	return cmd
}

func TestMainScreenOpensTaskAndPresetModals(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	press(&m, "a")
	require.NotNil(t, m.modal)
	require.Equal(t, modalTask, m.modal.kind)
	keys := map[string]bool{}
	for _, f := range m.modal.fields {
		keys[f.def.key] = true
	}
	for _, key := range []string{"op_convert", "op_subtitle", "op_fonts", "batch_name", "input_path", "input_format", "recursive", "output_path", "output_format", "preset", "advance", "fonts_path", "sub_suffix"} {
		assert.True(t, keys[key], "创建弹窗应直接包含 %s", key)
	}
	assert.Contains(t, m.modalView(), "递归扫描子目录", "没勾字幕时递归选项也要显示")
	start := m.modal.kind
	press(&m, "enter")
	require.NotNil(t, m.modal)
	assert.Equal(t, start, m.modal.kind, "enter 只在同一个弹窗里移动，不跳到下一步")
	assert.Equal(t, 1, m.modal.focus)
	press(&m, "esc")
	assert.Nil(t, m.modal)

	press(&m, "p")
	require.NotNil(t, m.modal)
	assert.Equal(t, modalPresetList, m.modal.kind)
	press(&m, "esc")
	assert.Nil(t, m.modal)
}

func TestSubtitleGroupsCollapseUntilFocused(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 40
	press(&m, "a")
	press(&m, "ctrl+n")
	require.NotNil(t, m.modal)

	m.modal.focus = 0
	folded := taskModalText(&m)
	assert.Contains(t, folded, "▸ 字幕 1")
	assert.Contains(t, folded, "▸ 字幕 2")
	assert.NotContains(t, folded, "默认字幕流")

	suffix := -1
	for i, f := range m.modal.fields {
		if f.def.key == "sub_suffix" {
			suffix = i
			break
		}
	}
	require.GreaterOrEqual(t, suffix, 0)
	m.modal.focus = suffix
	open := taskModalText(&m)
	assert.Contains(t, open, "▾ 字幕 1")
	assert.Contains(t, open, "默认字幕流")
	assert.Contains(t, open, "▸ 字幕 2")

	m.modal.focus = suffix + 3
	press(&m, "down")
	assert.Equal(t, suffix+4, m.modal.focus)
	assert.Equal(t, "sub_suffix", m.modal.fields[m.modal.focus].def.key)
}

func TestTaskModalStopsAtTheEnds(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 40
	press(&m, "a")
	require.NotNil(t, m.modal)

	press(&m, "up")
	assert.Equal(t, 0, m.modal.focus, "第一项再向上应停住")

	last := len(m.modal.fields) - 1
	m.modal.focus = last
	press(&m, "down")
	assert.Equal(t, last, m.modal.focus, "最后一项再向下应停住")

	m.modal.focus = 0
	top := m.taskModalView()
	m.modal.focus = last
	scrolled := m.taskModalView()
	assert.Contains(t, top, "ctrl+s")
	assert.Contains(t, scrolled, "ctrl+s", "滚到下面后快捷键提示仍在")
}

func TestPresetOpensAList(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 40
	m.presets = []presets.Preset{
		{Name: "H.264 通用", Args: "-c:v libx264 -crf 23"},
		{Name: "H.265 通用", Args: "-c:v libx265 -crf 26"},
	}
	press(&m, "a")
	require.NotNil(t, m.modal)
	preset := -1
	for i, f := range m.modal.fields {
		if f.def.key == "preset" {
			preset = i
		}
	}
	require.GreaterOrEqual(t, preset, 0)
	m.modal.focus = preset

	press(&m, "right")
	assert.Equal(t, 0, m.modal.fields[preset].choice, "左右键不再切换预设")
	assert.False(t, m.modal.picking)

	press(&m, "enter")
	assert.True(t, m.modal.picking)
	assert.Equal(t, preset, m.modal.focus)
	list := taskModalText(&m)
	assert.Contains(t, list, "手动输入")
	assert.Contains(t, list, "H.264 通用")
	assert.Contains(t, list, "H.265 通用")

	press(&m, "down")
	assert.Equal(t, 1, m.modal.fields[preset].choice)
	assert.Equal(t, preset, m.modal.focus)
	adv, ok := findField(&m.modal.fields, "advance")
	require.True(t, ok)
	assert.Equal(t, "-c:v libx264 -crf 23", adv.Value())

	press(&m, "down")
	press(&m, "down")
	assert.Equal(t, 2, m.modal.fields[preset].choice, "最后一项再向下停住")

	press(&m, "esc")
	assert.False(t, m.modal.picking)
	assert.NotNil(t, m.modal, "esc 只收起列表")
	assert.Equal(t, preset, m.modal.focus)

	press(&m, "enter")
	require.True(t, m.modal.picking)
	press(&m, "enter")
	assert.False(t, m.modal.picking)
	assert.Equal(t, "advance", m.modal.fields[m.modal.focus].def.key)
}

func TestPresetPickKeepsLongNameOnOneLine(t *testing.T) {
	f := field{choice: 1, options: []string{
		"手动输入",
		"无损封装（仅换容器）",
		"Apple H.264 (VideoToolbox)",
	}}
	for _, ln := range presetPickLines(&f, 72) {
		assert.Equal(t, 1, strings.Count(ln, "\n"), "每一项只占一行: %q", ln)
	}
	f.choice = 2
	for _, ln := range presetPickLines(&f, 72) {
		assert.Equal(t, 1, strings.Count(ln, "\n"), "每一项只占一行: %q", ln)
	}
}

func TestTaskModalHidesUncheckedSections(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 40
	press(&m, "a")
	require.NotNil(t, m.modal)

	plain := taskModalText(&m)
	assert.NotContains(t, plain, "目录里的字体都会作为附件写入")
	assert.NotContains(t, plain, "▸ 字幕")

	m.modal.focus = 2 // 添加字体
	press(&m, "space")
	fonts := taskModalText(&m)
	assert.Contains(t, fonts, "目录里的字体都会作为附件写入")
	assert.Contains(t, fonts, "字体文件夹")
	assert.NotContains(t, fonts, "▸ 字幕")

	m.modal.focus = 1 // 添加字幕
	press(&m, "space")
	shown := taskModalText(&m)
	assert.Contains(t, shown, "▸ 字幕 1")
	assert.Contains(t, shown, "字体文件夹")
}

func TestTaskOutputFollowsInput(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	press(&m, "a")
	in, ok := findField(&m.modal.fields, "input_path")
	require.True(t, ok)
	out, ok := findField(&m.modal.fields, "output_path")
	require.True(t, ok)
	assert.Empty(t, out.Value())
	assert.Equal(t, defaultOutputDir(in.resolved()), out.resolved())

	in.input.SetValue("/videos/show")
	m.syncTaskDefaults()
	assert.Equal(t, defaultOutputDir("/videos/show"), out.resolved())
	assert.Empty(t, out.Value(), "跟着源目录走的输出仍是灰色占位")

	out.input.SetValue("/custom/out")
	m.syncTaskDefaults()
	in.input.SetValue("/videos/other")
	m.syncTaskDefaults()
	assert.Equal(t, "/custom/out", out.resolved(), "手填的输出目录保持不变")
}

// TestTaskNameAndFontsDefaults 队列名称占位跟随源目录名且可自定义；字体路径默认值是 fonts。
func TestTaskNameAndFontsDefaults(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	press(&m, "a")
	name, ok := findField(&m.modal.fields, "batch_name")
	require.True(t, ok)
	assert.Empty(t, name.Value(), "队列名称默认留空")
	assert.Equal(t, "留空则用源目录名", name.input.Placeholder)

	in, _ := findField(&m.modal.fields, "input_path")
	in.input.SetValue("/videos/show")
	m.syncTaskDefaults()
	assert.Equal(t, "show", name.fallback, "队列名称占位跟随源目录名")
	assert.Empty(t, name.Value(), "灰色占位只是提示，不写入表单值")

	name.input.SetValue("我的队列")
	in.input.SetValue("/videos/other")
	m.syncTaskDefaults()
	assert.Equal(t, "我的队列", name.Value(), "手填的队列名称保持不变")

	fonts, ok := findField(&m.modal.fields, "fonts_path")
	require.True(t, ok)
	assert.Equal(t, "fonts", fonts.fallback, "字体路径默认值是 fonts")
	assert.Equal(t, "fonts", fonts.resolved(), "留空保存时采用 fonts")
}

func TestCreateTaskRejectsEmptySelection(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal, "没勾选操作时弹窗保持打开")
	assert.Contains(t, m.statusMsg, "至少勾选")
	assert.Empty(t, m.queue.Copies())
}

// TestCreatePlanFailureShowsToast 扫描/生成失败时提示浮在右上角，弹窗保持打开继续改参数。
func TestCreatePlanFailureShowsToast(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	m.modal.info = "正在扫描并生成任务…"
	m.createSeq = 1
	next, _ := m.Update(createPlanMsg{seq: 1, err: errors.New("输出目录不能为空")})
	m = next.(model)
	require.NotNil(t, m.modal, "失败后弹窗保持打开")
	assert.Equal(t, modalTask, m.modal.kind)
	assert.Contains(t, m.statusMsg, "输出目录不能为空")
	assert.Empty(t, m.modal.info, "失败后清掉扫描提示")

	m.createSeq = 2
	next, _ = m.Update(createPlanMsg{seq: 2, plan: &command.BatchPlan{}})
	m = next.(model)
	assert.Contains(t, m.statusMsg, "未找到匹配的视频文件")
}

// TestTaskScanShowsPendingInfo 开始扫描时弹窗内显示进行中提示，不用错误样式，也不发错误 toast。
func TestTaskScanShowsPendingInfo(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	_, i := fieldByKey(m.modal.fields, "op_convert")
	m.modal.fields[i].checked = true
	m.modal.focus = i
	cmd := pressCmd(&m, "ctrl+s")
	require.NotNil(t, cmd, "勾选转码后 ctrl+s 开始扫描")
	assert.Contains(t, m.modal.info, "正在扫描")
	assert.Empty(t, m.statusMsg, "扫描进行中不发错误提示")
	assert.Empty(t, m.modal.err)
}

// TestPresetModalAddEditDuplicate 验证预设的新增/编辑全部在弹窗内完成。
func TestPresetModalAddEditDuplicate(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	press(&m, "p")
	require.Equal(t, modalPresetList, m.modal.kind)

	press(&m, "a")
	require.NotNil(t, m.modal, "按 a 应打开新增弹窗")
	require.Equal(t, modalPreset, m.modal.kind)
	require.Equal(t, 2, len(m.modal.fields), "弹窗应包含名称与参数两个字段")
	assert.Empty(t, m.modal.fields[0].input.Value(), "空白新建时名称应为空")
	assert.Empty(t, m.modal.fields[1].input.Value(), "空白新建时参数应为空")
	fill := func(name, args string) {
		m.modal.fields[0].input.SetValue(name)
		m.modal.fields[1].input.SetValue(args)
		m.modal.focus = 1
	}

	fill("  H.265 画质  ", "  -c:v   h265 -crf 20 ")
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal)
	require.Equal(t, modalPresetList, m.modal.kind, "保存成功后回到预设列表")
	assert.Equal(t, []presets.Preset{{Name: "H.265 画质", Args: "-c:v h265 -crf 20"}}, m.presets,
		"名称去首尾空白，参数规范化空白")

	press(&m, "a")
	fill("", "-c:v h264")
	press(&m, "ctrl+s")
	assert.Equal(t, []presets.Preset{
		{Name: "H.265 画质", Args: "-c:v h265 -crf 20"},
		{Name: "", Args: "-c:v h264"},
	}, m.presets, "名称可留空")

	press(&m, "a")
	fill("重复", "-c:v h265 -crf 20")
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal, "参数重复应留在弹窗内修改")
	assert.NotEmpty(t, m.modal.err)
	press(&m, "esc")
	require.Equal(t, modalPresetList, m.modal.kind, "esc 取消编辑并回到列表")

	// 编辑第二条自定义预设
	m.presetCursor = 1
	m.modal.focus = 1
	press(&m, "e")
	require.NotNil(t, m.modal)
	m.modal.fields[0].input.SetValue("H.264")
	m.modal.fields[1].input.SetValue("-c:v hevc")
	m.modal.focus = 1
	press(&m, "ctrl+s")
	assert.Equal(t, []presets.Preset{
		{Name: "H.265 画质", Args: "-c:v h265 -crf 20"},
		{Name: "H.264", Args: "-c:v hevc"},
	}, m.presets)
}

// TestPresetDeleteUsesConfirmModal 验证删除改为确认弹窗交互。
func TestPresetDeleteUsesConfirmModal(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]presets.Preset{{Args: "a"}, {Args: "b"}}))
	m := newModel()
	press(&m, "p")
	m.presetCursor = 0
	m.modal.focus = 0

	press(&m, "d")
	require.NotNil(t, m.modal, "第一次按 d 应弹确认窗")
	assert.Equal(t, modalConfirm, m.modal.kind)

	press(&m, "esc")
	require.Equal(t, modalPresetList, m.modal.kind, "取消删除后回到预设列表")
	assert.Len(t, m.presets, 2, "esc 取消删除")

	m.presetCursor = 0
	m.modal.focus = 0
	press(&m, "d")
	press(&m, "enter")
	assert.Equal(t, []presets.Preset{{Args: "b"}}, m.presets, "enter 确认后删除")
	require.Equal(t, modalPresetList, m.modal.kind)

	m.presetCursor = 0
	m.modal.focus = 0
	press(&m, "d")
	press(&m, "j")
	require.NotNil(t, m.modal, "确认弹窗内按无关键应保持打开")
	assert.Equal(t, modalConfirm, m.modal.kind)
}

func TestPresetReorderPersists(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]presets.Preset{{Args: "a"}, {Args: "b"}, {Args: "c"}}))
	m := newModel()
	press(&m, "p")
	m.presetCursor = 0
	m.modal.focus = 0

	press(&m, "J")
	assert.Equal(t, []presets.Preset{{Args: "b"}, {Args: "a"}, {Args: "c"}}, m.presets)
	assert.Equal(t, 1, m.presetCursor, "移动后光标跟随条目")

	loaded, err := presets.Load()
	require.NoError(t, err)
	assert.Equal(t, []presets.Preset{{Args: "b"}, {Args: "a"}, {Args: "c"}}, loaded, "顺序应持久化")
}

func TestPresetGuardsBuiltins(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	press(&m, "p")
	require.Equal(t, modalPresetList, m.modal.kind)
	press(&m, "e")
	assert.Equal(t, modalPresetList, m.modal.kind)
	assert.Contains(t, m.presetListContent(), "还没有预设", "提示应出现在预设弹窗里")
	assert.Empty(t, m.presets)

	m.presets = []presets.Preset{{Name: "可删除", Args: "-c copy"}}
	m.openPresetList()
	press(&m, "d")
	require.Equal(t, modalConfirm, m.modal.kind)
	press(&m, "enter")
	assert.Empty(t, m.presets, "数据库里的预设可以删除")
}

func TestBuiltinCopyPresetFromPanelPresets(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	builtin := presets.Builtins()[0]
	src := presets.Preset{Name: builtin.Name, Args: builtin.Args}
	m.presets = []presets.Preset{src}
	press(&m, "p")
	require.Equal(t, 0, m.modal.focus)

	press(&m, "c")
	require.NotNil(t, m.modal, "选中预设按 c 应打开复制弹窗")
	require.Equal(t, modalPreset, m.modal.kind)
	assert.Equal(t, src.Name, m.modal.fields[0].input.Value(), "名称预填被复制的预设")
	assert.Equal(t, src.Args, m.modal.fields[1].input.Value(), "参数预填被复制的预设")

	m.modal.fields[1].input.SetValue(src.Args + " -sn")
	m.modal.focus = 1
	press(&m, "ctrl+s")
	require.Equal(t, modalPresetList, m.modal.kind, "保存后回到预设列表")
	require.Len(t, m.presets, 2)
	assert.Equal(t, src.Args+" -sn", m.presets[1].Args)
}

// TestModalSwallowsGlobalKeys 弹窗打开时全局快捷键不应生效。
func TestModalSwallowsGlobalKeys(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	press(&m, "1")
	require.Equal(t, modalTask, m.modal.kind, "数字键不应离开创建弹窗")
	press(&m, "x")
	require.Equal(t, modalTask, m.modal.kind, "x 不应触发执行")
	assert.False(t, m.running)
}

func TestTryStartRunValidation(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	press(&m, "ctrl+s")
	assert.Contains(t, m.statusMsg, "至少勾选", "校验失败用右上角 toast 提示")
	assert.Empty(t, m.queue.Copies())
	assert.False(t, m.running)

	m.modal.focus = 1 // 添加字幕
	press(&m, "space")
	m.modal.focus = len(m.modal.fields) - 1
	m.removeSubtitleFields()
	press(&m, "ctrl+s")
	assert.Contains(t, m.statusMsg, "字幕")
	assert.Empty(t, m.queue.Copies())
	assert.False(t, m.running)

	// 字体路径默认 fonts，勾选后表单校验通过，直接进入扫描而不是弹错误提示。
	m.modal.focus = 1 // 取消字幕
	press(&m, "space")
	m.modal.focus = 2 // 添加字体
	press(&m, "space")
	press(&m, "ctrl+s")
	assert.True(t, m.creating, "字体路径默认 fonts 时校验通过，开始扫描")
	assert.Empty(t, m.statusMsg, "进入扫描后清掉右上角提示")
	assert.Empty(t, m.queue.Copies())
	assert.False(t, m.running)
}

func TestEnterTaskListDoesNotEnqueue(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)

	press(&m, "enter")
	assert.Nil(t, m.modal, "任务列表里按回车不入队")
	assert.Empty(t, m.queue.Copies())
	press(&m, "x")
	assert.Empty(t, m.queue.Copies(), "按 x 不入队")
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind, "按 a 打开创建弹窗")
	assert.Empty(t, m.queue.Copies(), "打开弹窗还不入队")
}

func TestEscWithoutModalDoesNotQuitAndFocusKept(t *testing.T) {
	isolatePresets(t)
	m := newModel()

	press(&m, "up")
	assert.Equal(t, panelRun, m.focus, "方向键不离开当前焦点")
	press(&m, "esc")
	assert.Nil(t, m.modal, "没有弹窗时 esc 不退出程序")
}

func TestRunLockedFocusDuringExecution(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	plan := testPlanOut(t, "a.mkv")
	batch, err := m.queue.Enqueue(plan, command.Selection{Convert: true})
	require.NoError(t, err)
	require.NoError(t, m.queue.Start(batch.ID))
	m.running = true
	m.focus = panelRun
	before := len(m.queue.Copies())
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind, "执行中仍可打开创建弹窗")
	assert.Equal(t, before, len(m.queue.Copies()), "打开弹窗还不入队")
	press(&m, "esc")
	assert.Nil(t, m.modal)
	m.enqueue(testPlanOut(t, "b.mkv"), command.Selection{Convert: true})
	assert.Len(t, m.queue.Copies(), before+1, "执行中创建的主任务排在后面")
	assert.Contains(t, m.statusMsg, "按 x 开始")
	press(&m, "enter")
	assert.Len(t, m.queue.Copies(), before+1, "任务列表回车不入队")

	// 等真实暂停后再测 r/s/x（光标在该批次行上），避免竞态。
	m.roster = []rosterRow{{batchID: batch.ID, state: fileRunning, cmd: ""}}
	m.pausedBatch = batch.ID
	waitBatchEvent(t, &m, task.QueueBatchPaused)
	press(&m, "r")
	assert.Contains(t, m.statusMsg, "重试")
	m.pausedBatch = batch.ID
	m.roster = []rosterRow{{batchID: batch.ID, state: filePaused, cmd: ""}}
	m.fileCursor = 0
	press(&m, "s")
	assert.Contains(t, m.statusMsg, "跳过")
	m.pausedBatch = batch.ID
	m.roster = []rosterRow{{batchID: batch.ID, state: filePaused, cmd: ""}}
	m.fileCursor = 0
	press(&m, "x")
	assert.Contains(t, m.statusMsg, "中止")

	// esc 取消当前批次：非暂停时走 CancelCurrent，界面保持执行态直到结束。
	m.pausedBatch = ""
	press(&m, "esc")
	assert.Contains(t, m.statusMsg, "取消请求")
	assert.True(t, m.running, "取消是异步的，界面保持执行态直到结束")
	assert.Nil(t, m.modal, "esc 直接取消，不弹出退出确认")
}

func TestQuitConfirmsWhileRunning(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	testQueue(t, &m)

	idle := pressCmd(&m, "q")
	require.NotNil(t, idle)
	_, quit := idle().(tea.QuitMsg)
	assert.True(t, quit, "空闲时 q 直接退出")
	assert.Nil(t, m.modal)

	m.running = true
	again := pressCmd(&m, "q")
	assert.Nil(t, again, "执行中 q 先确认，不立刻退出")
	require.NotNil(t, m.modal)
	assert.True(t, m.modal.quitting)
	assert.Equal(t, "退出", m.modal.title)
	assert.True(t, m.running)
	assert.NotContains(t, m.statusMsg, "取消请求")

	press(&m, "q")
	require.NotNil(t, m.modal, "确认框里再按 q 不重复打开，也不退出")
	press(&m, "esc")
	assert.Nil(t, m.modal)
	assert.True(t, m.running, "留下时任务继续")
	assert.NotContains(t, m.statusMsg, "取消请求")

	press(&m, "q")
	confirmed := pressCmd(&m, "enter")
	require.NotNil(t, confirmed)
	_, quit = confirmed().(tea.QuitMsg)
	assert.True(t, quit, "确认后退出")
	assert.Nil(t, m.modal)

	// 暂停在失败项上时没有 ffmpeg 在跑，q 直接退出，不再确认。
	m.running = true
	m.pausedBatch = "batch"
	pausedQuit := pressCmd(&m, "q")
	require.NotNil(t, pausedQuit)
	_, ok := pausedQuit().(tea.QuitMsg)
	assert.True(t, ok, "暂停中 q 直接退出")

	// 暂停中 esc 仍是中止当前任务。
	m.running = true
	m.pausedBatch = "batch"
	press(&m, "esc")
	assert.Contains(t, m.statusMsg, "中止", "暂停中 esc 中止当前任务")
}

func TestQDoesNotQuitTaskOrPresetModal(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30

	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	cmd := pressCmd(&m, "q")
	assert.Nil(t, cmd, "创建任务里按 q 不退出")
	require.NotNil(t, m.modal)
	assert.Equal(t, modalTask, m.modal.kind)
	press(&m, "esc")
	assert.Nil(t, m.modal)
	quit := pressCmd(&m, "q")
	require.NotNil(t, quit)
	_, ok := quit().(tea.QuitMsg)
	assert.True(t, ok, "关掉创建弹窗后 q 才退出")

	m = newModel()
	m.width, m.height = 100, 30
	press(&m, "p")
	require.Equal(t, modalPresetList, m.modal.kind)
	assert.Nil(t, pressCmd(&m, "q"), "预设管理里按 q 不退出")
	require.Equal(t, modalPresetList, m.modal.kind)
	press(&m, "a")
	require.Equal(t, modalPreset, m.modal.kind)
	press(&m, "esc")
	require.Equal(t, modalPresetList, m.modal.kind, "预设编辑仍用 esc 返回列表")
	press(&m, "esc")
	assert.Nil(t, m.modal)
	quit = pressCmd(&m, "q")
	require.NotNil(t, quit)
	_, ok = quit().(tea.QuitMsg)
	assert.True(t, ok, "关掉预设管理后 q 才退出")
}

func TestQTypesIntoTextField(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.running = true
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	_, idx := fieldByKey(m.modal.fields, "input_path")
	require.NotEqual(t, -1, idx)
	m.modal.focus = idx
	focusField(m.modal.fields, idx)
	press(&m, "q")
	assert.Equal(t, modalTask, m.modal.kind, "文本框里的 q 不退出")
	assert.False(t, m.modal.quitting)
	assert.Equal(t, "q", m.modal.fields[idx].input.Value())
}

func TestIsISO639Code(t *testing.T) {
	valid := []string{"chi", "eng", "jpn"}
	invalid := []string{"", "zh", "zho-Hans", "CHI", "ch1", "chinese", "zh-Hans"}
	for _, s := range valid {
		assert.True(t, isISO639Code(s), s)
	}
	for _, s := range invalid {
		assert.False(t, isISO639Code(s), s)
	}
}

func TestOverwriteConfirmsOnce(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 36
	testQueue(t, &m)
	b, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)
	b.Status = task.StatusPaused
	b.Items[0].Status = task.StatusFailed
	b.Items[0].Error = "输出文件已存在: " + b.Items[0].Output
	b.Items[0].Attempts = 1
	m.pausedBatch = b.ID
	m.running = true
	m.syncRoster()
	m.fileCursor = 0

	press(&m, "o")
	require.NotNil(t, m.modal)
	assert.Equal(t, "覆盖已有文件", m.modal.title)
	assert.Contains(t, b.Items[0].PlanArgs, "-n")

	press(&m, "esc")
	assert.Nil(t, m.modal)
	assert.False(t, b.Items[0].Overwrite)

	press(&m, "o")
	press(&m, "enter")
	assert.Nil(t, m.modal)
	assert.True(t, b.Items[0].Overwrite)
	assert.Contains(t, b.Items[0].PlanArgs, "-y")
	assert.NotContains(t, b.Items[0].PlanArgs, "-n")
}

func TestCreateTaskListsInputsBeforeEnqueue(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	testQueue(t, &m)
	plan := &command.BatchPlan{Jobs: []command.Job{
		{Input: "/v/a.mkv", Args: []string{"-nostdin", "-n", "-i", "/v/a.mkv", "-i", "/v/a.chi.ass", "-attach", "/fonts/a.ttf", "-metadata:s:t:m:filename:a.ttf", "mimetype=application/x-truetype-font", "/out/a.mkv"}},
		{Input: "/v/b.mkv", Args: []string{"-nostdin", "-n", "-i", "/v/b.mkv", "-attach", "/fonts/a.ttf", "-metadata:s:t:m:filename:a.ttf", "mimetype=application/x-truetype-font", "/out/b.mkv"}},
	}}

	m.createSeq = 1
	m.modal = &modal{kind: modalTask}
	next, _ := m.Update(createPlanMsg{seq: 1, plan: plan})
	m = next.(model)
	require.NotNil(t, m.modal)
	assert.Equal(t, modalConfirm, m.modal.kind)
	require.Len(t, m.modal.jobOpen, 2)
	assert.True(t, m.modal.jobOpen[0] && m.modal.jobOpen[1], "子任务默认全部展开")
	shown := m.modalView()
	assert.Contains(t, shown, "文件")
	assert.Contains(t, shown, "/v/a.mkv")
	assert.Contains(t, shown, "/v/a.chi.ass")
	assert.Contains(t, shown, "/v/b.mkv")
	assert.Contains(t, shown, "字体")
	assert.Equal(t, 1, strings.Count(shown, "/fonts/a.ttf"), "各任务字体相同时只列一次")
	assert.Contains(t, m.footer(), "展开/收起")
	assert.Empty(t, m.queue.Copies(), "确认前不应入队")

	press(&m, "enter")
	assert.False(t, m.modal.jobOpen[0], "enter 收起当前子任务")
	assert.True(t, m.modal.jobOpen[1])
	folded := m.modalView()
	assert.NotContains(t, folded, "/v/a.chi.ass")
	assert.Contains(t, folded, "/v/b.mkv")
	assert.Contains(t, folded, "/fonts/a.ttf", "共用字体不受子任务收起影响")
	assert.Empty(t, m.queue.Copies(), "收起不应入队")

	press(&m, "enter")
	assert.True(t, m.modal.jobOpen[0])
	assert.Contains(t, m.modalView(), "/v/a.chi.ass")

	press(&m, "esc")
	assert.Equal(t, modalTask, m.modal.kind, "esc 返回创建任务表单而不是直接取消")
	assert.Empty(t, m.queue.Copies())
	press(&m, "esc")
	assert.Nil(t, m.modal, "在创建表单里再按 esc 才关闭")
	assert.Empty(t, m.queue.Copies())

	m.createSeq = 2
	m.modal = &modal{kind: modalTask}
	next, _ = m.Update(createPlanMsg{seq: 2, plan: plan})
	m = next.(model)
	press(&m, "ctrl+s")
	require.Len(t, m.queue.Copies(), 1)
}

func TestEnqueueConfirmListsFilesAndFonts(t *testing.T) {
	sharedPlan := &command.BatchPlan{Jobs: []command.Job{
		{Input: "/v/a.mkv", Args: []string{"-i", "/v/a.mkv", "-attach", "/fonts/a.ttf"}},
		{Input: "/v/b.mkv", Args: []string{"-i", "/v/b.mkv", "-attach", "/fonts/a.ttf"}},
	}}
	m := newModel()
	m.width, m.height = 100, 40
	m.openEnqueueConfirm(sharedPlan, nil)
	shared := m.modalView()
	assert.Contains(t, shared, "文件")
	assert.Contains(t, shared, "/v/a.mkv")
	assert.Contains(t, shared, "/v/b.mkv")
	assert.Equal(t, 1, strings.Count(shared, "字体"))
	assert.Equal(t, 1, strings.Count(shared, "/fonts/a.ttf"))

	differentPlan := &command.BatchPlan{Jobs: []command.Job{
		{Input: "/v/a.mkv", Args: []string{"-i", "/v/a.mkv", "-attach", "/fonts/a.ttf"}},
		{Input: "/v/b.mkv", Args: []string{"-i", "/v/b.mkv", "-attach", "/fonts/b.otf"}},
	}}
	m.openEnqueueConfirm(differentPlan, nil)
	different := m.modalView()
	assert.Contains(t, different, "/fonts/a.ttf")
	assert.Contains(t, different, "/fonts/b.otf")
	assert.Equal(t, 2, strings.Count(different, "字体"))
	press(&m, "down")
	press(&m, "enter")
	folded := m.modalView()
	assert.Contains(t, folded, "/fonts/a.ttf")
	assert.NotContains(t, folded, "/fonts/b.otf", "收起后不再列出该子任务的字体")
	assert.Contains(t, folded, "1 个字体")

	none := &command.BatchPlan{Jobs: []command.Job{
		{Input: "/v/a.mkv", Args: []string{"-i", "/v/a.mkv"}},
	}}
	m.openEnqueueConfirm(none, nil)
	text := m.modalView()
	assert.Contains(t, text, "文件")
	assert.NotContains(t, text, "字体")
}

// TestEnqueueConfirmMarksMissingSubtitles 入队确认里提示字幕匹配结果：
// 全部没配上的子任务标出“没有字幕”，只缺一部分时逐条给出原因。
func TestEnqueueConfirmMarksMissingSubtitles(t *testing.T) {
	m := newModel()
	m.width, m.height = 100, 40
	plan := &command.BatchPlan{Jobs: []command.Job{
		// 一条字幕都没配上：展开正文标出“没有字幕”，折行摘要同样可见。
		{Input: "/v/a.mkv", Args: []string{"-i", "/v/a.mkv", "/out/a.mkv"},
			SubtitleNotes: []string{"未找到 a.chi.ass"}},
		// 只缺一条：正文提示缺的那条，不标“没有字幕”。
		{Input: "/v/b.mkv", Args: []string{"-i", "/v/b.mkv", "-sub_charenc", "UTF-8", "-i", "/v/b.chi.ass", "/out/b.mkv"},
			SubtitleNotes: []string{"未找到 b.eng.srt"}},
		// 全部配上：不额外占行。
		{Input: "/v/c.mkv", Args: []string{"-i", "/v/c.mkv", "-sub_charenc", "UTF-8", "-i", "/v/c.chi.ass", "/out/c.mkv"}},
	}}
	m.openEnqueueConfirm(plan, nil)
	shown := m.modalView()
	assert.Contains(t, shown, "没有字幕", "全部没配上的子任务要标出“没有字幕”")
	assert.Contains(t, shown, "未找到 a.chi.ass")
	assert.Contains(t, shown, "未找到 b.eng.srt（已跳过）")
	assert.Equal(t, 1, strings.Count(shown, "没有字幕"), "只缺一部分的子任务不标“没有字幕”")

	// 收起第一条后，折行摘要仍提示“没有字幕”，正文里的原因不再显示。
	press(&m, "enter")
	folded := m.modalView()
	assert.Contains(t, folded, "没有字幕")
	assert.NotContains(t, folded, "未找到 a.chi.ass")
}

func TestEnqueueConfirmScrollsToFocusedJob(t *testing.T) {
	args := []string{"-i", "/v/a.mkv"}
	for i := 0; i < 12; i++ {
		args = append(args, "-i", fmt.Sprintf("/v/a.%02d.ass", i))
	}
	plan := &command.BatchPlan{Jobs: []command.Job{
		{Input: "/v/a.mkv", Args: args},
		{Input: "/v/b.mkv", Args: []string{"-i", "/v/b.mkv"}},
	}}
	m := newModel()
	m.width, m.height = 80, 12
	m.openEnqueueConfirm(plan, nil)
	require.Positive(t, m.modal.vp.Height(), "内容超出一屏时应可滚动")
	assert.Equal(t, 0, m.modal.vp.YOffset(), "打开时停在列表顶部")
	assert.Contains(t, m.modalView(), "确认将")

	press(&m, "down")
	assert.Equal(t, 1, m.modal.focus)
	assert.Positive(t, m.modal.vp.YOffset(), "移到下面的子任务时应把它滚进视口")
	assert.Contains(t, m.modal.vp.View(), "b.mkv")
}

// TestCreateTaskUsesCustomQueueName 确认弹窗与队列都使用表单里填写的队列名称。
func TestCreateTaskUsesCustomQueueName(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	testQueue(t, &m)
	plan := &command.BatchPlan{Jobs: []command.Job{
		{Input: "/v/a.mkv", Args: []string{"-nostdin", "-n", "-i", "/v/a.mkv", "/out/a.mkv"}},
	}}

	m.createSeq = 1
	m.modal = &modal{kind: modalTask}
	next, _ := m.Update(createPlanMsg{seq: 1, plan: plan, name: "我的队列"})
	m = next.(model)
	require.NotNil(t, m.modal)
	assert.Equal(t, modalConfirm, m.modal.kind)
	assert.Contains(t, m.modalView(), "我的队列", "确认弹窗显示自定义队列名称")

	press(&m, "ctrl+s")
	require.Len(t, m.queue.Copies(), 1)
	assert.Equal(t, "我的队列", m.queue.Copies()[0].Name, "入队后主任务使用自定义名称")
	assert.Contains(t, m.statusMsg, "我的队列", "入队成功提示带上队列名称")
}

// TestEnqueueConfirmEscReturnsToTaskForm 入队确认里按 esc 回到创建表单继续改参数，
// 已填内容保留；再次生成后仍能进入确认并正常入队。
func TestEnqueueConfirmEscReturnsToTaskForm(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	testQueue(t, &m)
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	name, ok := findField(&m.modal.fields, "batch_name")
	require.True(t, ok)
	name.input.SetValue("我的队列")

	plan := &command.BatchPlan{Jobs: []command.Job{
		{Input: "/v/a.mkv", Args: []string{"-nostdin", "-n", "-i", "/v/a.mkv", "/out/a.mkv"}},
	}}
	m.createSeq = 1
	next, _ := m.Update(createPlanMsg{seq: 1, plan: plan, name: "我的队列"})
	m = next.(model)
	require.Equal(t, modalConfirm, m.modal.kind)

	press(&m, "esc")
	require.Equal(t, modalTask, m.modal.kind, "esc 返回创建任务表单而不是直接取消")
	saved, ok := findField(&m.modal.fields, "batch_name")
	require.True(t, ok)
	assert.Equal(t, "我的队列", saved.input.Value(), "返回表单后已填内容保留")
	assert.Contains(t, m.modalView(), "新建任务", "表单正常渲染")
	assert.Empty(t, m.queue.Copies(), "esc 不会入队")

	// 回到表单后再生成一次，仍能进入确认并入队
	m.createSeq = 2
	next, _ = m.Update(createPlanMsg{seq: 2, plan: plan, name: "我的队列"})
	m = next.(model)
	require.Equal(t, modalConfirm, m.modal.kind)
	press(&m, "ctrl+s")
	require.Len(t, m.queue.Copies(), 1)
	assert.Equal(t, "我的队列", m.queue.Copies()[0].Name)
}

// TestRenameTaskUpdatesName 主任务行按 n 重命名：输入框预填当前名称，保存后队列
// 副本、落盘记录和列表标题同步更新；空名称弹窗保持打开；子任务行没有重命名。
func TestRenameTaskUpdatesName(t *testing.T) {
	isolatePresets(t)
	dir := t.TempDir()
	db, err := store.OpenDir(dir)
	require.NoError(t, err)
	task.BindStore(db)
	presets.Bind(db)
	t.Cleanup(func() {
		_ = db.Close()
		task.BindStore(nil)
		presets.Bind(nil)
	})

	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	m.db = db
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)
	m.syncRoster()
	require.NotEmpty(t, m.roster)
	require.Empty(t, m.roster[0].itemID, "光标应停在主任务行")

	press(&m, "n")
	require.NotNil(t, m.modal)
	require.Equal(t, modalRename, m.modal.kind)
	require.Len(t, m.modal.fields, 1)
	assert.Equal(t, batch.Name, m.modal.fields[0].Value(), "输入框预填当前名称")

	// 真实按键必须能写进输入框，不能只靠 SetValue 绕过按键分发。
	m.modal.fields[0].input.SetValue("")
	for _, ch := range "新的名字" {
		press(&m, string(ch))
	}
	require.Equal(t, "新的名字", m.modal.fields[0].Value(), "重命名弹窗里键入的字符应进入输入框")
	m.modal.fields[0].input.SetValue("  新的名字  ")
	press(&m, "enter")
	assert.Nil(t, m.modal)
	renamed, ok := m.queue.BatchCopy(batch.ID)
	require.True(t, ok)
	assert.Equal(t, "新的名字", renamed.Name, "队列里的名称已更新（去首尾空白）")
	assert.Contains(t, m.roster[0].title, "新的名字", "列表标题同步更新")
	assert.Contains(t, m.statusMsg, "新的名字")

	// 空名称不能保存，弹窗保持打开继续改
	press(&m, "n")
	require.Equal(t, modalRename, m.modal.kind)
	m.modal.fields[0].input.SetValue("   ")
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal, "空名称时弹窗保持打开")
	assert.Contains(t, m.modal.err, "不能为空")
	press(&m, "esc")
	assert.Nil(t, m.modal)

	// 子任务行不支持重命名
	press(&m, "enter")
	require.Greater(t, len(m.roster), 1)
	m.fileCursor = 1
	press(&m, "n")
	assert.Nil(t, m.modal, "子任务行没有重命名")

	// 名称随异步保存落盘
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		saved, err := task.LoadTask(batch.ID)
		if err == nil && saved.Name == "新的名字" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	saved, err := task.LoadTask(batch.ID)
	require.NoError(t, err)
	assert.Equal(t, "新的名字", saved.Name, "重命名已持久化")
}

// TestRenameHistoryTask 不在队列里的历史任务也能重命名：直接改数据库副本并刷新列表。
func TestRenameHistoryTask(t *testing.T) {
	isolatePresets(t)
	dir := t.TempDir()
	db, err := store.OpenDir(dir)
	require.NoError(t, err)
	task.BindStore(db)
	presets.Bind(db)
	t.Cleanup(func() {
		_ = db.Close()
		task.BindStore(nil)
		presets.Bind(nil)
	})

	first := newModel()
	first.db = db
	testQueue(t, &first)
	batch, err := first.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)

	// 新模型接同一个库：批次不在队列里，走历史任务路径
	again := newModel()
	again.db = db
	again.initQueue(make(chan tea.Msg, 8))
	t.Cleanup(again.queue.Stop)
	deadline := time.Now().Add(2 * time.Second)
	for {
		again.syncRosterFromStore()
		if len(again.roster) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotEmpty(t, again.roster)

	press(&again, "n")
	require.NotNil(t, again.modal)
	require.Equal(t, modalRename, again.modal.kind)
	again.modal.fields[0].input.SetValue("历史任务改名")
	press(&again, "enter")
	assert.Nil(t, again.modal)
	assert.Contains(t, again.roster[0].title, "历史任务改名", "列表标题同步更新")

	saved, err := db.LoadTask(batch.ID)
	require.NoError(t, err)
	assert.Equal(t, "历史任务改名", saved.Name, "历史任务改名直接落盘")
}

// editFixturePlan 用占位文件生成一份真实计划，供编辑测试预填与入队。
func editFixturePlan(t *testing.T, fx testutil.Fixture, subs []command.SubtitleOption) (*command.BatchPlan, command.Selection) {
	t.Helper()
	opt := command.DefaultOption()
	opt.InputPath, opt.InputFormat = fx.Dir1, "mp4"
	opt.OutputPath = fx.Out
	opt.OutputFormat = "mkv"
	opt.Subtitles = subs
	sel := command.Selection{Convert: true, AddSubtitle: len(subs) > 0}
	plan, err := command.BuildPlan(context.Background(), &opt, sel)
	require.NoError(t, err)
	return plan, sel
}

// TestEditTaskOpensPrefilledForm 主任务行按 e 重新编辑：表单预填任务保存的配置、
// 字幕组和队列名称；esc 关闭后再次打开内容不变；子任务行没有编辑入口。
func TestEditTaskOpensPrefilledForm(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	testQueue(t, &m)

	fx := testutil.NewFixture(t)
	plan, sel := editFixturePlan(t, fx, []command.SubtitleOption{
		{Suffix: "ass", Lang: "chi", Title: "简体中文", Default: true},
	})
	plan.Name = "我的队列"
	batch, err := m.queue.Enqueue(plan, sel)
	require.NoError(t, err)
	m.syncRoster()
	require.Empty(t, m.roster[0].itemID, "光标应停在主任务行")

	press(&m, "e")
	require.NotNil(t, m.modal)
	require.Equal(t, modalTask, m.modal.kind)
	assert.Equal(t, "编辑任务", m.modal.title)
	assert.Equal(t, batch.ID, m.modal.editBatchID)
	assert.Contains(t, m.modalView(), "保存修改", "编辑表单的提示文案应区别于新建")

	value := func(key string) string {
		f, ok := findField(&m.modal.fields, key)
		require.True(t, ok, "字段 %s 应存在", key)
		return f.Value()
	}
	checked := func(key string) bool {
		f, ok := findField(&m.modal.fields, key)
		require.True(t, ok, "字段 %s 应存在", key)
		return f.checked
	}
	assert.Equal(t, "我的队列", value("batch_name"))
	assert.Equal(t, fx.Dir1, value("input_path"))
	assert.Equal(t, "mp4", value("input_format"))
	assert.Equal(t, fx.Out, value("output_path"))
	assert.Equal(t, "mkv", value("output_format"))
	assert.Equal(t, "ass", value("sub_suffix"))
	assert.Equal(t, "chi", value("sub_lang"))
	assert.Equal(t, "简体中文", value("sub_title"))
	assert.True(t, checked("op_convert"))
	assert.True(t, checked("op_subtitle"))
	assert.True(t, checked("sub_default"), "默认字幕流的勾选应恢复")
	assert.False(t, checked("op_fonts"))
	assert.False(t, checked("recursive"))

	press(&m, "esc")
	assert.Nil(t, m.modal)
	press(&m, "e")
	require.Equal(t, modalTask, m.modal.kind)
	assert.Equal(t, "ass", value("sub_suffix"), "再次打开仍预填任务参数")
	press(&m, "esc")

	// 子任务行不支持编辑
	press(&m, "enter")
	require.Greater(t, len(m.roster), 1)
	m.fileCursor = 1
	press(&m, "e")
	assert.Nil(t, m.modal, "子任务行没有编辑入口")
}

// TestEditTaskAppliesReplacement 编辑确认后用新计划替换原任务：队列副本、列表
// 标题与落盘记录同步更新，状态回到等待；esc 可回到编辑表单继续改参数。
func TestEditTaskAppliesReplacement(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	testQueue(t, &m)

	fx := testutil.NewFixture(t)
	plan, sel := editFixturePlan(t, fx, nil)
	batch, err := m.queue.Enqueue(plan, sel)
	require.NoError(t, err)
	m.syncRoster()

	press(&m, "e")
	require.Equal(t, modalTask, m.modal.kind)
	// 输入用绝对路径构造：NewBatch 会把参数里的输入输出换成绝对路径。
	base := t.TempDir()
	input := filepath.Join(base, "a.mkv")
	output := filepath.Join(base, "a-out.mkv")
	newPlan := &command.BatchPlan{Name: "编辑后的队列", Jobs: []command.Job{{
		Input:  input,
		Output: output,
		Args:   []string{"-nostdin", "-n", "-i", input, output},
	}}}
	m.createSel = command.Selection{Convert: true}
	m.createSeq = 1
	next, _ := m.Update(createPlanMsg{seq: 1, plan: newPlan, name: "编辑后的队列"})
	m = next.(model)
	require.Equal(t, modalConfirm, m.modal.kind)
	shown := m.modalView()
	assert.Contains(t, shown, "确认更新任务")
	assert.Contains(t, shown, "原有执行记录会被替换")

	// esc 回到编辑表单继续改参数；再 esc 才关闭。
	press(&m, "esc")
	require.Equal(t, modalTask, m.modal.kind)
	require.Equal(t, batch.ID, m.modal.editBatchID)
	press(&m, "esc")
	assert.Nil(t, m.modal)

	press(&m, "e")
	require.Equal(t, batch.ID, m.modal.editBatchID)
	m.createSeq = 2
	next, _ = m.Update(createPlanMsg{seq: 2, plan: newPlan, name: "编辑后的队列"})
	m = next.(model)
	press(&m, "ctrl+s")

	got, ok := m.queue.BatchCopy(batch.ID)
	require.True(t, ok, "编辑不产生新任务，原 ID 保留")
	require.Len(t, got.Items, 1)
	assert.Equal(t, newPlan.Jobs[0].Args, got.Items[0].PlanArgs)
	assert.Equal(t, "编辑后的队列", got.Name)
	assert.Equal(t, task.StatusPending, got.Status, "替换后回到等待，按 x 开始")
	assert.Contains(t, m.statusMsg, "已更新任务")
	assert.Contains(t, m.roster[0].title, "编辑后的队列", "列表标题同步更新")

	// 落盘记录同步替换。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if saved, err := task.LoadTask(batch.ID); err == nil && saved.Name == "编辑后的队列" && len(saved.Items) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	saved, err := task.LoadTask(batch.ID)
	require.NoError(t, err)
	assert.Equal(t, "编辑后的队列", saved.Name)
	require.Len(t, saved.Items, 1)
}

// TestEditHistoryTaskUpdatesStore 不在队列里的历史任务编辑后按同 ID 重建记录写库。
func TestEditHistoryTaskUpdatesStore(t *testing.T) {
	isolatePresets(t)
	dir := t.TempDir()
	db, err := store.OpenDir(dir)
	require.NoError(t, err)
	task.BindStore(db)
	presets.Bind(db)
	t.Cleanup(func() {
		_ = db.Close()
		task.BindStore(nil)
		presets.Bind(nil)
	})
	m := newModel()
	m.db = db
	m.width, m.height = 100, 30
	m.resizePanes()
	testQueue(t, &m)

	fx := testutil.NewFixture(t)
	plan, sel := editFixturePlan(t, fx, nil)
	batch := task.NewBatch("history-1", plan, sel, time.Now())
	require.NoError(t, batch.Save())
	m.syncRosterFromStore()
	require.NotEmpty(t, m.roster)
	require.Empty(t, m.roster[0].itemID)

	press(&m, "e")
	require.Equal(t, modalTask, m.modal.kind)
	require.Equal(t, "history-1", m.modal.editBatchID)
	f, ok := findField(&m.modal.fields, "input_path")
	require.True(t, ok)
	assert.Equal(t, fx.Dir1, f.Value(), "历史任务的参数也应预填")

	newPlan := testPlanOut(t, "a.mkv")
	newPlan.Name = "历史任务改名"
	m.createSel = command.Selection{Convert: true}
	m.createSeq = 1
	next, _ := m.Update(createPlanMsg{seq: 1, plan: newPlan, name: "历史任务改名"})
	m = next.(model)
	require.Equal(t, modalConfirm, m.modal.kind)
	press(&m, "ctrl+s")

	assert.Contains(t, m.statusMsg, "已更新任务")
	assert.Contains(t, m.roster[0].title, "历史任务改名")
	saved, err := task.LoadTask("history-1")
	require.NoError(t, err)
	assert.Equal(t, "历史任务改名", saved.Name)
	require.Len(t, saved.Items, 1)
	assert.Equal(t, task.StatusPending, saved.Status)
}

// TestEditTaskRefusedWhenRunningOrPaused 运行中或暂停中的批次不能编辑；
// 子任务行没有编辑入口。
func TestEditTaskRefusedWhenRunningOrPaused(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	testQueue(t, &m)
	m.roster = []rosterRow{
		{batchID: "b1", title: "主任务"},
		{batchID: "b1", itemID: "001", title: "a.mkv"},
	}
	m.liveBatches = map[string]*task.Batch{
		"b1": {ID: "b1", Name: "主任务", Status: task.StatusRunning},
	}
	m.fileCursor = 0

	press(&m, "e")
	assert.Nil(t, m.modal)
	assert.Contains(t, m.statusMsg, "无法编辑")

	m.liveBatches["b1"].Status = task.StatusPaused
	press(&m, "e")
	assert.Nil(t, m.modal)
	assert.Contains(t, m.statusMsg, "无法编辑")

	// 等待中的批次可以打开编辑表单。
	m.liveBatches["b1"].Status = task.StatusPending
	press(&m, "e")
	require.Equal(t, modalTask, m.modal.kind)
	press(&m, "esc")
	assert.Nil(t, m.modal)
}

func TestSavedTaskRemainsAfterRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := store.OpenDir(dir)
	require.NoError(t, err)
	task.BindStore(db)
	presets.Bind(db)
	t.Cleanup(func() {
		_ = db.Close()
		task.BindStore(nil)
		presets.Bind(nil)
	})

	m := newModel()
	m.db = db
	testQueue(t, &m)
	_, err = m.queue.Enqueue(testPlanOut(t, "a.mkv"), command.Selection{Convert: true})
	require.NoError(t, err)

	again := newModel()
	again.db = db
	again.initQueue(make(chan tea.Msg, 8))
	t.Cleanup(again.queue.Stop)
	deadline := time.Now().Add(2 * time.Second)
	for {
		again.syncRosterFromStore()
		if len(again.roster) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	require.NotEmpty(t, again.roster)
	assert.Contains(t, again.roster[0].title, "等待执行")
}

// TestRemovingPausedBatchEndsRun 删除暂停中的任务后界面要收尾，恢复空闲。
func TestRemovingPausedBatchEndsRun(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	testQueue(t, &m)
	m.running = true
	m.activeBatch = "b1"
	m.pausedBatch = "b1"

	next, _ := m.Update(queueEventMsg{evt: task.QueueEvent{Type: task.QueueBatchRemoved, BatchID: "b1"}})
	m = next.(model)
	assert.False(t, m.running, "删除暂停中的任务后应恢复空闲")
	assert.Empty(t, m.activeBatch)
	assert.Empty(t, m.pausedBatch)

	quit := pressCmd(&m, "q")
	require.NotNil(t, quit)
	_, ok := quit().(tea.QuitMsg)
	assert.True(t, ok, "删除后 q 直接退出，不再提示任务正在执行")
}

// TestRosterMouseNavigation 右键点任务行进入详情，左键点详情区回到任务列表。
func TestRosterMouseNavigation(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	m.roster = []rosterRow{
		{batchID: "b1", title: "任务一"},
		{batchID: "b1", itemID: "001", title: "1.mp4", state: fileRunning},
		{batchID: "b2", itemID: "002", title: "2.mp4", state: fileDone},
	}
	m.fileCursor = 0

	// 第 3 行（y=3，列表从 y=2 开始）是第二个可见行。
	next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseRight, X: 3, Y: 3})
	m = next.(model)
	assert.Equal(t, 1, m.fileCursor, "右键应把光标移到点击的任务行")
	assert.True(t, m.outputFocus, "右键应进入详情输出")

	next, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: m.leftW + 4, Y: 5})
	m = next.(model)
	next, _ = m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: m.leftW + 4, Y: 5})
	m = next.(model)
	assert.False(t, m.outputFocus, "左键点详情区应回到任务列表")
	assert.Equal(t, 1, m.fileCursor, "光标保持在被点击的任务行")

	next, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 3, Y: 2})
	m = next.(model)
	next, _ = m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 3, Y: 2})
	m = next.(model)
	assert.Equal(t, 0, m.fileCursor, "左键点任务行应选中该行")
	assert.False(t, m.outputFocus)
}

// ---------- 辅助 ----------

func taskModalText(m *model) string {
	var b strings.Builder
	for _, ln := range m.taskModalLines(80) {
		b.WriteString(ln.text)
	}
	return b.String()
}

func findField(fields *[]field, key string) (*field, bool) {
	for i := range *fields {
		if (*fields)[i].def.key == key {
			return &(*fields)[i], true
		}
	}
	return nil, false
}

// ctrl+d 删除光标所在的整组字幕（后缀/语言/标题/默认四个字段）：
// 光标在组内任意字段都行，在组外（操作、路径等）时只提示、不删。
func TestRemoveSubtitleFieldsByFocus(t *testing.T) {
	groupCount := func(m *model) int {
		n := 0
		for _, f := range m.modal.fields {
			if f.def.key == "sub_suffix" {
				n++
			}
		}
		return n
	}
	// open 打开创建弹窗并保证有 3 组字幕，各组后缀依次是 a、b、c。
	open := func(t *testing.T) *model {
		t.Helper()
		isolatePresets(t)
		m := newModel()
		press(&m, "a")
		press(&m, "ctrl+n")
		press(&m, "ctrl+n")
		suffixes := []string{"a", "b", "c"}
		g := 0
		for i := range m.modal.fields {
			if m.modal.fields[i].def.key == "sub_suffix" {
				m.modal.fields[i].input.SetValue(suffixes[g])
				g++
			}
		}
		require.Equal(t, 3, groupCount(&m))
		return &m
	}
	suffixOrder := func(m *model) []string {
		var out []string
		for _, f := range m.modal.fields {
			if f.def.key == "sub_suffix" {
				out = append(out, f.input.Value())
			}
		}
		return out
	}
	_, baseIdx := fieldByKey(open(t).modal.fields, "sub_suffix")

	for offset, key := range []string{"sub_suffix", "sub_lang", "sub_title", "sub_default"} {
		t.Run("删除第二组/光标在"+key, func(t *testing.T) {
			m := open(t)
			total := len(m.modal.fields)
			m.modal.focus = baseIdx + len(subFieldDefs) + offset
			require.Equal(t, key, m.modal.fields[m.modal.focus].def.key)
			m.removeSubtitleFields()
			assert.Equal(t, []string{"a", "c"}, suffixOrder(m), "只删光标所在的那一组")
			assert.Equal(t, total-len(subFieldDefs), len(m.modal.fields))
			assert.Less(t, m.modal.focus, len(m.modal.fields), "光标仍在有效范围内")
		})
	}

	t.Run("删除最后一组后光标收回到末尾", func(t *testing.T) {
		m := open(t)
		m.modal.focus = len(m.modal.fields) - 1
		m.removeSubtitleFields()
		assert.Equal(t, []string{"a", "b"}, suffixOrder(m))
		assert.Equal(t, len(m.modal.fields)-1, m.modal.focus)
	})

	t.Run("光标不在字幕组里不删除", func(t *testing.T) {
		m := open(t)
		total := len(m.modal.fields)
		for _, key := range []string{"op_convert", "input_path", "advance", "fonts_path"} {
			_, idx := fieldByKey(m.modal.fields, key)
			require.GreaterOrEqual(t, idx, 0)
			m.modal.focus = idx
			m.setStatus("")
			m.removeSubtitleFields()
			assert.Equal(t, total, len(m.modal.fields), "%s 不属于字幕组，不应删除任何字段", key)
			assert.Contains(t, m.statusMsg, "ctrl+d")
		}
		assert.Equal(t, []string{"a", "b", "c"}, suffixOrder(m))
	})
}

// 编辑一个已经执行过、且正在详情区查看的批次后，详情不能残留上一次的执行记录：
// 子任务按新计划重建，旧次数、旧失败计数、旧输出都清掉，状态回到等待。
func TestEditExecutedBatchResetsDetail(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 30
	m.resizePanes()
	testQueue(t, &m)
	batch, err := m.queue.Enqueue(testPlanOut(t, "a.mkv"), queueSelection())
	require.NoError(t, err)
	id := batch.ID

	// 模拟这个批次执行过且失败：详情区留着两次执行记录与失败计数。
	m.filesBatch = id
	m.total, m.failed = 1, 1
	m.files = []fileItem{{
		input: "a.mkv", state: fileFailed,
		attempts: []attemptLog{
			{n: 1, status: fileFailed, lines: []string{"boom"}},
			{n: 2, status: fileFailed, lines: []string{"boom2"}},
		},
	}}
	m.logs = []string{"旧日志"}

	newPlan := testPlanOut(t, "b.mkv", "c.mkv")
	m.applyTaskEdit(id, newPlan, queueSelection())

	snap, ok := m.queue.BatchCopy(id)
	require.True(t, ok)
	require.Equal(t, task.StatusPending, snap.Status, "编辑后批次回到等待")

	require.Len(t, m.files, 2, "详情区子任务应与新计划一致")
	require.Zero(t, m.failed, "旧失败计数应清零")
	require.Equal(t, 2, m.total)
	for _, f := range m.files {
		assert.Empty(t, f.attempts, "不应残留上一个任务的执行次数")
		assert.Equal(t, filePending, f.state, "新子任务都是等待")
	}
	assert.Empty(t, m.logs, "旧输出日志应清空")
}
