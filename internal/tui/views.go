package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---------- View ----------

// View 实现 tea.Model，将整页内容放入备用屏幕缓冲（全屏模式）。
// 弹窗打开时通过图层叠加覆盖在主界面上。
// 开启鼠标滚轮事件，用于任务面板命令/日志的滚动查看。
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// narrowWidth 低于该宽度时退化为单列布局（只渲染当前聚焦的面板）。
const narrowWidth = 90

func (m model) render() string {
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
	if m.modal == nil {
		return assembled
	}
	// 弹窗居中覆盖。
	modalBox := m.modalView()
	x := max((m.width-lipgloss.Width(modalBox))/2, 0)
	y := max((m.height-lipgloss.Height(modalBox))/2, 0)
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(assembled),
		lipgloss.NewLayer(modalBox).X(x).Y(y),
	)
	return comp.Render()
}

// wideLayout 左侧五个小面板 + 右侧内容区。
func (m model) wideLayout() string {
	hs := m.panelHeights()
	boxes := make([]string, 0, panelCount)
	for p := panel(0); p < panelCount; p++ {
		boxes = append(boxes, m.sidebarBox(p, hs[p]))
	}
	right := drawBox(m.rightW, m.panelTitle(m.focus), true, true, m.focusedBody(), m.bodyH()-2)
	return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.JoinVertical(lipgloss.Left, boxes...), right)
}

// narrowLayout 窄终端：只渲染当前面板，任务面板附带文件列表。
func (m model) narrowLayout() string {
	body := m.focusedBody()
	if h := m.narrowFilePaneH(); h > 0 {
		body = m.filePane(h) + "\n\n" + body
	}
	return drawBox(m.width, m.panelTitle(m.focus), true, true, body, m.bodyH()-2)
}

// ---------- 顶栏与底栏 ----------

// header 顶部标题栏：左侧应用徽标，右侧显示当前操作组合或执行状态。
func (m model) header() string {
	left := appBadgeStyle.Render("batcher" + badgeVersion())

	var right string
	switch {
	case m.running:
		right = stepActiveStyle.Render("执行中…")
	case errors.Is(m.runErr, context.Canceled):
		right = stepTodoStyle.Render("上次执行已取消，可修改后重跑")
	case m.runErr != nil:
		right = errorStyle.Render("上次执行失败，可修改后重跑")
	case m.total > 0:
		right = successStyle.Render(fmt.Sprintf("上次执行 %d/%d 成功，可修改后重跑", m.done, m.total))
	default:
		right = stepTodoStyle.Render(displaySel(m.ops))
	}
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return left + strings.Repeat(" ", gap) + right + " "
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
		case m.previewDone && m.planErr == nil && len(m.files) > 0:
			title = fmt.Sprintf("5 · 任务（%d 个文件）", len(m.files))
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
		mark := uncheckedStyle.Render("[ ]")
		if feat.selected(m.ops) {
			mark = checkedStyle.Render("[✔]")
		}
		focused := i == m.featCursor
		rows = append(rows, cursorMark(focused)+mark+" "+itemStyle(focused).Render(feat.label))
	}
	return sidebarRows(rows, m.featCursor, h)
}

// presetsPane 预设列表（内置 → 自定义 → 手动输入），超出高度时随光标滚动。
func (m model) presetsPane(h int) string {
	rows := make([]string, 0, m.presetCount())
	for i, bp := range m.builtins {
		extra := descStyle.Render("内置 · " + bp.Args)
		rows = append(rows, presetsRow(i == m.presetCursor, bp.Name, extra))
	}
	for i, p := range m.presets {
		title := p.Args
		if p.Name != "" {
			title = p.Name + "  " + descStyle.Render(p.Args)
		}
		rows = append(rows, presetsRow(m.presetUserOffset()+i == m.presetCursor, title, ""))
	}
	manual := m.presetUserOffset() + len(m.presets)
	extra := ""
	if len(m.presets) == 0 {
		extra = descStyle.Render("按 a 复制内置")
	}
	rows = append(rows, presetsRow(m.presetCursor == manual, "(手动输入)", extra))
	return sidebarRows(rows, m.presetCursor, h)
}

// presetsRow 渲染一条预设列表行（不带换行），extra 为灰色说明。
func presetsRow(focused bool, label, extra string) string {
	line := itemStyle(focused).Render(label)
	if extra != "" {
		line += " " + extra
	}
	return cursorMark(focused) + line
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
		line := fmt.Sprintf("%d. .%s  %s", i+1, sub.Suffix, sub.Lang)
		if sub.Default {
			line += " " + checkedStyle.Render("[默认]")
		}
		rows = append(rows, cursorMark(focused)+itemStyle(focused).Render(line))
	}
	return sidebarRows(rows, m.subCursor, h)
}

// formPane 参数面板字段列表。
func (m model) formPane(h int) string {
	rows := make([]string, 0, len(m.fields))
	for i := range m.fields {
		f := &m.fields[i]
		focused := i == m.focusNo
		rows = append(rows, cursorMark(focused)+itemStyle(focused).Render(f.def.label))
	}
	return sidebarRows(rows, m.focusNo, h)
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
	b.WriteString(taskTitleStyle.Render(feat.label) + "\n")
	b.WriteString(subtitleStyle.Render(feat.desc) + "\n\n")
	for i := range features {
		mark := uncheckedStyle.Render("[ ]")
		if features[i].selected(m.ops) {
			mark = checkedStyle.Render("[✔]")
		}
		b.WriteString(mark + " " + itemStyle(i == m.featCursor).Render(features[i].label) + "\n")
	}
	b.WriteString("\n  空格 勾选；已选 " + selectedStyle.Render(displaySel(m.ops)) + "\n")
	if !m.ops.Any() {
		b.WriteString("  " + errorStyle.Render("× 请至少勾选一项") + "\n")
	}
	b.WriteString("\n  按 2/3/4 配置面板，完成后按 x 执行。\n")
	if m.featErr != "" {
		b.WriteString("  " + errorStyle.Render("× "+m.featErr) + "\n")
	}
	return statusSuffix(m.statusMsg) + b.String()
}

// presetsBody 预设面板：选中项详情与提示。
func (m model) presetsBody() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render("视频转码 · 参数选择") + "\n")
	offset := m.presetUserOffset()
	switch {
	case m.presetCursor < offset:
		bp := m.builtins[m.presetCursor]
		b.WriteString(subtitleStyle.Render("内置预设："+bp.Name+"（只读，按 a 可复制微调）") + "\n\n")
		b.WriteString("  " + bp.Args + "\n")
	case m.presetCursor < offset+len(m.presets):
		p := m.presets[m.presetCursor-offset]
		label := p.Name
		if label == "" {
			label = "（无名称）"
		}
		b.WriteString(subtitleStyle.Render("自定义预设："+label) + "\n\n")
		b.WriteString("  " + p.Args + "\n")
	default:
		b.WriteString(subtitleStyle.Render("手动输入：enter 后在面板 [4] 直接编辑高级参数") + "\n\n")
		b.WriteString("  " + descStyle.Render("ctrl+s 可把已填参数另存为预设") + "\n")
	}
	return statusSuffix(m.statusMsg) + b.String()
}

// subsBody 字幕面板：列表详情与匹配说明。
func (m model) subsBody() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render("添加字幕 · 字幕轨道") + "\n")
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
	return statusSuffix(m.statusMsg) + b.String()
}

// formBody 参数面板：表单字段（浏览态显示值，编辑态显示输入框）。
func (m model) formBody() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render(displaySel(m.ops)+" · 参数") + "\n")
	if m.editing {
		b.WriteString(subtitleStyle.Render("↑/↓/Tab 切换字段，enter 下一项，esc 完成") + "\n")
	} else {
		b.WriteString(subtitleStyle.Render("enter 进入编辑；ctrl+s 把高级参数另存为预设") + "\n")
	}
	b.WriteString("\n")

	for i := range m.fields {
		f := &m.fields[i]
		lbl := labelStyle.Render(f.def.label)
		if i == m.focusNo {
			lbl = labelFocusedStyle.Render(f.def.label)
		}
		b.WriteString("  " + lbl + " " + f.input.View() + "\n")
		if f.err != "" {
			b.WriteString("  " + strings.Repeat(" ", 18) + errorStyle.Render("× "+f.err) + "\n")
		}
	}
	if m.ops.AddSubtitle {
		parts := make([]string, 0, len(m.subs))
		for i, sub := range m.subs {
			parts = append(parts, fmt.Sprintf("%d.%s(%s)", i+1, sub.Suffix, sub.Lang))
		}
		b.WriteString("\n  " + labelStyle.Render("字幕列表") + " " + descStyle.Render(strings.Join(parts, "  ")) + "\n")
	}
	if m.planErr != nil {
		b.WriteString("  " + errorStyle.Render("× "+m.planErr.Error()) + "\n")
	}
	return statusSuffix(m.statusMsg) + b.String()
}

// runBody 任务面板：命令预览（浏览态）或置顶命令 + 进度与日志（执行中）。
// 队列视图激活（入队后）一直显示置顶命令/进度与日志，结束后可继续查看各批次。
func (m model) runBody() string {
	var b strings.Builder
	if m.running || m.queueViewActive() {
		if row, ok := m.focusedBatchRow(); ok {
			b.WriteString(m.batchOverviewBlock(row))
		} else {
			// 主体内容最上方固定显示所选任务的完整 CLI 命令（不随日志滚动）。
			b.WriteString(m.pinnedCmdBlock())
		}
		b.WriteString(m.progressBlock())
		b.WriteString(m.logVp.View())
		return statusSuffix(m.statusMsg) + b.String()
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
			b.WriteString("  " + descStyle.Render("↑/↓ 切换文件查看输出；直接修改配置按 x 重跑") + "\n\n")
		}
		b.WriteString(m.vp.View())
	default:
		b.WriteString("  " + descStyle.Render("配置变化时自动生成命令预览") + "\n")
	}
	return statusSuffix(m.statusMsg) + b.String()
}

// statusSuffix 以独立块返回临时提示文本（为空时返回空串）。
func statusSuffix(msg string) string {
	if msg == "" {
		return ""
	}
	return "\n  " + statusStyle.Render(msg) + "\n"
}

// progressBlock 渲染进度条与说明，行数与 runBarH 一致。
// 完成数不计仍可重试的失败项，所以失败后按 r 不会把进度再往前推一格。
func (m model) progressBlock() string {
	settled, failed, _, _, total, paused := m.progressNumbers()
	if total <= 0 {
		return "  " + subtitleStyle.Render("正在启动...") + "\n"
	}
	if !m.running && !paused {
		head := fmt.Sprintf("进度: %d/%d 完成，耗时 %s", settled, total, m.elapsedNow().Round(time.Second).String())
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
	b.WriteString("  " + descStyle.Render("固定 · 当前任务完整命令（y 复制）") + "\n")
	if cmd == "" {
		b.WriteString("  " + subtitleStyle.Render("等待任务开始…") + "\n")
	} else {
		b.WriteString("  " + wrapText(cmdPinStyle.Render(cmd), max(m.rightW-4, 10)) + "\n")
	}
	return b.String()
}

// batchOverviewBlock 渲染主任务总览，固定 4 行，与 runPinH 一致。
func (m model) batchOverviewBlock(row rosterRow) string {
	line := func(s string) string {
		return "  " + descStyle.Render(truncateText(s, max(m.rightW-4, 4))) + "\n"
	}
	if m.queue == nil {
		return line("主任务") + line("") + line("") + line("")
	}
	snap, ok := m.queue.BatchCopy(row.batchID)
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
	if cmd == "" {
		return 2
	}
	return 1 + len(strings.Split(wrapText(cmdPinStyle.Render(cmd), max(m.rightW-4, 10)), "\n"))
}

// runBarH 进度区占用的行数，与 progressBlock 一致。
func (m model) runBarH() int {
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

// filePane 渲染左侧列表。队列视图激活（入队后）显示批次与展开的子任务；
// 空闲时显示命令预览文件列表（第 0 项为“全部”汇总）。超出行数随光标滚动。
func (m model) filePane(h int) string {
	if len(m.roster) > 0 {
		rows := make([]string, 0, len(m.roster))
		for i, row := range m.roster {
			rows = append(rows, m.rosterRowView(i, row))
		}
		return strings.Join(windowRows(rows, m.fileCursor, h), "\n")
	}
	if len(m.files) == 0 {
		return ""
	}
	rows := make([]string, 0, len(m.files)+1)
	rows = append(rows, m.fileRow(-1, "全部"))
	for i, it := range m.files {
		rows = append(rows, m.fileRow(i, filepath.Base(it.input)))
	}
	return strings.Join(windowRows(rows, m.fileCursor, h), "\n")
}

// rosterRowView 渲染队列列表的一行：批次行显示状态与进度概要；子任务行缩进显示状态。
func (m model) rosterRowView(idx int, row rosterRow) string {
	focused := idx == m.fileCursor
	var name string
	if row.itemID == "" {
		name = truncateText(row.title, m.leftW-6)
	} else {
		name = "  " + truncateText(row.title, m.leftW-8)
	}
	name += " " + fileStatusMark(row.state)
	return cursorMark(focused) + itemStyle(focused).Render(name)
}

// fileRow 渲染文件列表一行（不带换行）；idx 为文件下标（-1 表示“全部”）。
func (m model) fileRow(idx int, name string) string {
	focused := idx+1 == m.fileCursor
	if idx < 0 && m.total > 0 {
		settled, _, _, _, total, _ := m.progressNumbers()
		name = fmt.Sprintf("全部（%d/%d）", settled, total)
	}
	if m.total > 0 {
		state := filePending
		if idx >= 0 && idx < len(m.files) {
			state = m.files[idx].state
		}
		name += " " + fileStatusMark(state)
	}
	name = truncateText(name, m.leftW-4)
	return cursorMark(focused) + itemStyle(focused).Render(name)
}

// fileStatusMark 渲染文件执行状态标记。
func fileStatusMark(state string) string {
	switch state {
	case fileRunning:
		return subtitleStyle.Render("▶")
	case filePaused:
		return subtitleStyle.Render("‖")
	case fileDone:
		return logLineDoneStyle.Render("✔")
	case fileFailed:
		return logLineFailedStyle.Render("✘")
	case fileCanceled, fileSkipped:
		return descStyle.Render("−")
	default:
		return descStyle.Render("○")
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
	borderColor := "238"
	switch {
	case !enabled:
		borderColor = "236"
	case focused:
		borderColor = "39"
	}
	border := lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor))
	titleStyle := descStyle
	if focused && enabled {
		titleStyle = selectedStyle
	}

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
			if m.modal.kind == modalSub {
				hints = []string{"↑/↓/Tab 切换", "enter 下一项/保存", "space 切换默认轨道", "esc 取消"}
			}
		}
	case m.outputFocus:
		hints = []string{"↑/↓ 滚动输出", "pgup/pgdn 翻页", "esc 返回", "y 复制"}
	case m.editing:
		hints = []string{"↑/↓/Tab 切换", "enter 下一项", "esc 完成", "ctrl+s 另存预设"}
	case m.running || (m.focus == panelRun && m.queueViewActive()):
		hints = []string{"↑/↓ 切换", "r 重试任务", "d 删除子任务/主任务", "c 取消任务", "s 跳过", "a 中止", "y 复制", "f 跟随"}
		if m.running {
			hints = append(hints, "q 取消执行")
		} else {
			hints = append(hints, "? 帮助", "q 退出")
		}
	case m.focus == panelOps:
		hints = []string{"space 勾选", "2-4 配置面板", "? 快捷键", "x 执行", "q 退出"}
	case m.focus == panelPresets:
		hints = []string{"↑/↓ 选择", "enter 使用", "n 新增", "e 编辑", "d 删除", "? 快捷键"}
	case m.focus == panelSubs:
		hints = []string{"↑/↓ 选择", "n 新增", "e 编辑", "t 切换默认轨道", "d 删除", "? 快捷键"}
	case m.focus == panelForm:
		hints = []string{"↑/↓ 选择字段", "enter 编辑", "ctrl+s 另存预设", "? 快捷键"}
	case m.focus == panelRun:
		hints = []string{"↑/↓ 切换文件", "enter/x 执行", "y 复制命令", "pgup/pgdn 滚动", "? 快捷键", "q 退出"}
	default:
		hints = []string{"1-5/tab 切面板", "? 快捷键", "q 退出"}
	}
	divider := dividerStyle.Render(strings.Repeat("─", max(m.width, 1)))
	keys := make([]string, 0, len(hints))
	for _, h := range hints {
		k, v, ok := strings.Cut(h, " ")
		if !ok {
			keys = append(keys, helpDescStyle.Render(h))
			continue
		}
		keys = append(keys, helpKeyStyle.Render(k)+" "+helpDescStyle.Render(v))
	}
	return divider + "\n " + ansi.Truncate(strings.Join(keys, helpSepStyle.Render("  ·  ")), max(m.width-2, 1), "…")
}
