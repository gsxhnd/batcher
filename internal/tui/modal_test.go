package tui

import (
	"testing"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModalEnterAdvancesAndCtrlSaves 验证表单弹窗：enter 在中间字段推进，
// 在末字段（含 bool）直接保存；中途任意字段可用 ctrl+s 保存，校验失败保持打开。
func TestModalEnterAdvancesAndCtrlSaves(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{AddSubtitle: true}
	m.focus = panelSubs
	press(&m, "n")
	require.NotNil(t, m.modal)
	require.Equal(t, modalSub, m.modal.kind)
	require.Equal(t, 0, m.modal.focus)

	// enter 推进：0 → 1 → 2 → 3(bool，末字段)
	for i, want := range []int{1, 2, 3} {
		press(&m, "enter")
		assert.Equal(t, want, m.modal.focus, "第 %d 次 enter", i+1)
	}

	// 末字段（bool）enter 直接保存：空后缀校验失败，弹窗保持打开
	press(&m, "enter")
	require.NotNil(t, m.modal)
	assert.NotEmpty(t, m.modal.fields[0].err, "空后缀应有字段错误")

	// 中途字段也可 ctrl+s：补全合法值后保存关闭
	m.modal.fields[0].input.SetValue("chi.ass")
	m.modal.fields[1].input.SetValue("chi")
	m.modal.fields[2].input.SetValue("简体中文")
	press(&m, "ctrl+s")
	require.Nil(t, m.modal, "ctrl+s 保存成功后应关闭弹窗")
	require.Len(t, m.subs, 1)
	assert.Equal(t, command.SubtitleOption{Suffix: "chi.ass", Lang: "chi", Title: "简体中文"}, m.subs[0])
}

// TestModalEnterSavesOnLastField 验证末字段（含 bool）enter 直接保存（不循环回开头）。
func TestModalEnterSavesOnLastField(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{Convert: true}
	m.focus = panelPresets
	press(&m, "a")
	require.NotNil(t, m.modal)
	require.Equal(t, modalPreset, m.modal.kind)

	m.modal.fields[0].input.SetValue("H.264")
	m.modal.fields[1].input.SetValue("-c:v h264")
	press(&m, "ctrl+s")
	require.Nil(t, m.modal, "末字段 enter 应保存并关闭弹窗")
	require.Len(t, m.presets, 1)
	assert.Equal(t, "-c:v h264", m.presets[0].Args)
}
