package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/task"
)

// ---------- View ----------

// View 实现 tea.Model，将整页内容放入备用屏幕缓冲（全屏模式）。
// 弹窗打开时通过图层叠加覆盖在主界面上。
// 开启鼠标：滚轮滚动日志，按住左键拖选文字（松手复制到剪贴板）。
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// dimScreen 去掉主界面原有颜色，铺成一层灰底，让弹窗成为唯一的焦点。
func (m model) dimScreen(s string) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Background(lipgloss.Color("234"))
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = fitWidth(style.Render(ansi.Strip(line)), m.width, style)
	}
	return strings.Join(lines, "\n")
}

// narrowWidth 低于该宽度时退化为单列布局（只渲染当前聚焦的面板）。
const narrowWidth = 90

func (m model) render() string {
	return m.paintSelection(m.compose())
}

// compose 画出主界面、弹窗和错误卡片，不含鼠标选区高亮。
func (m model) compose() string {
	if m.width == 0 {
		return "加载中..."
	}
	var content string
	if m.width < narrowWidth {
		content = m.narrowLayout()
	} else {
		content = m.wideLayout()
	}
	assembled := lipgloss.JoinVertical(lipgloss.Left, m.header(), content, m.footer())
	if m.modal != nil {
		// 弹窗打开时主界面只作底，压暗并且不再接收按键和滚轮。
		assembled = m.dimScreen(assembled)
		modalBox := m.modalView()
		x := max((m.width-lipgloss.Width(modalBox))/2, 0)
		y := max((m.height-lipgloss.Height(modalBox))/2, 0)
		return lipgloss.NewCompositor(
			lipgloss.NewLayer(assembled),
			lipgloss.NewLayer(modalBox).X(x).Y(y),
		).Render()
	}
	toast := m.errorToast()
	if toast == "" {
		return assembled
	}
	// 右边留一列，避免卡片贴死屏幕边缘。
	x := max(m.width-lipgloss.Width(toast)-1, 0)
	return placeBlock(assembled, toast, x, 1, m.width)
}

// placeBlock 把一块内容贴到画面的 (x, y)，并保持每一行仍是 width 列。
func placeBlock(screen, block string, x, y, width int) string {
	if block == "" || width <= 0 || x >= width {
		return screen
	}
	if x < 0 {
		x = 0
	}
	lines := strings.Split(screen, "\n")
	blockLines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	tw := 0
	for _, ln := range blockLines {
		tw = max(tw, lipgloss.Width(ln))
	}
	if tw == 0 {
		return screen
	}
	if room := width - x; tw > room {
		tw = room
	}
	for i, tl := range blockLines {
		row := y + i
		if row < 0 || row >= len(lines) {
			break
		}
		left := ansi.Truncate(lines[row], x, "")
		if gap := x - lipgloss.Width(left); gap > 0 {
			left += strings.Repeat(" ", gap)
		}
		lines[row] = fitCells(left+fitCells(tl, tw), width)
	}
	return strings.Join(lines, "\n")
}

// fitCells 把一行补齐或截断到 width 列。截断不加省略号，避免贴层时把旁边的格子挤掉。
func fitCells(line string, width int) string {
	if width <= 0 {
		return ""
	}
	switch gap := width - lipgloss.Width(line); {
	case gap == 0:
		return line
	case gap > 0:
		return line + strings.Repeat(" ", gap)
	default:
		return ansi.Truncate(line, width, "")
	}
}

// wideLayout 左侧任务列表 + 右侧任务详情。
func (m model) wideLayout() string {
	bodyH := m.bodyH() - 2
	left := drawBox(m.leftW, m.taskListTitle(), true, true, m.filePane(bodyH), bodyH)
	right := drawBox(m.rightW, "任务详情", true, true, m.taskDetail(), bodyH)
	gutter := gutterColumn(max(lipgloss.Height(left), lipgloss.Height(right)))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gutter, right)
}

// gutterColumn 在左右面板之间留出一列间隔，避免两边框贴在一起。
func gutterColumn(h int) string {
	if h <= 0 {
		return ""
	}
	line := gutterStyle.Render(" ")
	lines := make([]string, h)
	for i := range lines {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// narrowLayout 窄终端：任务列表在上，详情在下，合成一列。
func (m model) narrowLayout() string {
	body := m.taskDetail()
	if h := m.narrowFilePaneH(); h > 0 {
		body = m.filePane(h) + "\n" + body
	}
	return drawBox(m.width, m.taskListTitle(), true, true, body, m.bodyH()-2)
}

// taskListTitle 左侧列表标题，带上主任务数量。
func (m model) taskListTitle() string {
	n := 0
	for _, row := range m.roster {
		if row.itemID == "" && row.attempt == 0 {
			n++
		}
	}
	if n == 0 {
		return "任务"
	}
	return fmt.Sprintf("任务（%d）", n)
}

// taskDetail 右侧主体：有任务时显示光标所在任务，否则提示如何创建。
func (m model) taskDetail() string {
	if len(m.roster) > 0 || m.running {
		return m.runBody()
	}
	var b strings.Builder
	b.WriteString(titleLine("还没有任务") + "\n")
	b.WriteString(subtitleStyle.Render("按 a 创建任务，按 p 管理转码预设") + "\n")
	if m.statusMsg != "" && !isErrorNotice(m.statusMsg) {
		b.WriteString("\n  " + statusStyle.Render(m.statusMsg) + "\n")
	}
	return b.String()
}

// ---------- 顶栏与底栏 ----------

// header 顶部标题栏：左侧应用徽标，右侧显示当前操作组合或执行状态。
func (m model) header() string {
	left := appBadgeStyle.Render("batcher" + badgeVersion())
	right := m.headerStatus()
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = ansi.Truncate(right, max(m.width-lipgloss.Width(left)-1, 1), "…")
		gap = max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 0)
	}
	return fitWidth(left+headerBarStyle.Render(strings.Repeat(" ", gap))+right, m.width, headerBarStyle)
}

// headerStatus 右侧状态胶囊，文案与原先一致，只改颜色和底色。
func (m model) headerStatus() string {
	var text string
	style := stepTodoStyle
	switch {
	case m.running:
		text = "执行中…"
		style = stepActiveStyle
	case errors.Is(m.runErr, context.Canceled):
		text = "上次执行已取消，可修改后重跑"
	case m.runErr != nil:
		text = "上次执行失败，可修改后重跑"
		style = stepErrStyle
	case m.total > 0:
		text = fmt.Sprintf("上次执行 %d/%d 成功，可修改后重跑", m.done, m.total)
		style = stepOkStyle
	default:
		n := 0
		for _, row := range m.roster {
			if row.itemID == "" && row.attempt == 0 {
				n++
			}
		}
		if n == 0 {
			text = "按 a 创建任务"
		} else {
			text = fmt.Sprintf("%d 个任务", n)
			style = stepOkStyle
		}
	}
	return style.Padding(0, 1).Render(text)
}

// panelTitle 返回面板标题（含编号与动态状态）。
func (m model) panelTitle(p panel) string {
	switch p {
	case panelOps:
		return "1 · 操作"
	case panelPresets:
		return "2 · 转码预设"
	case panelSubs:
		return "3 · 字幕轨道"
	case panelForm:
		return "4 · 参数"
	case panelRun:
		title := "5 · 任务"
		switch {
		case len(m.roster) > 0:
			batches := 0
			for _, row := range m.roster {
				if row.itemID == "" {
					batches++
				}
			}
			title = fmt.Sprintf("5 · 任务（%d 批次）", batches)
		case m.total > 0:
			settled, _, _, _, total, _ := m.progressNumbers()
			title = fmt.Sprintf("5 · 任务 %d/%d", settled, total)
		}
		return title
	}
	return ""
}

// sidebarBox 渲染左侧一个面板小方块（聚焦高亮，不可用变灰）。
func (m model) sidebarBox(p panel, contentH int) string {
	return drawBox(m.leftW, m.panelTitle(p), m.focus == p, m.panelEnabled(p), m.sidebarContent(p, contentH), contentH)
}

// windowRows 在固定行数 h 中返回一个让 rows[cur] 始终可见的滚动窗口。
// rows 各元素不带换行。
func windowRows(rows []string, cur int, h int) []string {
	if len(rows) == 0 || h <= 0 {
		return nil
	}
	start := 0
	if end := cur + 1; end > h {
		start = end - h
	}
	if cur < start {
		start = cur
	}
	end := min(start+h, len(rows))
	return rows[start:end]
}

// sidebarContent 返回左侧面板的列表内容（h 为分配的内容行数，光标行始终可见）。
func (m model) sidebarContent(p panel, h int) string {
	switch p {
	case panelOps:
		return m.opsPane(h)
	case panelPresets:
		return m.presetsPane(h)
	case panelSubs:
		return m.subsPane(h)
	case panelForm:
		return m.formPane(h)
	case panelRun:
		return m.filePane(h)
	}
	return ""
}

// sidebarRows 把 windowRows 结果组装成多行字符串。
func sidebarRows(rows []string, cur, h int) string {
	return strings.Join(windowRows(rows, cur, h), "\n")
}

// opsPane 操作面板勾选列表（3 行固定，折叠时窗口滚动）。
func (m model) opsPane(h int) string {
	rows := make([]string, 0, len(features))
	for i, feat := range features {
		mark := "[ ]"
		markStyled := uncheckedStyle.Render(mark)
		if feat.selected(m.ops) {
			mark = "[✔]"
			markStyled = checkedStyle.Render(mark)
		}
		focused := i == m.featCursor
		plain := mark + " " + feat.label
		styled := m.mark(panelOps, focused) + markStyled + " " + m.rowStyle(panelOps, focused).Render(feat.label)
		rows = append(rows, m.barLine(m.focus == panelOps, focused, plain, styled))
	}
	return sidebarRows(rows, m.featCursor, h)
}

// presetsPane 预设列表（内置 → 自定义 → 手动输入），超出高度时随光标滚动。
func (m model) presetsPane(h int) string {
	rows := make([]string, 0, m.presetCount())
	for i, bp := range m.builtins {
		rows = append(rows, m.presetsRow(i == m.presetCursor, bp.Name, "内置 · "+bp.Args))
	}
	for i, p := range m.presets {
		title := p.Args
		extra := ""
		if p.Name != "" {
			title = p.Name
			extra = p.Args
		}
		rows = append(rows, m.presetsRow(m.presetUserOffset()+i == m.presetCursor, title, extra))
	}
	manual := m.presetUserOffset() + len(m.presets)
	extra := ""
	if len(m.presets) == 0 {
		extra = "按 a 空白新建"
	}
	rows = append(rows, m.presetsRow(m.presetCursor == manual, "(手动输入)", extra))
	return sidebarRows(rows, m.presetCursor, h)
}

// presetsRow 渲染一条预设列表行（不带换行），extra 为灰色说明。
func (m model) presetsRow(focused bool, label, extra string) string {
	plain := label
	if extra != "" {
		plain += "  " + extra
	}
	line := m.rowStyle(panelPresets, focused).Render(label)
	if extra != "" {
		line += " " + descStyle.Render(extra)
	}
	return m.barLine(m.focus == panelPresets, focused, plain, m.mark(panelPresets, focused)+line)
}

// subsPane 字幕列表。
func (m model) subsPane(h int) string {
	if len(m.subs) == 0 {
		rows := []string{descStyle.Render("（空）")}
		if h > 0 {
			return rows[0]
		}
		return ""
	}
	rows := make([]string, 0, len(m.subs))
	for i, sub := range m.subs {
		focused := i == m.subCursor
		plain := fmt.Sprintf("%d. .%s  %s", i+1, sub.Suffix, sub.Lang)
		if sub.Default {
			plain += " [默认]"
		}
		line := m.rowStyle(panelSubs, focused).Render(fmt.Sprintf("%d. .%s  %s", i+1, sub.Suffix, sub.Lang))
		if sub.Default {
			line += " " + checkedStyle.Render("[默认]")
		}
		rows = append(rows, m.barLine(m.focus == panelSubs, focused, plain, m.mark(panelSubs, focused)+line))
	}
	return sidebarRows(rows, m.subCursor, h)
}

// formPane 参数面板字段列表。
func (m model) formPane(h int) string {
	rows := make([]string, 0, len(m.fields))
	for i := range m.fields {
		f := &m.fields[i]
		focused := i == m.focusNo
		rows = append(rows, m.barLine(m.focus == panelForm, focused, f.def.label, m.mark(panelForm, focused)+m.rowStyle(panelForm, focused).Render(f.def.label)))
	}
	return sidebarRows(rows, m.focusNo, h)
}

// barLine 在当前面板里把选中行铺成整行高亮。其他面板仍只用文字颜色，避免五条高亮叠在一起。
func (m model) barLine(active, focused bool, plain, styled string) string {
	if !active || !focused || m.leftW < 8 {
		return styled
	}
	return fillLine(m.leftW-2, selectedRowStyle, "▸ "+plain)
}

// fillLine 把文本补齐或截断到 width 后套上样式，使底色铺满这一行。
func fillLine(width int, style lipgloss.Style, text string) string {
	if width < 4 {
		return style.Render(text)
	}
	if lipgloss.Width(text) > width {
		text = ansi.Truncate(text, width, "…")
	}
	if pad := width - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return style.Render(text)
}

// fitWidth 把一行补齐到指定显示宽度，补上的空格沿用 pad 的底色。
func fitWidth(line string, width int, pad lipgloss.Style) string {
	w := lipgloss.Width(line)
	if w == width || width <= 0 {
		return line
	}
	if w > width {
		return ansi.Truncate(line, width, "…")
	}
	return line + pad.Render(strings.Repeat(" ", width-w))
}

// inputWidth 是表单输入框的可视宽度。
// 扣掉边框、两格缩进、16 列标签、间隔、两列提示符，以及文本框自带的一列光标。
func inputWidth(boxW int) int {
	return max(boxW-24, 8)
}
func titleLine(text string) string {
	return " " + titleMarkStyle.Render("▌ ") + taskTitleStyle.Render(text)
}

// mark 只在当前面板标出光标，其它面板保持安静。
func (m model) mark(p panel, focused bool) string {
	if focused && m.focus == p {
		return selectedMark.Render("▸ ")
	}
	return "  "
}

// rowStyle 当前面板的光标行用强调色，其它面板用正文色。
func (m model) rowStyle(p panel, focused bool) lipgloss.Style {
	if focused && m.focus == p {
		return selectedStyle
	}
	return normalStyle
}

// cursorMark 返回列表行的光标前缀。
func cursorMark(focused bool) string {
	if focused {
		return selectedMark.Render("▸ ")
	}
	return "  "
}

// itemStyle 返回列表行文本的样式：聚焦行高亮。
func itemStyle(focused bool) lipgloss.Style {
	if focused {
		return selectedStyle
	}
	return normalStyle
}

// ---------- 右侧内容区 ----------

// focusedBody 当前聚焦面板的右侧内容。
func (m model) focusedBody() string {
	switch m.focus {
	case panelOps:
		return m.opsBody()
	case panelPresets:
		return m.presetsBody()
	case panelSubs:
		return m.subsBody()
	case panelForm:
		return m.formBody()
	case panelRun:
		return m.runBody()
	}
	return ""
}

// opsBody 操作面板：当前勾选组合的说明与引导。
func (m model) opsBody() string {
	var b strings.Builder
	feat := features[m.featCursor]
	b.WriteString(titleLine(feat.label) + "\n")
	b.WriteString(subtitleStyle.Render(feat.desc) + "\n\n")
	for i := range features {
		mark := uncheckedStyle.Render("[ ]")
		if features[i].selected(m.ops) {
			mark = checkedStyle.Render("[✔]")
		}
		b.WriteString("  " + mark + " " + itemStyle(i == m.featCursor).Render(features[i].label) + "\n")
	}
	b.WriteString("\n  空格 勾选；已选 " + selectedStyle.Render(displaySel(m.ops)) + "\n")
	if !m.ops.Any() {
		b.WriteString("  " + errorStyle.Render("× 请至少勾选一项") + "\n")
	}
	b.WriteString("\n  按 5 或 x 进入任务列表。列表为空，按 x 弹出确认后才会入队。\n")
	if m.featErr != "" {
		b.WriteString("  " + errorStyle.Render("× "+m.featErr) + "\n")
	}
	return m.statusBlock() + b.String()
}

// presetsBody 预设面板：选中项详情与提示。
func (m model) presetsBody() string {
	var b strings.Builder
	b.WriteString(titleLine("视频转码 · 参数选择") + "\n")
	offset := m.presetUserOffset()
	switch {
	case m.presetCursor < offset:
		bp := m.builtins[m.presetCursor]
		b.WriteString(subtitleStyle.Render("内置预设："+bp.Name+"（只读，按 a 可复制微调）") + "\n\n")
		b.WriteString("  " + codeStyle.Render(bp.Args) + "\n")
	case m.presetCursor < offset+len(m.presets):
		p := m.presets[m.presetCursor-offset]
		label := p.Name
		if label == "" {
			label = "（无名称）"
		}
		b.WriteString(subtitleStyle.Render("自定义预设："+label) + "\n\n")
		b.WriteString("  " + codeStyle.Render(p.Args) + "\n")
	default:
		b.WriteString(subtitleStyle.Render("手动输入：enter 后在面板 [4] 直接编辑高级参数") + "\n\n")
		b.WriteString("  " + descStyle.Render("ctrl+s 可把已填参数另存为预设") + "\n")
	}
	return m.statusBlock() + b.String()
}

// subsBody 字幕面板：列表详情与匹配说明。
func (m model) subsBody() string {
	var b strings.Builder
	b.WriteString(titleLine("添加字幕 · 字幕轨道") + "\n")
	b.WriteString(subtitleStyle.Render("字幕按 <视频名>.<后缀> 或 <视频名>.<标记>.<后缀> 匹配") + "\n\n")
	if len(m.subs) == 0 {
		b.WriteString("  " + descStyle.Render("字幕列表为空，按 n 新增一条") + "\n")
	} else {
		for i, sub := range m.subs {
			focused := i == m.subCursor
			line := fmt.Sprintf("%s%d. %s  语言: %s  标题: %s  默认: ", cursorMark(focused), i+1,
				itemStyle(focused).Render("."+sub.Suffix), sub.Lang, sub.Title)
			if sub.Default {
				line += checkedStyle.Render("是")
			} else {
				line += normalStyle.Render("否")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("  " + descStyle.Render("按 t 切换默认字幕流；播放器会默认加载 default 轨道") + "\n")
	}
	return m.statusBlock() + b.String()
}

// formBody 参数面板的主体：只显示左侧选中的那一项。
// 源视频路径和字体文件夹下面再列出将要处理的文件。
func (m model) formBody() string {
	var b strings.Builder
	if len(m.fields) == 0 {
		b.WriteString(titleLine("参数") + "\n")
		b.WriteString(subtitleStyle.Render("没有可编辑的参数") + "\n")
		return m.statusBlock() + b.String()
	}
	f := &m.fields[clamp(m.focusNo, 0, len(m.fields)-1)]
	b.WriteString(titleLine(f.def.label) + "\n")
	if m.editing {
		b.WriteString(subtitleStyle.Render("↑/↓/Tab 切换参数，enter 下一项，esc 完成") + "\n")
	} else {
		b.WriteString(subtitleStyle.Render("灰色为默认值，留空即使用；enter 进入编辑") + "\n")
	}
	if note := fieldNote(f.def.key); note != "" {
		b.WriteString(subtitleStyle.Render(note) + "\n")
	}
	b.WriteString("\n")
	f.input.SetWidth(max(m.rightW-7, 8))
	b.WriteString("  " + f.input.View() + "\n")
	if f.err != "" {
		b.WriteString("  " + errorStyle.Render("× "+f.err) + "\n")
	}
	if f.def.key == "input_path" || f.def.key == "fonts_path" {
		b.WriteString("\n")
		b.WriteString(m.dirFileBlock(f.def.key))
	}
	if m.planErr != nil {
		b.WriteString("  " + errorStyle.Render("× "+m.planErr.Error()) + "\n")
	}
	return m.statusBlock() + b.String()
}

// fieldNote 是当前参数在主体里的一行说明。
func fieldNote(key string) string {
	switch key {
	case "input_path":
		return "下面列出该文件夹里、后缀匹配的视频"
	case "input_format":
		return "不带点，例如 mp4"
	case "output_path":
		return "成品写入这个目录"
	case "output_format":
		return "不带点，例如 mkv"
	case "advance":
		return "放在全部输入之后、输出文件之前"
	case "fonts_path":
		return "下面列出该文件夹里的 ttf、otf、ttc"
	default:
		return ""
	}
}

// dirFileBlock 渲染当前文件夹参数下的文件列表。
func (m model) dirFileBlock(key string) string {
	q, ok := m.dirQuery()
	if !ok {
		return "  " + descStyle.Render("先填写文件夹，下面会列出其中的文件") + "\n"
	}
	if m.dirLoading && m.dirKey != q.key {
		return "  " + subtitleStyle.Render("正在读取文件…") + "\n"
	}
	if m.dirKey != q.key {
		return "  " + descStyle.Render("正在准备文件列表…") + "\n"
	}
	if m.dirErr != nil {
		return "  " + errorStyle.Render("× "+m.dirErr.Error()) + "\n"
	}
	title := "文件"
	switch key {
	case "input_path":
		title = "视频"
		if q.format != "" {
			title = q.format
		}
	case "fonts_path":
		title = "字体"
	}
	var b strings.Builder
	b.WriteString("  " + sectionTitleStyle.Render(fmt.Sprintf("%s · %d", title, len(m.dirFiles))) + "\n")
	if len(m.dirFiles) == 0 {
		b.WriteString("  " + descStyle.Render("没有匹配的文件") + "\n")
		return b.String()
	}
	const limit = 80
	shown := m.dirFiles
	rest := 0
	if len(shown) > limit {
		rest = len(shown) - limit
		shown = shown[:limit]
	}
	width := max(m.rightW-4, 8)
	for _, path := range shown {
		b.WriteString("  " + truncateText(relativeTo(q.path, path), width) + "\n")
	}
	if rest > 0 {
		b.WriteString("  " + descStyle.Render(fmt.Sprintf("还有 %d 个", rest)) + "\n")
	}
	return b.String()
}

// relativeTo 把扫描到的路径显示成相对文件夹的名字。
func relativeTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// runBody 任务面板：命令预览，或队列里的主任务 / 子任务主体。
// 主任务只放状态与子任务列表；子任务才有固定命令和可折叠的输出。
func (m model) runBody() string {
	var b strings.Builder
	if m.running || m.queueViewActive() {
		if row, ok := m.focusedBatchRow(); ok {
			b.WriteString(m.batchBody(row))
		} else if _, ok := m.focusedAttemptRow(); ok {
			b.WriteString(m.attemptBody())
		} else if _, ok := m.focusedItemRow(); ok {
			b.WriteString(m.subtaskBody())
		} else {
			b.WriteString(m.itemBody())
		}
		return m.statusBlock() + b.String()
	}
	switch {
	case m.planLoading:
		b.WriteString("  " + m.spin.View() + " " + subtitleStyle.Render("正在生成命令列表…") + "\n")
	case m.planErr != nil:
		b.WriteString("  " + errorStyle.Render("× 生成命令失败：") + subtitleStyle.Render(m.planErr.Error()) + "\n")
		b.WriteString("  " + descStyle.Render("修改配置后会自动重新生成") + "\n")
	case m.previewDone:
		if m.total > 0 {
			head := successStyle.Render(fmt.Sprintf("✔ 上次执行: %d/%d 成功，耗时 %s", m.done, m.total, m.elapsed.String()))
			b.WriteString("  " + head + "\n")
			if m.runErr != nil {
				b.WriteString("  " + errorStyle.Render("× "+m.runErr.Error()) + "\n")
			}
		}
		b.WriteString("  " + descStyle.Render("队列为空。按 x 弹出确认后才会入队。") + "\n")
	default:
		b.WriteString("  " + descStyle.Render("配置变化时自动生成命令预览") + "\n")
	}
	return m.statusBlock() + b.String()
}

// statusSuffix 以独立块返回临时提示文本（为空时返回空串）。
func statusSuffix(msg string) string {
	if msg == "" {
		return ""
	}
	return "\n  " + statusStyle.Render(msg) + "\n"
}

// isErrorNotice 判断临时提示是不是错误。界面上的失败文案都以 × 开头。
func isErrorNotice(msg string) bool {
	return strings.HasPrefix(strings.TrimSpace(msg), "×")
}

// statusBlock 把普通提示留在面板正文里。错误改到右上角浮层，避免撑开固定行高。
func (m model) statusBlock() string {
	if isErrorNotice(m.statusMsg) {
		return ""
	}
	return statusSuffix(m.statusMsg)
}

// errorToast 渲染右上角错误卡片，最多两行正文。没有错误时返回空串。
func (m model) errorToast() string {
	msg := strings.TrimSpace(m.statusMsg)
	// 边框 2 列、内边距 2 列，再给屏幕右缘留 1 列。
	maxInner := min(m.width-6, 40)
	if !isErrorNotice(msg) || maxInner < 8 {
		return ""
	}
	if lipgloss.Width(msg) > maxInner*2 {
		msg = ansi.Truncate(msg, maxInner*2, "…")
	}
	lines := strings.Split(wrapText(msg, maxInner), "\n")
	if len(lines) > 2 {
		lines = lines[:2]
		lines[1] = ansi.Truncate(lines[1], maxInner, "…")
	}
	return errorToastStyle.Render(strings.Join(lines, "\n"))
}

// progressBlock 渲染进度条与说明，行数与 runBarH 一致。
// 完成数不计仍可重试的失败项，所以失败后按 r 不会把进度再往前推一格。
func (m model) progressBlock() string {
	settled, failed, _, _, total, paused := m.progressNumbers()
	if total <= 0 {
		return "  " + subtitleStyle.Render("正在启动...") + "\n"
	}
	if !m.running && !paused {
		head := fmt.Sprintf("进度: %d/%d 完成，耗时 %s", settled, total, m.elapsed.Round(time.Second).String())
		if failed > 0 {
			head += fmt.Sprintf("，失败 %d", failed)
		}
		style := descStyle
		if m.runErr != nil || failed > 0 {
			style = errorStyle
		}
		return "  " + style.Render(head) + "\n"
	}
	percent := float64(settled) / float64(total)
	return "  " + m.bar.ViewAs(percent) + "\n" + m.progressHint() + "\n"
}

// progressHint 组合进度/耗时的一行状态文本，超宽时截断。
func (m model) progressHint() string {
	settled, failed, skipped, canceled, total, paused := m.progressNumbers()
	hint := fmt.Sprintf("进度: %d/%d", settled, total)
	if failed > 0 {
		hint += fmt.Sprintf("，失败 %d", failed)
	}
	if canceled > 0 {
		hint += fmt.Sprintf("，取消 %d", canceled)
	}
	if skipped > 0 {
		hint += fmt.Sprintf("，跳过 %d", skipped)
	}
	hint += "   耗时: " + m.elapsedNow().Round(time.Second).String()
	if paused {
		hint += "（已暂停）"
	}
	if len(m.roster) == 0 && m.fileCursor == 0 && m.current != "" {
		hint += "   当前: " + m.current
	}
	hint = subtitleStyle.Render(hint)
	return "  " + ansi.Truncate(hint, max(m.rightW-2, 10), "…")
}

// pinnedCmdText 返回任务面板置顶显示的完整 ffmpeg 命令：
// 队列视图激活时优先光标所在子任务的命令（无论是否正在执行，选中即可查看/复制），
// 其次当前正在执行的任务，最后最近启动的任务。
func (m model) pinnedCmdText() string {
	if len(m.roster) > 0 && m.fileCursor >= 0 && m.fileCursor < len(m.roster) {
		if cmd := m.roster[m.fileCursor].cmd; cmd != "" {
			return cmd
		}
	}
	if m.current != "" {
		return m.current
	}
	if idx := m.runIdx; idx >= 0 && idx < len(m.files) {
		return m.files[idx].cmd
	}
	return ""
}

// pinnedCmdBlock 渲染执行中固定在主体内容最上方的完整 CLI 命令块：
// 一行说明 + 按宽度折行的命令原文，长命令不会被截断。
func (m model) pinnedCmdBlock() string {
	cmd := m.pinnedCmdText()
	var b strings.Builder
	b.WriteString("  " + sectionTitleStyle.Render("固定 · 当前任务完整命令（y 复制）") + "\n")
	if cmd == "" {
		b.WriteString("  " + subtitleStyle.Render("等待任务开始…") + "\n")
	} else {
		b.WriteString("  " + wrapText(cmdPinStyle.Render(cmd), max(m.rightW-4, 10)) + "\n")
	}
	b.WriteString(m.subtaskStatusLine())
	return b.String()
}

// outputDivider 把子任务的固定信息和下面的输出区隔开，固定一行。
func (m model) outputDivider() string {
	title := "── 输出 ──"
	width := max(m.rightW-2, lipgloss.Width(title))
	pad := max(width-lipgloss.Width(title), 0)
	left, right := pad/2, pad-pad/2
	line := strings.Repeat("─", left) + title + strings.Repeat("─", right)
	return sectionTitleStyle.Render(truncateText(line, width)) + "\n"
}

// batchBody 是主任务自己的主体：状态、进度和子任务列表，没有输出区。
func (m model) batchBody(row rosterRow) string {
	var b strings.Builder
	b.WriteString(m.batchOverviewBlock(row))
	b.WriteString(m.progressBlock())
	b.WriteString("  " + sectionTitleStyle.Render("子任务") + "\n")
	b.WriteString(m.logVp.View())
	return b.String()
}

// subtaskBody 是子任务行的主体：命令和次数列表，没有 ffmpeg 输出。
func (m model) subtaskBody() string {
	var b strings.Builder
	b.WriteString(m.pinnedCmdBlock())
	b.WriteString(m.taskProgressBlock())
	b.WriteString("  " + sectionTitleStyle.Render("次数") + "\n")
	b.WriteString(m.logVp.View())
	return b.String()
}

// attemptBody 是某一次执行的主体：固定信息下面只放这一次的输出。
func (m model) attemptBody() string {
	var b strings.Builder
	b.WriteString(m.pinnedCmdBlock())
	b.WriteString(m.taskProgressBlock())
	b.WriteString(m.outputDivider())
	b.WriteString(m.logVp.View())
	return b.String()
}

// itemBody 是还没有队列列表时的执行主体。
func (m model) itemBody() string {
	var b strings.Builder
	b.WriteString(m.pinnedCmdBlock())
	b.WriteString(m.taskProgressBlock())
	b.WriteString(m.outputDivider())
	b.WriteString(m.logVp.View())
	return b.String()
}

// taskChromeH 是滚动区域以上的固定行数。主任务和子任务各自计算。
func (m model) taskChromeH() int {
	if _, ok := m.focusedBatchRow(); ok {
		return m.batchFixedH()
	}
	return m.itemFixedH()
}

// batchFixedH 与 batchBody 里视口之前的行数一致。
func (m model) batchFixedH() int {
	return 4 + m.runBarH() + 1
}

// itemFixedH 与 itemBody 里视口之前的行数一致。
func (m model) itemFixedH() int {
	return m.runPinH() + m.runBarH() + 1
}

// batchOverviewBlock 渲染主任务总览，固定 4 行，与 runPinH 一致。
func (m model) batchOverviewBlock(row rosterRow) string {
	line := func(s string) string {
		return "  " + descStyle.Render(truncateText(s, max(m.rightW-4, 4))) + "\n"
	}
	if m.queue == nil {
		return line("主任务") + line("") + line("") + line("")
	}
	snap, ok := m.batchByID(row.batchID)
	if !ok || snap == nil {
		return line("主任务已不在队列中") + line("") + line("") + line("")
	}
	c := snap.Counts()
	return line(fmt.Sprintf("主任务 · %s", snap.Name)) +
		line(fmt.Sprintf("状态：%s    完成 %d / 失败 %d / 跳过 %d / 取消 %d，共 %d",
			batchStateText(snap.Status), c.Done, c.Failed, c.Skipped, c.Canceled, c.Total)) +
		line("输入: "+snap.InputDir) +
		line("输出: "+snap.OutputDir)
}

// runPinH 置顶区域占用的行数：主任务行为总览，子任务行为完整命令。
func (m model) runPinH() int {
	if _, ok := m.focusedBatchRow(); ok {
		return 4
	}
	cmd := m.pinnedCmdText()
	lines := 2
	if cmd != "" {
		lines = 1 + len(strings.Split(wrapText(cmdPinStyle.Render(cmd), max(m.rightW-4, 10)), "\n"))
	}
	return lines + 1 // 命令区再加一行状态
}

// taskProgressBlock 在子任务和单次执行上显示这一次的 ffmpeg 进度。
// 还没有进度快照时仍用主任务的总进度，避免执行一开始出现空白。
func (m model) taskProgressBlock() string {
	if p, ok := m.liveItemProgress(); ok {
		return m.itemProgressBlock(p)
	}
	return m.progressBlock()
}

// itemProgressBlock 渲染单个子任务的进度，固定两行，与 runBarH 一致。
func (m model) itemProgressBlock(p ffmpeg.Progress) string {
	var head string
	if ratio, ok := p.Percent(); ok {
		head = "  " + m.bar.ViewAs(ratio)
	} else {
		head = "  " + subtitleStyle.Render("处理中")
	}
	return head + "\n" + m.itemProgressHint(p) + "\n"
}

// itemProgressHint 写出当前子任务的百分比、已处理时长、速度和帧率。
func (m model) itemProgressHint(p ffmpeg.Progress) string {
	hint := "当前"
	if n, ok := progressPercent(p); ok {
		hint += fmt.Sprintf(" %d%%", n)
	}
	withHour := p.Duration >= time.Hour || p.OutTime >= time.Hour
	switch {
	case p.Duration > 0:
		hint += fmt.Sprintf("  %s / %s", formatClock(p.OutTime, withHour), formatClock(p.Duration, withHour))
	case p.OutTime > 0:
		hint += "  已处理 " + formatClock(p.OutTime, withHour)
	}
	if p.Speed > 0 {
		hint += "  " + trimFloat(p.Speed) + "x"
	}
	if p.FPS > 0 {
		hint += "  " + trimFloat(p.FPS) + " fps"
	}
	return "  " + ansi.Truncate(subtitleStyle.Render(hint), max(m.rightW-2, 10), "…")
}

// progressLabel 是列表里用的短进度。
func progressLabel(p ffmpeg.Progress) string {
	if n, ok := progressPercent(p); ok {
		return fmt.Sprintf("%d%%", n)
	}
	if p.OutTime > 0 {
		return formatClock(p.OutTime, p.OutTime >= time.Hour)
	}
	return ""
}

func progressPercent(p ffmpeg.Progress) (int, bool) {
	ratio, ok := p.Percent()
	if !ok {
		return 0, false
	}
	return int(ratio*100 + 0.5), true
}

func formatClock(d time.Duration, withHour bool) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	s := int(d % time.Minute / time.Second)
	if withHour || h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// runBarH 进度区占用的行数，与 progressBlock / itemProgressBlock 一致。
func (m model) runBarH() int {
	if _, ok := m.liveItemProgress(); ok {
		return 2
	}
	_, _, _, _, total, paused := m.progressNumbers()
	if total <= 0 {
		return 1
	}
	if !m.running && !paused {
		return 1
	}
	return 2
}

// truncate 截断到指定显示宽度。
func truncateText(s string, w int) string {
	return ansi.Truncate(s, max(w, 4), "…")
}

// ---------- 文件列表 ----------

// filePane 渲染左侧列表。只有真正入队后才显示批次与子任务；未入队时不列出文件。
func (m model) filePane(h int) string {
	if len(m.roster) == 0 {
		return descStyle.Render("暂无任务，按 a 创建")
	}
	rows := make([]string, 0, len(m.roster))
	for i, row := range m.roster {
		rows = append(rows, m.rosterRowView(i, row))
	}
	return strings.Join(windowRows(rows, m.fileCursor, h), "\n")
}

// rosterRowView 渲染队列列表的一行：批次行显示状态与进度概要；正在执行的子任务带上自己的进度。
func (m model) rosterRowView(idx int, row rosterRow) string {
	focused := idx == m.fileCursor
	indent, budget := "", m.leftW-6
	switch {
	case row.attempt > 0:
		indent, budget = "    ", m.leftW-10
	case row.itemID != "":
		indent, budget = "  ", m.leftW-8
	}
	extra := ""
	if row.attempt == 0 && row.state == fileRunning {
		extra = m.itemProgressLabel(row.batchID, row.itemID, task.StatusRunning)
	}
	glyph := fileStatusGlyph(row.state)
	titleWidth := budget - lipgloss.Width(glyph) - 1
	if extra != "" {
		titleWidth -= lipgloss.Width(extra) + 1
	}
	core := truncateText(row.title, max(titleWidth, 1))
	if extra != "" {
		core += " " + extra
	}
	name := indent + core + " " + glyph
	shown := indent + core + " " + fileStatusMark(row.state)
	styled := m.mark(panelRun, focused) + m.rowStyle(panelRun, focused).Render(shown)
	return m.barLine(m.focus == panelRun, focused, name, styled)
}

// fileRow 渲染文件列表一行（不带换行）；idx 为文件下标（-1 表示“全部”）。
func (m model) fileRow(idx int, name string) string {
	focused := idx+1 == m.fileCursor
	if idx < 0 && m.total > 0 {
		settled, _, _, _, total, _ := m.progressNumbers()
		name = fmt.Sprintf("全部（%d/%d）", settled, total)
	}
	glyph := ""
	if m.total > 0 {
		state := filePending
		if idx >= 0 && idx < len(m.files) {
			state = m.files[idx].state
		}
		glyph = fileStatusGlyph(state)
	}
	plain := name
	shown := name
	if glyph != "" {
		plain += " " + glyph
		shown += " " + fileStatusMark(fileStateOf(m, idx))
	}
	limit := max(m.leftW-4, 4)
	plain = truncateText(plain, limit)
	shown = truncateText(shown, limit)
	return m.barLine(m.focus == panelRun, focused, plain, m.mark(panelRun, focused)+m.rowStyle(panelRun, focused).Render(shown))
}

// fileStateOf 返回文件列表行对应的执行状态。idx < 0 表示“全部”。
func fileStateOf(m model, idx int) string {
	if idx >= 0 && idx < len(m.files) {
		return m.files[idx].state
	}
	return filePending
}

// fileStatusMark 渲染文件执行状态标记。
func fileStatusMark(state string) string {
	switch state {
	case fileRunning:
		return logLineInfoStyle.Render(fileStatusGlyph(state))
	case filePaused:
		return statusStyle.Render(fileStatusGlyph(state))
	case fileDone:
		return logLineDoneStyle.Render(fileStatusGlyph(state))
	case fileFailed:
		return logLineFailedStyle.Render(fileStatusGlyph(state))
	default:
		return descStyle.Render(fileStatusGlyph(state))
	}
}

// fileStatusGlyph 是状态标记的纯文本，选中行高亮时不再套一层颜色。
func fileStatusGlyph(state string) string {
	switch state {
	case fileRunning:
		return "▶"
	case filePaused:
		return "‖"
	case fileDone:
		return "✔"
	case fileFailed:
		return "✘"
	case fileCanceled, fileSkipped:
		return "−"
	default:
		return "○"
	}
}

// wrapText 将文本按显示宽度硬换行（保留 ANSI 颜色序列），供 viewport 展示长命令与日志。
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if lipgloss.Width(line) <= width {
			out = append(out, line)
			continue
		}
		out = append(out, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
	}
	return strings.Join(out, "\n")
}

// ---------- 手绘边框盒子 ----------

// drawBox 手绘圆角边框盒子（带内嵌标题），内容按行截断/补齐到 contentH 行；
// contentH < 0 表示按内容自然高度。
func drawBox(w int, title string, focused, enabled bool, content string, contentH int) string {
	borderColor := cBorder
	titleStyle := panelTitleIdleStyle
	switch {
	case !enabled:
		borderColor = cBorderDim
		titleStyle = descStyle
	case focused:
		borderColor = cAccent
		titleStyle = panelTitleStyle
	}
	border := lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor))

	innerW := max(w-2, 2)
	var head string
	if title != "" {
		// 标题超长时截断，保证顶边不超宽（"─ " 与 " " 共占 4 列）。
		title = truncateText(title, innerW-4)
		head = "─ " + titleStyle.Render(title) + " "
	} else {
		head = ""
	}
	// 顶边总宽 = 1(╭) + head + fill + 1(╮)，必须与边框体宽度(1+innerW+1)一致。
	fill := max(innerW-lipgloss.Width(head), 0)
	top := border.Render("╭" + head + strings.Repeat("─", fill) + "╮")

	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content != "" && len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	if contentH == 0 {
		lines = nil
	} else if contentH > 0 {
		if len(lines) > contentH {
			more := len(lines) - contentH
			clipped := make([]string, 0, contentH)
			clipped = append(clipped, lines[:max(contentH-1, 0)]...)
			clipped = append(clipped, descStyle.Render(fmt.Sprintf("…（%d 行已折叠，进入面板查看）", more)))
			lines = clipped
		}
		for len(lines) < contentH {
			lines = append(lines, "")
		}
	}

	var body strings.Builder
	for _, ln := range lines {
		inner := truncateText(ln, innerW)
		pad := max(innerW-lipgloss.Width(inner), 0)
		body.WriteString(border.Render("│") + inner + strings.Repeat(" ", pad) + border.Render("│") + "\n")
	}
	bottom := border.Render("╰" + strings.Repeat("─", innerW) + "╯")
	out := top + "\n" + body.String() + bottom
	return out
}

// ---------- 底栏快捷键提示 ----------

func (m model) footer() string {
	var hints []string
	switch {
	case m.modal != nil:
		switch m.modal.kind {
		case modalHelp:
			hints = []string{"↑/↓ 滚动", "esc 关闭"}
		case modalConfirm:
			hints = []string{"enter 确认", "esc 取消", "? 快捷键"}
		default:
			hints = []string{"↑/↓/Tab 切换", "enter 下一项/保存", "esc 取消", "? 快捷键"}
			switch m.modal.kind {
			case modalSub:
				hints = []string{"↑/↓/Tab 切换", "enter 下一项/保存", "space 切换默认轨道", "esc 取消"}
			case modalTask:
				hints = []string{"↑/↓ 切换", "space 勾选", "ctrl+n 加字幕", "ctrl+s 创建", "esc 取消"}
			case modalPresetList:
				hints = []string{"↑/↓ 选择", "a 空白新建", "c 复制", "e 编辑", "d 删除", "J/K 排序", "esc 关闭"}
			}
		}
	case m.outputFocus:
		hints = []string{"↑/↓ 滚动输出", "esc 返回", "y 复制"}
	case m.running || m.queueViewActive():
		hints = []string{"↑/↓ 切换", "r 重试", "d 删除", "c 取消", "s 跳过", "x 中止", "y 复制", "f 跟随"}
		if m.running || m.pausedBatch != "" {
			hints = append(hints, "a 新建", "q 取消执行")
		} else {
			hints = append(hints, "a 新建", "p 预设", "? 帮助")
		}
	default:
		hints = []string{"a 新建任务", "p 转码预设", "↑/↓ 选择任务", "? 帮助", "q 退出"}
	}
	divider := dividerStyle.Render(strings.Repeat("─", max(m.width, 1)))
	desc := helpDescStyle.Background(lipgloss.Color(cHeaderBg))
	sep := helpSepStyle.Background(lipgloss.Color(cHeaderBg))
	keys := make([]string, 0, len(hints))
	for _, h := range hints {
		k, v, ok := strings.Cut(h, " ")
		if !ok {
			keys = append(keys, desc.Render(h))
			continue
		}
		keys = append(keys, helpKeyStyle.Render(k)+footerPadStyle.Render(" ")+desc.Render(v))
	}
	line := footerPadStyle.Render(" ") + strings.Join(keys, sep.Render("  ·  "))
	line = ansi.Truncate(line, max(m.width, 1), "…")
	return divider + "\n" + fitWidth(line, m.width, footerPadStyle)
}
