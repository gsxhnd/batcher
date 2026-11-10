package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/task"
)

// ---------- 面板 [1] 操作 ----------

func (m *model) updateOpsPanel(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		m.featCursor = (m.featCursor - 1 + len(features)) % len(features)
	case "down", "j":
		m.featCursor = (m.featCursor + 1) % len(features)
	case "enter", " ", "space":
		features[m.featCursor].toggle(&m.ops)
		if m.ops.Any() {
			m.featErr = ""
		}
		m.rebuildFields()
		return m.schedulePlan(0)
	}
	return nil
}

// ---------- 面板 [2] 转码预设 ----------

func (m *model) updatePresetsPanel(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	offset := m.presetUserOffset()
	manual := offset + len(m.presets) // 手动输入选项的下标，同时等于列表长度
	switch key.String() {
	case "up", "k":
		m.presetCursor = (m.presetCursor - 1 + manual + 1) % (manual + 1)
		return nil
	case "down", "j":
		m.presetCursor = (m.presetCursor + 1) % (manual + 1)
		return nil
	case "enter":
		switch {
		case m.presetCursor < offset:
			m.manualAdvance = m.builtins[m.presetCursor].Args
		case m.presetCursor < manual:
			m.manualAdvance = m.presets[m.presetCursor-offset].Args
		default:
			// 手动输入：直接在面板 [4] 编辑高级参数。
			m.manualAdvance = ""
		}
		m.applyAdvanceToForm()
		return m.schedulePlan(0)
	case "a", "n":
		if m.presetCursor < offset {
			b := m.builtins[m.presetCursor]
			return m.openPresetModal(-1, presets.Preset(b))
		}
		return m.openPresetModal(-1, presets.Preset{})
	case "e":
		if idx, ok := m.selectedUserPreset(); ok {
			return m.openPresetModal(idx, m.presets[idx])
		}
		m.statusMsg = "仅自定义预设可编辑；内置预设按 a 复制，手动参数在面板 4 编辑"
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
		return nil
	case "J", "shift+down":
		m.movePreset(1)
		return nil
	}
	return nil
}

// selectedUserPreset 只接受自定义段，统一保护内置项与末尾的手动输入项。
func (m model) selectedUserPreset() (int, bool) {
	idx := m.presetCursor - m.presetUserOffset()
	return idx, idx >= 0 && idx < len(m.presets)
}

// applyAdvanceToForm 把当前选中的预设参数写入主表单的“高级自定义参数”字段。
func (m *model) applyAdvanceToForm() {
	if m.fieldValues == nil {
		m.fieldValues = make(map[string]string)
	}
	m.fieldValues["advance"] = m.manualAdvance
	for i := range m.fields {
		if m.fields[i].def.key == "advance" {
			m.fields[i].input.SetValue(m.manualAdvance)
			return
		}
	}
}

// deletePreset 删除下标 idx 的自定义预设；如删除的是当前选中的预设，清空已选参数。
func (m *model) deletePreset(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.presets) {
		return nil
	}
	removed := m.presets[idx]
	next := slices.Delete(slices.Clone(m.presets), idx, idx+1)
	if err := m.persistPresets(next, "已删除"); err != nil {
		return nil
	}
	if m.manualAdvance == removed.Args {
		m.manualAdvance = ""
		m.applyAdvanceToForm()
	}
	m.presetCursor = min(m.presetCursor, m.presetCount()-1)
	return m.schedulePlan(planDebounceDelay)
}

// movePreset 把当前自定义预设上移（-1）或下移（+1），并保存顺序。
func (m *model) movePreset(delta int) {
	idx, ok := m.selectedUserPreset()
	to := idx + delta
	if !ok || len(m.presets) < 2 || to < 0 || to >= len(m.presets) {
		return
	}
	next := slices.Clone(m.presets)
	next[idx], next[to] = next[to], next[idx]
	if err := m.persistPresets(next, "已调整顺序"); err == nil {
		m.presetCursor = m.presetUserOffset() + to
	}
}

// presetDisplayName 返回预设删除确认中显示的名称。
func presetDisplayName(p presets.Preset) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Args
}

// persistPresets 写入用户预设文件，并以 action 作为成功提示前缀。
func (m *model) persistPresets(next []presets.Preset, action string) error {
	if err := presets.Save(next); err != nil {
		m.statusMsg = "× 保存预设失败: " + err.Error()
		return err
	}
	m.presets = next
	m.statusMsg = action + "，已保存到 " + presetHintText()
	return nil
}

// ---------- 面板 [3] 字幕轨道 ----------

func (m *model) updateSubsPanel(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		if len(m.subs) > 0 {
			m.subCursor = (m.subCursor - 1 + len(m.subs)) % len(m.subs)
		}
		return nil
	case "down", "j":
		if len(m.subs) > 0 {
			m.subCursor = (m.subCursor + 1) % len(m.subs)
		}
		return nil
	case "a", "n":
		return m.openSubModal(-1)
	case "e", "enter":
		if len(m.subs) > 0 {
			return m.openSubModal(m.subCursor)
		}
		m.statusMsg = "字幕列表为空，按 n 新增一条"
		return nil
	case "d":
		if len(m.subs) > 0 {
			idx := m.subCursor
			s := m.subs[idx]
			m.openConfirmModal("删除字幕轨道", fmt.Sprintf("删除 .%s（%s）？enter 确认，esc 取消", s.Suffix, s.Lang),
				func(m *model) tea.Cmd {
					m.subs = append(m.subs[:idx], m.subs[idx+1:]...)
					if m.subCursor >= len(m.subs) && m.subCursor > 0 {
						m.subCursor--
					}
					return m.schedulePlan(planDebounceDelay)
				})
			return nil
		}
		m.statusMsg = "字幕列表为空"
		return nil
	case "t":
		// t（toggle）：把当前字幕标记/取消为默认字幕流（-disposition default）。
		if len(m.subs) > 0 {
			selected := !m.subs[m.subCursor].Default
			for i := range m.subs {
				m.subs[i].Default = selected && i == m.subCursor
			}
			return m.schedulePlan(planDebounceDelay)
		}
		m.statusMsg = "字幕列表为空"
		return nil
	}
	return nil
}

// ---------- 面板 [4] 参数 ----------

// updateFormPanel 面板 [4] 浏览态：移动高亮字段、进入编辑或另存预设。
func (m *model) updateFormPanel(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		m.focusNo = clamp(m.focusNo-1, 0, len(m.fields)-1)
	case "down", "j":
		m.focusNo = clamp(m.focusNo+1, 0, len(m.fields)-1)
	case "enter":
		return m.beginFormEdit()
	case "ctrl+s":
		return m.saveAdvancePreset()
	}
	return nil
}

// updateFormEdit 表单输入态：方向键/Tab 切换字段，esc 退出，字符交给输入框。
func (m *model) updateFormEdit(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m.updateFormInput(msg)
	}
	if key.String() == "ctrl+s" {
		return m.saveAdvancePreset()
	}
	switch key.String() {
	case "esc":
		m.blurForm()
		return m.schedulePlan(0)
	case "tab", "down":
		m.focusNo = (m.focusNo + 1) % len(m.fields)
		return focusField(m.fields, m.focusNo)
	case "shift+tab", "up":
		m.focusNo = (m.focusNo - 1 + len(m.fields)) % len(m.fields)
		return focusField(m.fields, m.focusNo)
	case "enter":
		if m.focusNo == len(m.fields)-1 {
			m.blurForm()
			return m.schedulePlan(0)
		}
		m.focusNo++
		return focusField(m.fields, m.focusNo)
	default:
		return m.updateFormInput(msg)
	}
}

// updateFormInput 同时处理键入、终端粘贴、剪贴板结果与输入框内部消息。
func (m *model) updateFormInput(msg tea.Msg) tea.Cmd {
	f := &m.fields[m.focusNo]
	before := f.input.Value()
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	if before == f.input.Value() {
		return cmd
	}
	if f.def.key == "advance" {
		m.manualAdvance = f.input.Value()
	}
	return tea.Batch(cmd, m.schedulePlan(planDebounceDelay))
}

// saveAdvancePreset 把当前“高级自定义参数”保存为常用预设（自定义预设文件）。
func (m *model) saveAdvancePreset() tea.Cmd {
	var args string
	for i := range m.fields {
		if m.fields[i].def.key == "advance" {
			args = m.fields[i].Value()
			break
		}
	}
	if args == "" {
		m.statusMsg = "× 参数为空，先在“高级自定义参数”中填写，例如: -c:v h265 -crf 20 -c:a copy"
		return nil
	}
	if !m.ops.Convert {
		m.statusMsg = "× 未勾选“视频转码”，无需保存高级参数"
		return nil
	}
	next := presets.Upsert(slices.Clone(m.presets), presets.Preset{Args: args})
	if err := m.persistPresets(next, "已保存常用预设"); err != nil {
		return nil
	}
	return nil
}

// focusField 聚焦 fields[focus] 并失焦其余字段，返回聚焦输入框的光标闪烁命令。
func focusField(fields []field, focus int) tea.Cmd {
	var cmd tea.Cmd
	for i := range fields {
		if i == clamp(focus, 0, len(fields)-1) {
			cmd = fields[i].input.Focus()
		} else {
			fields[i].input.Blur()
		}
	}
	return cmd
}

// ---------- 面板 [5] 任务 ----------

// updateRunPanel 任务面板的按键：切换文件、滚动视口；非执行态回车也开始执行。
func (m *model) updateRunPanel(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		m.moveFilePane(-1)
		return nil
	case "down", "j":
		m.moveFilePane(1)
		return nil
	}
	// 暂停等待决策时：r 重试 / s 跳过 / a 中止当前主任务。
	if m.pausedBatch != "" {
		switch key.String() {
		case "r":
			m.queue.Decide(task.DecisionRetry)
			m.statusMsg = "已发送重试指令"
			return nil
		case "s":
			m.queue.Decide(task.DecisionSkip)
			m.statusMsg = "已发送跳过指令"
			return nil
		case "a":
			m.queue.Decide(task.DecisionAbort)
			m.statusMsg = "已发送中止指令"
			return nil
		}
	}
	switch key.String() {
	case "f":
		// 跳回正在执行的子任务并恢复自动跟随。
		m.followRunningItem()
		return nil
	case "enter":
		if m.queueViewActive() {
			// 批次行=展开/折叠子任务；子任务行=折叠其批次。
			m.toggleRosterExpand()
			return nil
		}
		if !m.running && !m.editing && m.modal == nil {
			return m.tryStartRun()
		}
		return nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		if m.running || m.queueViewActive() {
			var cmd tea.Cmd
			m.logVp, cmd = m.logVp.Update(msg) // 视口自带 pgup/pgdn/半页滚动键位
			// 手动滚动后按是否处于底部决定是否继续跟随新输出。
			m.logFollow = m.logVp.AtBottom()
			return cmd
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return cmd
	case "home":
		if m.running || m.queueViewActive() {
			m.logVp.GotoTop()
			m.logFollow = m.logVp.AtBottom()
			return nil
		}
		m.vp.GotoTop()
		return nil
	case "end":
		if m.running || m.queueViewActive() {
			m.logVp.GotoBottom()
			m.logFollow = true
			return nil
		}
		m.vp.GotoBottom()
		return nil
	case "y":
		// y（yank）：复制当前正在查看/执行的完整命令到剪贴板（OSC52）。
		return m.copyShownCommand()
	}
	return nil
}

// batchCommands 返回指定批次全部子任务的完整命令行。
func (m *model) batchCommands(batchID string) []string {
	snap, ok := m.queue.BatchCopy(batchID)
	if !ok {
		return nil
	}
	cmds := make([]string, 0, len(snap.Items))
	for _, it := range snap.Items {
		cmds = append(cmds, command.FormatCommand(it.PlanArgs))
	}
	return cmds
}

// ---------- 执行 ----------

// copyShownCommand 把任务面板正在展示的命令复制到系统剪贴板：
// 队列视图复制光标所在子任务的完整 CLI（批次行=整个批次全部命令），
// 预览态复制所选文件的命令。
// 通过 OSC52 终端转义序列写入，无需外部依赖；由终端负责转贴到系统剪贴板。
func (m *model) copyShownCommand() tea.Cmd {
	if m.running || m.queueViewActive() {
		if len(m.roster) > 0 && m.fileCursor >= 0 && m.fileCursor < len(m.roster) && m.roster[m.fileCursor].cmd == "" {
			// 批次行：复制该批次全部子任务命令。
			commands := m.batchCommands(m.roster[m.fileCursor].batchID)
			if len(commands) == 0 {
				m.statusMsg = "× 尚无命令可复制，等待任务开始"
				return nil
			}
			m.statusMsg = fmt.Sprintf("✔ 已复制批次全部 %d 条命令到剪贴板", len(commands))
			return tea.SetClipboard(strings.Join(commands, "\n"))
		}
		cmd := m.pinnedCmdText()
		if cmd == "" {
			m.statusMsg = "× 尚无命令可复制，等待任务开始"
			return nil
		}
		m.statusMsg = "✔ 已复制所选任务的完整命令到剪贴板"
		return tea.SetClipboard(cmd)
	}
	if m.planLoading || !m.previewDone || m.planErr != nil || len(m.files) == 0 {
		m.statusMsg = "× 命令尚未生成，无法复制"
		return nil
	}
	var commands []string
	if idx := m.fileCursor - 1; idx >= 0 && idx < len(m.files) {
		commands = append(commands, m.files[idx].cmd)
	} else {
		for _, item := range m.files {
			commands = append(commands, item.cmd)
		}
	}
	m.statusMsg = "✔ 已复制所选命令到剪贴板"
	return tea.SetClipboard(strings.Join(commands, "\n"))
}

// tryStartRun 校验配置并把计划加入任务队列：失败时把焦点跳到出错的面板并给出提示。
// 队列执行中同样可以入队新批次，使用最新生成的计划。
func (m *model) tryStartRun() tea.Cmd {
	if !m.ops.Any() {
		m.featErr = "请至少勾选一项操作"
		m.setFocus(panelOps)
		return nil
	}
	_, err := m.buildOption()
	if err != nil {
		m.statusMsg = "× 校验失败：" + err.Error()
		m.setFocus(panelForm)
		return nil
	}
	if m.ops.AddSubtitle && len(m.subs) == 0 {
		m.statusMsg = "× 已勾选添加字幕，请至少添加一条（面板 3 按 n）"
		m.setFocus(panelSubs)
		return nil
	}
	// 运行期间重新生成的计划暂存在 pendingPlan；入队使用最新计划。
	plan, planErr := m.plan, m.planErr
	if m.pendingPlan != nil {
		plan, planErr = m.pendingPlan, m.pendingPlanErr
	}
	if planErr != nil && !m.planLoading {
		m.statusMsg = "× 请先修正命令预览中的错误"
		m.setFocus(panelRun)
		return nil
	}
	if m.planLoading || m.planGenerate || !m.previewDone || plan == nil {
		m.statusMsg = "请等待最新命令预览生成，查看后再按 x 入队"
		m.setFocus(panelRun)
		if !m.planLoading {
			return m.schedulePlan(0)
		}
		return nil
	}
	if len(plan.Jobs) == 0 {
		m.statusMsg = "× 未找到匹配的视频文件，未入队任何任务"
		m.setFocus(panelRun)
		return nil
	}
	return m.enqueue(plan)
}

// enqueue 把计划交给任务队列，队列空闲时立即开始执行。
func (m *model) enqueue(plan *command.BatchPlan) tea.Cmd {
	if m.queue == nil {
		m.statusMsg = "× 队列未就绪，请重启程序"
		return nil
	}
	b, err := m.queue.Enqueue(plan, m.ops)
	if err != nil {
		m.statusMsg = "× 入队失败: " + err.Error()
		return nil
	}
	m.statusMsg = fmt.Sprintf("已加入任务队列（%d 个任务），队列空闲时立即开始", len(b.Items))
	return nil
}
