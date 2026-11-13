package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/commandline"
	"github.com/gsxhnd/batcher/internal/presets"
)

// modalKind 描述弹窗类型：表单编辑（预设/新建任务）、确认、帮助或预设列表。
type modalKind int

const (
	modalPreset     modalKind = iota // 转码参数预设的新增/编辑（名称 + 参数）
	modalConfirm                     // 通用确认（删除等），enter 确认 esc 取消
	modalHelp                        // 快捷键帮助（? 键开关）
	modalTask                        // 新建任务：全部字段在同一个弹窗里
	modalPresetList                  // 转码预设管理列表
)

// modalReturn 是这个弹窗关闭后要回到的界面。只记在当前弹窗上，
// 离开预设管理之后，别的确认框按 esc 不会再把列表打开。
type modalReturn int

const (
	returnNone modalReturn = iota
	returnPresetList
)

// modal 是覆盖在主界面上的浮层。非空时全部按键由弹窗处理。
// 预设编辑失败时错误显示在弹窗内且弹窗保持打开，方便直接修改；
// 新建任务的失败提示发到右上角 toast，弹窗同样保持打开。
type modal struct {
	kind         modalKind
	title        string
	fields       []field
	focus        int            // 表单弹窗内的字段下标
	picking      bool           // 新建任务里转码预设列表是否展开
	err          string         // 预设弹窗/预设列表的错误，显示在弹窗内
	info         string         // 弹窗内的进行中提示（新建任务扫描时），不用错误样式
	editIndex    int            // 编辑的条目下标，-1 表示新增
	prompt       string         // 确认弹窗的说明文字 / 帮助弹窗的原始文本
	jobs         []enqueueJob   // 入队确认里的子任务；为空时按普通确认框处理
	sharedFonts  []string       // 各子任务字体相同时，列在列表上方
	showJobFonts bool           // 字体不同，展开后写在各自的子任务里
	jobOpen      []bool         // 与 jobs 等长，默认全部展开
	quitting     bool           // 这是退出确认；再按 q 不再重复打开
	vp           viewport.Model // 长内容弹窗的滚动视口（帮助、入队确认等）
	// onConfirm 确认回调：由打开处捕获需要的下标等上下文，内部完成变更并清理弹窗。
	onConfirm func(*model) tea.Cmd
	returnTo  modalReturn
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
	back := returnNone
	if m.modal != nil && m.modal.kind == modalPresetList {
		back = returnPresetList
	}
	m.modal = &modal{kind: modalPreset, title: title, fields: fields, editIndex: index, returnTo: back}
	return m.modal.fields[0].input.Focus()
}

// openConfirmModal 打开通用确认弹窗，onConfirm 里完成实际变更并负责关闭弹窗。
// 内容超过一屏时可以上下滚动，enter 仍然确认。
func (m *model) openConfirmModal(title, prompt string, onConfirm func(*model) tea.Cmd) {
	back := returnNone
	if m.modal != nil && m.modal.kind == modalPresetList {
		back = returnPresetList
	}
	m.modal = &modal{kind: modalConfirm, title: title, prompt: prompt, onConfirm: onConfirm, returnTo: back}
	m.layoutConfirm()
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

// confirmModalSize 返回确认弹窗的宽度。折行和边框用同一宽度，避免长路径被截断。
func (m model) confirmModalSize() (w, inner int) {
	w = min(84, max(m.width-4, 12))
	return w, max(w-4, 8)
}

// resizeModal 按当前尺寸重设帮助弹窗的视口（打开时与终端尺寸变化时调用）。
func (m *model) resizeModal() {
	if m.modal == nil {
		return
	}
	switch m.modal.kind {
	case modalHelp:
		_, inner := m.helpModalSize()
		md := m.modal
		md.vp.SetWidth(inner)
		md.vp.SetHeight(max(m.bodyH()-4, 3))
		md.vp.SetContent(wrapText(helpText(), inner))
	case modalConfirm:
		m.layoutConfirm()
	}
}

// layoutConfirm 内容放得下时按原文高度显示；超出一屏则放进可滚动视口。
// 入队确认会把光标所在的子任务滚进视口。
func (m *model) layoutConfirm() {
	md := m.modal
	if md == nil || md.kind != modalConfirm {
		return
	}
	w, inner := m.confirmModalSize()
	maxH := max(m.bodyH()-6, 4)
	if len(md.jobs) > 0 {
		m.layoutEnqueueConfirm(max(w-2, 4), maxH)
		return
	}
	wrapped := wrapText(md.prompt, max(inner-2, 4))
	n := 1
	if wrapped != "" {
		n = len(strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n"))
	}
	if n <= maxH {
		md.vp = viewport.Model{}
		return
	}
	md.vp.SetWidth(inner)
	md.vp.SetHeight(maxH)
	md.vp.SetContent(wrapped)
}

// layoutEnqueueConfirm 按展开状态重画子任务列表。放得下就不用视口。
func (m *model) layoutEnqueueConfirm(width, maxH int) {
	md := m.modal
	lines, spans := enqueueConfirmLines(md, width)
	if len(lines) <= maxH {
		md.vp = viewport.Model{}
		return
	}
	offset := 0
	if md.vp.Height() > 0 {
		offset = md.vp.YOffset()
	}
	md.vp.SetWidth(width)
	md.vp.SetHeight(maxH)
	md.vp.SetContent(strings.Join(lines, "\n"))
	if md.focus >= 0 && md.focus < len(spans) {
		offset = offsetForSpan(offset, maxH, len(lines), spans[md.focus].start, spans[md.focus].end)
	}
	md.vp.SetYOffset(offset)
}

// offsetForSpan 让 [start, end] 尽量落在视口里。这一段比视口高时，把第一行对齐到顶。
func offsetForSpan(offset, height, total, start, end int) int {
	if height <= 0 || total <= 0 {
		return 0
	}
	maxOff := max(total-height, 0)
	clampOff := func(n int) int {
		if n < 0 {
			return 0
		}
		if n > maxOff {
			return maxOff
		}
		return n
	}
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end >= total {
		end = total - 1
	}
	// 标题已经在视口里时保持原位，避免一打开就把共用字体滚出屏幕。
	if start >= offset && start < offset+height {
		return clampOff(offset)
	}
	if start < offset || end-start+1 >= height {
		return clampOff(start)
	}
	return clampOff(end - height + 1)
}

// helpText 组装快捷键帮助内容（多行，带样式段落标题）。
func helpText() string {
	var b strings.Builder
	section := func(t string) { b.WriteString("\n" + sectionTitleStyle.Render(t) + "\n") }
	row := func(k, v string) {
		b.WriteString("  " + helpKeyStyle.Render(k) + strings.Repeat(" ", max(2, 22-lipgloss.Width(k))) +
			helpDescStyle.Render(v) + "\n")
	}

	row("a", "新建任务；执行中也可以创建，新任务排在当前任务之后")
	row("p", "转码预设管理")
	row("?", "打开/关闭本帮助")
	row("q", "退出；创建任务或预设管理打开时先按 esc 关掉，执行中再确认一次")
	row("esc", "关闭弹窗或返回；执行中取消当前任务，暂停中中止当前任务")
	row("ctrl+c", "强制退出并中止任务")
	row("拖选", "按住鼠标拖选界面文字，松手后复制；输入框里可直接粘贴")

	section("任务列表")
	row("↑/↓", "在主任务、子任务和每一次之间移动")
	row("enter", "折叠/展开主任务或某一次；进入输出后 esc 返回")
	row("y", "复制当前子任务的命令（主任务行=整个任务全部命令）")
	row("r", "重试失败的子任务；已结束或历史任务重新排队")
	row("o", "输出已存在时覆盖。确认一次后重试")
	row("d", "删除子任务，或把主任务从列表和数据库移除")
	row("c", "取消主任务：执行中的停止，等待中的不再开始")
	row("x", "等待中的主任务：开始执行")
	row("s / x", "暂停时：跳过当前子任务 / 中止当前主任务")
	row("f", "跳回正在执行的子任务")
	row("pgup/pgdn/滚轮", "滚动命令和日志")

	section("新建任务")
	row("a", "打开创建弹窗，所有参数在同一个弹窗里填写")
	row("space", "勾选转码、字幕、字体")
	row("enter", "下一项；转码预设先展开列表，上下选择后再确认；最后一项先确认输入再创建")
	row("ctrl+n / ctrl+d", "增加或删除一条字幕")
	row("字幕组", "多条字幕时，没选中的组收成一行，移到该行展开")
	row("ctrl+s", "生成任务；确认弹窗里再按一次，把任务加入队列")
	row("确认入队", "子任务默认全部展开，↑/↓ 选择，enter 收起或展开")
	row("esc", "关闭预设列表，否则取消")

	section("转码预设")
	row("p", "打开预设管理")
	row("a", "从空白表单新建自定义预设")
	row("c", "把当前选中的预设复制成一条新的")
	row("e", "编辑当前预设")
	row("d", "删除自定义预设（弹窗确认）")
	row("J / K", "下移 / 上移自定义预设")
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
	switch md.kind {
	case modalHelp, modalTask:
	case modalConfirm:
		w, _ = m.confirmModalSize()
	default:
		w = min(64, max(m.width-4, 12))
	}
	var content string
	switch md.kind {
	case modalHelp:
		return drawBox(w, md.title+"（↑/↓ 滚动，esc 关闭）", true, true, md.vp.View(), -1)
	case modalPresetList:
		return drawBox(w, md.title, true, true, m.presetListContent(), -1)
	case modalTask:
		return m.taskModalView()
	case modalConfirm:
		if len(md.jobs) > 0 {
			if md.vp.Height() > 0 {
				content = md.vp.View()
				break
			}
			content = m.enqueueConfirmBody()
			break
		}
		if md.vp.Height() > 0 {
			content = md.vp.View()
			break
		}
		_, inner := m.confirmModalSize()
		wrapped := wrapText(md.prompt, max(inner-2, 4))
		var b strings.Builder
		for _, line := range strings.Split(wrapped, "\n") {
			b.WriteString("  " + line + "\n")
		}
		content = strings.TrimSuffix(b.String(), "\n")
	default:
		var b strings.Builder
		for i := range md.fields {
			f := &md.fields[i]
			lbl := labelStyle.Render(f.def.label)
			if i == md.focus {
				lbl = labelFocusedStyle.Render(f.def.label)
			}
			if f.def.kind == fieldBool {
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
			if f.def.kind == fieldChoice {
				label := "（无）"
				if f.choice >= 0 && f.choice < len(f.options) {
					label = f.options[f.choice]
				}
				b.WriteString("  " + lbl + " " + codeStyle.Render(label) + " " + descStyle.Render("←/→ 切换") + "\n")
				continue
			}
			f.useWidth(inputWidth(w))
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
		return m.updateModalInput(msg)
	}
	md := m.modal
	switch md.kind {
	case modalHelp:
		return m.updateHelpModal(key, msg)
	case modalConfirm:
		return m.updateConfirmModal(key, msg)
	case modalPresetList:
		return m.updatePresetList(key.String())
	}
	if md.kind == modalTask && md.picking {
		return m.updatePresetPick(key.String())
	}
	return m.updateFormModal(key, msg)
}

// updateModalInput 把粘贴和输入框内部消息交给当前文本字段。
func (m *model) updateModalInput(msg tea.Msg) tea.Cmd {
	md := m.modal
	if (md.kind != modalPreset && md.kind != modalTask) || len(md.fields) == 0 {
		return nil
	}
	f := &md.fields[md.focus]
	if f.def.kind == fieldBool || f.def.kind == fieldChoice {
		return nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	if md.kind == modalTask {
		m.syncTaskOutputPlaceholder()
	}
	return cmd
}

func (m *model) updateHelpModal(key tea.KeyPressMsg, msg tea.Msg) tea.Cmd {
	switch key.String() {
	case "esc", "?", "enter", "space", "y":
		m.modal = nil
		return nil
	case "up", "k", "down", "j", "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
		var cmd tea.Cmd
		m.modal.vp, cmd = m.modal.vp.Update(msg)
		return cmd
	}
	return nil
}

func (m *model) updateConfirmModal(key tea.KeyPressMsg, msg tea.Msg) tea.Cmd {
	md := m.modal
	if len(md.jobs) > 0 {
		return m.updateEnqueueConfirm(key, msg)
	}
	switch key.String() {
	case "enter", "y":
		m.modal = nil
		if md.onConfirm != nil {
			return md.onConfirm(m)
		}
		return nil
	case "esc", "n":
		back := md.returnTo
		m.modal = nil
		if back == returnPresetList {
			m.openPresetList()
		}
		return nil
	case "up", "k", "down", "j", "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
		if md.vp.Height() > 0 {
			var cmd tea.Cmd
			m.modal.vp, cmd = m.modal.vp.Update(msg)
			return cmd
		}
	}
	return nil
}

// updateEnqueueConfirm 在入队确认里移动和展开子任务。enter 只切换展开，ctrl+s 才加入队列。
func (m *model) updateEnqueueConfirm(key tea.KeyPressMsg, msg tea.Msg) tea.Cmd {
	md := m.modal
	switch key.String() {
	case "ctrl+s", "y":
		m.modal = nil
		if md.onConfirm != nil {
			return md.onConfirm(m)
		}
		return nil
	case "esc", "n":
		m.modal = nil
		return nil
	case "enter":
		if md.focus >= 0 && md.focus < len(md.jobOpen) {
			md.jobOpen[md.focus] = !md.jobOpen[md.focus]
			m.layoutConfirm()
		}
		return nil
	case "up", "k":
		if md.focus > 0 {
			md.focus--
			m.layoutConfirm()
		}
		return nil
	case "down", "j":
		if md.focus < len(md.jobs)-1 {
			md.focus++
			m.layoutConfirm()
		}
		return nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
		if md.vp.Height() > 0 {
			var cmd tea.Cmd
			m.modal.vp, cmd = m.modal.vp.Update(msg)
			return cmd
		}
	}
	return nil
}

func (m *model) updateFormModal(key tea.KeyPressMsg, msg tea.Msg) tea.Cmd {
	md := m.modal
	switch key.String() {
	case "esc":
		back := md.returnTo
		if md.kind == modalTask {
			m.stopCreate()
		}
		m.modal = nil
		if back == returnPresetList {
			m.openPresetList()
		}
		return nil
	case "tab", "down":
		if md.kind == modalTask {
			md.focus = nextTaskFocus(md, 1)
		} else {
			md.focus = (md.focus + 1) % len(md.fields)
		}
		return focusField(md.fields, md.focus)
	case "shift+tab", "up":
		if md.kind == modalTask {
			md.focus = nextTaskFocus(md, -1)
		} else {
			md.focus = (md.focus - 1 + len(md.fields)) % len(md.fields)
		}
		return focusField(md.fields, md.focus)
	case "enter":
		// enter 在中间字段推进到下一项；在最后一项（含被折叠后看不见的后续字段）直接保存。
		// 中途任意字段也可 ctrl+s 保存。
		if md.kind == modalTask && md.fields[md.focus].def.kind == fieldChoice {
			md.picking = true
			return nil
		}
		if md.kind == modalTask {
			next := nextTaskFocus(md, 1)
			if next == md.focus {
				return m.saveModal()
			}
			md.focus = next
			return focusField(md.fields, md.focus)
		}
		if md.focus == len(md.fields)-1 {
			return m.saveModal()
		}
		md.focus++
		return focusField(md.fields, md.focus)
	case "ctrl+s":
		return m.saveModal()
	case "ctrl+n":
		if md.kind == modalTask {
			m.checkTaskOp("op_subtitle")
			m.appendSubtitleFields(subtitleDefaults())
			md.focus = len(md.fields) - len(subFieldDefs)
			return focusField(md.fields, md.focus)
		}
		return nil
	case "ctrl+d":
		if md.kind == modalTask {
			m.removeSubtitleFields()
			return focusField(md.fields, md.focus)
		}
		return nil
	case "left", "right", "h", "l":
		// 文本字段的光标移动；bool/choice 字段在 updateModalInput 里被守卫拦下。
		return m.updateModalInput(msg)
	default:
		if len(md.fields) == 0 {
			return nil
		}
		f := &md.fields[md.focus]
		if f.def.kind == fieldChoice {
			return nil
		}
		if f.def.kind == fieldBool {
			// 勾选型字段：space 切换状态，其余按键吞掉不进入文本输入。
			if key.String() == " " || key.String() == "space" {
				f.checked = !f.checked
			}
			return nil
		}
		return m.updateModalInput(msg)
	}
}

// saveModal 校验并保存当前编辑弹窗（预设或新建任务）；失败时把错误写在弹窗内并保持打开。
// 确认弹窗不经过此函数，由 updateModal 直接回调。
func (m *model) saveModal() tea.Cmd {
	switch m.modal.kind {
	case modalPreset:
		return m.savePresetModal()
	case modalTask:
		return m.saveTaskModal()
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
		next[md.editIndex] = entry
		if err := m.persistPresets(next, "已更新"); err != nil {
			md.err = "保存失败: " + err.Error()
			return nil
		}
		if md.returnTo == returnPresetList {
			m.presetCursor = md.editIndex
		} else {
			m.presetCursor = m.presetUserOffset() + md.editIndex
		}
	} else {
		// 新增不影响当前选中的预设参数，只把光标指向新条目。
		next = append(next, entry)
		if err := m.persistPresets(next, "已新增"); err != nil {
			md.err = "保存失败: " + err.Error()
			return nil
		}
		if md.returnTo == returnPresetList {
			m.presetCursor = len(next) - 1
		} else {
			m.presetCursor = m.presetCount() - 2 // 手动输入项之前
		}
	}
	back := md.returnTo
	m.modal = nil
	if back == returnPresetList {
		m.openPresetList()
	}
	return nil
}
