package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectionText(t *testing.T) {
	screen := "0123456789\nabcdefghij\n尾"
	sel := textSelection{on: true, ax: 6, ay: 0, bx: 3, by: 1}
	assert.Equal(t, "6789\nabcd", selectionText(screen, sel))

	sel = textSelection{on: true, ax: 3, ay: 1, bx: 6, by: 0}
	assert.Equal(t, "6789\nabcd", selectionText(screen, sel), "反向拖选应得到同样的文本")

	wide := textSelection{on: true, ax: 2, ay: 0, bx: 5, by: 0}
	assert.Equal(t, "汉字", selectionText("ab汉字cd", wide))

	assert.Empty(t, selectionText(screen, textSelection{}))
}

func TestMouseDragCopiesSelection(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 30
	m.resizePanes()

	next, cmd := m.Update(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	m = next.(model)
	require.Nil(t, cmd)
	assert.True(t, m.sel.dragging)

	next, cmd = m.Update(tea.MouseMotionMsg{X: 7, Y: 0, Button: tea.MouseLeft})
	m = next.(model)
	require.Nil(t, cmd)

	next, cmd = m.Update(tea.MouseReleaseMsg{X: 7, Y: 0, Button: tea.MouseLeft})
	m = next.(model)
	require.NotNil(t, cmd, "拖选松手后应写入剪贴板")
	assert.False(t, m.sel.dragging)
	assert.Equal(t, "batcher", selectionText(m.render(), m.sel))

	out := m.render()
	for i, ln := range strings.Split(out, "\n") {
		assert.Equal(t, m.width, lipgloss.Width(ln), "选中后第 %d 行宽度不一致", i+1)
	}

	next, _ = m.Update(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	m = next.(model)
	next, cmd = m.Update(tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	m = next.(model)
	assert.Nil(t, cmd, "没有拖动时不复制")
	assert.False(t, m.sel.on)
}
