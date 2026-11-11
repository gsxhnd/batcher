package tui

import (
	"testing"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModalEnterAdvancesAndCtrlSaves 验证表单弹窗：enter 在中间字段推进，
// 在末字段（含 bool）直接保存；中途任意字段可用 ctrl+s 保存，校验失败保持打开。
func TestModalEnterAdvancesAndCtrlSaves(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.openSubModal(-1)
	require.NotNil(t, m.modal)
	require.Equal(t, modalSub, m.modal.kind)
	require.Equal(t, 0, m.modal.focus)

	defaults := subtitleDefaults()
	assert.Empty(t, m.modal.fields[0].input.Value(), "默认值只作灰色占位，不预填成正文")
	assert.Equal(t, defaults.Suffix, m.modal.fields[0].input.Placeholder)
	assert.Equal(t, defaults.Lang, m.modal.fields[1].input.Placeholder)
	assert.Equal(t, defaults.Title, m.modal.fields[2].input.Placeholder)

	// enter 推进：0 → 1 → 2 → 3(bool，末字段)
	for i, want := range []int{1, 2, 3} {
		press(&m, "enter")
		assert.Equal(t, want, m.modal.focus, "第 %d 次 enter", i+1)
	}

	// 非法语言码仍拒绝保存
	m.modal.fields[1].input.SetValue("zh")
	press(&m, "enter")
	require.NotNil(t, m.modal)
	assert.NotEmpty(t, m.modal.fields[1].err)

	// 留空则使用默认值，末字段 enter 直接保存
	m.modal.fields[1].input.SetValue("")
	m.modal.focus = len(m.modal.fields) - 1
	press(&m, "enter")
	require.Nil(t, m.modal, "留空应使用默认值并关闭弹窗")
	require.Len(t, m.subs, 1)
	assert.Equal(t, defaults, m.subs[0])

	// 中途字段也可 ctrl+s：补全合法值后保存关闭
	m.openSubModal(-1)
	require.NotNil(t, m.modal)
	m.modal.fields[0].input.SetValue("chi.ass")
	m.modal.fields[1].input.SetValue("chi")
	m.modal.fields[2].input.SetValue("繁体中文")
	press(&m, "ctrl+s")
	require.Nil(t, m.modal, "ctrl+s 保存成功后应关闭弹窗")
	require.Len(t, m.subs, 2)
	assert.Equal(t, command.SubtitleOption{Suffix: "chi.ass", Lang: "chi", Title: "繁体中文"}, m.subs[1])
}

// TestModalEnterSavesOnLastField 验证末字段（含 bool）enter 直接保存（不循环回开头）。
func TestModalEnterSavesOnLastField(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.openPresetModal(-1, presets.Preset{})
	require.NotNil(t, m.modal)
	require.Equal(t, modalPreset, m.modal.kind)

	m.modal.fields[0].input.SetValue("H.264")
	m.modal.fields[1].input.SetValue("-c:v h264")
	press(&m, "ctrl+s")
	require.Nil(t, m.modal, "末字段 enter 应保存并关闭弹窗")
	require.Len(t, m.presets, 1)
	assert.Equal(t, "-c:v h264", m.presets[0].Args)
}
