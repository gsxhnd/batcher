package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	fields := make([]field, 0, len(defs)+len(subFieldDefs))
	for _, def := range defs {
		nf := newField(def)
		nf.applyDefaults(&d)
		if def.kind == fieldChoice {
			nf.options, nf.choiceArgs = m.presetChoices()
		}
		fields = append(fields, nf)
	}
	m.modal = &modal{kind: modalTask, title: "新建任务", fields: fields}
	m.appendSubtitleFields(subtitleDefaults())
	m.syncTaskOutputPlaceholder()
	return focusField(m.modal.fields, 0)
}

// defaultOutputDir 是未填写输出目录时的位置：源目录下的 result。
func defaultOutputDir(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		input = "."
	}
	return filepath.Join(input, "result")
}

// syncTaskOutputPlaceholder 在输出目录还没手填时，把灰色占位改成源目录下的 result。
func (m *model) syncTaskOutputPlaceholder() {
	md := m.modal
	if md == nil {
		return
	}
	var input string
	var output *field
	for i := range md.fields {
		switch md.fields[i].def.key {
		case "input_path":
			input = md.fields[i].resolved()
		case "output_path":
			output = &md.fields[i]
		}
	}
	if output == nil || output.Value() != "" {
		return
	}
	output.useDefault(defaultOutputDir(input))
}

// checkTaskOp 勾选新建任务里的一项操作，用来在添加字幕时把字幕区展开。
func (m *model) checkTaskOp(key string) {
	if m.modal == nil {
		return
	}
	for i := range m.modal.fields {
		if m.modal.fields[i].def.key == key {
			m.modal.fields[i].checked = true
		}
	}
}

func (m model) presetChoices() (labels, args []string) {
	labels = append(labels, "手动输入")
	args = append(args, "")
	for _, p := range m.presets {
		labels = append(labels, presetDisplayName(p))
		args = append(args, p.Args)
	}
	return labels, args
}

func (m model) taskModalHint() string {
	if m.modal != nil && m.modal.picking {
		return "↑/↓ 选择预设 · enter 确定 · esc 返回"
	}
	return "↑/↓ 切换 · enter 下一项 · ctrl+s 确认创建 · ctrl+n / ctrl+d 增删字幕"
}

// updatePresetPick 处理展开后的转码预设列表。上下移动选项，esc 只收起列表。
func (m *model) updatePresetPick(key string) tea.Cmd {
	md := m.modal
	switch key {
	case "up", "k":
		m.stepPresetChoice(-1)
		return nil
	case "down", "j":
		m.stepPresetChoice(1)
		return nil
	case "esc":
		md.picking = false
		return nil
	case "enter", "tab":
		md.picking = false
		if md.focus == len(md.fields)-1 {
			return m.saveModal()
		}
		md.focus = nextTaskFocus(md, 1)
		return focusField(md.fields, md.focus)
	case "shift+tab":
		md.picking = false
		md.focus = nextTaskFocus(md, -1)
		return focusField(md.fields, md.focus)
	case "ctrl+s":
		md.picking = false
		return m.saveModal()
	case "ctrl+n":
		md.picking = false
		m.checkTaskOp("op_subtitle")
		m.appendSubtitleFields(subtitleDefaults())
		md.focus = len(md.fields) - len(subFieldDefs)
		return focusField(md.fields, md.focus)
	case "ctrl+d":
		md.picking = false
		m.removeSubtitleFields()
		return focusField(md.fields, md.focus)
	default:
		return nil
	}
}

func (m *model) stepPresetChoice(delta int) {
	md := m.modal
	if md == nil || md.focus < 0 || md.focus >= len(md.fields) {
		return
	}
	f := &md.fields[md.focus]
	if f.def.kind != fieldChoice || len(f.options) == 0 {
		return
	}
	next := f.choice + delta
	if next < 0 || next >= len(f.options) {
		return
	}
	f.choice = next
	m.applyPresetChoice(f)
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
	n := len(subFieldDefs)
	if start < 0 || start+n > len(md.fields) {
		m.setStatus("× 把光标移到字幕上再按 ctrl+d")
		return
	}
	md.fields = slices.Delete(md.fields, start, start+n)
	if md.focus >= len(md.fields) {
		md.focus = max(len(md.fields)-1, 0)
	}
}

// saveTaskModal 校验弹窗里的全部字段，扫描目录并创建任务。失败时弹窗保持打开。
func (m *model) saveTaskModal() tea.Cmd {
	if m.creating || m.modal == nil {
		return nil
	}
	sel, opt, cmd, ok := m.collectTaskForm(m.modal)
	if !ok {
		return cmd
	}
	return m.startTaskScan(sel, opt)
}

// collectTaskForm 读取并校验新建任务表单。校验失败时 ok 为 false，
// 错误发到右上角 toast，cmd 用来把光标移回出错字段，弹窗保持打开。
func (m *model) collectTaskForm(md *modal) (command.Selection, command.VideoBatchOption, tea.Cmd, bool) {
	sel := command.Selection{}
	opt := command.DefaultOption()
	subs := readSubtitleGroups(md.fields, &sel, &opt)
	if !sel.Any() {
		m.setStatus("× 请至少勾选一项操作")
		return sel, opt, focusField(md.fields, 0), false
	}
	if sel.AddFonts && strings.TrimSpace(opt.FontsPath) == "" {
		m.setStatus("× 添加字体需要填写字体文件夹")
		return sel, opt, focusTaskField(md, "fonts_path"), false
	}
	if sel.Convert && strings.TrimSpace(opt.Advance) != "" {
		if _, err := commandline.Parse(opt.Advance); err != nil {
			m.setStatus("× " + err.Error())
			return sel, opt, focusTaskField(md, "advance"), false
		}
	}
	if !sel.Convert {
		opt.Advance = ""
	}
	if !sel.AddFonts {
		opt.FontsPath = ""
	}
	if !sel.AddSubtitle {
		opt.Subtitles = nil
		return sel, opt, nil, true
	}
	if err := validateSubtitles(subs); err != "" {
		m.setStatus("× " + err)
		return sel, opt, nil, false
	}
	opt.Subtitles = subs
	return sel, opt, nil, true
}

// readSubtitleGroups 从字段里收集操作勾选、路径和字幕组。空白字幕组会被丢掉。
func readSubtitleGroups(fields []field, sel *command.Selection, opt *command.VideoBatchOption) []command.SubtitleOption {
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
	for i := range fields {
		f := &fields[i]
		f.err = ""
		switch f.def.key {
		case "op_convert":
			sel.Convert = f.checked
		case "op_subtitle":
			sel.AddSubtitle = f.checked
		case "op_fonts":
			sel.AddFonts = f.checked
		case "input_path", "input_format", "output_path", "output_format", "advance", "fonts_path":
			setStringOption(opt, f.def.key, f.resolved())
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
	return subs
}

func validateSubtitles(subs []command.SubtitleOption) string {
	if len(subs) == 0 {
		return "至少填写一条字幕"
	}
	defaults := 0
	for i, sub := range subs {
		if strings.TrimSpace(sub.Suffix) == "" {
			return fmt.Sprintf("字幕 %d 后缀不能为空", i+1)
		}
		if !isISO639Code(sub.Lang) {
			return fmt.Sprintf("字幕 %d 语言需为三字母码，如 chi", i+1)
		}
		if sub.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return "最多只能有一条默认字幕"
	}
	return ""
}

func (m *model) startTaskScan(sel command.Selection, opt command.VideoBatchOption) tea.Cmd {
	m.createSel = sel
	m.creating = true
	m.createSeq++
	seq := m.createSeq
	if m.createCancel != nil {
		m.createCancel()
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.createCancel = cancel
	m.modal.info = "正在扫描并生成任务…"
	m.setStatus("")
	return func() tea.Msg {
		plan, err := command.BuildPlan(ctx, &opt, sel)
		return createPlanMsg{seq: seq, plan: plan, err: err}
	}
}

// cliFlagValues 取出一条 ffmpeg 参数里某个旗标后面的值。
func cliFlagValues(args []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] != flag {
			continue
		}
		out = append(out, args[i+1])
		i++
	}
	return out
}

// cliInputs 取出一条 ffmpeg 参数里的全部 -i 输入。
func cliInputs(args []string) []string {
	return cliFlagValues(args, "-i")
}

// enqueueJob 是入队确认里的一条子任务。字体相同的批次不把字体放进每一条。
type enqueueJob struct {
	name  string
	files []string
	fonts []string
}

// jobSpan 是一条子任务在确认列表里占用的行，含标题，不含它前面的空行。
type jobSpan struct {
	start, end int
}

// enqueueJobs 拆出入队确认要展示的标题、共用字体和各子任务。
// 字体由 -attach 写入，不在 -i 里；各任务字体相同时只准备一份。
func enqueueJobs(plan *command.BatchPlan) (header string, shared []string, jobs []enqueueJob, showJobFonts bool) {
	n := 0
	if plan != nil {
		n = len(plan.Jobs)
	}
	header = fmt.Sprintf("确认将 %d 个任务加入队列？", n)
	if plan == nil || n == 0 {
		return header, nil, nil, false
	}

	fontSets := make([][]string, n)
	sameFonts := true
	for i, job := range plan.Jobs {
		fontSets[i] = cliFlagValues(job.Args, "-attach")
		if i > 0 && !slices.Equal(fontSets[i], fontSets[0]) {
			sameFonts = false
		}
	}
	if sameFonts && len(fontSets[0]) > 0 {
		shared = fontSets[0]
	}
	showJobFonts = !sameFonts
	jobs = make([]enqueueJob, n)
	for i, job := range plan.Jobs {
		name := filepath.Base(job.Input)
		if name == "" || name == "." {
			name = fmt.Sprintf("任务 %d", i+1)
		}
		jobs[i] = enqueueJob{name: name, files: jobInputs(job)}
		if showJobFonts {
			jobs[i].fonts = fontSets[i]
		}
	}
	return header, shared, jobs, showJobFonts
}

// openEnqueueConfirm 打开入队确认。子任务默认全部展开，enter 收起或展开，ctrl+s 加入队列。
func (m *model) openEnqueueConfirm(plan *command.BatchPlan, onConfirm func(*model) tea.Cmd) {
	header, shared, jobs, showFonts := enqueueJobs(plan)
	open := make([]bool, len(jobs))
	for i := range open {
		open[i] = true
	}
	m.modal = &modal{
		kind:         modalConfirm,
		title:        "确认加入队列",
		prompt:       header,
		jobs:         jobs,
		sharedFonts:  shared,
		showJobFonts: showFonts,
		jobOpen:      open,
		onConfirm:    onConfirm,
	}
	m.layoutConfirm()
}

// enqueueConfirmBody 画出当前展开状态下的入队确认正文。
func (m model) enqueueConfirmBody() string {
	if m.modal == nil {
		return ""
	}
	w, _ := m.confirmModalSize()
	lines, _ := enqueueConfirmLines(m.modal, max(w-2, 4))
	return strings.Join(lines, "\n")
}

// wrapLines 把文本按宽度折行后逐行返回；空字符串也会产生一行空行，保持占位。
func wrapLines(text string, width int) []string {
	return strings.Split(wrapText(text, width), "\n")
}

// enqueueConfirmLines 按展开状态生成确认列表，并标出每条子任务的行范围。
func enqueueConfirmLines(md *modal, width int) ([]string, []jobSpan) {
	width = max(width, 8)
	var lines []string
	add := func(s string) {
		lines = append(lines, wrapLines(s, width)...)
	}
	add(md.prompt)
	if len(md.sharedFonts) > 0 {
		add("")
		add(sectionTitleStyle.Render("字体"))
		for _, font := range md.sharedFonts {
			add("  " + font)
		}
	}
	spans := make([]jobSpan, len(md.jobs))
	for i, job := range md.jobs {
		add("")
		start := len(lines)
		open := i < len(md.jobOpen) && md.jobOpen[i]
		add(fitLine(jobHeaderLine(job, i, open, i == md.focus, md.showJobFonts), width))
		if open {
			lines = append(lines, pathSection(job.files, "文件", width)...)
			if md.showJobFonts {
				lines = append(lines, pathSection(job.fonts, "字体", width)...)
			}
		}
		spans[i] = jobSpan{start: start, end: max(len(lines)-1, start)}
	}
	return lines, spans
}

func jobHeaderLine(job enqueueJob, index int, open, focused, showFonts bool) string {
	mark := "▾"
	if !open {
		mark = "▸"
	}
	title := fmt.Sprintf("%s [%d] %s", mark, index+1, job.name)
	if !open {
		title += "  " + jobFoldSummary(job, showFonts)
	}
	switch {
	case focused:
		return selectedStyle.Render(title)
	case open:
		return sectionTitleStyle.Render(title)
	default:
		return descStyle.Render(title)
	}
}

func jobFoldSummary(job enqueueJob, showFonts bool) string {
	parts := []string{countPhrase(len(job.files), "文件")}
	if showFonts {
		parts = append(parts, countPhrase(len(job.fonts), "字体"))
	}
	return strings.Join(parts, " · ")
}

func countPhrase(n int, name string) string {
	if n == 0 {
		return "没有" + name
	}
	return fmt.Sprintf("%d 个%s", n, name)
}

func pathSection(paths []string, title string, width int) []string {
	var lines []string
	add := func(s string) {
		lines = append(lines, wrapLines(s, width)...)
	}
	add("  " + descStyle.Render(title))
	if len(paths) == 0 {
		add("    " + descStyle.Render("（没有"+title+"）"))
		return lines
	}
	for _, p := range paths {
		add("    " + p)
	}
	return lines
}

func fitLine(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return truncateText(s, width)
}

func jobInputs(job command.Job) []string {
	inputs := cliInputs(job.Args)
	if len(inputs) == 0 && job.Input != "" {
		return []string{job.Input}
	}
	return inputs
}

func focusTaskField(md *modal, key string) tea.Cmd {
	_, i := fieldByKey(md.fields, key)
	if i < 0 {
		return nil
	}
	md.focus = i
	return focusField(md.fields, i)
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

// copyPresetModal 把当前选中的自定义预设复制成一条新的自定义预设。
func (m *model) copyPresetModal() tea.Cmd {
	idx, ok := m.listedPreset()
	if !ok {
		m.notePresetList("还没有预设，按 a 从空白新建")
		return nil
	}
	p := m.presets[idx]
	return m.openPresetModal(-1, presets.Preset{Name: p.Name, Args: p.Args})
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
		label := presetDisplayName(m.presets[i])
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
// 顶部徽标与摘要、扫描提示和底部快捷键固定不滚动，保证勾选操作的反馈始终可见。
func (m model) taskModalView() string {
	md := m.modal
	w, contentH := m.taskModalSize()
	header := m.taskModalHeader()
	lines := m.taskModalLines(w)
	focusAt := 0
	for i, ln := range lines {
		if ln.field == md.focus {
			focusAt = i
			break
		}
	}
	// 顶部徽标、扫描提示与底部快捷键这三处不参与滚动。
	pinned := len(header) + 1
	if md.info != "" {
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
	body = append(body, header...)
	if md.info != "" {
		body = append(body, "  "+statusStyle.Render(md.info)+"\n")
	}
	for _, ln := range lines[start:end] {
		body = append(body, ln.text)
	}
	for len(body)+1 < contentH {
		body = append(body, "\n")
	}
	body = append(body, "  "+descStyle.Render(m.taskModalHint())+"\n")
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

// taskModalFlags 汇总顶部徽标需要的勾选状态和字幕组数。
func taskModalFlags(fields []field) (convert, sub, font bool, groups int) {
	for i := range fields {
		switch fields[i].def.key {
		case "op_convert":
			convert = fields[i].checked
		case "op_subtitle":
			sub = fields[i].checked
		case "op_fonts":
			font = fields[i].checked
		case "sub_suffix":
			groups++
		}
	}
	return
}

// taskModalHeader 画出创建表单顶部的操作徽标和本次会做什么的摘要。
// 这两行固定在弹窗顶部不参与滚动，勾选任何一项操作都能立刻在顶部看到反馈。
func (m model) taskModalHeader() []string {
	md := m.modal
	if md == nil {
		return nil
	}
	convertOn, subOn, fontOn, subGroups := taskModalFlags(md.fields)
	chip := func(on bool, name string) string {
		if on {
			return stepOkStyle.Render(" " + name + " ")
		}
		return stepTodoStyle.Render(" " + name + " ")
	}
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
	return []string{
		"  " + chip(convertOn, "转码") + " " + chip(subOn, "字幕") + " " + chip(fontOn, "字体") + "\n",
		"  " + descStyle.Render(summary) + "\n",
	}
}

func (m model) taskModalLines(boxW int) []taskLine {
	md := m.modal
	if md == nil {
		return nil
	}
	convertOn, subOn, fontOn := false, false, false
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
	fieldsOf := func(idxs []int, extra map[string]string) {
		for _, idx := range idxs {
			f := &md.fields[idx]
			picking := f.def.kind == fieldChoice && idx == md.focus && md.picking
			for _, ln := range taskFieldLines(f, idx == md.focus, picking, boxW, extra[f.def.key]) {
				add(idx, ln)
			}
			if f.def.key == "preset" {
				if idx == md.focus && md.picking {
					for _, ln := range presetPickLines(f, boxW) {
						add(idx, ln)
					}
				}
				for _, ln := range presetPreviewLines(f, boxW) {
					add(idx, ln)
				}
			}
		}
	}

	rule()
	head("操作", "可以同时勾选，一次 ffmpeg 做完")
	fieldsOf(ops, map[string]string{
		"op_convert":  "重新编码，可选用预设或手写参数",
		"op_subtitle": "封装外挂字幕，新字幕排在原字幕之前",
		"op_fonts":    "把字体文件夹里的字体打进容器",
	})

	rule()
	head("源与输出", "递归扫描视频；输出留空则写到源目录下的 result")
	fieldsOf(paths, nil)

	note := "未勾选转码时，这里的参数不会写入命令"
	if convertOn {
		note = "enter 展开预设列表，也可以直接改高级参数"
	}
	rule()
	head("转码", note)
	fieldsOf(encode, nil)

	if fontOn {
		rule()
		head("字体", "目录里的字体都会作为附件写入")
		fieldsOf(fonts, nil)
	}

	appendTaskSubtitles(md, subs, subOn, boxW, add, head, rule)
	return lines
}

func appendTaskSubtitles(md *modal, subs []int, subOn bool, boxW int, add func(int, string), head func(string, string), rule func()) {
	if !subOn {
		return
	}
	rule()
	head("字幕", "语言用三字母码，例如 chi / eng；最多一条默认字幕")
	if len(subs) == 0 {
		add(-1, "  "+descStyle.Render("还没有字幕组，按 ctrl+n 添加"))
		return
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
		for _, ln := range taskFieldLines(f, idx == md.focus, false, boxW, extra) {
			add(idx, ln)
		}
	}
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
	case "fonts_path":
		return taskOpChecked(fields, "op_fonts")
	case "sub_suffix", "sub_lang", "sub_title", "sub_default":
		if !taskOpChecked(fields, "op_subtitle") {
			return false
		}
		if fields[idx].def.key == "sub_suffix" {
			return true
		}
		return subtitleGroupOpen(fields, focus, idx)
	default:
		return true
	}
}

func taskOpChecked(fields []field, key string) bool {
	for i := range fields {
		if fields[i].def.key == key {
			return fields[i].checked
		}
	}
	return false
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

func taskFieldLines(f *field, focused, picking bool, boxW int, extra string) []string {
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
		hint := "enter 选择"
		if picking {
			hint = "↑/↓ 选择"
		}
		body = lbl + " " + codeStyle.Render(label) + "  " + descStyle.Render(hint)
	default:
		f.useWidth(inputWidth(boxW))
		body = lbl + " " + f.input.View()
	}
	lines := []string{lead + body + "\n"}
	if f.err != "" {
		lines = append(lines, "    "+errorStyle.Render("× "+f.err)+"\n")
	}
	return lines
}

// presetPickLines 画出展开的预设列表，当前项用 ▸ 标出，过长时只显示附近几条。
func presetPickLines(f *field, boxW int) []string {
	const window = 7
	n := len(f.options)
	if n == 0 {
		return nil
	}
	start := f.choice - window/2
	if start < 0 {
		start = 0
	}
	if start+window > n {
		start = max(n-window, 0)
	}
	end := min(start+window, n)
	width := max(boxW-8, 8)
	var lines []string
	for i := start; i < end; i++ {
		name := truncateText(f.options[i], max(width-2, 4))
		if i == f.choice {
			// 不用带固定宽度的标签样式，长名称会在 16 列处被折行，把边框挤乱。
			lines = append(lines, "    "+selectedStyle.Render("▸ "+name)+"\n")
			continue
		}
		lines = append(lines, "    "+descStyle.Render("  "+name)+"\n")
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
