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
	if m.running || m.pausedBatch != "" {
		m.statusMsg = "当前任务还未结束，结束后再按 a 创建"
		return nil
	}
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
	for _, b := range m.builtins {
		labels = append(labels, b.Name)
		args = append(args, b.Args)
	}
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
	return len(m.builtins) + len(m.presets)
}

// openPresetList 打开转码预设管理。内置项只读，自定义项可以增删改和排序。
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
	case "down", "j":
		if n > 0 {
			m.presetCursor = (m.presetCursor + 1) % n
		}
	case "a", "n":
		if n > 0 && m.presetCursor < len(m.builtins) {
			b := m.builtins[m.presetCursor]
			return m.openPresetModal(-1, presets.Preset{Name: b.Name, Args: b.Args})
		}
		return m.openPresetModal(-1, presets.Preset{})
	case "e", "enter":
		if idx, ok := m.selectedUserPreset(); ok {
			return m.openPresetModal(idx, m.presets[idx])
		}
		m.statusMsg = "内置预设只读，按 a 复制成自定义预设"
		return nil
	case "d":
		idx, ok := m.selectedUserPreset()
		if !ok {
			m.statusMsg = "仅自定义预设可删除"
			return nil
		}
		name := presetDisplayName(m.presets[idx])
		m.openConfirmModal("删除自定义预设", fmt.Sprintf("删除「%s」？enter 确认，esc 取消", name),
			func(m *model) tea.Cmd { return m.deletePreset(idx) })
		return nil
	case "K", "shift+up":
		m.movePreset(-1)
	case "J", "shift+down":
		m.movePreset(1)
	}
	if m.modal != nil && m.modal.kind == modalPresetList {
		m.modal.focus = m.presetCursor
	}
	return nil
}

func (m model) presetListContent() string {
	n := m.presetManageCount()
	cur := 0
	if m.modal != nil {
		cur = m.modal.focus
	}
	offset := len(m.builtins)
	rows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		label, tag := "", "内置"
		if i < offset {
			label = m.builtins[i].Name
		} else {
			p := m.presets[i-offset]
			label = p.Name
			if label == "" {
				label = p.Args
			}
			tag = "自定义"
		}
		mark := "  "
		if i == cur {
			mark = "▸ "
		}
		rows = append(rows, mark+label+"  "+descStyle.Render(tag))
	}
	var b strings.Builder
	for _, line := range windowRows(rows, cur, max(m.bodyH()-10, 6)) {
		b.WriteString(line + "\n")
	}
	switch {
	case cur >= 0 && cur < offset:
		b.WriteString("\n  " + codeStyle.Render(m.builtins[cur].Args) + "\n")
	case cur >= offset && cur-offset < len(m.presets):
		b.WriteString("\n  " + codeStyle.Render(m.presets[cur-offset].Args) + "\n")
	}
	b.WriteString("  " + descStyle.Render("a 新增 · e 编辑 · d 删除 · J/K 排序 · esc 关闭") + "\n")
	return b.String()
}
