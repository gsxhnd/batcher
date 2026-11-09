package tui

import (
	"os"
	"path/filepath"
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
	default:
		key = tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
	}
	next, _ := m.Update(key)
	*m = next.(model)
}

func TestFormBackReturnsToSubtitlePage(t *testing.T) {
	m := newModel()
	m.ops = ffmpeg.Selection{AddSubtitle: true}
	m.subs = []ffmpeg.SubtitleOption{{Suffix: "ass", Lang: "chi"}}
	m.phase = phaseSubs

	press(&m, "enter")
	require.Equal(t, phaseForm, m.phase)

	press(&m, "esc")
	assert.Equal(t, phaseSubs, m.phase, "esc 应返回字幕列表而不是第一页")
}

func TestFormBackWithConvertAndSubtitle(t *testing.T) {
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true, AddSubtitle: true}
	m.subs = []ffmpeg.SubtitleOption{{Suffix: "ass", Lang: "chi"}}
	m.openForm()

	press(&m, "esc")
	assert.Equal(t, phaseSubs, m.phase, "表单 esc 应回到字幕列表")

	press(&m, "esc")
	assert.Equal(t, phaseAdvance, m.phase, "字幕列表 esc 应回到转码页")
}

func TestSubtitleListBackGoesToFeatureWithoutConvert(t *testing.T) {
	m := newModel()
	m.ops = ffmpeg.Selection{AddSubtitle: true}
	m.phase = phaseSubs

	press(&m, "esc")
	assert.Equal(t, phaseFeature, m.phase)
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

func TestSaveSubEditRejectsBadLanguage(t *testing.T) {
	m := newModel()
	m.ops = ffmpeg.Selection{AddSubtitle: true}
	m.openSubEdit(-1)
	m.subFields[0].input.SetValue("ass")
	m.subFields[1].input.SetValue("zh")

	m.saveSubEdit()
	assert.Empty(t, m.subs, "非法语言码不应保存")
	assert.NotEmpty(t, m.subFields[1].err)
	assert.Equal(t, phaseSubEdit, m.phase)
}

// setPresetEdit 在编辑表单中填入名称与参数（按 presetFieldDefs 的顺序）。
// 字段值直接写入输入框，焦点停在最后一项，随后按 Enter 即可提交。
func setPresetEdit(m *model, name, args string) {
	m.mgrFields[0].input.SetValue(name)
	m.mgrFields[1].input.SetValue(args)
	m.mgrFocus = len(m.mgrFields) - 1
}

func TestPresetsAddEditDuplicateAndDelete(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.phase = phaseFeature

	press(&m, "p")
	require.Equal(t, phasePresets, m.phase)
	assert.Equal(t, phaseFeature, m.mgrReturn)

	press(&m, "a")
	require.Equal(t, phasePresetEdit, m.phase)
	require.Len(t, m.mgrFields, 2, "编辑表单应包含名称与参数两个字段")
	setPresetEdit(&m, "  H.265 画质  ", "  -c:v   h265 -crf 20 ")
	press(&m, "enter")
	require.Equal(t, phasePresets, m.phase)
	assert.Equal(t, []presets.Preset{{Name: "H.265 画质", Args: "-c:v h265 -crf 20"}}, m.presets, "名称去首尾空白，参数规范化空白")

	press(&m, "a")
	setPresetEdit(&m, "", "-c:v h264")
	press(&m, "enter")
	assert.Equal(t, []presets.Preset{
		{Name: "H.265 画质", Args: "-c:v h265 -crf 20"},
		{Name: "", Args: "-c:v h264"},
	}, m.presets, "名称可留空")

	press(&m, "a")
	setPresetEdit(&m, "重复", "-c:v h265 -crf 20")
	press(&m, "enter")
	assert.Equal(t, phasePresetEdit, m.phase, "参数重复应拒绝")
	assert.NotEmpty(t, m.mgrErr)
	press(&m, "esc")

	m.mgrCursor = 1
	press(&m, "e")
	assert.Equal(t, "", m.mgrFields[0].Value(), "编辑应回填名称")
	assert.Equal(t, "-c:v h264", m.mgrFields[1].Value(), "编辑应回填参数")
	setPresetEdit(&m, "H.264", "-c:v hevc")
	press(&m, "enter")
	assert.Equal(t, []presets.Preset{
		{Name: "H.265 画质", Args: "-c:v h265 -crf 20"},
		{Name: "H.264", Args: "-c:v hevc"},
	}, m.presets)

	m.mgrCursor = 0
	press(&m, "d")
	press(&m, "d")
	assert.Equal(t, []presets.Preset{{Name: "H.264", Args: "-c:v hevc"}}, m.presets, "自定义预设应在二次确认后删除")

	press(&m, "esc")
	assert.Equal(t, phaseFeature, m.phase, "esc 应回到打开它的操作页")
}

func TestPresetEditTabMovesBetweenFields(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.phase = phasePresets
	press(&m, "a")
	require.Equal(t, 0, m.mgrFocus)

	press(&m, "tab")
	assert.Equal(t, 1, m.mgrFocus)
	press(&m, "enter")
	assert.Equal(t, 1, m.mgrFocus, "最后一项回车提交，焦点不再前进")
	assert.NotEmpty(t, m.mgrErr, "参数为空时应停留在编辑页并提示")
	assert.Equal(t, phasePresetEdit, m.phase)
}

func TestPresetsReorder(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]presets.Preset{{Args: "a"}, {Args: "b"}, {Args: "c"}}))
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.phase = phaseAdvance
	press(&m, "p")

	press(&m, "down")
	press(&m, "J")
	assert.Equal(t, []presets.Preset{{Args: "a"}, {Args: "c"}, {Args: "b"}}, m.presets)
	assert.Equal(t, 2, m.mgrCursor, "移动后光标跟随条目")

	loaded, err := presets.Load()
	require.NoError(t, err)
	assert.Equal(t, []presets.Preset{{Args: "a"}, {Args: "c"}, {Args: "b"}}, loaded, "顺序应持久化")
}

func TestPresetsEscReturnsToAdvancePage(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.phase = phaseAdvance
	press(&m, "p")
	press(&m, "esc")
	assert.Equal(t, phaseAdvance, m.phase)
}

func TestAdvancePageSelectsBuiltinPreset(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.phase = phaseFeature
	press(&m, "enter")
	require.Equal(t, phaseAdvance, m.phase)
	require.NotEmpty(t, m.builtins, "应有内置预设")

	press(&m, "enter")
	assert.Equal(t, m.builtins[0].Args, m.manualAdvance, "默认选中第一条内置预设")

	// 回到转码参数页，光标移到手动输入项并继续，不改变高级参数
	m.phase = phaseAdvance
	m.presetCursor = m.presetCount() - 1
	press(&m, "enter")
	assert.Equal(t, "", m.manualAdvance, "手动输入参数应为空")
}

func TestAdvancePageSelectsCustomPresetArgs(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.builtins = presets.Builtins()
	m.presets = []presets.Preset{{Name: "我的预设", Args: "-c:v hevc -crf 22"}}
	m.phase = phaseAdvance
	m.presetCursor = m.advanceUserOffset()

	press(&m, "enter")
	assert.Equal(t, "-c:v hevc -crf 22", m.manualAdvance, "选中自定义预设应使用其参数")
}

func TestAdvancePageDeleteGuardsBuiltins(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.builtins = presets.Builtins()
	m.presets = []presets.Preset{{Args: "-c copy 2"}}
	m.phase = phaseAdvance
	m.presetCursor = 0 // 选中第一条内置预设

	press(&m, "d")
	assert.NotEmpty(t, m.statusMsg, "内置预设删除应提示不可删除")
	assert.Equal(t, []presets.Preset{{Args: "-c copy 2"}}, m.presets, "内置条目不应影响自定义列表")

	// 光标跨过内置区删除自定义条目（需按两次 d 确认）
	m.presetCursor = len(m.builtins)
	press(&m, "d")
	assert.Equal(t, []presets.Preset{{Args: "-c copy 2"}}, m.presets, "第一次按 d 只进入待确认状态")
	press(&m, "d")
	assert.Empty(t, m.presets, "第二次按 d 才应删除")
}

func TestAdvancePageCopyBuiltinToCustom(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.builtins = presets.Builtins()
	m.phase = phaseAdvance
	m.presetCursor = 0

	press(&m, "a")
	require.Equal(t, phasePresetEdit, m.phase)
	assert.Equal(t, m.builtins[0].Name, m.mgrFields[0].Value(), "复制时名称预填为内置预设名")
	assert.Equal(t, m.builtins[0].Args, m.mgrFields[1].Value(), "复制时参数预填为内置预设参数")

	press(&m, "tab")
	press(&m, "enter")
	require.Equal(t, phasePresets, m.phase)
	assert.Equal(t, []presets.Preset{{Name: m.builtins[0].Name, Args: m.builtins[0].Args}}, m.presets, "复制后应成为自定义预设")
}

func TestPresetDeleteRequiresDoubleConfirm(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.builtins = presets.Builtins()
	m.presets = []presets.Preset{{Args: "-c:v h264"}, {Args: "-c:v h265"}}
	m.phase = phaseAdvance
	m.presetCursor = len(m.builtins) // 第一条自定义预设

	both := []presets.Preset{{Args: "-c:v h264"}, {Args: "-c:v h265"}}
	press(&m, "d")
	assert.Equal(t, both, m.presets, "第一次按 d 不应删除")
	assert.NotEmpty(t, m.statusMsg, "第一次按 d 应给出确认提示")
	assert.Equal(t, 0, m.delPendingIdx, "应记录待删除项")

	// 其他任意键取消确认
	press(&m, "up")
	assert.Equal(t, -1, m.delPendingIdx, "其他按键应取消删除确认")
	press(&m, "down")
	press(&m, "d")
	assert.Equal(t, both, m.presets, "取消后单次 d 不应删除")

	// 连续两次 d 执行删除
	press(&m, "d")
	assert.Equal(t, []presets.Preset{{Args: "-c:v h265"}}, m.presets, "第二次 d 完成删除")
	assert.Equal(t, -1, m.delPendingIdx, "删除后应清理待确认状态")

	// 对剩余条目可重新进入待确认
	press(&m, "d")
	assert.Equal(t, 0, m.delPendingIdx, "删除后可以对剩余条目重新确认")
	assert.Equal(t, []presets.Preset{{Args: "-c:v h265"}}, m.presets)
}

func TestPresetMgrDeleteDoubleConfirmAndCancel(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]presets.Preset{{Args: "a"}, {Args: "b"}, {Args: "c"}}))
	m := newModel()
	m.phase = phaseFeature
	press(&m, "p")
	require.Equal(t, phasePresets, m.phase)

	press(&m, "d")
	assert.Equal(t, []presets.Preset{{Args: "a"}, {Args: "b"}, {Args: "c"}}, m.presets, "第一次按 d 不应删除")
	assert.Equal(t, 0, m.delPendingIdx)

	// esc 直接返回，同样取消确认
	press(&m, "esc")
	require.Equal(t, phaseFeature, m.phase)

	press(&m, "p")
	require.Equal(t, phasePresets, m.phase)
	assert.Equal(t, -1, m.delPendingIdx, "重新进入管理页应重置确认状态")
	press(&m, "d")
	press(&m, "d")
	assert.Equal(t, []presets.Preset{{Args: "b"}, {Args: "c"}}, m.presets)
}

func TestPresetsLoadLegacyFileInManager(t *testing.T) {
	isolatePresets(t)
	dir, err := presets.Dir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, presets.PresetFile), []byte(`["-c copy"]`), 0o644))

	m := newModel()
	m.phase = phaseFeature
	press(&m, "p")
	require.Equal(t, phasePresets, m.phase)
	require.Equal(t, []presets.Preset{{Args: "-c copy"}}, m.presets, "旧版参数列表应可直接显示与管理")
}
