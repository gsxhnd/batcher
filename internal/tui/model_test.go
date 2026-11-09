package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolatePresets 把自定义预设目录指向临时文件夹，避免测试读写真实预设文件。
func isolatePresets(t *testing.T) {
	t.Helper()
	t.Setenv("BATCHER_PRESETS_DIR", t.TempDir())
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

func TestPanelSwitchSkipsDisabledPanels(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{} // 未勾选任何操作：预设/字幕面板不可用

	press(&m, "2")
	assert.Equal(t, panelOps, m.focus, "未勾选转码时不能进入预设面板")
	assert.NotEmpty(t, m.statusMsg, "应提示面板不可用的原因")

	press(&m, "3")
	assert.Equal(t, panelOps, m.focus, "未勾选字幕时不能进入字幕面板")

	press(&m, "4")
	assert.Equal(t, panelForm, m.focus, "参数面板始终可用")

	press(&m, "5")
	assert.Equal(t, panelRun, m.focus, "任务面板始终可用")
}

func TestTabCyclesToNextEnabledPanel(t *testing.T) {
	isolatePresets(t)
	m := newModel() // 默认聚焦 [1] 操作

	press(&m, "tab")
	assert.Equal(t, panelForm, m.focus, "预设/字幕不可用时应跳到参数面板")

	press(&m, "tab")
	assert.Equal(t, panelRun, m.focus)

	press(&m, "tab")
	assert.Equal(t, panelOps, m.focus, "循环回到第一个面板")
}

func TestToggleConvertEnablesAndRebuildsForm(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]presets.Preset{{Name: "画质", Args: "-c:v h265"}}))
	m := newModel()
	m.ops = ffmpeg.Selection{AddSubtitle: true}
	m.rebuildFields()
	require.False(t, slicesAny(m.fields, "advance"), "未勾选转码时不应有高级参数字段")

	press(&m, "2") // 未勾选转码，不可进入
	assert.Equal(t, panelOps, m.focus)

	press(&m, "space") // 第一个操作为视频转码
	require.True(t, m.ops.Convert)
	require.True(t, slicesAny(m.fields, "advance"), "勾选转码后应出现高级参数字段")

	press(&m, "2")
	assert.Equal(t, panelPresets, m.focus, "勾选转码后预设面板可用")

	press(&m, "enter") // 选择第一个内置预设
	advance, ok := findField(&m.fields, "advance")
	require.True(t, ok)
	assert.Equal(t, m.builtins[0].Args, m.manualAdvance)
	assert.Equal(t, m.builtins[0].Args, advance.input.Value(), "选中的预设参数应同步到主表单")

	press(&m, "1")     // 回到操作面板
	press(&m, "space") // 再按一次取消转码
	assert.False(t, m.ops.Convert)
	assert.False(t, slicesAny(m.fields, "advance"), "取消转码后应移除高级参数字段")

	press(&m, "2")
	assert.Equal(t, panelOps, m.focus, "取消转码后预设面板不可用，不能进入")
}

// TestPresetModalAddEditDuplicate 验证预设的新增/编辑全部在弹窗内完成。
func TestPresetModalAddEditDuplicate(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.focus = panelPresets

	press(&m, "a")
	require.NotNil(t, m.modal, "按 a 应打开新增弹窗")
	require.Equal(t, 2, len(m.modal.fields), "弹窗应包含名称与参数两个字段")
	fill := func(name, args string) {
		m.modal.fields[0].input.SetValue(name)
		m.modal.fields[1].input.SetValue(args)
		m.modal.focus = 1
	}

	fill("  H.265 画质  ", "  -c:v   h265 -crf 20 ")
	press(&m, "enter")
	require.Nil(t, m.modal, "保存成功后应关闭弹窗")
	assert.Equal(t, []presets.Preset{{Name: "H.265 画质", Args: "-c:v h265 -crf 20"}}, m.presets,
		"名称去首尾空白，参数规范化空白")

	press(&m, "a")
	fill("", "-c:v h264")
	press(&m, "enter")
	assert.Equal(t, []presets.Preset{
		{Name: "H.265 画质", Args: "-c:v h265 -crf 20"},
		{Name: "", Args: "-c:v h264"},
	}, m.presets, "名称可留空")

	press(&m, "a")
	fill("重复", "-c:v h265 -crf 20")
	press(&m, "enter")
	require.NotNil(t, m.modal, "参数重复应留在弹窗内修改")
	assert.NotEmpty(t, m.modal.err)
	press(&m, "esc")
	assert.Nil(t, m.modal, "esc 应取消弹窗")

	// 编辑第二条自定义预设
	m.presetCursor = m.presetUserOffset() + 1
	press(&m, "e")
	require.NotNil(t, m.modal)
	m.modal.fields[0].input.SetValue("H.264")
	m.modal.fields[1].input.SetValue("-c:v hevc")
	m.modal.focus = 1
	press(&m, "enter")
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
	m.ops = ffmpeg.Selection{Convert: true}
	m.focus = panelPresets
	m.presetCursor = m.presetUserOffset()
	m.manualAdvance = "a" // 删除当前选中预设时应同步清空主表单

	press(&m, "d")
	require.NotNil(t, m.modal, "第一次按 d 应弹确认窗")
	assert.Equal(t, modalConfirm, m.modal.kind)

	press(&m, "esc")
	assert.Nil(t, m.modal)
	assert.Len(t, m.presets, 2, "esc 取消删除")

	press(&m, "d")
	press(&m, "enter")
	assert.Equal(t, []presets.Preset{{Args: "b"}}, m.presets, "enter 确认后删除")
	assert.Equal(t, "", m.manualAdvance, "删除当前选中预设后应清空已选参数")

	// 其他按键不应误触发弹窗内的确认
	m.modal = nil
	press(&m, "d")
	press(&m, "j")
	assert.NotNil(t, m.modal, "确认弹窗内按无关键应保持打开")
}

func TestPresetReorderPersists(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]presets.Preset{{Args: "a"}, {Args: "b"}, {Args: "c"}}))
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.focus = panelPresets
	m.presetCursor = m.presetUserOffset()

	press(&m, "J")
	assert.Equal(t, []presets.Preset{{Args: "b"}, {Args: "a"}, {Args: "c"}}, m.presets)
	assert.Equal(t, m.presetUserOffset()+1, m.presetCursor, "移动后光标跟随条目")

	loaded, err := presets.Load()
	require.NoError(t, err)
	assert.Equal(t, []presets.Preset{{Args: "b"}, {Args: "a"}, {Args: "c"}}, loaded, "顺序应持久化")
}

func TestPresetGuardsBuiltins(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.focus = panelPresets
	m.presetCursor = 0 // 内置条目

	press(&m, "d")
	assert.Empty(t, m.presets, "内置预设不可删除")
	assert.NotEmpty(t, m.statusMsg)

	press(&m, "e")
	assert.Nil(t, m.modal, "内置条目不进入编辑弹窗")
	assert.NotEmpty(t, m.statusMsg, "内置条目按 e 应提示只读")
}

func TestBuiltinCopyPresetFromPanelPresets(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.focus = panelPresets
	require.NotEmpty(t, m.builtins)

	press(&m, "a")
	require.NotNil(t, m.modal, "选中内置预设按 a 应打开复制弹窗")
	assert.Equal(t, m.builtins[0].Name, m.modal.fields[0].input.Value(), "名称预填内置预设名")
	assert.Equal(t, m.builtins[0].Args, m.modal.fields[1].input.Value(), "参数预填内置预设参数")

	m.modal.focus = 1
	press(&m, "enter")
	require.Nil(t, m.modal)
	assert.Equal(t, []presets.Preset{{Name: m.builtins[0].Name, Args: m.builtins[0].Args}}, m.presets,
		"复制后成为自定义预设")
}

// TestModalSwallowsGlobalKeys 弹窗打开时全局快捷键不应生效。
func TestModalSwallowsGlobalKeys(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.focus = panelPresets

	press(&m, "a")
	require.NotNil(t, m.modal)
	press(&m, "1")
	require.NotNil(t, m.modal, "数字键不应触发面板切换")
	press(&m, "x")
	require.NotNil(t, m.modal, "x 不应触发执行")
	assert.False(t, m.running)
	assert.Equal(t, panelPresets, m.focus, "弹窗期间焦点不变")
}

// TestSublistModalCRUD 验证字幕面板的增删改在弹窗内完成。
func TestSublistModalCRUD(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{AddSubtitle: true}
	m.focus = panelSubs

	press(&m, "n")
	require.NotNil(t, m.modal)
	m.modal.fields[0].input.SetValue("ass")
	m.modal.fields[1].input.SetValue("zh") // 非法语言码
	m.modal.fields[2].input.SetValue("中文")
	m.modal.focus = 3 // 最后一个字段（默认字幕流）后按 enter 保存
	press(&m, "enter")
	require.NotNil(t, m.modal, "非法语言码应留在弹窗")
	assert.NotEmpty(t, m.modal.fields[1].err)
	assert.Nil(t, findSub(m.subs, "ass"), "不应保存非法条目")

	m.modal.fields[1].input.SetValue("chi")
	press(&m, "enter")
	assert.Nil(t, m.modal)
	require.Len(t, m.subs, 1)
	assert.Equal(t, ffmpeg.SubtitleOption{Suffix: "ass", Lang: "chi", Title: "中文"}, m.subs[0])

	press(&m, "e")
	require.NotNil(t, m.modal, "e 应打开编辑弹窗并回填")
	assert.Equal(t, "ass", m.modal.fields[0].input.Value())
	press(&m, "esc")

	press(&m, "d")
	require.NotNil(t, m.modal, "删除应弹确认窗")
	press(&m, "enter")
	assert.Empty(t, m.subs, "确认后删除")
}

// TestSubModalDefaultTrack 验证弹窗内默认字幕流可勾选，且面板 3 按 t 可切换。
func TestSubModalDefaultTrack(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{AddSubtitle: true}
	m.focus = panelSubs

	press(&m, "n")
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
	press(&m, "enter") // 保存

	assert.Nil(t, m.modal)
	require.Len(t, m.subs, 1)
	assert.True(t, m.subs[0].Default, "保存后应写入默认轨道标记")

	// 面板 3 按 t 切换默认标记
	press(&m, "t")
	assert.False(t, m.subs[0].Default, "t 应取消默认标记")
	press(&m, "t")
	assert.True(t, m.subs[0].Default, "t 应重新标记默认")

	// 再编辑一条时勾选状态应回填
	press(&m, "e")
	require.NotNil(t, m.modal)
	assert.True(t, m.modal.fields[3].Checked(), "编辑弹窗应回填默认标记")
	press(&m, "esc")
}

func TestFormEditingSwallowsGlobalKeys(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.rebuildFields()
	m.focus = panelForm

	press(&m, "enter")
	require.True(t, m.editing, "enter 应进入编辑态")

	m.fields[0].input.SetValue("") // 清掉默认值，便于断言输入
	press(&m, "1")
	assert.Equal(t, panelForm, m.focus, "编辑态下数字键应输入而不切换面板")
	assert.Equal(t, "1", m.fields[0].input.Value(), "字符应输入到字段里")

	press(&m, "esc")
	assert.False(t, m.editing, "esc 应退出编辑态")
	press(&m, "1")
	assert.Equal(t, panelOps, m.focus, "退出编辑态后全局快捷键恢复生效")
}

func TestTryStartRunValidation(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	// 未勾选任何操作
	press(&m, "x")
	assert.NotEmpty(t, m.featErr, "未勾选操作应提示")
	assert.Equal(t, panelOps, m.focus)
	assert.False(t, m.running)

	// 勾选添加字体但未填字体目录
	m.ops = ffmpeg.Selection{AddFonts: true}
	m.rebuildFields()
	press(&m, "x")
	assert.Equal(t, panelForm, m.focus, "校验失败应跳到参数面板")
	assert.NotEmpty(t, m.statusMsg)
	assert.False(t, m.running)

	// 勾选添加字幕但字幕列表为空
	m.ops = ffmpeg.Selection{AddSubtitle: true}
	m.rebuildFields()
	press(&m, "x")
	assert.Equal(t, panelSubs, m.focus, "字幕为空应跳到字幕面板")
	assert.False(t, m.running)
}

// TestPlanMsgStaleDiscard 验证过期 Plan 结果被丢弃。
func TestPlanMsgStaleDiscard(t *testing.T) {
	m := newModel()
	m.planSeq = 3
	m.planLoading = true

	next, _ := m.Update(planMsg{seq: 2, cmds: [][]string{{"-i", "a.mkv", "o.mkv"}}})
	m = next.(model)
	assert.False(t, m.previewDone, "过期结果不应生效")
	assert.Nil(t, m.files)

	next, _ = m.Update(planMsg{seq: 3, cmds: [][]string{{"-i", "a.mkv", "o.mkv"}}})
	m = next.(model)
	assert.True(t, m.previewDone)
	require.Len(t, m.files, 1)
}

func TestFormEscDoesNotQuitAndFocusKept(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.focus = panelForm

	press(&m, "up")
	assert.Equal(t, panelForm, m.focus, "方向键只移动字段焦点的显示")
	press(&m, "5")
	assert.Equal(t, panelRun, m.focus)
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
	m.running = true
	m.focus = panelRun

	press(&m, "1")
	assert.Equal(t, panelRun, m.focus, "执行中不允许切换面板")

	// q 取消执行但不退出（cancel 已设置才会真正取消）
	var canceled bool
	m.cancel = func() { canceled = true }
	press(&m, "q")
	assert.True(t, canceled, "执行中按 q 应发送取消请求")
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

func findSub(subs []ffmpeg.SubtitleOption, suffix string) *ffmpeg.SubtitleOption {
	for i := range subs {
		if subs[i].Suffix == suffix {
			return &subs[i]
		}
	}
	return nil
}
