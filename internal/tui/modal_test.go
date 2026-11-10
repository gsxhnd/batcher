package tui

import (
	"testing"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModalEnterAdvancesAndCtrlSaves 验证表单弹窗：enter 永远循环推进到下一个字段，
// 保存由显式的 ctrl+s 触发；校验失败时弹窗保持打开。
func TestModalEnterAdvancesAndCtrlSaves(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{AddSubtitle: true}
	m.focus = panelSubs
	press(&m, "n")
	require.NotNil(t, m.modal)
	require.Equal(t, modalSub, m.modal.kind)
	require.Equal(t, 0, m.modal.focus)

	// enter 循环推进：0 → 1 → 2 → 3(bool) → 0
	for i, want := range []int{1, 2, 3, 0} {
		press(&m, "enter")
		assert.Equal(t, want, m.modal.focus, "第 %d 次 enter", i+1)
	}

	// 空后缀直接 ctrl+s：校验失败，弹窗保持打开
	press(&m, "ctrl+s")
	require.NotNil(t, m.modal)
	assert.NotEmpty(t, m.modal.fields[0].err, "空后缀应有字段错误")

	// 补全合法值后再 ctrl+s：保存并关闭弹窗
	m.modal.fields[0].input.SetValue("chi.ass")
	m.modal.fields[1].input.SetValue("chi")
	m.modal.fields[2].input.SetValue("简体中文")
	press(&m, "ctrl+s")
	require.Nil(t, m.modal, "ctrl+s 保存成功后应关闭弹窗")
	require.Len(t, m.subs, 1)
	assert.Equal(t, command.SubtitleOption{Suffix: "chi.ass", Lang: "chi", Title: "简体中文"}, m.subs[0])
}

// TestModalEnterNeverSavesOnLastField 验证在最末字段（含 bool）按 enter 绝不直接保存。
func TestModalEnterNeverSavesOnLastField(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.ops = command.Selection{Convert: true}
	m.focus = panelPresets
	press(&m, "a")
	require.NotNil(t, m.modal)
	require.Equal(t, modalPreset, m.modal.kind)

	// 名称 → 参数（末字段）→ 回到名称，弹窗始终打开
	press(&m, "enter")
	assert.Equal(t, 1, m.modal.focus)
	press(&m, "enter")
	assert.Equal(t, 0, m.modal.focus)
	require.NotNil(t, m.modal, "enter 不得在末字段直接确认")
}
