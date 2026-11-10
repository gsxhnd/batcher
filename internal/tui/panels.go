package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
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
			return m.openPresetModal(-1, presets.Preset{Name: b.Name, Args: b.Args})
		}
		return m.openPresetModal(-1, presets.Preset{})
	case "e":
		if m.presetCursor >= offset && len(m.presets) > 0 {
			idx := m.presetCursor - offset
			return m.openPresetModal(idx, m.presets[idx])
		}
		m.statusMsg = "内置预设只读，按 a 可复制为自定义预设后再编辑"
		return nil
	case "d":
		if m.presetCursor < offset {
			m.statusMsg = "内置预设不可删除，自定义预设才可删除"
			return nil
		}
		idx := m.presetCursor - offset
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

// applyAdvanceToForm 把当前选中的预设参数写入主表单的“高级自定义参数”字段。
func (m *model) applyAdvanceToForm() {
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
	if m.manualAdvance == removed.Args {
		m.manualAdvance = ""
		m.applyAdvanceToForm()
	}
	m.presets = append(m.presets[:idx], m.presets[idx+1:]...)
	m.presetCursor = min(m.presetCursor, m.presetCount()-1)
	m.persistPresets("已删除")
	return m.schedulePlan(planDebounceDelay)
}

// movePreset 把当前自定义预设上移（-1）或下移（+1），并保存顺序。
func (m *model) movePreset(delta int) {
	offset := m.presetUserOffset()
	idx := m.presetCursor - offset
	to := idx + delta
	if len(m.presets) < 2 || to < 0 || to >= len(m.presets) {
		return
	}
	m.presets[idx], m.presets[to] = m.presets[to], m.presets[idx]
	m.presetCursor = offset + to
	m.persistPresets("已调整顺序")
}

// presetDisplayName 返回预设删除确认中显示的名称。
func presetDisplayName(p presets.Preset) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Args
}

// persistPresets 写入用户预设文件，并以 action 作为成功提示前缀。
func (m *model) persistPresets(action string) {
	if err := presets.Save(m.presets); err != nil {
		m.statusMsg = "× 保存预设失败: " + err.Error()
		return
	}
	m.statusMsg = action + "，已保存到 " + presetHintText()
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
			m.subs[m.subCursor].Default = !m.subs[m.subCursor].Default
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
		return m.focusFormNo(m.focusNo - 1)
	case "down", "j":
		return m.focusFormNo(m.focusNo + 1)
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
		return nil
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
		f := &m.fields[m.focusNo]
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return tea.Batch(cmd, m.schedulePlan(planDebounceDelay))
	}
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
	m.presets = presets.Upsert(m.presets, presets.Preset{Args: args})
	if err := presets.Save(m.presets); err != nil {
		m.statusMsg = "× 保存失败: " + err.Error()
		return nil
	}
	m.statusMsg = "已保存常用预设并写入 " + presetHintText()
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
	case "f":
		// 跳回正在执行的文件并恢复自动跟随。
		if m.runIdx >= 0 && m.runIdx < len(m.files) && m.files[m.runIdx].state == fileRunning {
			m.fileFollow = true
			m.fileCursor = m.runIdx + 1
			m.logFollow = true
			m.syncLogPane()
		}
		return nil
	case "enter":
		if !m.running && !m.editing && m.modal == nil {
			return m.tryStartRun()
		}
		return nil
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		if m.running {
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
		if m.running {
			m.logVp.GotoTop()
			m.logFollow = m.logVp.AtBottom()
			return nil
		}
		m.vp.GotoTop()
		return nil
	case "end":
		if m.running {
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

// ---------- 执行 ----------

// copyShownCommand 把任务面板正在展示的命令复制到系统剪贴板：
// 执行中固定区显示的是当前任务的完整 CLI，预览态复制所选文件的命令。
// 通过 OSC52 终端转义序列写入，无需外部依赖；由终端负责转贴到系统剪贴板。
func (m *model) copyShownCommand() tea.Cmd {
	if m.running {
		cmd := m.pinnedCmdText()
		if cmd == "" {
			m.statusMsg = "× 尚无命令可复制，等待任务开始"
			return nil
		}
		m.statusMsg = "✔ 已复制当前任务的完整命令到剪贴板"
		return tea.SetClipboard(cmd)
	}
	if !m.previewDone || m.planErr != nil || len(m.previewContent()) == 0 {
		m.statusMsg = "× 命令尚未生成，无法复制"
		return nil
	}
	m.statusMsg = "✔ 已复制所选命令到剪贴板"
	return tea.SetClipboard(m.previewContent())
}

// tryStartRun 校验配置并启动执行：失败时把焦点跳到出错的面板并给出提示。
func (m *model) tryStartRun() tea.Cmd {
	if m.running {
		return nil
	}
	if !m.ops.Any() {
		m.featErr = "请至少勾选一项操作"
		m.setFocus(panelOps)
		return nil
	}
	opt, err := m.buildOption()
	if err != nil {
		m.statusMsg = "× 校验失败：" + err.Error()
		m.setFocus(panelForm)
		return nil
	}
	m.opt = opt
	if m.ops.AddSubtitle && len(m.subs) == 0 {
		m.statusMsg = "× 已勾选添加字幕，请至少添加一条（面板 3 按 n）"
		m.setFocus(panelSubs)
		return nil
	}
	return m.startRun()
}

// startRun 初始化执行状态并启动批处理，随后配置全部锁定。
func (m *model) startRun() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.evCh = make(chan ffmpeg.Event, 256)
	m.doneCh = make(chan error, 1)
	m.total, m.done, m.failed = 0, 0, 0
	m.current = ""
	m.logs = nil
	m.runErr = nil
	m.runIdx = -1
	m.fileFollow = true
	m.fileCursor = 0
	for i := range m.files {
		m.files[i].state = filePending
		m.files[i].logs = nil
	}
	m.startAt = time.Now()
	m.elapsed = 0
	m.running = true
	m.logFollow = true
	m.setFocus(panelRun)
	m.refreshRunHeights()
	m.syncLogPane()
	m.logVp.GotoBottom()
	return startBatchCmd(ctx, m.opt, m.ops, m.evCh, m.doneCh)
}
