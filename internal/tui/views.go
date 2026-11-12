package tui

import (
	"context"
	"errors"
	"fmt"
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

var dimStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color(cBorder)).
	Background(lipgloss.Color(cDimBg))

// dimScreen 去掉主界面原有颜色，铺成一层灰底，让弹窗成为唯一的焦点。
func (m model) dimScreen(s string) string {
	style := dimStyle
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = fitWidth(style.Render(ansi.Strip(line)), m.width, style)
	}
	return strings.Join(lines, "\n")
}

// narrowWidth 低于该宽度时左右两栏改成上下叠成一列。
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

// windowBounds 返回让第 cur 项可见的半开区间，规则与 windowRows 相同。
func windowBounds(n, cur, h int) (start, end int) {
	if n == 0 || h <= 0 {
		return 0, 0
	}
	if endPos := cur + 1; endPos > h {
		start = endPos - h
	}
	if cur < start {
		start = cur
	}
	if start < 0 {
		start = 0
	}
	end = min(start+h, n)
	return start, end
}

// windowRows 在固定行数 h 中返回一个让 rows[cur] 始终可见的滚动窗口。
// rows 各元素不带换行。
func windowRows(rows []string, cur int, h int) []string {
	start, end := windowBounds(len(rows), cur, h)
	if start >= end {
		return nil
	}
	return rows[start:end]
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
	b.WriteString("  " + descStyle.Render("队列为空。按 a 新建任务。") + "\n")
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
		head := fmt.Sprintf("进度: %d/%d 完成，耗时 %s", settled, total, m.focusedElapsed().Round(time.Second).String())
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

// focusedElapsed 用光标所在主任务自己的起止时间。还没跑过的任务显示 0，不用上一次执行的耗时。
func (m model) focusedElapsed() time.Duration {
	if snap, ok := m.focusedSnapshot(); ok {
		if snap.StartedAt.IsZero() || snap.FinishedAt.IsZero() {
			return 0
		}
		return snap.FinishedAt.Sub(snap.StartedAt)
	}
	return m.elapsed
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
	if row, ok := m.focusedRosterRow(); ok && row.cmd != "" {
		return row.cmd
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

// itemBody 与某一次执行的主体相同：固定信息下面是这一次的输出。
func (m model) itemBody() string {
	return m.attemptBody()
}

// taskChromeH 是滚动区域以上的固定行数。主任务和子任务各自计算，普通提示另占两行。
func (m model) taskChromeH() int {
	h := m.itemFixedH()
	if _, ok := m.focusedBatchRow(); ok {
		h = m.batchFixedH()
	}
	return h + m.statusBlockH()
}

// statusBlockH 是详情顶部临时提示占用的行数。错误改在右上角，不占这里。
func (m model) statusBlockH() int {
	if m.statusMsg == "" || isErrorNotice(m.statusMsg) {
		return 0
	}
	return 2
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
	start, end := windowBounds(len(m.roster), m.fileCursor, h)
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, m.rosterRowView(i, m.roster[i]))
	}
	return strings.Join(rows, "\n")
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

// drawBox 手绘圆角边框盒子（带内嵌标题）。contentH > 0 时内容超出的行直接截掉；
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
	vert := border.Render("│")
	dash := border.Render("─")
	topLeft := border.Render("╭")
	topRight := border.Render("╮")
	bottomLeft := border.Render("╰")
	bottomRight := border.Render("╯")

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
	top := topLeft + head + strings.Repeat(dash, fill) + topRight

	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content != "" && len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	if contentH == 0 {
		lines = nil
	} else if contentH > 0 {
		if len(lines) > contentH {
			lines = lines[:contentH]
		}
		for len(lines) < contentH {
			lines = append(lines, "")
		}
	}

	var body strings.Builder
	for _, ln := range lines {
		inner := truncateText(ln, innerW)
		pad := max(innerW-lipgloss.Width(inner), 0)
		body.WriteString(vert + inner + strings.Repeat(" ", pad) + vert + "\n")
	}
	bottom := bottomLeft + strings.Repeat(dash, innerW) + bottomRight
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
			if m.modal.vp.Height() > 0 {
				hints = []string{"↑/↓ 滚动", "enter 确认", "esc 取消"}
			}
		default:
			hints = []string{"↑/↓/Tab 切换", "enter 下一项/保存", "esc 取消", "? 快捷键"}
			switch m.modal.kind {
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
		if m.canOverwriteFocused() {
			withOverwrite := make([]string, 0, len(hints)+1)
			for _, h := range hints {
				withOverwrite = append(withOverwrite, h)
				if h == "r 重试" {
					withOverwrite = append(withOverwrite, "o 覆盖")
				}
			}
			hints = withOverwrite
		}
		if m.canStartFocused() {
			for i, h := range hints {
				if h == "x 中止" {
					hints[i] = "x 开始"
				}
			}
		}
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
