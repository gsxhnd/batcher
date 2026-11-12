package tui

import (
	"strings"
	"testing"

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
	case "pgup":
		key = tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		key = tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "end":
		key = tea.KeyPressMsg{Code: tea.KeyEnd}
	default:
		key = tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
	}
	next, _ := m.Update(key)
	*m = next.(model)
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
	m.ops = command.Selection{Convert: true}
	m.focus = panelPresets
	m.presetCursor = m.presetUserOffset()
	m.manualAdvance = "a" // 删除当前选中预设时应同步清空主表单

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
	assert.Equal(t, "", m.manualAdvance, "删除当前选中预设后应清空已选参数")
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

// TestSublistModalCRUD 验证字幕面板的增删改在弹窗内完成。
func TestSublistModalCRUD(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.openSubModal(-1)
	require.NotNil(t, m.modal)
	m.modal.fields[0].input.SetValue("ass")
	m.modal.fields[1].input.SetValue("zh") // 非法语言码
	m.modal.fields[2].input.SetValue("中文")
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal, "非法语言码应留在弹窗")
	assert.NotEmpty(t, m.modal.fields[1].err)
	assert.Nil(t, findSub(m.subs, "ass"), "不应保存非法条目")

	m.modal.fields[1].input.SetValue("chi")
	press(&m, "ctrl+s")
	assert.Nil(t, m.modal)
	require.Len(t, m.subs, 1)
	assert.Equal(t, command.SubtitleOption{Suffix: "ass", Lang: "chi", Title: "中文"}, m.subs[0])

	m.openSubModal(0)
	require.NotNil(t, m.modal, "编辑弹窗应回填")
	assert.Equal(t, "ass", m.modal.fields[0].input.Value())
	press(&m, "esc")

	m.updateSubsPanel(tea.KeyPressMsg{Text: "d", Code: 'd'})
	require.NotNil(t, m.modal, "删除应弹确认窗")
	press(&m, "enter")
	assert.Empty(t, m.subs, "确认后删除")
}

// TestSubModalDefaultTrack 验证弹窗内默认字幕流可勾选，且面板 3 按 t 可切换。
func TestSubModalDefaultTrack(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.openSubModal(-1)
	require.NotNil(t, m.modal)
	require.Len(t, m.modal.fields, 4, "弹窗应包含后缀/语言/标题/默认轨道四个字段")
	m.modal.fields[0].input.SetValue("chi.ass")
	m.modal.fields[1].input.SetValue("chi")
	m.modal.fields[2].input.SetValue("简体中文")
	m.modal.focus = 3
	require.False(t, m.modal.fields[3].Checked(), "默认轨道初始未勾选")

	press(&m, "space")
	require.True(t, m.modal.fields[3].Checked(), "space 应勾选默认轨道")
	press(&m, "space")
	require.False(t, m.modal.fields[3].Checked(), "再按 space 应取消勾选")
	press(&m, "space")
	press(&m, "ctrl+s") // 保存

	assert.Nil(t, m.modal)
	require.Len(t, m.subs, 1)
	assert.True(t, m.subs[0].Default, "保存后应写入默认轨道标记")

	m.updateSubsPanel(tea.KeyPressMsg{Text: "t", Code: 't'})
	assert.False(t, m.subs[0].Default, "切换应取消默认标记")
	m.updateSubsPanel(tea.KeyPressMsg{Text: "t", Code: 't'})
	assert.True(t, m.subs[0].Default, "再切换应重新标记默认")

	m.openSubModal(0)
	require.NotNil(t, m.modal)
	assert.True(t, m.modal.fields[3].Checked(), "编辑弹窗应回填默认标记")
	press(&m, "esc")
}

func TestFormDefaultsStayAsPlaceholder(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{Convert: true}
	m.rebuildFields()
	d := command.DefaultOption()
	checks := map[string]string{
		"input_path":    d.InputPath,
		"input_format":  d.InputFormat,
		"output_path":   d.OutputPath,
		"output_format": d.OutputFormat,
	}
	for key, want := range checks {
		f, ok := findField(&m.fields, key)
		require.True(t, ok, key)
		assert.Empty(t, f.input.Value(), "%s 默认值不应写成白色正文", key)
		assert.Equal(t, want, f.input.Placeholder, key)
		assert.Equal(t, want, f.resolved(), key)
	}
	opt, err := m.buildOption()
	require.NoError(t, err)
	assert.Equal(t, d.InputPath, opt.InputPath)
	assert.Equal(t, d.InputFormat, opt.InputFormat)
	assert.Equal(t, d.OutputPath, opt.OutputPath)
	assert.Equal(t, d.OutputFormat, opt.OutputFormat)

	path, _ := findField(&m.fields, "input_path")
	path.input.SetValue("/videos")
	opt, err = m.buildOption()
	require.NoError(t, err)
	assert.Equal(t, "/videos", opt.InputPath, "用户输入应覆盖默认值")
}

func TestFormEditingSwallowsGlobalKeys(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{Convert: true}
	m.rebuildFields()
	m.beginFormEdit()
	require.True(t, m.editing)

	m.fields[0].input.SetValue("")
	press(&m, "1")
	assert.True(t, m.editing, "编辑态下数字键应输入")
	assert.Equal(t, "1", m.fields[0].input.Value(), "字符应输入到字段里")

	press(&m, "esc")
	assert.False(t, m.editing, "esc 应退出编辑态")
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

// TestPlanMsgStaleDiscard 验证过期 Plan 结果被丢弃。
func TestPlanMsgStaleDiscard(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.planSeq = 3
	m.planLoading = true

	next, _ := m.Update(planMsg{seq: 2, plan: testPlan("a.mkv")})
	m = next.(model)
	assert.False(t, m.previewDone, "过期结果不应生效")
	assert.Nil(t, m.files)

	next, _ = m.Update(planMsg{seq: 3, plan: testPlan("a.mkv")})
	m = next.(model)
	assert.True(t, m.previewDone)
	require.Len(t, m.files, 1)
}

func TestEnterTaskListDoesNotEnqueue(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops.Convert = true
	m.rebuildFields()
	testQueue(t, &m)
	m.plan, m.previewDone = testPlan("a.mkv"), true

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
	m.ops.Convert = true
	m.rebuildFields()
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
	m.enqueue(testPlanOut(t, "b.mkv"))
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

	// q/esc 取消当前批次：非暂停时走 CancelCurrent，界面保持执行态直到结束。
	m.pausedBatch = ""
	press(&m, "q")
	assert.Contains(t, m.statusMsg, "取消请求")
	assert.True(t, m.running, "取消是异步的，界面保持执行态直到结束")
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

// ---------- 辅助 ----------

func taskModalText(m *model) string {
	var b strings.Builder
	for _, ln := range m.taskModalLines(80) {
		b.WriteString(ln.text)
	}
	return b.String()
}

func slicesAny(fields []field, key string) bool {
	for _, f := range fields {
		if f.def.key == key {
			return true
		}
	}
	return false
}

func findField(fields *[]field, key string) (*field, bool) {
	for i := range *fields {
		if (*fields)[i].def.key == key {
			return &(*fields)[i], true
		}
	}
	return nil, false
}

func findSub(subs []command.SubtitleOption, suffix string) *command.SubtitleOption {
	for i := range subs {
		if subs[i].Suffix == suffix {
			return &subs[i]
		}
	}
	return nil
}
