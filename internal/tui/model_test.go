package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateHome 把 HOME 指向临时目录，避免测试读写真实的 ~/.batcher 预设文件。
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
	isolateHome(t)
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
	assert.Equal(t, []string{"-c:v hevc"}, m.presets)

	press(&m, "esc")
	assert.Equal(t, phaseFeature, m.phase, "esc 应回到打开它的操作页")
}

func TestPresetsReorder(t *testing.T) {
	isolateHome(t)
	require.NoError(t, ffmpeg.SaveAdvancePresets([]string{"a", "b", "c"}))
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.phase = phaseAdvance
	press(&m, "p")

	press(&m, "down")
	press(&m, "J")
	assert.Equal(t, []string{"a", "c", "b"}, m.presets)
	assert.Equal(t, 2, m.mgrCursor, "移动后光标跟随条目")

	loaded, err := ffmpeg.LoadAdvancePresets()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c", "b"}, loaded, "顺序应持久化")
}

func TestPresetsEscReturnsToAdvancePage(t *testing.T) {
	isolateHome(t)
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.phase = phaseAdvance
	press(&m, "p")
	press(&m, "esc")
	assert.Equal(t, phaseAdvance, m.phase)
}
