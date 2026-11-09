package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gsxhnd/batcher/internal/presets"
)

// ---------- View ----------

// View 实现 tea.Model，将整页内容放入备用屏幕缓冲（全屏模式）。
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m model) render() string {
	if m.width == 0 {
		return "加载中..."
	}

	frame := panelStyle.Width(m.width - 2).Render(m.body())
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), frame, m.footer())
}

// stepLabels 返回当前操作组合下的流程步骤，与 afterAdvance / formBackPhase 的分流保持一致。
func (m model) stepLabels() []string {
	steps := []string{"操作"}
	if m.ops.Convert {
		steps = append(steps, "转码")
	}
	if m.ops.AddSubtitle {
		steps = append(steps, "字幕")
	}
	return append(steps, "参数", "预览", "执行")
}

// currentStep 返回当前界面对应的步骤名。
func (m model) currentStep() string {
	switch m.phase {
	case phaseFeature:
		return "操作"
	case phaseAdvance:
		return "转码"
	case phaseSubs, phaseSubEdit:
		return "字幕"
	case phaseForm:
		return "参数"
	case phasePreview:
		return "预览"
	default:
		return "执行"
	}
}

// header 顶部标题栏：左侧应用徽标（发布版本时会带版本号），右侧显示流程步骤并高亮当前步骤。
func (m model) header() string {
	left := appBadgeStyle.Render("batcher" + badgeVersion())
	if m.phase == phasePresets || m.phase == phasePresetEdit {
		right := stepActiveStyle.Render("转码参数管理") + " "
		gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
		return left + strings.Repeat(" ", gap) + right
	}
	cur := m.currentStep()
	reached := true
	var parts []string
	for _, s := range m.stepLabels() {
		switch {
		case s == cur:
			parts = append(parts, stepActiveStyle.Render(s))
			reached = false
		case reached:
			parts = append(parts, stepDoneStyle.Render("✔ "+s))
		default:
			parts = append(parts, stepTodoStyle.Render(s))
		}
	}
	right := strings.Join(parts, stepSepStyle.Render(" › "))
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return left + strings.Repeat(" ", gap) + right + " "
}

func (m model) body() string {
	switch m.phase {
	case phaseFeature:
		return m.featureView()
	case phaseAdvance:
		return m.advanceView()
	case phaseSubs:
		return m.subsView()
	case phaseSubEdit:
		return m.subEditView()
	case phaseForm:
		return m.formView()
	case phasePreview:
		return m.previewView()
	case phaseRunning:
		return m.runningView()
	case phaseResult:
		return m.resultView()
	case phasePresets:
		return m.presetsView()
	case phasePresetEdit:
		return m.presetEditView()
	}
	return ""
}

// statusLine 渲染界面上的临时提示（保存成功、错误等）。
func statusLine(msg string) string {
	if msg == "" {
		return ""
	}
	return "\n  " + statusStyle.Render(msg) + "\n"
}

// ---------- 各页面渲染 ----------

func (m model) featureView() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("勾选本次批处理要执行的操作（可任意组合，一次生成一条 ffmpeg 命令）：") + "\n\n")
	for i, feat := range features {
		mark := uncheckedStyle.Render("[ ]")
		if feat.selected(m.ops) {
			mark = checkedStyle.Render("[✔]")
		}
		focused := i == m.featCursor
		b.WriteString(cursorMark(focused) + mark + " " + itemStyle(focused).Render(feat.label) + "\n")
		if focused {
			b.WriteString("      " + descStyle.Render(feat.desc) + "\n")
		}
	}
	b.WriteString("\n  已选择: " + selectedStyle.Render(displaySel(m.ops)) + "\n")
	if m.featErr != "" {
		b.WriteString("  " + errorStyle.Render("× "+m.featErr) + "\n")
	}
	return b.String()
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

// presetRows 渲染一条转码参数列表项，extra 为追加在参数后的灰色说明。
func presetRows(focused bool, args, extra string) string {
	line := itemStyle(focused).Render(args)
	if extra != "" {
		line += "  " + descStyle.Render(extra)
	}
	return cursorMark(focused) + line
}

func (m model) advanceView() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render("视频转码 · 转码参数") + "\n")
	b.WriteString(subtitleStyle.Render("↑/↓ 选择，Enter 使用所选继续；d 删除自定义预设；a 从内置预设复制微调；p 管理自定义预设") + "\n\n")

	// 内置预设（只读）
	for i, bp := range m.builtins {
		b.WriteString(presetRows(i == m.presetCursor, bp.Args, bp.Name+" · 内置") + "\n")
	}

	// 用户自定义预设：名称为主标签，参数作为说明；无名称时直接显示参数。
	for i, p := range m.presets {
		focused := m.advanceUserOffset()+i == m.presetCursor
		line := presetRows(focused, presetTitle(p), presetDetail(p, "自定义"))
		if i == m.delPendingIdx {
			line += "  " + errorStyle.Render("× 再次按 d 确认删除")
		}
		b.WriteString(line + "\n")
	}

	// 手动输入
	manual := m.advanceUserOffset() + len(m.presets)
	extra := ""
	if len(m.presets) == 0 {
		extra = "或按 a 从选中的内置预设复制"
	}
	b.WriteString(presetRows(m.presetCursor == manual, "(手动输入参数)", extra) + "\n")

	b.WriteString(statusLine(m.statusMsg))
	return b.String()
}

func (m model) presetsView() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render("转码参数预设管理") + "\n")
	b.WriteString(subtitleStyle.Render("内置预设只读提供参考；自定义预设支持 a 新增，e/Enter 编辑，d 删除，shift+↑/↓ 调整顺序") + "\n\n")

	// 内置预设（只读展示）
	b.WriteString("  " + sectionStyle.Render("内置预设") + "\n")
	for i, bp := range m.builtins {
		b.WriteString(fmt.Sprintf("    %2d. %s\n", i+1, normalStyle.Render(bp.Args)+descStyle.Render("  "+bp.Name)))
	}

	// 用户自定义预设：每条占一行，名称在前、参数在后，名称为空时只显示参数。
	b.WriteString("\n  " + sectionStyle.Render(fmt.Sprintf("自定义预设（%d 条）", len(m.presets))) + "\n")
	if len(m.presets) == 0 {
		b.WriteString("    " + descStyle.Render("暂无自定义预设，按 a 新增一条") + "\n")
	}
	for i, p := range m.presets {
		focused := i == m.mgrCursor
		line := fmt.Sprintf("%s%2d. %s", cursorMark(focused), i+1, itemStyle(focused).Render(presetTitle(p)))
		if p.Name != "" {
			line += "  " + descStyle.Render(p.Args)
		}
		if focused && i == m.delPendingIdx {
			line += "  " + errorStyle.Render("× 再次按 d 确认删除")
		}
		b.WriteString(line + "\n")
	}

	b.WriteString(statusLine(m.statusMsg))
	b.WriteString("\n  " + descStyle.Render("自定义预设保存位置: "+presetHintText()) + "\n")
	return b.String()
}

// presetTitle 返回预设列表中的主标签：有名称用名称，否则用参数文本。
func presetTitle(p presets.Preset) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Args
}

// presetDetail 返回预设列表中的说明文字：有名称时附上参数，无名称时只标注来源。
func presetDetail(p presets.Preset, source string) string {
	if p.Name != "" {
		return p.Args
	}
	return source
}

func (m model) presetEditView() string {
	var b strings.Builder
	title := "新增转码参数"
	if m.mgrEditIndex >= 0 {
		title = fmt.Sprintf("编辑第 %d 条转码参数", m.mgrEditIndex+1)
	}
	b.WriteString(taskTitleStyle.Render(title) + "\n")
	b.WriteString(subtitleStyle.Render("↑/↓ 或 Tab 切换字段，Enter 下一项 / 最后一项保存，esc 取消；参数会按空格拆分后传给 ffmpeg") + "\n\n")
	for i := range m.mgrFields {
		f := &m.mgrFields[i]
		lbl := labelStyle.Render(f.def.label)
		if i == m.mgrFocus {
			lbl = labelFocusedStyle.Render(f.def.label)
		}
		b.WriteString("  " + lbl + " " + f.input.View() + "\n")
	}
	if m.mgrErr != "" {
		b.WriteString("  " + strings.Repeat(" ", 18) + errorStyle.Render("× "+m.mgrErr) + "\n")
	}
	return b.String()
}

func (m model) subsView() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render("添加字幕 · 字幕列表") + "\n")
	b.WriteString(subtitleStyle.Render("字幕按 <视频名>.<后缀> 或 <视频名>.<标记>.<后缀> 匹配文件") + "\n\n")
	if len(m.subs) == 0 {
		b.WriteString("  " + descStyle.Render("字幕列表为空，按 a 新增一条") + "\n")
	}
	for i, sub := range m.subs {
		focused := i == m.subCursor
		b.WriteString(fmt.Sprintf("%s%d. %s  语言: %s  标题: %s\n", cursorMark(focused), i+1,
			itemStyle(focused).Render("."+sub.Suffix), sub.Lang, sub.Title))
	}
	if m.subsErr != "" {
		b.WriteString("\n  " + errorStyle.Render("× "+m.subsErr) + "\n")
	}
	return b.String()
}

func (m model) subEditView() string {
	var b strings.Builder
	title := "新增字幕"
	if m.subEditIndex >= 0 {
		title = fmt.Sprintf("编辑第 %d 条字幕", m.subEditIndex+1)
	}
	b.WriteString(taskTitleStyle.Render(title) + "\n")
	b.WriteString(subtitleStyle.Render("↑/↓ 或 Tab 切换字段，Enter 编辑与保存，esc 取消返回") + "\n\n")
	for i := range m.subFields {
		f := &m.subFields[i]
		lbl := labelStyle.Render(f.def.label)
		if i == m.editFocus {
			lbl = labelFocusedStyle.Render(f.def.label)
		}
		b.WriteString("  " + lbl + " " + f.input.View() + "\n")
		if f.err != "" {
			b.WriteString("  " + strings.Repeat(" ", 18) + errorStyle.Render("× "+f.err) + "\n")
		}
	}
	return b.String()
}

func (m model) formView() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render(displaySel(m.ops)+" · 参数填写") + "\n")
	b.WriteString(subtitleStyle.Render("↑/↓ 或 Tab 切换字段，Enter 进入下一项、最后一项直接提交，esc 返回") + "\n\n")

	for i := range m.fields {
		f := &m.fields[i]
		lbl := labelStyle.Render(f.def.label)
		if i == m.focus {
			lbl = labelFocusedStyle.Render(f.def.label)
		}
		b.WriteString("  " + lbl + " " + f.input.View() + "\n")
		if f.err != "" {
			b.WriteString("  " + strings.Repeat(" ", 18) + errorStyle.Render("× "+f.err) + "\n")
		}
	}
	if m.ops.AddSubtitle {
		var parts []string
		for i, sub := range m.subs {
			parts = append(parts, fmt.Sprintf("%d.%s(%s)", i+1, sub.Suffix, sub.Lang))
		}
		b.WriteString("  " + labelStyle.Render("字幕列表") + " " + descStyle.Render(strings.Join(parts, "  ")) + "\n")
	}
	b.WriteString("\n")
	if m.planErr != nil {
		b.WriteString("  " + errorStyle.Render("× "+m.planErr.Error()) + "\n")
	}
	if m.statusMsg != "" {
		b.WriteString("  " + statusStyle.Render(m.statusMsg) + "\n")
	}
	return b.String()
}

func (m model) previewView() string {
	// Plan 进行中或出错时还没有可用的文件列表，退回整页提示。
	if !m.previewDone || m.planErr != nil {
		var b strings.Builder
		b.WriteString(taskTitleStyle.Render(displaySel(m.ops)+" · 命令预览") + "\n")
		switch {
		case m.planErr != nil:
			b.WriteString("  " + errorStyle.Render("× 生成命令失败: "+m.planErr.Error()) + "\n\n")
			b.WriteString("  按 Enter / esc 返回修改参数。\n")
		case !m.previewDone:
			b.WriteString("  " + m.spin.View() + " " + subtitleStyle.Render(m.preview) + "\n")
		}
		return b.String()
	}

	title := ansi.Truncate(taskTitleStyle.Render(displaySel(m.ops)+" · 命令预览"), max(m.rightW, 10), "…") + "\n"
	right := title + m.vp.View()
	return m.splitView(m.filePane(), right)
}

func (m model) runningView() string {
	var b strings.Builder
	if m.total > 0 {
		b.WriteString("  " + m.bar.View() + "\n")
		b.WriteString(m.progressHint())
	} else {
		b.WriteString("  " + subtitleStyle.Render("正在启动...") + "\n")
		b.WriteString("  " + subtitleStyle.Render("进度: 0"))
	}
	right := b.String() + "\n" + m.logVp.View()
	return m.splitView(m.filePane(), right)
}

// progressHint 组合进度/耗时/当前命令的一行状态文本，超宽时截断。
func (m model) progressHint() string {
	hint := fmt.Sprintf("进度: %d/%d", m.done+m.failed, m.total)
	if m.failed > 0 {
		hint += fmt.Sprintf("，失败 %d", m.failed)
	}
	hint += "   耗时: " + time.Since(m.startAt).Round(time.Second).String()
	// “全部”视图下附上正在执行的命令，其他视图下该命令已在对应文件日志里。
	if m.fileCursor == 0 && m.current != "" {
		hint += "   当前: " + m.current
	}
	hint = subtitleStyle.Render(hint)
	return "  " + ansi.Truncate(hint, max(m.rightW-2, 10), "…")
}

func (m model) resultView() string {
	head := ""
	if m.runErr != nil {
		head = errorStyle.Render(fmt.Sprintf("× 批处理失败: %v", m.runErr))
	} else {
		head = successStyle.Render(fmt.Sprintf("✔ 批处理完成: %d/%d 个任务成功", m.done, m.total))
	}
	right := "  " + head + "   耗时: " + m.elapsed.String() + "\n" +
		"  " + descStyle.Render("↑/↓ 切换文件查看该文件的执行输出") + "\n" + m.logVp.View()
	return m.splitView(m.filePane(), right)
}

// ---------- 左右分栏渲染 ----------

// splitView 把左侧源文件栏与右侧内容水平拼接，两栏顶对齐。
func (m model) splitView(left, right string) string {
	pane := lipgloss.NewStyle().
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("238")).
		Width(m.leftW).
		Height(m.bodyH())
	return lipgloss.JoinHorizontal(lipgloss.Top, pane.Render(left), right)
}

// filePane 渲染左侧源文件列表：第 0 项为“全部”汇总，其后为各个源文件与执行状态。
func (m model) filePane() string {
	var b strings.Builder
	if len(m.files) == 0 {
		b.WriteString("  " + descStyle.Render("未找到匹配的源文件") + "\n")
	} else {
		rows := make([]string, 0, len(m.files)+1)
		rows = append(rows, m.fileRow(-1, "全部"))
		for i, it := range m.files {
			rows = append(rows, m.fileRow(i, filepath.Base(it.input)))
		}
		// 列表超出可视高度时，保证光标行始终可见。
		h := max(m.bodyH(), 1)
		start := 0
		if end := m.fileCursor + 1; end >= start+h {
			start = end - h
		}
		if m.fileCursor < start {
			start = m.fileCursor
		}
		end := min(start+h, len(rows))
		for _, row := range rows[start:end] {
			b.WriteString(row)
		}
	}
	return b.String()
}

// fileRow 渲染左侧列表一行；idx 为文件下标（-1 表示“全部”）。
func (m model) fileRow(idx int, name string) string {
	focused := idx+1 == m.fileCursor
	// 执行/结果阶段在“全部”行尾显示汇总进度。
	if idx < 0 && m.total > 0 && (m.phase == phaseRunning || m.phase == phaseResult) {
		name = fmt.Sprintf("全部（%d/%d）", m.done+m.failed, m.total)
	}
	if m.phase == phaseRunning || m.phase == phaseResult {
		state := filePending
		if idx >= 0 && idx < len(m.files) {
			state = m.files[idx].state
		}
		name += " " + fileStatusMark(state)
	}
	return cursorMark(focused) + itemStyle(focused).Render(ansi.Truncate(name, max(m.leftW-3, 4), "…")) + "\n"
}

// fileStatusMark 渲染文件执行状态标记。
func fileStatusMark(state string) string {
	switch state {
	case fileRunning:
		return subtitleStyle.Render("▶")
	case fileDone:
		return logLineDoneStyle.Render("✔")
	case fileFailed:
		return logLineFailedStyle.Render("✘")
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

func (m model) footer() string {
	var hints []string
	switch m.phase {
	case phaseFeature:
		hints = []string{"↑/↓ 切换", "space 勾选", "Enter 下一步", "p 参数管理", "q 退出"}
	case phaseAdvance:
		hints = []string{"↑/↓ 选择", "Enter 使用", "a 复制内置", "d×2 删除", "p 参数管理", "esc 返回"}
	case phasePresets:
		hints = []string{"a 新增", "e 编辑", "d×2 删除", "shift+↑/↓ 排序", "esc 返回"}
	case phasePresetEdit:
		hints = []string{"↑/↓/Tab 切换", "Enter 下一个/保存", "esc 取消"}
	case phaseSubs:
		hints = []string{"a 新增", "e 编辑", "d 删除", "↑/↓ 选择", "Enter 继续", "esc 返回"}
	case phaseSubEdit:
		hints = []string{"↑/↓/Tab 切换", "Enter 保存", "esc 取消"}
	case phaseForm:
		hints = []string{"↑/↓/Tab 切换", "Enter 下一个/提交", "ctrl+s 保存预设", "esc 返回"}
	case phasePreview:
		hints = []string{"↑/↓ 切换文件", "pgup/pgdn 滚动", "Enter 开始执行", "esc 返回"}
	case phaseRunning:
		hints = []string{"↑/↓ 切换文件", "f 跟随当前", "pgup/pgdn 滚动", "q/esc 中止并退出"}
	case phaseResult:
		hints = []string{"↑/↓ 切换文件", "r 重新执行", "b 返回配置", "q 退出"}
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
	return divider + "\n" + " " + strings.Join(keys, helpSepStyle.Render("  ·  "))
}
