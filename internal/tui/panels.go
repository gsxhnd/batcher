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

// deletePreset 删除下标 idx 的自定义预设。
func (m *model) deletePreset(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.presets) {
		return nil
	}
	next := slices.Delete(slices.Clone(m.presets), idx, idx+1)
	err := m.persistPresets(next, "已删除")
	if err == nil {
		m.presetCursor = min(m.presetCursor, max(m.presetManageCount()-1, 0))
	}
	msg := "已删除，已保存到 " + presetHintText()
	if err != nil {
		msg = "× 保存预设失败: " + err.Error()
	}
	m.openPresetList()
	m.notePresetList(msg)
	return nil
}

// movePreset 把当前自定义预设上移（-1）或下移（+1），并保存顺序。
func (m *model) movePreset(delta int) {
	idx, ok := m.listedPreset()
	to := idx + delta
	if !ok || len(m.presets) < 2 || to < 0 || to >= len(m.presets) {
		return
	}
	next := slices.Clone(m.presets)
	next[idx], next[to] = next[to], next[idx]
	if err := m.persistPresets(next, "已调整顺序"); err == nil {
		m.presetCursor = to
	}
}

// presetDisplayName 返回预设列表与删除确认里显示的名称；名称为空时回退到参数。
func presetDisplayName(p presets.Preset) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Args
}

// persistPresets 保存自定义预设（写入任务数据库），并以 action 作为成功提示前缀。
func (m *model) persistPresets(next []presets.Preset, action string) error {
	if err := presets.Save(next); err != nil {
		m.notePresetList("× 保存预设失败: " + err.Error())
		return err
	}
	m.presets = next
	m.notePresetList(action + "，已保存到 " + presetHintText())
	return nil
}

// focusField 聚焦 fields[focus] 并失焦其余字段，返回聚焦输入框的光标闪烁命令。
func focusField(fields []field, focus int) tea.Cmd {
	var cmd tea.Cmd
	target := clamp(focus, 0, len(fields)-1)
	for i := range fields {
		if i == target {
			cmd = fields[i].input.Focus()
		} else {
			fields[i].input.Blur()
		}
	}
	return cmd
}

// ---------- 面板 [5] 任务 ----------

// updateRunPanel 任务面板的按键：切换文件、滚动视口。回车只操作已有任务，不入队。
func (m *model) updateRunPanel(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		if m.cmdFocus {
			m.scrollCmdPane(-1)
			return nil
		}
		if m.outputFocus {
			m.scrollOutput(-1)
			return nil
		}
		m.moveFilePane(-1)
		return nil
	case "down", "j":
		if m.cmdFocus {
			m.scrollCmdPane(1)
			return nil
		}
		if m.outputFocus {
			m.scrollOutput(1)
			return nil
		}
		m.moveFilePane(1)
		return nil
	case "esc":
		if m.outputFocus {
			m.leaveOutput()
			return nil
		}
	case "tab":
		// Tab 在命令区与输出区之间切换滚动焦点；没有命令区的界面不消费。
		if m.running || m.queueViewActive() {
			m.toggleDetailPane()
			return nil
		}
		return nil
	}
	// 队列视图的任务操作键：按光标所在行分发（子任务/批次行）。
	if consumed, cmd := m.runTaskKeys(key.String()); consumed {
		return cmd
	}
	switch key.String() {
	case "f":
		// 跳回正在执行的子任务并恢复自动跟随。
		m.followRunningItem()
		return nil
	case "enter":
		if m.queueViewActive() {
			if m.outputFocus {
				return nil
			}
			row, ok := m.focusedRosterRow()
			if !ok {
				return nil
			}
			if row.attempt > 0 {
				m.openOutput()
				return nil
			}
			if row.itemID != "" {
				m.toggleItemAttempts(row)
				return nil
			}
			m.toggleRosterExpand()
			return nil
		}
		return nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		if m.running || m.queueViewActive() {
			var cmd tea.Cmd
			if m.cmdFocus {
				m.cmdVp, cmd = m.cmdVp.Update(msg) // 视口自带 pgup/pgdn/半页滚动键位
				return cmd
			}
			m.logVp, cmd = m.logVp.Update(msg)
			// 手动滚动后按是否处于底部决定是否继续跟随新输出。
			m.logFollow = m.logVp.AtBottom()
			return cmd
		}
		return nil
	case "home":
		if m.running || m.queueViewActive() {
			if m.cmdFocus {
				m.cmdVp.GotoTop()
				return nil
			}
			m.logVp.GotoTop()
			m.logFollow = m.logVp.AtBottom()
		}
		return nil
	case "end":
		if m.running || m.queueViewActive() {
			if m.cmdFocus {
				m.cmdVp.GotoBottom()
				return nil
			}
			m.logVp.GotoBottom()
			m.logFollow = true
		}
		return nil
	case "y":
		// y（yank）：复制当前正在查看/执行的完整命令到剪贴板（OSC52）。
		return m.copyShownCommand()
	}
	return nil
}

// runTaskKeys 处理队列视图下的任务操作。按光标所在行分发：
// n 重命名主任务；r 重试（暂停中的失败子任务，或已结束主任务里失败/取消的子任务）；
// d 删除子任务或整个主任务；c 取消主任务；暂停时 s 跳过、x 中止。
// 返回是否消费了该按键，以及要执行的命令（打开输入弹窗时的光标闪烁）。
func (m *model) runTaskKeys(key string) (bool, tea.Cmd) {
	if m.queue == nil {
		return false, nil
	}
	if key == "c" && !m.queueViewActive() {
		if !m.running {
			return false, nil
		}
		m.queue.CancelCurrent()
		m.setStatus("已发送取消请求，等待正在执行的任务结束…")
		return true, nil
	}
	if !m.queueViewActive() {
		return false, nil
	}
	row, ok := m.focusedRosterRow()
	if !ok {
		return false, nil
	}
	switch key {
	case "n":
		return m.renameFocused(row)
	case "e":
		return m.editFocused(row)
	case "c":
		return m.cancelFocused(row), nil
	case "d":
		return m.deleteFocused(row), nil
	case "r":
		return m.retryFocused(row), nil
	case "o":
		return m.confirmOverwrite(row), nil
	case "s":
		return m.pauseDecision(row, task.DecisionSkip, "已发送跳过指令"), nil
	case "x":
		if m.startFocused(row) {
			return true, nil
		}
		return m.pauseDecision(row, task.DecisionAbort, "已发送中止指令"), nil
	default:
		return false, nil
	}
}

// renameFocused 重命名光标所在的主任务。子任务行和某一次的行不支持。
func (m *model) renameFocused(row rosterRow) (bool, tea.Cmd) {
	if row.itemID != "" {
		return false, nil
	}
	b, ok := m.batchByID(row.batchID)
	if !ok {
		m.setStatus("× 未找到任务，无法重命名")
		return true, nil
	}
	return true, m.openRenameModal(b)
}

// editFocused 重新编辑光标所在主任务的创建参数。子任务行和某一次的行不支持；
// 运行中或暂停中的批次不能编辑，先中止或等它结束再按 e。
func (m *model) editFocused(row rosterRow) (bool, tea.Cmd) {
	if row.itemID != "" {
		return false, nil
	}
	b, ok := m.batchByID(row.batchID)
	if !ok {
		m.setStatus("× 未找到任务，无法编辑")
		return true, nil
	}
	if b.Status == task.StatusRunning || b.Status == task.StatusPaused {
		m.setStatus("× 任务正在执行或暂停中，无法编辑")
		return true, nil
	}
	return true, m.openEditTaskModal(b)
}

// renameBatch 修改主任务名称并持久化。队列中的任务经队列改名（异步落盘，列表实时刷新）；
// 历史任务不在队列里，直接改数据库副本。最后重读数据库并刷新列表。
func (m *model) renameBatch(id, name string) error {
	if m.queue != nil {
		if _, inQueue := m.queue.BatchCopy(id); inQueue {
			if err := m.queue.Rename(id, name); err != nil {
				return err
			}
			m.syncRoster()
			return nil
		}
	}
	b, ok := m.batchByID(id)
	if !ok {
		return fmt.Errorf("未找到任务 %s", id)
	}
	b.Name = strings.TrimSpace(name)
	if err := b.Save(); err != nil {
		return err
	}
	m.syncRosterFromStore()
	return nil
}

// confirmOverwrite 对「输出已存在」的失败确认一次，然后带覆盖重试。
func (m *model) confirmOverwrite(row rosterRow) bool {
	if m.queue == nil || m.pausedBatch == "" || row.batchID != m.pausedBatch {
		return false
	}
	itemID := row.itemID
	if itemID == "" {
		itemID = m.pausedFailedItem(row.batchID)
	}
	snap, ok := m.batchByID(row.batchID)
	if !ok {
		return false
	}
	var target *task.Item
	for _, it := range snap.Items {
		if it.ID == itemID {
			target = it
			break
		}
	}
	if target == nil || !task.IsOutputExists(target.Error) {
		m.setStatus("当前失败不是输出文件已存在，不能覆盖")
		return true
	}
	batchID, id, path := row.batchID, target.ID, target.Output
	m.openConfirmModal("覆盖已有文件", fmt.Sprintf("输出文件已存在：\n%s\n\n覆盖会替换该文件，无法恢复。enter 覆盖并重试，esc 取消", path), func(m *model) tea.Cmd {
		if err := m.queue.PrepareOverwrite(batchID, id); err != nil {
			m.setStatus("× 覆盖失败: " + err.Error())
			return nil
		}
		m.queue.Decide(task.DecisionRetry)
		m.clearPause("已确认覆盖，正在重试")
		return nil
	})
	return true
}

// pausedFailedItem 返回暂停批次里当前失败的子任务。
func (m model) pausedFailedItem(batchID string) string {
	snap, ok := m.batchByID(batchID)
	if !ok {
		return ""
	}
	for _, it := range snap.Items {
		if it.Status == task.StatusFailed {
			return it.ID
		}
	}
	return ""
}

// retryFocused 重试光标所在任务。暂停中重试当前失败的子任务；已结束的主任务把失败项重新排队。
func (m *model) retryFocused(row rosterRow) bool {
	if row.itemID != "" && m.pausedBatch != row.batchID {
		return false
	}
	if row.itemID != "" {
		m.queue.Decide(task.DecisionRetry)
		m.clearPause("已发送重试指令")
		return true
	}
	if err := m.queue.RetryBatch(row.batchID); err != nil {
		if saved, ok := m.batchByID(row.batchID); ok {
			if err2 := m.queue.Requeue(saved); err2 != nil {
				m.setStatus("× 重试失败: " + err2.Error())
				return true
			}
			m.setStatus("已重新排队")
			return true
		}
		m.setStatus("× 重试失败: " + err.Error())
		return true
	}
	if m.pausedBatch == row.batchID {
		m.clearPause("已发送重试指令，等待执行")
		return true
	}
	m.setStatus("已发送重试指令，等待执行")
	return true
}

// canStartRow 报告该行是一条还在等待、可以手动开始的主任务。暂停中的批次留给中止键。
func (m model) canStartRow(row rosterRow) bool {
	if m.queue == nil || row.batchID == "" || m.pausedBatch == row.batchID {
		return false
	}
	snap, ok := m.batchByID(row.batchID)
	return ok && snap.Status == task.StatusPending
}

// startFocused 开始光标所在、还在等待的主任务。暂停中的批次留给中止键。
func (m *model) startFocused(row rosterRow) bool {
	if !m.canStartRow(row) {
		return false
	}
	busy := m.running || m.pausedBatch != ""
	if err := m.queue.Start(row.batchID); err != nil {
		m.setStatus("× 开始失败: " + err.Error())
		return true
	}
	if busy {
		m.setStatus("已安排执行，当前任务结束后开始")
		return true
	}
	m.setStatus("已开始执行")
	return true
}

// canOverwriteFocused 表示光标所在的暂停任务是因为输出文件已存在而失败。
func (m model) canOverwriteFocused() bool {
	row, ok := m.focusedRosterRow()
	if m.queue == nil || m.pausedBatch == "" || !ok {
		return false
	}
	if row.batchID != m.pausedBatch {
		return false
	}
	id := row.itemID
	if id == "" {
		id = m.pausedFailedItem(row.batchID)
	}
	snap, ok := m.batchByID(row.batchID)
	if !ok {
		return false
	}
	for _, it := range snap.Items {
		if it.ID == id && task.IsOutputExists(it.Error) {
			return true
		}
	}
	return false
}

// canStartFocused 表示光标在一条等待执行的主任务上，底栏把 x 显示为开始。
func (m model) canStartFocused() bool {
	row, ok := m.focusedRosterRow()
	if !ok {
		return false
	}
	return m.canStartRow(row)
}

// pauseDecision 在暂停等待时发送跳过或中止。子任务行与主任务行都作用于当前暂停的批次。
func (m *model) pauseDecision(row rosterRow, d task.PauseDecision, msg string) bool {
	if m.pausedBatch == "" || m.pausedBatch != row.batchID {
		return false
	}
	m.queue.Decide(d)
	m.clearPause(msg)
	return true
}

// deleteFocused 删除光标所在的子任务，或把整个主任务移出队列。
func (m *model) deleteFocused(row rosterRow) bool {
	if row.itemID == "" {
		if _, inQueue := m.queue.BatchCopy(row.batchID); !inQueue {
			if m.db != nil {
				if err := m.db.DeleteTask(row.batchID); err != nil {
					m.setStatus("× 删除失败: " + err.Error())
					return true
				}
			}
			m.setStatus("已删除任务")
			m.syncRosterFromStore()
			return true
		}
		if m.forget == nil {
			m.forget = map[string]struct{}{}
		}
		m.forget[row.batchID] = struct{}{}
		if err := m.queue.Discard(row.batchID); err != nil {
			delete(m.forget, row.batchID)
			m.setStatus("× 删除失败: " + err.Error())
			return true
		}
		if m.pausedBatch == row.batchID {
			m.clearPause("已删除任务")
			return true
		}
		m.setStatus("已删除任务")
		return true
	}
	pausedItem := false
	if snap, ok := m.batchByID(row.batchID); ok && snap.Status == task.StatusPaused {
		for _, it := range snap.Items {
			if it.ID == row.itemID && it.Status == task.StatusFailed {
				pausedItem = true
			}
		}
	}
	if err := m.queue.DiscardItem(row.batchID, row.itemID); err != nil {
		m.setStatus("× 删除失败: " + err.Error())
		return true
	}
	if pausedItem {
		m.clearPause("已删除子任务")
		return true
	}
	m.setStatus("已删除子任务")
	return true
}

// cancelFocused 取消光标所在的主任务：执行中的停止当前命令，等待中的不再开始。
func (m *model) cancelFocused(row rosterRow) bool {
	if err := m.queue.CancelBatch(row.batchID); err != nil {
		m.setStatus("× 取消失败: " + err.Error())
		return true
	}
	if m.pausedBatch == row.batchID {
		m.clearPause("已发送取消请求，等待任务结束")
		return true
	}
	m.setStatus("已发送取消请求")
	return true
}

// batchCommands 返回指定批次全部子任务的完整命令行。
func (m *model) batchCommands(batchID string) []string {
	snap, ok := m.batchByID(batchID)
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

// copyShownCommand 把任务面板正在展示的命令复制到系统剪贴板。
// 执行中或队列视图复制光标所在子任务的完整 CLI，批次行复制整批命令。
// 通过 OSC52 终端转义序列写入，无需外部依赖；由终端负责转贴到系统剪贴板。
func (m *model) copyShownCommand() tea.Cmd {
	if m.running || m.queueViewActive() {
		if row, ok := m.focusedRosterRow(); ok && row.cmd == "" {
			// 批次行：复制该批次全部子任务命令。
			commands := m.batchCommands(row.batchID)
			if len(commands) == 0 {
				m.setStatus("× 尚无命令可复制，等待任务开始")
				return nil
			}
			m.setStatus(fmt.Sprintf("✔ 已复制批次全部 %d 条命令到剪贴板", len(commands)))
			return tea.SetClipboard(strings.Join(commands, "\n"))
		}
		cmd := m.pinnedCmdText()
		if cmd == "" {
			m.setStatus("× 尚无命令可复制，等待任务开始")
			return nil
		}
		m.setStatus("✔ 已复制所选任务的完整命令到剪贴板")
		return tea.SetClipboard(cmd)
	}
	m.setStatus("× 尚无命令可复制，等待任务开始")
	return nil
}

// enqueue 把计划交给任务队列。入队后停在等待，需在主任务上按 x 开始。
func (m *model) enqueue(plan *command.BatchPlan, sel command.Selection) tea.Cmd {
	if m.queue == nil {
		m.setStatus("× 队列未就绪，请重启程序")
		return nil
	}
	b, err := m.queue.Enqueue(plan, sel)
	if err != nil {
		m.setStatus("× 入队失败: " + err.Error())
		return nil
	}
	if name := strings.TrimSpace(plan.Name); name != "" {
		m.setStatus(fmt.Sprintf("已加入队列「%s」（%d 个任务），在该任务上按 x 开始", name, len(b.Items)))
		return nil
	}
	m.setStatus(fmt.Sprintf("已加入任务队列（%d 个任务），在该任务上按 x 开始", len(b.Items)))
	return nil
}
