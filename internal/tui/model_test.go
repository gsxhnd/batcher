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
	m.mgrInput.SetValue("  -c:v   h265 -crf 20 ")
	press(&m, "enter")
	require.Equal(t, phasePresets, m.phase)
	assert.Equal(t, []string{"-c:v h265 -crf 20"}, m.presets, "应规范化空白")

	press(&m, "a")
	m.mgrInput.SetValue("-c:v h264")
	press(&m, "enter")
	assert.Equal(t, []string{"-c:v h265 -crf 20", "-c:v h264"}, m.presets)

	press(&m, "a")
	m.mgrInput.SetValue("-c:v h265 -crf 20")
	press(&m, "enter")
	assert.Equal(t, phasePresetEdit, m.phase, "重复参数应拒绝")
	assert.NotEmpty(t, m.mgrErr)
	press(&m, "esc")

	m.mgrCursor = 1
	press(&m, "e")
	m.mgrInput.SetValue("-c:v hevc")
	press(&m, "enter")
	assert.Equal(t, []string{"-c:v h265 -crf 20", "-c:v hevc"}, m.presets)

	m.mgrCursor = 0
	press(&m, "d")
	press(&m, "d")
	assert.Equal(t, []string{"-c:v hevc"}, m.presets, "自定义预设应在二次确认后删除")

	press(&m, "esc")
	assert.Equal(t, phaseFeature, m.phase, "esc 应回到打开它的操作页")
}

func TestPresetsReorder(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]string{"a", "b", "c"}))
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.phase = phaseAdvance
	press(&m, "p")

	press(&m, "down")
	press(&m, "J")
	assert.Equal(t, []string{"a", "c", "b"}, m.presets)
	assert.Equal(t, 2, m.mgrCursor, "移动后光标跟随条目")

	loaded, err := presets.Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c", "b"}, loaded, "顺序应持久化")
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

func TestAdvancePageDeleteGuardsBuiltins(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.builtins = presets.Builtins()
	m.presets = []string{"-c copy 2"}
	m.phase = phaseAdvance
	m.presetCursor = 0 // 选中第一条内置预设

	press(&m, "d")
	assert.NotEmpty(t, m.statusMsg, "内置预设删除应提示不可删除")
	assert.Equal(t, []string{"-c copy 2"}, m.presets, "内置条目不应影响自定义列表")

	// 光标跨过内置区删除自定义条目（需按两次 d 确认）
	m.presetCursor = len(m.builtins)
	press(&m, "d")
	assert.Equal(t, []string{"-c copy 2"}, m.presets, "第一次按 d 只进入待确认状态")
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
	assert.Equal(t, m.builtins[0].Args, m.mgrInput.Value(), "应预填内置预设参数")

	press(&m, "enter")
	require.Equal(t, phasePresets, m.phase)
	assert.Equal(t, []string{m.builtins[0].Args}, m.presets, "复制后应成为自定义预设")
}

func TestPresetDeleteRequiresDoubleConfirm(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.builtins = presets.Builtins()
	m.presets = []string{"-c:v h264", "-c:v h265"}
	m.phase = phaseAdvance
	m.presetCursor = len(m.builtins) // 第一条自定义预设

	press(&m, "d")
	assert.Equal(t, []string{"-c:v h264", "-c:v h265"}, m.presets, "第一次按 d 不应删除")
	assert.NotEmpty(t, m.statusMsg, "第一次按 d 应给出确认提示")
	assert.Equal(t, 0, m.delPendingIdx, "应记录待删除项")

	// 其他任意键取消确认
	press(&m, "up")
	assert.Equal(t, -1, m.delPendingIdx, "其他按键应取消删除确认")
	press(&m, "down")
	press(&m, "d")
	assert.Equal(t, []string{"-c:v h264", "-c:v h265"}, m.presets, "取消后单次 d 不应删除")

	// 连续两次 d 执行删除
	press(&m, "d")
	assert.Equal(t, []string{"-c:v h265"}, m.presets, "第二次 d 完成删除")
	assert.Equal(t, -1, m.delPendingIdx, "删除后应清理待确认状态")

	// 对剩余条目可重新进入待确认
	press(&m, "d")
	assert.Equal(t, 0, m.delPendingIdx, "删除后可以对剩余条目重新确认")
	assert.Equal(t, []string{"-c:v h265"}, m.presets)
}

func TestPresetMgrDeleteDoubleConfirmAndCancel(t *testing.T) {
	isolatePresets(t)
	require.NoError(t, presets.Save([]string{"a", "b", "c"}))
	m := newModel()
	m.phase = phaseFeature
	press(&m, "p")
	require.Equal(t, phasePresets, m.phase)

	press(&m, "d")
	assert.Equal(t, []string{"a", "b", "c"}, m.presets, "第一次按 d 不应删除")
	assert.Equal(t, 0, m.delPendingIdx)

	// esc 直接返回，同样取消确认
	press(&m, "esc")
	require.Equal(t, phaseFeature, m.phase)

	press(&m, "p")
	require.Equal(t, phasePresets, m.phase)
	assert.Equal(t, -1, m.delPendingIdx, "重新进入管理页应重置确认状态")
	press(&m, "d")
	press(&m, "d")
	assert.Equal(t, []string{"b", "c"}, m.presets)
}
