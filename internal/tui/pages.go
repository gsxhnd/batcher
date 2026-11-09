package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
)

// ---------- 页面跳转 ----------

// confirmFeature 校验至少选择一个操作，进入下一界面（有转码先管参数，有字幕先管字幕列表）。
func (m *model) confirmFeature() tea.Cmd {
	if m.ops.String() == "无" {
		m.featErr = "请至少勾选一项操作"
		return nil
	}
	if m.ops.Convert {
		user, err := presets.Load()
		m.presets = user
		if err != nil {
			// 读取失败时视作无预设，可继续手动输入。
			m.presets = nil
			m.statusMsg = "× 读取自定义预设失败: " + err.Error()
		}
		m.presetCursor = 0
		m.phase = phaseAdvance
		return nil
	}
	return m.afterAdvance()
}

// afterAdvance 处理完转码参数后的分流。
func (m *model) afterAdvance() tea.Cmd {
	if m.ops.AddSubtitle {
		m.subCursor = 0
		m.phase = phaseSubs
		return nil
	}
	return m.openForm()
}

// ---------- 操作勾选 ----------

func (m *model) updateFeature(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		m.featCursor = (m.featCursor - 1 + len(features)) % len(features)
	case "down", "j":
		m.featCursor = (m.featCursor + 1) % len(features)
	case " ", "space":
		features[m.featCursor].toggle(&m.ops)
		if m.ops.String() != "无" {
			m.featErr = ""
		}
	case "enter", "tab":
		return m.confirmFeature()
	case "p":
		return m.openPresets(phaseFeature)
	case "q", "esc":
		return tea.Quit
	}
	return nil
}

// ---------- 转码参数选择（phaseAdvance） ----------

// advanceUserOffset 返回自定义预设区在完整列表中的起始下标。
func (m model) advanceUserOffset() int { return len(m.builtins) }

func (m *model) updateAdvance(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	s := key.String()
	if s != "d" {
		m.delPendingIdx = -1 // 任何其他按键都取消删除确认
	}
	offset := m.advanceUserOffset()
	manual := offset + len(m.presets) // 手动输入选项的下标，同时等于列表长度
	switch s {
	case "up", "k":
		m.presetCursor = (m.presetCursor - 1 + manual + 1) % (manual + 1)
	case "down", "j":
		m.presetCursor = (m.presetCursor + 1) % (manual + 1)
	case "d":
		if m.presetCursor < offset {
			m.statusMsg = "内置预设不可删除，自定义预设可在参数管理页创建"
		} else {
			idx := m.presetCursor - offset
			if m.delPendingIdx != idx {
				// 第一次按 d：进入待确认状态，再次按 d 才真正删除。
				m.delPendingIdx = idx
				m.statusMsg = fmt.Sprintf("将删除自定义预设 %d，再次按 d 确认（其他任意键取消）", idx+1)
			} else {
				m.delPendingIdx = -1
				m.presets = append(m.presets[:idx], m.presets[idx+1:]...)
				m.persistPresets("已删除")
				if m.presetCursor >= manual-1 && m.presetCursor > 0 {
					m.presetCursor--
				}
			}
		}
	case "a":
		// 从选中的内置预设复制一份到自定义预设，随后可微调参数。
		if m.presetCursor < offset {
			b := m.builtins[m.presetCursor]
			return m.openPresetEditWith(-1, presets.Preset{Name: b.Name, Args: b.Args})
		}
		return m.openPresetEditWith(-1, presets.Preset{})
	case "p":
		return m.openPresets(phaseAdvance)
	case "enter":
		switch {
		case m.presetCursor < offset:
			m.manualAdvance = m.builtins[m.presetCursor].Args
		case m.presetCursor < manual:
			m.manualAdvance = m.presets[m.presetCursor-offset].Args
		default:
			// 手动输入：进入主表单后自行填写高级参数。
			m.manualAdvance = ""
		}
		return m.afterAdvance()
	case "q", "esc":
		if key.String() == "q" {
			return tea.Quit
		}
		m.phase = phaseFeature
		return nil
	}
	return nil
}

// ---------- 转码参数管理（phasePresets / phasePresetEdit） ----------

// openPresets 进入转码参数管理页（仅管理自定义预设），from 为返回目标。
func (m *model) openPresets(from phase) tea.Cmd {
	user, err := presets.Load()
	if err != nil {
		m.statusMsg = "× 读取自定义预设失败: " + err.Error()
	}
	m.presets = user
	m.mgrReturn = from
	m.mgrErr = ""
	m.delPendingIdx = -1
	m.mgrCursor = min(m.mgrCursor, max(m.mgrCount()-1, 0))
	m.phase = phasePresets
	return nil
}

func (m *model) updatePresets(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	s := key.String()
	if s != "d" && s != "x" {
		m.delPendingIdx = -1 // 任何其他按键都取消删除确认
	}
	// 光标与转码页一致：0..len(builtins)-1 为内置条目，之后为自定义条目。
	total := m.mgrCount()
	offset := m.advanceUserOffset()
	onBuiltin := m.mgrCursor < offset
	switch s {
	case "up", "k":
		if total > 0 {
			m.mgrCursor = (m.mgrCursor - 1 + total) % total
		}
	case "down", "j":
		if total > 0 {
			m.mgrCursor = (m.mgrCursor + 1) % total
		}
	case "a", "n":
		// 选中内置条目时复制一份到自定义预设，随后可微调；否则新增空白条目。
		if onBuiltin {
			b := m.builtins[m.mgrCursor]
			return m.openPresetEditWith(-1, presets.Preset{Name: b.Name, Args: b.Args})
		}
		return m.openPresetEditWith(-1, presets.Preset{})
	case "e", "enter":
		if onBuiltin {
			m.statusMsg = "内置预设只读，按 a 可复制为自定义预设后再编辑"
		} else if len(m.presets) > 0 {
			return m.openPresetEditWith(m.userIndex(), m.presets[m.userIndex()])
		}
	case "d", "x":
		if onBuiltin {
			m.statusMsg = "内置预设不可删除，自定义预设可在参数管理页创建"
		} else if len(m.presets) > 0 {
			idx := m.userIndex()
			if m.delPendingIdx != idx {
				// 第一次按 d：进入待确认状态，再次按 d/x 才真正删除。
				m.delPendingIdx = idx
				m.statusMsg = fmt.Sprintf("将删除自定义预设 %d，再次按 d 确认（其他任意键取消）", idx+1)
			} else {
				m.delPendingIdx = -1
				m.presets = append(m.presets[:idx], m.presets[idx+1:]...)
				if m.mgrCursor >= m.mgrCount() && m.mgrCursor > 0 {
					m.mgrCursor--
				}
				m.persistPresets("已删除")
			}
		}
	case "K", "shift+up":
		if !onBuiltin {
			m.movePreset(-1)
		}
	case "J", "shift+down":
		if !onBuiltin {
			m.movePreset(1)
		}
	case "esc":
		m.phase = m.mgrReturn
		return m.afterPresetsBack()
	case "q":
		return tea.Quit
	}
	return nil
}

// userIndex 返回当前光标对应的自定义预设下标（光标位于内置段时结果无意义，调用方需先判断）。
func (m *model) userIndex() int {
	return m.mgrCursor - m.advanceUserOffset()
}

// movePreset 把当前自定义预设上移（-1）或下移（+1），并保存顺序。
func (m *model) movePreset(delta int) {
	idx := m.userIndex()
	to := idx + delta
	if len(m.presets) < 2 || to < 0 || to >= len(m.presets) {
		return
	}
	m.presets[idx], m.presets[to] = m.presets[to], m.presets[idx]
	m.mgrCursor = m.advanceUserOffset() + to
	m.persistPresets("已调整顺序")
}

// persistPresets 写入用户预设文件，并以 action 作为成功提示前缀。
func (m *model) persistPresets(action string) {
	if err := presets.Save(m.presets); err != nil {
		m.statusMsg = "× 保存预设失败: " + err.Error()
		return
	}
	m.statusMsg = action + "，已保存到 " + presetHintText()
}

// afterPresetsBack 从管理页返回时收缩转码页光标，避免删除后越界。
func (m *model) afterPresetsBack() tea.Cmd {
	m.presetCursor = min(m.presetCursor, len(m.builtins)+len(m.presets))
	return nil
}

// openPresetEditWith 打开单条转码参数编辑，index=-1 表示新增，p 为表单的初始值。
func (m *model) openPresetEditWith(index int, p presets.Preset) tea.Cmd {
	m.mgrEditIndex = index
	m.mgrErr = ""
	m.delPendingIdx = -1
	m.mgrFields = make([]field, 0, len(presetFieldDefs))
	values := map[string]string{"preset_name": p.Name, "preset_args": p.Args}
	for _, def := range presetFieldDefs {
		nf := newField(def)
		nf.applyPresetDefaults()
		nf.input.SetValue(values[def.key])
		m.mgrFields = append(m.mgrFields, nf)
	}
	m.mgrFocus = 0
	m.phase = phasePresetEdit
	return focusField(m.mgrFields, m.mgrFocus)
}

func (m *model) updatePresetEdit(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	last := len(m.mgrFields) - 1
	switch key.String() {
	case "esc":
		m.phase = phasePresets
		return nil
	case "tab", "down":
		m.mgrFocus = (m.mgrFocus + 1) % len(m.mgrFields)
		return focusField(m.mgrFields, m.mgrFocus)
	case "shift+tab", "up":
		m.mgrFocus = (m.mgrFocus - 1 + len(m.mgrFields)) % len(m.mgrFields)
		return focusField(m.mgrFields, m.mgrFocus)
	case "enter":
		if m.mgrFocus == last {
			return m.savePresetEdit()
		}
		m.mgrFocus++
		return focusField(m.mgrFields, m.mgrFocus)
	default:
		var cmd tea.Cmd
		m.mgrFields[m.mgrFocus].input, cmd = m.mgrFields[m.mgrFocus].input.Update(msg)
		return cmd
	}
}

// savePresetEdit 校验并保存编辑结果：参数不能为空，且参数文本不能与其他预设重复。
func (m *model) savePresetEdit() tea.Cmd {
	entry := presets.Preset{
		Name: strings.TrimSpace(m.mgrFields[0].Value()),
		Args: presets.Trim(m.mgrFields[1].Value()),
	}
	if entry.Args == "" {
		m.mgrErr = "高级参数不能为空"
		m.mgrFocus = len(m.mgrFields) - 1
		return focusField(m.mgrFields, m.mgrFocus)
	}
	for i, p := range m.presets {
		if p.Args == entry.Args && i != m.mgrEditIndex {
			m.mgrErr = "已存在相同的参数"
			m.mgrFocus = len(m.mgrFields) - 1
			return focusField(m.mgrFields, m.mgrFocus)
		}
	}
	if m.mgrEditIndex >= 0 && m.mgrEditIndex < len(m.presets) {
		m.presets[m.mgrEditIndex] = entry
		m.mgrCursor = m.advanceUserOffset() + m.mgrEditIndex
		m.persistPresets("已更新")
	} else {
		m.presets = append(m.presets, entry)
		m.mgrCursor = m.mgrCount() - 1
		m.persistPresets("已新增")
	}
	m.mgrErr = ""
	m.phase = phasePresets
	return nil
}

// ---------- 字幕列表（phaseSubs / phaseSubEdit） ----------

func (m *model) updateSubs(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		if len(m.subs) > 0 {
			m.subCursor = (m.subCursor - 1 + len(m.subs)) % len(m.subs)
		}
	case "down", "j":
		if len(m.subs) > 0 {
			m.subCursor = (m.subCursor + 1) % len(m.subs)
		}
	case "a", "n":
		return m.openSubEdit(-1)
	case "e":
		if len(m.subs) > 0 {
			return m.openSubEdit(m.subCursor)
		}
	case "d":
		if len(m.subs) > 0 {
			m.subs = append(m.subs[:m.subCursor], m.subs[m.subCursor+1:]...)
			if m.subCursor >= len(m.subs) && m.subCursor > 0 {
				m.subCursor--
			}
		}
	case "enter":
		// 至少需要一条字幕才能继续。
		if len(m.subs) == 0 {
			m.subsErr = "请至少添加一条字幕（按 a 新增）"
			return nil
		}
		return m.openForm()
	case "q", "esc":
		if key.String() == "q" {
			return tea.Quit
		}
		m.phase = m.subsBackPhase()
	}
	return nil
}

// subsBackPhase 字幕列表的上一步：有转码则回到转码参数页，否则回到操作勾选页。
func (m *model) subsBackPhase() phase {
	if m.ops.Convert {
		return phaseAdvance
	}
	return phaseFeature
}

// formBackPhase 主参数表单的上一步，与 afterAdvance 的分流顺序保持一致。
func (m *model) formBackPhase() phase {
	if m.ops.AddSubtitle {
		return phaseSubs
	}
	if m.ops.Convert {
		return phaseAdvance
	}
	return phaseFeature
}

// openSubEdit 打开单条字幕编辑表单，index=-1 表示新增。
func (m *model) openSubEdit(index int) tea.Cmd {
	m.subEditIndex = index
	m.subFields = make([]field, 0, len(subFieldDefs))
	var firstCmd tea.Cmd

	var entry ffmpeg.SubtitleOption
	if index >= 0 {
		entry = m.subs[index]
	} else {
		// 新增条目默认沿用上一条的扩展名，方便批量录入 srt/eng 等。
		if len(m.subs) > 0 {
			entry.Suffix = m.subs[len(m.subs)-1].Suffix
		}
	}

	for i, def := range subFieldDefs {
		nf := newField(def)
		nf.applySubDefaults()
		switch def.key {
		case "sub_suffix":
			nf.input.SetValue(entry.Suffix)
		case "sub_lang":
			nf.input.SetValue(entry.Lang)
		case "sub_title":
			nf.input.SetValue(entry.Title)
		}
		if i == 0 {
			firstCmd = nf.input.Focus()
		}
		m.subFields = append(m.subFields, nf)
	}
	m.editFocus = 0
	m.phase = phaseSubEdit
	return firstCmd
}

func (m *model) updateSubEdit(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	f := &m.subFields[m.editFocus]
	switch key.String() {
	case "esc":
		m.phase = phaseSubs
		return nil
	case "tab", "down":
		m.editFocus = (m.editFocus + 1) % len(m.subFields)
		return m.refocusSubEdit()
	case "shift+tab", "up":
		m.editFocus = (m.editFocus - 1 + len(m.subFields)) % len(m.subFields)
		return m.refocusSubEdit()
	case "enter":
		if m.editFocus == len(m.subFields)-1 {
			return m.saveSubEdit()
		}
		m.editFocus++
		return m.refocusSubEdit()
	default:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return cmd
	}
}

func (m *model) refocusSubEdit() tea.Cmd {
	return focusField(m.subFields, m.editFocus)
}

// saveSubEdit 校验并保存正在编辑的字幕条目。
func (m *model) saveSubEdit() tea.Cmd {
	suffix := strings.TrimSpace(m.subFields[0].Value())
	lang := strings.TrimSpace(m.subFields[1].Value())
	title := strings.TrimSpace(m.subFields[2].Value())
	if suffix == "" {
		m.subFields[0].err = "必填"
		return nil
	}
	if !isISO639Code(lang) {
		m.subFields[1].err = "需为 ISO 639-2 三字母码，如 chi / eng / jpn"
		return nil
	}
	entry := ffmpeg.SubtitleOption{Suffix: suffix, Lang: lang, Title: title}
	if m.subEditIndex >= 0 && m.subEditIndex < len(m.subs) {
		m.subs[m.subEditIndex] = entry
	} else {
		m.subs = append(m.subs, entry)
		m.subCursor = len(m.subs) - 1
	}
	m.subsErr = ""
	m.phase = phaseSubs
	return nil
}

// ---------- 主参数表单（phaseForm） ----------

// openForm 构建主参数表单字段。
func (m *model) openForm() tea.Cmd {
	defs := fieldsFor(m.ops)
	f := ffmpeg.DefaultOption()

	m.fields = make([]field, 0, len(defs))
	var firstCmd tea.Cmd
	for i, def := range defs {
		nf := newField(def)
		nf.applyDefaults(&f)
		// 高级参数预填常用预设的值。
		if def.key == "advance" && m.manualAdvance != "" {
			nf.input.SetValue(m.manualAdvance)
		}
		if i == 0 {
			firstCmd = nf.input.Focus()
		}
		m.fields = append(m.fields, nf)
	}
	m.focus = 0
	m.planErr = nil
	m.previewDone = false
	m.phase = phaseForm
	return firstCmd
}

func (m *model) updateForm(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if key.String() == "ctrl+s" {
		return m.saveAdvancePreset()
	}

	f := &m.fields[m.focus]

	switch key.String() {
	case "esc":
		m.statusMsg = ""
		m.phase = m.formBackPhase()
		return nil
	case "tab", "down":
		m.focus = (m.focus + 1) % len(m.fields)
		return m.refocus()
	case "shift+tab", "up":
		m.focus = (m.focus - 1 + len(m.fields)) % len(m.fields)
		return m.refocus()
	case "enter":
		if m.focus == len(m.fields)-1 {
			return m.confirmForm()
		}
		m.focus++
		return m.refocus()
	default:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return cmd
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

func (m *model) refocus() tea.Cmd {
	return focusField(m.fields, m.focus)
}

// focusField 聚焦 fields[focus] 并失焦其余字段，返回聚焦输入框的光标闪烁命令。
func focusField(fields []field, focus int) tea.Cmd {
	var cmd tea.Cmd
	for i := range fields {
		if i == focus {
			cmd = fields[i].input.Focus()
		} else {
			fields[i].input.Blur()
		}
	}
	return cmd
}

// confirmForm 校验表单并进入预览（Plan 阶段，不执行命令）。
func (m *model) confirmForm() tea.Cmd {
	opt, err := m.buildOption()
	if err != nil {
		return nil
	}
	if m.ops.AddSubtitle && len(m.subs) == 0 {
		m.planErr = fmt.Errorf("至少需要一条字幕（esc 返回字幕管理）")
		return nil
	}
	m.opt = opt
	m.planErr = nil
	m.previewDone = false
	m.previewSummary = ""
	m.preview = "正在生成命令列表"
	m.files = nil
	m.fileCursor = 0
	m.runIdx = -1
	m.phase = phasePreview
	return tea.Batch(planCmd(m.opt, m.ops), m.spin.Tick)
}

// ---------- 预览与执行（phasePreview / phaseRunning / phaseResult） ----------

func (m *model) updatePreview(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "esc":
		m.phase = phaseForm
		return m.refocus()
	case "enter":
		if m.planErr != nil {
			m.phase = phaseForm
			return m.refocus()
		}
		return m.startRun()
	case "up", "k":
		m.moveFilePane(-1)
	case "down", "j":
		m.moveFilePane(1)
	case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return cmd
	}
	return nil
}

// startRun 初始化执行状态并启动批处理。
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
	m.phase = phaseRunning
	m.logVp.SetContent("")
	return startBatchCmd(ctx, m.opt, m.ops, m.evCh, m.doneCh)
}

func (m *model) updateRunning(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "q", "esc":
		m.quitCleanup()
		return tea.Quit
	case "up", "k":
		m.moveFilePane(-1)
	case "down", "j":
		m.moveFilePane(1)
	case "f":
		// 跳回正在执行的文件并恢复自动跟随。
		if m.runIdx >= 0 && m.runIdx < len(m.files) && m.files[m.runIdx].state == fileRunning {
			m.fileFollow = true
			m.fileCursor = m.runIdx + 1
			m.syncLogPane()
		}
	case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
		var cmd tea.Cmd
		m.logVp, cmd = m.logVp.Update(msg)
		return cmd
	}
	return nil
}

func (m *model) updateResult(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		m.moveFilePane(-1)
	case "down", "j":
		m.moveFilePane(1)
	case "b", "esc":
		m.phase = phaseForm
		return m.refocus()
	case "r":
		return m.confirmForm()
	case "q":
		return tea.Quit
	}
	return nil
}
