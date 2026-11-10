package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/commandline"
	"github.com/gsxhnd/batcher/internal/presets"
)

// modalKind 描述弹窗类型：表单编辑（预设/字幕）或删除确认。
type modalKind int

const (
	modalPreset  modalKind = iota // 转码参数预设的新增/编辑（名称 + 参数）
	modalSub                      // 单条字幕的新增/编辑（后缀/语言/标题）
	modalConfirm                  // 通用确认（删除等），enter 确认 esc 取消
	modalHelp                     // 快捷键帮助（? 键开关）
)

// modal 是覆盖在主界面上的浮层。非空时全部按键由弹窗处理，
// 保存失败时错误显示在弹窗内且弹窗保持打开，方便直接修改。
type modal struct {
	kind      modalKind
	title     string
	fields    []field
	focus     int            // 表单弹窗内的字段下标
	err       string         // 表单校验错误，显示在弹窗底部
	editIndex int            // 编辑的条目下标，-1 表示新增
	prompt    string         // 确认弹窗的说明文字 / 帮助弹窗的原始文本
	vp        viewport.Model // 帮助弹窗的滚动视口
	// onConfirm 确认回调：由打开处捕获需要的下标等上下文，内部完成变更并清理弹窗。
	onConfirm func(*model) tea.Cmd
}

// openPresetModal 打开转码参数编辑弹窗。index=-1 表示新增，p 为表单初始值。
// 从内置预设复制时 p 直接带上名称与参数。
func (m *model) openPresetModal(index int, p presets.Preset) tea.Cmd {
	values := map[string]string{"preset_name": p.Name, "preset_args": p.Args}
	fields := make([]field, 0, len(presetFieldDefs))
	for _, def := range presetFieldDefs {
		nf := newField(def)
		nf.applyPresetDefaults()
		nf.input.SetValue(values[def.key])
		fields = append(fields, nf)
	}
	title := "新增转码参数预设"
	if index >= 0 {
		title = fmt.Sprintf("编辑自定义预设 %d", index+1)
	}
	m.modal = &modal{kind: modalPreset, title: title, fields: fields, editIndex: index}
	return m.modal.fields[0].input.Focus()
}

// openSubModal 打开单条字幕编辑弹窗。index=-1 表示新增，默认沿用上一条的扩展名。
func (m *model) openSubModal(index int) tea.Cmd {
	var entry command.SubtitleOption
	if index >= 0 && index < len(m.subs) {
		entry = m.subs[index]
	} else if len(m.subs) > 0 {
		entry.Suffix = m.subs[len(m.subs)-1].Suffix
	}

	values := map[string]string{
		"sub_suffix": entry.Suffix,
		"sub_lang":   entry.Lang,
		"sub_title":  entry.Title,
	}
	fields := make([]field, 0, len(subFieldDefs))
	for _, def := range subFieldDefs {
		nf := newField(def)
		nf.applySubDefaults()
		nf.input.SetValue(values[def.key])
		nf.checked = entry.Default // bool 字段从条目读取勾选状态
		fields = append(fields, nf)
	}
	title := "新增字幕轨道"
	if index >= 0 {
		title = fmt.Sprintf("编辑字幕轨道 %d", index+1)
	}
	m.modal = &modal{kind: modalSub, title: title, fields: fields, editIndex: index}
	return m.modal.fields[0].input.Focus()
}

// openConfirmModal 打开通用确认弹窗，onConfirm 里完成实际变更并负责关闭弹窗。
func (m *model) openConfirmModal(title, prompt string, onConfirm func(*model) tea.Cmd) {
	m.modal = &modal{kind: modalConfirm, title: title, prompt: prompt, onConfirm: onConfirm}
}

// openHelpModal 打开快捷键帮助弹窗（? 键开关），内容放入可滚动视口。
func (m *model) openHelpModal() {
	md := &modal{kind: modalHelp, title: "快捷键"}
	m.modal = md
	m.resizeModal()
}

// helpModalSize 返回帮助弹窗的宽度与视口内容宽度。
func (m model) helpModalSize() (w, inner int) {
	w = min(84, max(m.width-4, 12))
	return w, max(w-4, 8)
}

// resizeModal 按当前尺寸重设帮助弹窗的视口（打开时与终端尺寸变化时调用）。
func (m *model) resizeModal() {
	if m.modal == nil || m.modal.kind != modalHelp {
		return
	}
	_, inner := m.helpModalSize()
	md := m.modal
	md.vp.SetWidth(inner)
	md.vp.SetHeight(max(m.bodyH()-4, 3))
	md.vp.SetContent(wrapText(helpText(), inner))
}

// helpText 组装快捷键帮助内容（多行，带样式段落标题）。
func helpText() string {
	var b strings.Builder
	section := func(t string) { b.WriteString("\n" + sectionTitleStyle.Render(t) + "\n") }
	row := func(k, v string) {
		b.WriteString("  " + helpKeyStyle.Render(k) + strings.Repeat(" ", max(2, 22-lipgloss.Width(k))) +
			helpDescStyle.Render(v) + "\n")
	}

	row("1-5 / tab", "切换面板（未启用的自动跳过）")
	row("x", "把当前配置加入任务队列（校验出错跳到对应面板）")
	row("?", "打开/关闭本帮助")
	row("q", "退出；执行中=取消当前批处理")
	row("ctrl+c", "强制退出并中止任务")

	section("[1] 操作")
	row("space / enter", "勾选 / 取消勾选当前操作")
	row("↑/↓", "在操作之间移动")
	section("[2] 转码预设")
	row("↑/↓", "选择预设（内置 → 自定义 → 手动输入）")
	row("enter", "使用所选参数（同步到面板 4）")
	row("a", "新增；选中内置预设时复制为自定义")
	row("e", "编辑自定义预设（内置只读）")
	row("d", "删除自定义预设（弹窗确认）")
	row("J / K", "下移 / 上移自定义预设")
	row("弹窗", "enter 下一项（末项保存）· ctrl+s 保存 · esc 取消")
	section("[3] 字幕轨道")
	row("n", "新增字幕（弹窗）")
	row("e / enter", "编辑选中的字幕")
	row("t", "标记/取消默认字幕流（-disposition default）")
	row("d", "删除字幕（弹窗确认）")
	row("弹窗", "enter 下一项（末项保存）· ctrl+s 保存 · esc 取消")
	section("[4] 参数")
	row("enter", "进入编辑 / 下一项；esc 完成")
	row("↑/↓", "切换字段（编辑时=在字段间移动）")
	row("ctrl+s", "把高级参数另存为预设")
	section("[5] 任务")
	row("↑/↓", "在主任务与子任务之间移动（enter 折叠/展开）")
	row("enter / x", "入队当前配置（队列空闲时立即开始；执行中不可重复入队）")
	row("f", "跳回正在执行的子任务（执行中）")
	row("y", "复制当前子任务的命令（主任务行=整个任务全部命令）")
	row("r", "重试：子任务行=重试该子任务；主任务行=重试整个任务")
	row("d", "删除：光标所在主任务从队列移除（记录文件保留）")
	row("s / a", "暂停时子任务：跳过 / 中止当前主任务")
	row("pgup/pgdn/滚轮", "滚动命令/日志（向上滚动暂停跟随新输出）")
	section("执行中")
	row("完整命令", "固定显示在主体内容最上方，长命令自动折行")
	row("↑/↓", "切换查看文件")
	row("q", "取消当前批处理（界面保留）")
	if version != "" || buildDate != "" || gitCommit != "" {
		section("构建信息")
		if version != "" {
			row("版本", version)
		}
		if buildDate != "" {
			row("构建时间", buildDate)
		}
		if gitCommit != "" {
			row("提交", gitCommit)
		}
	}
	return strings.TrimPrefix(b.String(), "\n")
}

// modalView 渲染当前弹窗为一个居中的盒子（宽度受限，内容自适应高度）。
func (m model) modalView() string {
	md := m.modal
	w, _ := m.helpModalSize()
	if md.kind != modalHelp {
		w = min(64, max(m.width-4, 12))
	}
	var content string
	switch md.kind {
	case modalHelp:
		return drawBox(w, md.title+"（↑/↓ 滚动，esc 关闭）", true, true, md.vp.View(), -1)
	case modalConfirm:
		content = "  " + md.prompt
	default:
		var b strings.Builder
		for i := range md.fields {
			f := &md.fields[i]
			lbl := labelStyle.Render(f.def.label)
			if i == md.focus {
				lbl = labelFocusedStyle.Render(f.def.label)
			}
			if f.def.kind == fieldBool {
				// 勾选型字段渲染为复选框，space 切换，不吃文本输入。
				mark := uncheckedStyle.Render("[ ]")
				if f.checked {
					mark = checkedStyle.Render("[✔]")
				}
				light := descStyle
				if i == md.focus {
					light = normalStyle
				}
				b.WriteString("  " + lbl + " " + mark + " " + light.Render(boolHint()))
				b.WriteString("\n")
				continue
			}
			b.WriteString("  " + lbl + " " + f.input.View() + "\n")
			if f.err != "" {
				b.WriteString("  " + errorStyle.Render("× "+f.err) + "\n")
			}
		}
		if md.err != "" {
			b.WriteString("  " + errorStyle.Render("× "+md.err) + "\n")
		}
		b.WriteString("  " + descStyle.Render("enter 下一项（末项保存）· ctrl+s 保存 · esc 取消") + "\n")
		content = b.String()
	}
	return drawBox(w, md.title, true, true, content, -1)
}

// updateModal 处理弹窗内的按键，全部按键在此消费，不会泄漏到主界面。
func (m *model) updateModal(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		md := m.modal
		if md.kind == modalPreset || md.kind == modalSub {
			f := &md.fields[md.focus]
			if f.def.kind != fieldBool {
				var cmd tea.Cmd
				f.input, cmd = f.input.Update(msg)
				return cmd
			}
		}
		return nil
	}
	md := m.modal
	if md.kind == modalHelp {
		switch key.String() {
		case "esc", "q", "?", "enter", "space", "y":
			m.modal = nil
			return nil
		case "up", "k", "down", "j", "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
			var cmd tea.Cmd
			m.modal.vp, cmd = m.modal.vp.Update(msg)
			return cmd
		}
		return nil
	}
	if md.kind == modalConfirm {
		switch key.String() {
		case "enter", "y":
			m.modal = nil
			if md.onConfirm != nil {
				return md.onConfirm(m)
			}
			return nil
		case "esc", "n", "q":
			m.modal = nil
			return nil
		}
		return nil
	}

	switch key.String() {
	case "esc":
		m.modal = nil
		return nil
	case "tab", "down":
		md.focus = (md.focus + 1) % len(md.fields)
		return focusField(md.fields, md.focus)
	case "shift+tab", "up":
		md.focus = (md.focus - 1 + len(md.fields)) % len(md.fields)
		return focusField(md.fields, md.focus)
	case "enter":
		// enter 在中间字段推进到下一项；在末字段（含 bool）直接保存，
		// 避免循环跳回开头的困惑。中途任意字段也可 ctrl+s 保存。
		if md.focus == len(md.fields)-1 {
			return m.saveModal()
		}
		md.focus++
		return focusField(md.fields, md.focus)
	case "ctrl+s":
		return m.saveModal()
	default:
		f := &md.fields[md.focus]
		if f.def.kind == fieldBool {
			// 勾选型字段：space 切换状态，其余按键吞掉不进入文本输入。
			if key.String() == " " || key.String() == "space" {
				f.checked = !f.checked
			}
			return nil
		}
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return cmd
	}
}

// saveModal 校验并保存当前编辑弹窗（预设或字幕）；失败时把错误写在弹窗内并保持打开。
// 确认弹窗不经过此函数，由 updateModal 直接回调。
func (m *model) saveModal() tea.Cmd {
	switch m.modal.kind {
	case modalPreset:
		return m.savePresetModal()
	case modalSub:
		return m.saveSubModal()
	}
	return nil
}

// savePresetModal 校验并保存预设弹窗：参数不能为空，且参数文本不能与其他预设重复。
func (m *model) savePresetModal() tea.Cmd {
	md := m.modal
	entry := presets.Preset{
		Name: strings.TrimSpace(md.fields[0].Value()),
		Args: presets.Trim(md.fields[1].Value()),
	}
	if entry.Args == "" {
		md.err = "高级参数不能为空"
		return focusField(md.fields, len(md.fields)-1)
	}
	if _, err := commandline.Parse(entry.Args); err != nil {
		md.err = err.Error()
		return focusField(md.fields, len(md.fields)-1)
	}
	for i, p := range m.presets {
		if p.Args == entry.Args && i != md.editIndex {
			md.err = "已存在相同的参数"
			return focusField(md.fields, len(md.fields)-1)
		}
	}
	next := slices.Clone(m.presets)
	if md.editIndex >= 0 && md.editIndex < len(next) {
		// 若被编辑的正是当前使用中的预设，保存后需同步主表单里的高级参数。
		oldArgs := m.presets[md.editIndex].Args
		next[md.editIndex] = entry
		if err := m.persistPresets(next, "已更新"); err != nil {
			md.err = "保存失败: " + err.Error()
			return nil
		}
		m.presetCursor = m.presetUserOffset() + md.editIndex
		m.syncManualAdvance(oldArgs, entry.Args)
	} else {
		// 新增不影响当前选中的预设参数，只把光标指向新条目。
		next = append(next, entry)
		if err := m.persistPresets(next, "已新增"); err != nil {
			md.err = "保存失败: " + err.Error()
			return nil
		}
		m.presetCursor = m.presetCount() - 2 // 手动输入项之前
	}
	m.modal = nil
	return m.schedulePlan(planDebounceDelay)
}

// syncManualAdvance 当被编辑的预设正是当前使用中的参数时，同步主表单里的高级参数字段。
func (m *model) syncManualAdvance(before, after string) {
	if before != "" && m.manualAdvance != before {
		return // 编辑的不是当前选中的预设
	}
	if before != "" {
		m.manualAdvance = after
	}
	for i := range m.fields {
		if m.fields[i].def.key == "advance" && (before == "" || m.fields[i].input.Value() == before) {
			m.fields[i].input.SetValue(after)
		}
	}
}

// saveSubModal 校验并保存字幕弹窗：后缀必填，语言码需为 ISO 639-2 三字母小写码。
func (m *model) saveSubModal() tea.Cmd {
	md := m.modal
	suffix := strings.TrimSpace(md.fields[0].Value())
	lang := strings.TrimSpace(md.fields[1].Value())
	title := strings.TrimSpace(md.fields[2].Value())
	if suffix == "" {
		md.fields[0].err = "必填"
		return focusField(md.fields, 0)
	}
	if !isISO639Code(lang) {
		md.fields[1].err = "需为 ISO 639-2 三字母码，如 chi / eng / jpn"
		return focusField(md.fields, 1)
	}
	entry := command.SubtitleOption{
		Suffix:  suffix,
		Lang:    lang,
		Title:   title,
		Default: md.fields[3].Checked(),
	}
	if entry.Default {
		for i := range m.subs {
			m.subs[i].Default = false
		}
	}
	if md.editIndex >= 0 && md.editIndex < len(m.subs) {
		m.subs[md.editIndex] = entry
	} else {
		m.subs = append(m.subs, entry)
		m.subCursor = len(m.subs) - 1
	}
	m.modal = nil
	return m.schedulePlan(planDebounceDelay)
}
