package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// textSelection 是屏幕上的一段拖选。坐标是终端格子，左上角为 (0, 0)。
type textSelection struct {
	on       bool
	dragging bool
	ax, ay   int
	bx, by   int
}

func (m *model) clearSelection() {
	m.sel = textSelection{}
}

func (m *model) clampCell(x, y int) (int, int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if m.width > 0 && x >= m.width {
		x = m.width - 1
	}
	if m.height > 0 && y >= m.height {
		y = m.height - 1
	}
	return x, y
}

// handleMouseClick 按下左键时从这一格开始拖选。
func (m *model) handleMouseClick(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft {
		return nil
	}
	x, y := m.clampCell(msg.X, msg.Y)
	m.sel = textSelection{on: true, dragging: true, ax: x, ay: y, bx: x, by: y}
	return nil
}

// handleMouseMotion 拖动时扩展选区。
func (m *model) handleMouseMotion(msg tea.MouseMotionMsg) tea.Cmd {
	if !m.sel.dragging {
		return nil
	}
	m.sel.bx, m.sel.by = m.clampCell(msg.X, msg.Y)
	m.sel.on = true
	return nil
}

// handleMouseRelease 松手后把选中的纯文本写入系统剪贴板。点一下而不拖动则取消选区。
func (m *model) handleMouseRelease(msg tea.MouseReleaseMsg) tea.Cmd {
	if !m.sel.dragging {
		return nil
	}
	if msg.Button != tea.MouseLeft && msg.Button != tea.MouseNone {
		return nil
	}
	m.sel.dragging = false
	m.sel.bx, m.sel.by = m.clampCell(msg.X, msg.Y)
	if m.sel.ax == m.sel.bx && m.sel.ay == m.sel.by {
		m.clearSelection()
		return nil
	}
	text := selectionText(m.render(), m.sel)
	if strings.TrimSpace(text) == "" {
		m.clearSelection()
		return nil
	}
	return tea.Batch(tea.SetClipboard(text), tea.SetPrimaryClipboard(text))
}

// ordered 把锚点和当前点排成从左上到右下的阅读顺序。
func (s textSelection) ordered() (x1, y1, x2, y2 int, ok bool) {
	if !s.on {
		return 0, 0, 0, 0, false
	}
	x1, y1, x2, y2 = s.ax, s.ay, s.bx, s.by
	if y1 > y2 || (y1 == y2 && x1 > x2) {
		x1, y1, x2, y2 = x2, y2, x1, y1
	}
	return x1, y1, x2, y2, true
}

// selectionText 按终端的拖选方式取出纯文本：首行从起点到行尾，中间整行，末行到终点（含）。
func selectionText(screen string, sel textSelection) string {
	x1, y1, x2, y2, ok := sel.ordered()
	if !ok {
		return ""
	}
	lines := strings.Split(screen, "\n")
	for i := range lines {
		lines[i] = ansi.Strip(lines[i])
	}
	if y1 < 0 {
		y1 = 0
	}
	if y1 >= len(lines) {
		return ""
	}
	if y2 >= len(lines) {
		y2 = len(lines) - 1
	}
	var b strings.Builder
	for y := y1; y <= y2; y++ {
		line := lines[y]
		w := lipgloss.Width(line)
		start, end := 0, w
		if y == y1 {
			start = min(max(x1, 0), w)
		}
		if y == y2 {
			end = min(max(x2+1, 0), w)
		}
		chunk := ""
		if end > start {
			chunk = ansi.Cut(line, start, end)
		}
		chunk = strings.TrimRight(chunk, " ")
		if y < y2 {
			b.WriteString(chunk)
			b.WriteByte('\n')
		} else {
			b.WriteString(chunk)
		}
	}
	return b.String()
}

// paintSelection 给选区铺上底色，并保持每一行仍是终端宽度。
func (m model) paintSelection(screen string) string {
	x1, y1, x2, y2, ok := m.sel.ordered()
	if !ok || m.width <= 0 {
		return screen
	}
	lines := strings.Split(screen, "\n")
	if y1 < 0 {
		y1 = 0
	}
	if y1 >= len(lines) {
		return screen
	}
	if y2 >= len(lines) {
		y2 = len(lines) - 1
	}
	for y := y1; y <= y2; y++ {
		line := lines[y]
		w := lipgloss.Width(line)
		start, end := 0, w
		if y == y1 {
			start = min(max(x1, 0), w)
		}
		if y == y2 {
			end = min(max(x2+1, 0), w)
		}
		if end <= start {
			continue
		}
		left := ansi.Cut(line, 0, start)
		mid := ansi.Strip(ansi.Cut(line, start, end))
		right := ansi.Cut(line, end, w)
		painted := left + dragSelectStyle.Render(mid) + right
		switch gap := m.width - lipgloss.Width(painted); {
		case gap > 0:
			painted += strings.Repeat(" ", gap)
		case gap < 0:
			painted = ansi.Truncate(painted, m.width, "")
		}
		lines[y] = painted
	}
	return strings.Join(lines, "\n")
}
