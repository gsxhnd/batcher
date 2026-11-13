package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/store"
	"github.com/gsxhnd/batcher/internal/task"
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
	for _, key := range []string{"op_convert", "op_subtitle", "op_fonts", "input_path", "output_path", "preset", "advance", "fonts_path", "sub_suffix"} {
		assert.True(t, keys[key], "创建弹窗应直接包含 %s", key)
	}
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
	m.syncTaskOutputPlaceholder()
	assert.Equal(t, defaultOutputDir("/videos/show"), out.resolved())
	assert.Empty(t, out.Value(), "跟着源目录走的输出仍是灰色占位")

	out.input.SetValue("/custom/out")
	m.syncTaskOutputPlaceholder()
	in.input.SetValue("/videos/other")
	m.syncTaskOutputPlaceholder()
	assert.Equal(t, "/custom/out", out.resolved(), "手填的输出目录保持不变")
}

func TestCreateTaskRejectsEmptySelection(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind)
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal, "没勾选操作时弹窗保持打开")
	assert.NotEmpty(t, m.modal.err)
	assert.Empty(t, m.queue.Snapshot())
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
	src := presets.Preset{Name: m.builtins[0].Name, Args: m.builtins[0].Args}
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
	assert.Contains(t, m.modal.err, "至少勾选")
	assert.Empty(t, m.queue.Snapshot())
	assert.False(t, m.running)

	m.modal.focus = 2 // 添加字体
	press(&m, "space")
	press(&m, "ctrl+s")
	assert.Contains(t, m.modal.err, "字体")
	assert.Empty(t, m.queue.Snapshot())

	m.modal.focus = 2
	press(&m, "space") // 取消字体
	m.modal.focus = 1  // 添加字幕
	press(&m, "space")
	m.modal.focus = len(m.modal.fields) - 1
	m.removeSubtitleFields()
	press(&m, "ctrl+s")
	assert.Contains(t, m.modal.err, "字幕")
	assert.Empty(t, m.queue.Snapshot())
	assert.False(t, m.running)
}

func TestEnterTaskListDoesNotEnqueue(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	testQueue(t, &m)

	press(&m, "enter")
	assert.Nil(t, m.modal, "任务列表里按回车不入队")
	assert.Empty(t, m.queue.Snapshot())
	press(&m, "x")
	assert.Empty(t, m.queue.Snapshot(), "按 x 不入队")
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind, "按 a 打开创建弹窗")
	assert.Empty(t, m.queue.Snapshot(), "打开弹窗还不入队")
}

func TestFormEscDoesNotQuitAndFocusKept(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.focus = panelForm

	press(&m, "up")
	assert.Equal(t, panelForm, m.focus, "方向键不离开当前焦点")
	press(&m, "esc")
	assert.Nil(t, m.modal, "没有弹窗时 esc 不退出程序")
}

func TestSetFocusLeavesRunStopsFollow(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.focus = panelRun
	m.fileFollow = true
	m.running = false

	m.setFocus(panelOps)
	assert.Equal(t, panelOps, m.focus)
	assert.False(t, m.fileFollow, "离开任务面板应取消自动跟随")
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
	before := len(m.queue.Snapshot())
	press(&m, "a")
	require.Equal(t, modalTask, m.modal.kind, "执行中仍可打开创建弹窗")
	assert.Equal(t, before, len(m.queue.Snapshot()), "打开弹窗还不入队")
	press(&m, "esc")
	assert.Nil(t, m.modal)
	m.enqueue(testPlanOut(t, "b.mkv"), command.Selection{Convert: true})
	assert.Len(t, m.queue.Snapshot(), before+1, "执行中创建的主任务排在后面")
	assert.Contains(t, m.statusMsg, "按 x 开始")
	press(&m, "enter")
	assert.Len(t, m.queue.Snapshot(), before+1, "任务列表回车不入队")

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

	m.running = true
	m.pausedBatch = "batch"
	press(&m, "q")
	require.NotNil(t, m.modal)
	assert.Equal(t, "batch", m.pausedBatch, "确认退出时还不中止")
	press(&m, "esc")
	press(&m, "esc")
	assert.Contains(t, m.statusMsg, "中止", "暂停中 esc 中止当前任务")
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
	assert.Empty(t, m.queue.Snapshot(), "确认前不应入队")

	press(&m, "enter")
	assert.False(t, m.modal.jobOpen[0], "enter 收起当前子任务")
	assert.True(t, m.modal.jobOpen[1])
	folded := m.modalView()
	assert.NotContains(t, folded, "/v/a.chi.ass")
	assert.Contains(t, folded, "/v/b.mkv")
	assert.Contains(t, folded, "/fonts/a.ttf", "共用字体不受子任务收起影响")
	assert.Empty(t, m.queue.Snapshot(), "收起不应入队")

	press(&m, "enter")
	assert.True(t, m.modal.jobOpen[0])
	assert.Contains(t, m.modalView(), "/v/a.chi.ass")

	press(&m, "esc")
	assert.Nil(t, m.modal)
	assert.Empty(t, m.queue.Snapshot())

	m.createSeq = 2
	m.modal = &modal{kind: modalTask}
	next, _ = m.Update(createPlanMsg{seq: 2, plan: plan})
	m = next.(model)
	press(&m, "ctrl+s")
	require.Len(t, m.queue.Snapshot(), 1)
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
