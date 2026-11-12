package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/commandline"
	"github.com/gsxhnd/batcher/internal/presets"
)

// openTaskModal 打开新建任务弹窗。转码、字幕、字体和路径都在这一个表单里，没有分步。
func (m *model) openTaskModal() tea.Cmd {
	d := command.DefaultOption()
	defs := []fieldDef{
		{key: "op_convert", label: "视频转码", kind: fieldBool},
		{key: "op_subtitle", label: "添加字幕", kind: fieldBool},
		{key: "op_fonts", label: "添加字体", kind: fieldBool},
		{key: "input_path", label: "源视频路径"},
		{key: "input_format", label: "源视频后缀"},
		{key: "output_path", label: "输出目录"},
		{key: "output_format", label: "输出后缀"},
		{key: "preset", label: "转码预设", kind: fieldChoice},
		{key: "advance", label: "高级参数"},
		{key: "fonts_path", label: "字体文件夹"},
	}
	fields := make([]field, 0, len(defs)+4)
	for _, def := range defs {
		nf := newField(def)
		nf.applyDefaults(&d)
		if def.key == "advance" {
			nf.input.Placeholder = "例如: -c:v libx265 -crf 26 -c:a copy"
		}
		if def.kind == fieldChoice {
			nf.options, nf.choiceArgs = m.presetChoices()
		}
		fields = append(fields, nf)
	}
	m.modal = &modal{kind: modalTask, title: "新建任务", fields: fields}
	m.appendSubtitleFields(subtitleDefaults())
	return focusField(m.modal.fields, 0)
}

func (m model) presetChoices() (labels, args []string) {
	labels = append(labels, "手动输入")
	args = append(args, "")
	for _, p := range m.presets {
		label := p.Name
		if label == "" {
			label = p.Args
		}
		labels = append(labels, label)
		args = append(args, p.Args)
	}
	return labels, args
}

func (m *model) applyPresetChoice(f *field) {
	if m.modal == nil || f.choice < 0 || f.choice >= len(f.choiceArgs) {
		return
	}
	args := f.choiceArgs[f.choice]
	if args == "" {
		return
	}
	for i := range m.modal.fields {
		if m.modal.fields[i].def.key == "advance" {
			m.modal.fields[i].input.SetValue(args)
		}
	}
}

func (m *model) appendSubtitleFields(entry command.SubtitleOption) {
	if m.modal == nil {
		return
	}
	values := map[string]string{
		"sub_suffix": entry.Suffix,
		"sub_lang":   entry.Lang,
		"sub_title":  entry.Title,
	}
	for _, def := range subFieldDefs {
		nf := newField(def)
		nf.applySubDefaults()
		nf.input.SetValue(values[def.key])
		nf.checked = entry.Default
		m.modal.fields = append(m.modal.fields, nf)
	}
}

func (m *model) removeSubtitleFields() {
	md := m.modal
	if md == nil || len(md.fields) == 0 {
		return
	}
	start := -1
	for i := md.focus; i >= 0; i-- {
		switch md.fields[i].def.key {
		case "sub_suffix":
			start = i
		case "sub_lang", "sub_title", "sub_default":
			continue
		default:
			i = -1
		}
		if start >= 0 || i < 0 {
			break
		}
	}
	if start < 0 || start+4 > len(md.fields) {
		md.err = "把光标移到字幕上再按 ctrl+d"
		return
	}
	md.fields = slices.Delete(md.fields, start, start+4)
	if md.focus >= len(md.fields) {
		md.focus = max(len(md.fields)-1, 0)
	}
	md.err = ""
}

// saveTaskModal 校验弹窗里的全部字段，扫描目录并创建任务。失败时弹窗保持打开。
func (m *model) saveTaskModal() tea.Cmd {
	if m.creating {
		return nil
	}
	md := m.modal
	if md == nil {
		return nil
	}
	md.err = ""
	sel := command.Selection{}
	opt := command.DefaultOption()
	var subs []command.SubtitleOption
	var cur *command.SubtitleOption
	flush := func() {
		if cur == nil {
			return
		}
		blank := strings.TrimSpace(cur.Suffix) == "" && strings.TrimSpace(cur.Lang) == "" &&
			strings.TrimSpace(cur.Title) == "" && !cur.Default
		if !blank {
			subs = append(subs, *cur)
		}
		cur = nil
	}
	for i := range md.fields {
		f := &md.fields[i]
		f.err = ""
		switch f.def.key {
		case "op_convert":
			sel.Convert = f.checked
		case "op_subtitle":
			sel.AddSubtitle = f.checked
		case "op_fonts":
			sel.AddFonts = f.checked
		case "input_path", "input_format", "output_path", "output_format", "advance", "fonts_path":
			setStringOption(&opt, f.def.key, f.resolved())
		case "sub_suffix":
			flush()
			cur = &command.SubtitleOption{Suffix: f.resolved()}
		case "sub_lang":
			if cur == nil {
				cur = &command.SubtitleOption{}
			}
			cur.Lang = f.resolved()
		case "sub_title":
			if cur == nil {
				cur = &command.SubtitleOption{}
			}
			cur.Title = f.resolved()
		case "sub_default":
			if cur == nil {
				cur = &command.SubtitleOption{}
			}
			cur.Default = f.checked
		}
	}
	flush()
	if !sel.Any() {
		md.err = "请至少勾选一项操作"
		return focusField(md.fields, 0)
	}
	if sel.AddFonts && strings.TrimSpace(opt.FontsPath) == "" {
		md.err = "勾选了添加字体，必须填写字体文件夹"
		return focusTaskField(md, "fonts_path")
	}
	if sel.Convert && strings.TrimSpace(opt.Advance) != "" {
		if _, err := commandline.Parse(opt.Advance); err != nil {
			md.err = err.Error()
			return focusTaskField(md, "advance")
		}
	}
	if !sel.Convert {
		opt.Advance = ""
	}
	if !sel.AddFonts {
		opt.FontsPath = ""
	}
	if sel.AddSubtitle {
		if len(subs) == 0 {
			md.err = "已勾选添加字幕，请至少填写一条"
			return nil
		}
		defaults := 0
		for i, sub := range subs {
			if strings.TrimSpace(sub.Suffix) == "" {
				md.err = fmt.Sprintf("字幕 %d 的后缀不能为空", i+1)
				return nil
			}
			if !isISO639Code(sub.Lang) {
				md.err = fmt.Sprintf("字幕 %d 的语言需为三字母码，如 chi", i+1)
				return nil
			}
			if sub.Default {
				defaults++
			}
		}
		if defaults > 1 {
			md.err = "最多只能有一条默认字幕"
			return nil
		}
		opt.Subtitles = subs
	} else {
		opt.Subtitles = nil
	}

	m.createSel = sel
	m.creating = true
	m.createSeq++
	seq := m.createSeq
	ctx := m.ctx
	optCopy := opt
	md.err = "正在扫描并生成任务…"
	return func() tea.Msg {
		plan, err := command.BuildPlan(ctx, &optCopy, sel)
		return createPlanMsg{seq: seq, plan: plan, err: err}
	}
}

func focusTaskField(md *modal, key string) tea.Cmd {
	for i := range md.fields {
		if md.fields[i].def.key == key {
			md.focus = i
			return focusField(md.fields, i)
		}
	}
	return nil
}

func (m model) presetManageCount() int {
	return len(m.presets)
}

// listedPreset 是预设管理列表里的当前项。列表里的每一条都存在数据库中，可以编辑和删除。
func (m model) listedPreset() (int, bool) {
	if m.presetCursor >= 0 && m.presetCursor < len(m.presets) {
		return m.presetCursor, true
	}
	return 0, false
}

func (m *model) notePresetList(msg string) {
	m.setStatus(msg)
	if m.modal != nil && m.modal.kind == modalPresetList {
		m.modal.err = msg
	}
}

// openPresetList 打开转码预设管理。列表来自数据库，每一条都可以编辑和删除。
func (m *model) openPresetList() {
	m.managePresets = true
	n := m.presetManageCount()
	if n == 0 {
		m.presetCursor = 0
	} else if m.presetCursor < 0 || m.presetCursor >= n {
		m.presetCursor = 0
	}
	m.modal = &modal{kind: modalPresetList, title: "转码预设", focus: m.presetCursor}
}

func (m *model) updatePresetList(key string) tea.Cmd {
	n := m.presetManageCount()
	if m.modal != nil {
		m.presetCursor = m.modal.focus
	}
	switch key {
	case "esc":
		m.managePresets = false
		m.modal = nil
		return nil
	case "up", "k":
		if n > 0 {
			m.presetCursor = (m.presetCursor - 1 + n) % n
		}
		if m.modal != nil {
			m.modal.err = ""
		}
	case "down", "j":
		if n > 0 {
			m.presetCursor = (m.presetCursor + 1) % n
		}
		if m.modal != nil {
			m.modal.err = ""
		}
	case "a", "n":
		return m.openPresetModal(-1, presets.Preset{})
	case "c":
		return m.copyPresetModal()
	case "e", "enter":
		idx, ok := m.listedPreset()
		if !ok {
			m.notePresetList("还没有预设，按 a 从空白新建")
			return nil
		}
		return m.openPresetModal(idx, m.presets[idx])
	case "d":
		idx, ok := m.listedPreset()
		if !ok {
			m.notePresetList("还没有预设，按 a 从空白新建")
			return nil
		}
		name := presetDisplayName(m.presets[idx])
		m.openConfirmModal("删除预设", fmt.Sprintf("删除「%s」？enter 确认，esc 取消", name),
			func(m *model) tea.Cmd { return m.deletePreset(idx) })
		return nil
	case "K", "shift+up":
		if _, ok := m.listedPreset(); !ok {
			m.notePresetList("还没有预设，按 a 从空白新建")
			return nil
		}
		m.movePreset(-1)
	case "J", "shift+down":
		if _, ok := m.listedPreset(); !ok {
			m.notePresetList("还没有预设，按 a 从空白新建")
			return nil
		}
		m.movePreset(1)
	}
	if m.modal != nil && m.modal.kind == modalPresetList {
		m.modal.focus = m.presetCursor
	}
	return nil
}

// copyPresetModal 把当前选中的内置或自定义预设复制成一条新的自定义预设。
func (m *model) copyPresetModal() tea.Cmd {
	if m.managePresets {
		idx, ok := m.listedPreset()
		if !ok {
			m.notePresetList("还没有预设，按 a 从空白新建")
			return nil
		}
		p := m.presets[idx]
		return m.openPresetModal(-1, presets.Preset{Name: p.Name, Args: p.Args})
	}
	if m.presetCursor >= 0 && m.presetCursor < len(m.builtins) {
		b := m.builtins[m.presetCursor]
		return m.openPresetModal(-1, presets.Preset{Name: b.Name, Args: b.Args})
	}
	if idx, ok := m.selectedUserPreset(); ok {
		p := m.presets[idx]
		return m.openPresetModal(-1, presets.Preset{Name: p.Name, Args: p.Args})
	}
	m.setStatus("先选中一条预设，再按 c 复制")
	return nil
}

func (m model) presetListContent() string {
	n := m.presetManageCount()
	cur := 0
	if m.modal != nil {
		cur = m.modal.focus
	}
	rows := make([]string, 0, n)
	if n == 0 {
		rows = append(rows, "  "+descStyle.Render("还没有预设"))
	}
	for i := 0; i < n; i++ {
		p := m.presets[i]
		label := p.Name
		if label == "" {
			label = p.Args
		}
		mark := "  "
		if i == cur {
			mark = "▸ "
		}
		rows = append(rows, mark+label)
	}
	var b strings.Builder
	for _, line := range windowRows(rows, cur, max(m.bodyH()-10, 6)) {
		b.WriteString(line + "\n")
	}
	if cur >= 0 && cur < len(m.presets) {
		b.WriteString("\n  " + codeStyle.Render(m.presets[cur].Args) + "\n")
	}
	if m.modal != nil && m.modal.err != "" {
		b.WriteString("  " + errorStyle.Render("× "+m.modal.err) + "\n")
	}
	b.WriteString("  " + descStyle.Render("a 空白新建 · c 复制当前 · e 编辑 · d 删除 · J/K 排序 · esc 关闭") + "\n")
	return b.String()
}

// taskLine 是新建任务弹窗里的一行。field 是它所属字段的下标，-1 表示分区标题。
type taskLine struct {
	field int
	text  string
}

// taskModalSize 把创建弹窗限制在适中尺寸：比一行表单宽，但不铺满整个终端。
func (m model) taskModalSize() (w, contentH int) {
	w = 72
	contentH = 20
	if m.width > 0 {
		w = min(72, max(m.width-10, 40))
		if w > m.width-2 {
			w = max(m.width-2, 12)
		}
	}
	if m.height > 0 {
		contentH = min(20, max(m.height-10, 12))
		if contentH+2 > m.height-1 {
			contentH = max(m.height-3, 6)
		}
	}
	return w, contentH
}

// taskModalView 渲染分区后的创建表单，并把光标所在字段滚进可视区域。
func (m model) taskModalView() string {
	md := m.modal
	w, contentH := m.taskModalSize()
	lines := m.taskModalLines(w)
	focusAt := 0
	for i, ln := range lines {
		if ln.field == md.focus {
			focusAt = i
			break
		}
	}
	// 错误在顶部、快捷键在底部，这两行不参与滚动。
	pinned := 1
	if md.err != "" {
		pinned++
	}
	window := max(contentH-pinned, 1)
	start := focusAt - max(window/5, 1)
	if start < 0 {
		start = 0
	}
	if start+window > len(lines) {
		start = max(len(lines)-window, 0)
	}
	end := min(start+window, len(lines))
	var body []string
	if md.err != "" {
		body = append(body, "  "+errorStyle.Render("× "+md.err)+"\n")
	}
	for _, ln := range lines[start:end] {
		body = append(body, ln.text)
	}
	for len(body)+1 < contentH {
		body = append(body, "\n")
	}
	body = append(body, "  "+descStyle.Render("↑/↓ 切换 · enter 下一项 · ctrl+s 创建 · ctrl+n / ctrl+d 增删字幕")+"\n")
	var b strings.Builder
	for _, ln := range body {
		b.WriteString(ln)
	}
	title := "新建任务"
	if len(lines) > window {
		title = fmt.Sprintf("新建任务  %d–%d / %d", start+1, end, len(lines))
	}
	return drawBox(w, title, true, true, b.String(), contentH)
}

func (m model) taskModalLines(boxW int) []taskLine {
	md := m.modal
	if md == nil {
		return nil
	}
	convertOn, subOn, fontOn := false, false, false
	subGroups := 0
	var ops, paths, encode, fonts, subs []int
	for i := range md.fields {
		switch md.fields[i].def.key {
		case "op_convert":
			convertOn = md.fields[i].checked
			ops = append(ops, i)
		case "op_subtitle":
			subOn = md.fields[i].checked
			ops = append(ops, i)
		case "op_fonts":
			fontOn = md.fields[i].checked
			ops = append(ops, i)
		case "input_path", "input_format", "output_path", "output_format":
			paths = append(paths, i)
		case "preset", "advance":
			encode = append(encode, i)
		case "fonts_path":
			fonts = append(fonts, i)
		default:
			if md.fields[i].def.key == "sub_suffix" {
				subGroups++
			}
			subs = append(subs, i)
		}
	}

	var lines []taskLine
	add := func(field int, text string) {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		lines = append(lines, taskLine{field: field, text: text})
	}
	head := func(title, note string) {
		text := "  " + sectionTitleStyle.Render(title)
		if note != "" {
			text += "  " + descStyle.Render(note)
		}
		add(-1, text)
	}
	rule := func() {
		add(-1, "  "+dividerStyle.Render(strings.Repeat("─", max(boxW-4, 4))))
	}
	chip := func(on bool, name string) string {
		if on {
			return stepOkStyle.Render(" " + name + " ")
		}
		return stepTodoStyle.Render(" " + name + " ")
	}
	fieldsOf := func(idxs []int, extra map[string]string) {
		for _, idx := range idxs {
			f := &md.fields[idx]
			for _, ln := range taskFieldLines(f, idx == md.focus, boxW, extra[f.def.key]) {
				add(idx, ln)
			}
			if f.def.key == "preset" {
				for _, ln := range presetPreviewLines(f, boxW) {
					add(idx, ln)
				}
			}
		}
	}

	add(-1, "  "+chip(convertOn, "转码")+" "+chip(subOn, "字幕")+" "+chip(fontOn, "字体"))
	var parts []string
	if convertOn {
		parts = append(parts, "转码")
	}
	if subOn {
		parts = append(parts, "字幕")
	}
	if fontOn {
		parts = append(parts, "字体")
	}
	summary := "先勾选要做的事，路径、预设和字幕都在这一页"
	if len(parts) > 0 {
		summary = "这次会做：" + strings.Join(parts, "、")
		if subOn {
			summary += fmt.Sprintf("，%d 组字幕", subGroups)
		}
	}
	add(-1, "  "+descStyle.Render(summary))

	rule()
	head("操作", "可以同时勾选，一次 ffmpeg 做完")
	fieldsOf(ops, map[string]string{
		"op_convert":  "重新编码，可选用预设或手写参数",
		"op_subtitle": "封装外挂字幕，新字幕排在原字幕之前",
		"op_fonts":    "把字体文件夹里的字体打进容器",
	})

	rule()
	head("源与输出", "递归扫描视频；已有输出文件不会覆盖")
	fieldsOf(paths, nil)

	note := "未勾选转码时，这里的参数不会写入命令"
	if convertOn {
		note = "左右键选用预设，也可以直接改高级参数"
	}
	rule()
	head("转码", note)
	fieldsOf(encode, nil)

	note = "未勾选添加字体时，不会读取这个文件夹"
	if fontOn {
		note = "目录里的字体都会作为附件写入"
	}
	rule()
	head("字体", note)
	fieldsOf(fonts, nil)

	note = "未勾选添加字幕时，下面的字幕组不会写入命令"
	if subOn {
		note = "语言用三字母码，例如 chi / eng；最多一条默认字幕"
	}
	rule()
	head("字幕", note)
	if len(subs) == 0 {
		add(-1, "  "+descStyle.Render("还没有字幕组，按 ctrl+n 添加"))
	}
	group := 0
	openGroup := 0
	for _, idx := range subs {
		f := &md.fields[idx]
		if f.def.key == "sub_suffix" {
			group++
			if subtitleGroupOpen(md.fields, md.focus, idx) {
				openGroup = group
				head(fmt.Sprintf("▾ 字幕 %d", group), "灰色是默认值，留空即使用")
			} else {
				add(idx, subtitleFoldLine(md.fields, idx, group))
				continue
			}
		} else if group != openGroup {
			continue
		}
		extra := ""
		if f.def.key == "sub_default" {
			extra = "同一任务里只能有一条"
		}
		for _, ln := range taskFieldLines(f, idx == md.focus, boxW, extra) {
			add(idx, ln)
		}
	}
	return lines
}

// nextTaskFocus 在新建任务表单里移动光标，跳过折叠字幕组里的字段。
// 到第一项或最后一项后停住，不再绕回另一头。
func nextTaskFocus(md *modal, delta int) int {
	n := len(md.fields)
	if n == 0 || delta == 0 {
		return md.focus
	}
	i := md.focus
	for {
		next := i + delta
		if next < 0 || next >= n {
			return md.focus
		}
		i = next
		if taskFieldReachable(md.fields, md.focus, i) {
			return i
		}
	}
}

// taskFieldReachable 报告字段能否成为光标。折叠组只保留后缀那一行。
func taskFieldReachable(fields []field, focus, idx int) bool {
	if idx < 0 || idx >= len(fields) {
		return false
	}
	switch fields[idx].def.key {
	case "sub_lang", "sub_title", "sub_default":
		g, ok := subtitleGroup(fields, idx)
		fg, fok := subtitleGroup(fields, focus)
		return ok && fok && g == fg
	default:
		return true
	}
}

// subtitleGroup 返回字幕字段所属的组号（从 1 起）。
func subtitleGroup(fields []field, idx int) (int, bool) {
	if idx < 0 || idx >= len(fields) {
		return 0, false
	}
	switch fields[idx].def.key {
	case "sub_suffix", "sub_lang", "sub_title", "sub_default":
	default:
		return 0, false
	}
	group := 0
	for i := 0; i <= idx; i++ {
		if fields[i].def.key == "sub_suffix" {
			group++
		}
	}
	return group, true
}

// subtitleGroupOpen 表示光标正在这组字幕里，字段需要展开。
func subtitleGroupOpen(fields []field, focus, idx int) bool {
	g, ok := subtitleGroup(fields, idx)
	fg, fok := subtitleGroup(fields, focus)
	return ok && fok && g == fg
}

// subtitleFoldLine 把一组字幕收成一行摘要。
func subtitleFoldLine(fields []field, suffixIdx, group int) string {
	suffix, lang, title := "", "", ""
	def := false
	if suffixIdx < len(fields) {
		suffix = fields[suffixIdx].resolved()
	}
	if suffixIdx+1 < len(fields) {
		lang = fields[suffixIdx+1].resolved()
	}
	if suffixIdx+2 < len(fields) {
		title = fields[suffixIdx+2].resolved()
	}
	if suffixIdx+3 < len(fields) && fields[suffixIdx+3].def.key == "sub_default" {
		def = fields[suffixIdx+3].checked
	}
	if title == "" {
		title = "无标题"
	}
	mark := ""
	if def {
		mark = "  默认"
	}
	text := fmt.Sprintf("▸ 字幕 %d  .%s  %s  %s%s", group, suffix, lang, title, mark)
	return "  " + descStyle.Render(text)
}

func taskFieldLines(f *field, focused bool, boxW int, extra string) []string {
	lbl := labelStyle.Render(f.def.label)
	if focused {
		lbl = labelFocusedStyle.Render(f.def.label)
	}
	lead := "  "
	if focused {
		lead = titleMarkStyle.Render("▌") + " "
	}
	var body string
	switch f.def.kind {
	case fieldBool:
		mark := uncheckedStyle.Render("[ ]")
		if f.checked {
			mark = checkedStyle.Render("[✔]")
		}
		note := ""
		if extra != "" {
			if focused {
				note = "  " + normalStyle.Render(extra)
			} else {
				note = "  " + descStyle.Render(extra)
			}
		}
		body = lbl + " " + mark + note
	case fieldChoice:
		label := "（无）"
		if f.choice >= 0 && f.choice < len(f.options) {
			label = f.options[f.choice]
		}
		body = lbl + " " + codeStyle.Render(label) + "  " + descStyle.Render("←/→ 切换")
	default:
		f.input.SetWidth(inputWidth(boxW))
		body = lbl + " " + f.input.View()
	}
	lines := []string{lead + body + "\n"}
	if f.err != "" {
		lines = append(lines, "    "+errorStyle.Render("× "+f.err)+"\n")
	}
	return lines
}

func presetPreviewLines(f *field, boxW int) []string {
	args := ""
	if f.choice >= 0 && f.choice < len(f.choiceArgs) {
		args = strings.TrimSpace(f.choiceArgs[f.choice])
	}
	if args == "" {
		return []string{"    " + descStyle.Render("当前是手动输入，创建时使用下面的高级参数") + "\n"}
	}
	width := max(boxW-8, 12)
	var lines []string
	lines = append(lines, "    "+descStyle.Render("参数预览")+"\n")
	for _, ln := range strings.Split(wrapText(args, width), "\n") {
		if ln == "" {
			continue
		}
		lines = append(lines, "    "+codeStyle.Render(ln)+"\n")
	}
	return lines
}
