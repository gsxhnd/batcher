package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
)

type phase int

const (
	phaseFeature    phase = iota // 勾选要执行的操作
	phaseAdvance                 // 转码高级参数预设管理
	phaseSubs                    // 字幕列表管理
	phaseSubEdit                 // 单条字幕编辑
	phaseForm                    // 填写主参数
	phasePreview                 // 预览命令
	phaseRunning                 // 执行中
	phaseResult                  // 结果
	phasePresets                 // 转码参数管理列表
	phasePresetEdit              // 单条转码参数编辑
)

// feature 描述组合选择界面中的一个可勾选项。
type feature struct {
	hint  string
	apply func(*ffmpeg.Selection)
}

var features = []feature{
	{
		hint:  "视频转码",
		apply: func(s *ffmpeg.Selection) { s.Convert = !s.Convert },
	},
	{
		hint:  "添加字幕",
		apply: func(s *ffmpeg.Selection) { s.AddSubtitle = !s.AddSubtitle },
	},
	{
		hint:  "添加字体",
		apply: func(s *ffmpeg.Selection) { s.AddFonts = !s.AddFonts },
	},
}

var featureDescs = map[string]string{
	"视频转码": "按“高级自定义参数 + 输出后缀”转码；参数可在下一步选择/保存常用预设",
	"添加字幕": "把源目录下匹配视频名的字幕文件封装进视频，支持多条字幕",
	"添加字体": "把字体文件夹中的字体文件内嵌到视频（mkv 附带流）",
}

type model struct {
	width, height int

	phase phase

	// 操作组合勾选
	ops        ffmpeg.Selection
	featCursor int
	featErr    string

	// 转码高级参数预设
	presets       []string
	presetCursor  int
	manualAdvance string
	statusMsg     string // 界面上的临时提示（保存成功等）

	// 转码参数管理页
	mgrCursor    int
	mgrEditIndex int             // 当前编辑的预设下标，-1 表示新增
	mgrInput     textinput.Model // 编辑中的参数文本
	mgrErr       string
	mgrReturn    phase // 管理页返回的界面（操作页或转码页）

	// 字幕列表
	subs         []ffmpeg.SubtitleOption
	subCursor    int
	subsErr      string
	subEditIndex int     // 当前编辑的字幕下标，-1 表示新增
	editFocus    int     // 字幕编辑表单内的焦点
	subFields    []field // 字幕编辑表单（suffix/lang/title）

	// 主参数表单
	fields  []field
	focus   int
	preview string

	// 预览
	vp          viewport.Model
	previewDone bool
	planErr     error

	// 执行
	opt      *ffmpeg.VideoBatchOption
	total    int
	done     int
	failed   int
	current  string
	logs     []string
	logVp    viewport.Model
	bar      progress.Model
	cancel   context.CancelFunc
	evCh     chan ffmpeg.Event
	doneCh   chan error
	eventing bool

	// 结果
	runErr error
}

func newModel() model {
	return model{
		phase: phaseFeature,
		bar:   progress.New(progress.WithDefaultBlend()),
	}
}

// ---------- tea.Model ----------

func (m model) Init() tea.Cmd {
	return nil
}

// ---------- 消息 ----------

type planMsg struct {
	cmds [][]string
	err  error
}

type eventMsg struct{ event ffmpeg.Event }

type doneMsg struct{ err error }

type batchStartedMsg struct{}

// ---------- 命令 ----------

func planCmd(opt *ffmpeg.VideoBatchOption, sel ffmpeg.Selection) tea.Cmd {
	return func() tea.Msg {
		_, cmds, err := ffmpeg.PlanCombined(opt, sel)
		return planMsg{cmds: cmds, err: err}
	}
}

func listenEvents(ch <-chan ffmpeg.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return eventMsg{event: e}
	}
}

func listenDone(ch <-chan error) tea.Cmd {
	return func() tea.Msg {
		err := <-ch
		return doneMsg{err: err}
	}
}

func startBatchCmd(ctx context.Context, opt *ffmpeg.VideoBatchOption, sel ffmpeg.Selection, evCh chan ffmpeg.Event, doneCh chan error) tea.Cmd {
	return func() tea.Msg {
		opt.OnEvent = func(e ffmpeg.Event) {
			select {
			case evCh <- e:
			case <-ctx.Done():
			}
		}
		go func() {
			err := ffmpeg.RunCombined(ctx, opt, sel)
			close(evCh)
			doneCh <- err
			close(doneCh)
		}()
		return batchStartedMsg{}
	}
}

// ---------- Update ----------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
		// 纵向占用：顶栏 1 + 面板上下边框 2 + 底部分隔线与提示 2，其余留给面板内容。
		m.vp.SetWidth(max(ws.Width-6, 10))
		m.vp.SetHeight(max(ws.Height-9, 3))
		m.logVp.SetWidth(max(ws.Width-6, 10))
		m.logVp.SetHeight(max(ws.Height-14, 3))
		m.bar.SetWidth(max(ws.Width-8, 10))
		return m, nil
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.String() == "ctrl+c" {
			m.quitCleanup()
			return m, tea.Quit
		}
	}

	switch msg := msg.(type) {
	case planMsg:
		m.previewDone = true
		if msg.err != nil {
			m.planErr = msg.err
		} else {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("已选择：%s，共生成 %d 条命令（%d 个视频）\n", displaySel(m.ops), len(msg.cmds), len(msg.cmds)))
			if m.ops.AddSubtitle {
				var suffixes []string
				for _, s := range m.subs {
					suffixes = append(suffixes, s.Suffix)
				}
				fmt.Fprintf(&b, "字幕（%d 条）：.%s\n", len(m.subs), strings.Join(suffixes, " / ."))
			}
			b.WriteString("确认无误后按 Enter 开始执行；此阶段不会运行任何命令，执行后可用 ↑/↓ 查看日志。\n\n")
			for i, c := range msg.cmds {
				fmt.Fprintf(&b, "[%d/%d] %s\n\n", i+1, len(msg.cmds), ffmpeg.FormatCommand(c))
			}
			m.vp.SetContent(b.String())
			m.vp.GotoTop()
		}
		return m, nil

	case eventMsg:
		barCmd := m.handleEvent(msg.event)
		var cmd tea.Cmd
		if m.eventing {
			cmd = tea.Batch(listenEvents(m.evCh))
		}
		if barCmd != nil {
			if cmd == nil {
				cmd = barCmd
			} else {
				cmd = tea.Batch(cmd, barCmd)
			}
		}
		return m, cmd

	case doneMsg:
		m.eventing = false
		m.runErr = msg.err
		m.phase = phaseResult
		return m, nil

	case batchStartedMsg:
		m.eventing = true
		return m, tea.Batch(listenEvents(m.evCh), listenDone(m.doneCh))
	}

	var cmd tea.Cmd
	switch m.phase {
	case phaseFeature:
		cmd = m.updateFeature(msg)
	case phaseAdvance:
		cmd = m.updateAdvance(msg)
	case phaseSubs:
		cmd = m.updateSubs(msg)
	case phaseSubEdit:
		cmd = m.updateSubEdit(msg)
	case phaseForm:
		cmd = m.updateForm(msg)
	case phasePreview:
		cmd = m.updatePreview(msg)
	case phaseRunning:
		cmd = m.updateRunning(msg)
	case phaseResult:
		cmd = m.updateResult(msg)
	case phasePresets:
		cmd = m.updatePresets(msg)
	case phasePresetEdit:
		cmd = m.updatePresetEdit(msg)
	}
	return m, cmd
}

// ---------- 转码参数管理 ----------

// openPresets 进入转码参数管理页，from 为返回目标。
func (m *model) openPresets(from phase) tea.Cmd {
	presets, err := ffmpeg.LoadAdvancePresets()
	if err != nil {
		m.statusMsg = "× 读取常用参数失败: " + err.Error()
	}
	m.presets = presets
	m.mgrReturn = from
	m.mgrErr = ""
	if m.mgrCursor >= len(m.presets) {
		m.mgrCursor = max(len(m.presets)-1, 0)
	}
	m.phase = phasePresets
	return nil
}

func (m *model) updatePresets(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	n := len(m.presets)
	switch key.String() {
	case "up", "k":
		if n > 0 {
			m.mgrCursor = (m.mgrCursor - 1 + n) % n
		}
	case "down", "j":
		if n > 0 {
			m.mgrCursor = (m.mgrCursor + 1) % n
		}
	case "a", "n":
		return m.openPresetEdit(-1)
	case "e", "enter":
		if n > 0 {
			return m.openPresetEdit(m.mgrCursor)
		}
	case "d", "x":
		if n > 0 {
			m.presets = append(m.presets[:m.mgrCursor], m.presets[m.mgrCursor+1:]...)
			if m.mgrCursor >= len(m.presets) && m.mgrCursor > 0 {
				m.mgrCursor--
			}
			m.persistPresets("已删除")
		}
	case "K", "shift+up":
		m.movePreset(-1)
	case "J", "shift+down":
		m.movePreset(1)
	case "esc":
		m.phase = m.mgrReturn
		return m.afterPresetsBack()
	case "q":
		return tea.Quit
	}
	return nil
}

// movePreset 把当前预设上移（-1）或下移（+1），并保存顺序。
func (m *model) movePreset(delta int) {
	to := m.mgrCursor + delta
	if len(m.presets) < 2 || to < 0 || to >= len(m.presets) {
		return
	}
	m.presets[m.mgrCursor], m.presets[to] = m.presets[to], m.presets[m.mgrCursor]
	m.mgrCursor = to
	m.persistPresets("已调整顺序")
}

// persistPresets 写入 ~/.batcher/advance_presets.json，并以 action 作为成功提示前缀。
func (m *model) persistPresets(action string) {
	if m.mgrCursor >= len(m.presets) {
		m.mgrCursor = max(len(m.presets)-1, 0)
	}
	if err := ffmpeg.SaveAdvancePresets(m.presets); err != nil {
		m.statusMsg = "× 保存参数预设失败: " + err.Error()
		return
	}
	m.statusMsg = action + "，已保存到 ~/.batcher/" + ffmpeg.AdvancePresetFile
}

// afterPresetsBack 从管理页返回时收缩转码页光标，避免删除后越界。
func (m *model) afterPresetsBack() tea.Cmd {
	m.presetCursor = min(m.presetCursor, len(m.presets))
	return nil
}

// openPresetEdit 打开单条转码参数编辑，index=-1 表示新增。
func (m *model) openPresetEdit(index int) tea.Cmd {
	m.mgrEditIndex = index
	m.mgrErr = ""
	m.mgrInput = textinput.New()
	m.mgrInput.Placeholder = "例如: -c:v h265 -crf 20 -c:a copy"
	m.mgrInput.CharLimit = 512
	if index >= 0 {
		m.mgrInput.SetValue(m.presets[index])
	}
	m.phase = phasePresetEdit
	return m.mgrInput.Focus()
}

func (m *model) updatePresetEdit(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "esc":
		m.phase = phasePresets
		return nil
	case "enter":
		return m.savePresetEdit()
	default:
		var cmd tea.Cmd
		m.mgrInput, cmd = m.mgrInput.Update(msg)
		return cmd
	}
}

// savePresetEdit 校验并保存编辑结果；空参数与已存在的重复参数会被拒绝。
func (m *model) savePresetEdit() tea.Cmd {
	args := strings.Join(strings.Fields(m.mgrInput.Value()), " ")
	if args == "" {
		m.mgrErr = "参数不能为空"
		return nil
	}
	for i, p := range m.presets {
		if p == args && i != m.mgrEditIndex {
			m.mgrErr = "已存在相同的参数"
			return nil
		}
	}
	if m.mgrEditIndex >= 0 && m.mgrEditIndex < len(m.presets) {
		m.presets[m.mgrEditIndex] = args
		m.mgrCursor = m.mgrEditIndex
		m.persistPresets("已更新")
	} else {
		m.presets = append(m.presets, args)
		m.mgrCursor = len(m.presets) - 1
		m.persistPresets("已新增")
	}
	m.mgrErr = ""
	m.phase = phasePresets
	return nil
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
		features[m.featCursor].apply(&m.ops)
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

// confirmFeature 校验至少选择一个操作，进入下一界面（有转码先管参数，有字幕先管字幕列表）。
func (m *model) confirmFeature() tea.Cmd {
	if m.ops.String() == "无" {
		m.featErr = "请至少勾选一项操作"
		return nil
	}
	if m.ops.Convert {
		presets, err := ffmpeg.LoadAdvancePresets()
		m.presets = presets
		if err != nil {
			// 读取失败时视作无预设，可继续手动输入。
			m.presets = nil
			m.statusMsg = "× 读取常用参数失败: " + err.Error()
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

// ---------- 转码高级参数 ----------

func (m *model) updateAdvance(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	manual := len(m.presets) // 手动输入选项的下标
	switch key.String() {
	case "up", "k":
		m.presetCursor = (m.presetCursor - 1 + manual + 1) % (manual + 1)
	case "down", "j":
		m.presetCursor = (m.presetCursor + 1) % (manual + 1)
	case "d":
		if m.presetCursor < manual {
			m.presets = append(m.presets[:m.presetCursor], m.presets[m.presetCursor+1:]...)
			if err := ffmpeg.SaveAdvancePresets(m.presets); err != nil {
				m.statusMsg = "× 保存参数预设失败: " + err.Error()
			} else {
				m.statusMsg = "已删除，并保存到 ~/.batcher/" + ffmpeg.AdvancePresetFile
			}
			if m.presetCursor >= manual-1 && m.presetCursor > 0 {
				m.presetCursor--
			}
		}
	case "p":
		return m.openPresets(phaseAdvance)
	case "enter":
		if m.presetCursor == manual {
			// 手动输入：进入主表单后自行填写高级参数。
			m.manualAdvance = ""
		} else {
			m.manualAdvance = m.presets[m.presetCursor]
		}
		return m.afterAdvance()
	case "q", "esc":
		if key.String() == "q" {
			return tea.Quit
		}
		m.phase = phaseFeature
	}
	return nil
}

// ---------- 字幕列表 ----------

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
	var cmd tea.Cmd
	for i := range m.subFields {
		if i == m.editFocus {
			cmd = m.subFields[i].input.Focus()
		} else {
			m.subFields[i].input.Blur()
		}
	}
	return cmd
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

// ---------- 主参数表单 ----------

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

// saveAdvancePreset 把当前“高级自定义参数”保存为常用参数（~/.batcher/advance_presets.json）。
func (m *model) saveAdvancePreset() tea.Cmd {
	var args string
	for i := range m.fields {
		if m.fields[i].def.key == "advance" {
			args = m.fields[i].Value()
			break
		}
	}
	if args == "" {
		m.statusMsg = "× 参数为空，先在‘高级自定义参数’中填写，例如: -c:v h265 -crf 20 -c:a copy"
		return nil
	}
	if !m.ops.Convert {
		m.statusMsg = "× 未勾选“视频转码”，无需保存高级参数"
		return nil
	}
	m.presets = ffmpeg.UpsertPreset(m.presets, args)
	if err := ffmpeg.SaveAdvancePresets(m.presets); err != nil {
		m.statusMsg = "× 保存失败: " + err.Error()
		return nil
	}
	m.statusMsg = "已保存常用参数并写入 ~/.batcher/" + ffmpeg.AdvancePresetFile
	return nil
}

func (m *model) refocus() tea.Cmd {
	var cmd tea.Cmd
	for i := range m.fields {
		if i == m.focus {
			cmd = m.fields[i].input.Focus()
		} else {
			m.fields[i].input.Blur()
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
	m.preview = "正在生成命令列表..."
	m.phase = phasePreview
	return planCmd(m.opt, m.ops)
}

// ---------- 预览与执行 ----------

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
	case "up", "k", "pgup", "down", "j", "pgdown":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return cmd
	}
	return nil
}

func (m *model) startRun() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.evCh = make(chan ffmpeg.Event, 256)
	m.doneCh = make(chan error, 1)
	m.total, m.done, m.failed = 0, 0, 0
	m.current = ""
	m.logs = nil
	m.runErr = nil
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
		var cmd tea.Cmd
		m.logVp, cmd = m.logVp.Update(msg)
		return cmd
	case "down", "j":
		var cmd tea.Cmd
		m.logVp, cmd = m.logVp.Update(msg)
		m.logVp.GotoBottom()
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

func (m *model) quitCleanup() {
	if m.cancel != nil {
		m.cancel()
	}
}

// handleEvent 将 ffmpeg 事件写入日志、更新进度，返回进度条动画命令。
func (m *model) handleEvent(e ffmpeg.Event) tea.Cmd {
	switch e.Type {
	case ffmpeg.EventBatchStart:
		m.total = e.Total
		m.appendLog(fmt.Sprintf("开始执行批处理，共 %d 个任务", e.Total), logLineInfoStyle)
	case ffmpeg.EventCommandStart:
		m.current = e.Command
		m.appendLog(fmt.Sprintf("▶ [%d/%d] %s", e.Index+1, e.Total, e.Command), logLineInfoStyle)
	case ffmpeg.EventLog:
		m.appendLog("    "+e.Message, logLineDefaultStyle)
	case ffmpeg.EventCommandDone:
		m.done++
		m.appendLog(fmt.Sprintf("✔ [%d/%d] 完成", e.Index+1, e.Total), logLineDoneStyle)
	case ffmpeg.EventCommandFailed:
		m.failed++
		m.appendLog(fmt.Sprintf("✘ [%d/%d] 失败: %s", e.Index+1, e.Total, e.Message), logLineFailedStyle)
	}
	var cmd tea.Cmd
	if m.total > 0 {
		cmd = m.bar.SetPercent(float64(m.done+m.failed) / float64(m.total))
	}
	m.logVp.GotoBottom()
	return cmd
}

func (m *model) appendLog(line string, s lipgloss.Style) {
	m.logs = append(m.logs, s.Render(line))
	if len(m.logs) > 1000 {
		m.logs = m.logs[len(m.logs)-1000:]
	}
	m.logVp.SetContent(strings.Join(m.logs, "\n"))
}

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

// header 顶部标题栏：左侧应用徽标，右侧显示流程步骤并高亮当前步骤。
func (m model) header() string {
	left := appBadgeStyle.Render("batcher")
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

func (m model) presetsView() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render("转码参数管理") + "\n")
	b.WriteString(subtitleStyle.Render("a 新增，e/Enter 编辑，d 删除，shift+↑/↓ 调整顺序；顺序即转码页中的显示顺序") + "\n\n")
	if len(m.presets) == 0 {
		b.WriteString("  " + descStyle.Render("暂无常用参数，按 a 新增一条") + "\n")
	}
	for i, p := range m.presets {
		cursor, style := "  ", normalStyle
		if i == m.mgrCursor {
			cursor = selectedMark.Render("▸ ")
			style = selectedStyle
		}
		b.WriteString(fmt.Sprintf("%s%2d. %s\n", cursor, i+1, style.Render(p)))
	}
	if m.statusMsg != "" {
		b.WriteString("\n  " + statusStyle.Render(m.statusMsg) + "\n")
	}
	return b.String()
}

func (m model) presetEditView() string {
	var b strings.Builder
	title := "新增转码参数"
	if m.mgrEditIndex >= 0 {
		title = fmt.Sprintf("编辑第 %d 条转码参数", m.mgrEditIndex+1)
	}
	b.WriteString(taskTitleStyle.Render(title) + "\n")
	b.WriteString(subtitleStyle.Render("Enter 保存，esc 取消；参数会按空格拆分后传给 ffmpeg") + "\n\n")
	b.WriteString("  " + labelFocusedStyle.Render("高级参数") + " " + m.mgrInput.View() + "\n")
	if m.mgrErr != "" {
		b.WriteString("  " + strings.Repeat(" ", 18) + errorStyle.Render("× "+m.mgrErr) + "\n")
	}
	return b.String()
}

func (m model) featureView() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("勾选本次批处理要执行的操作（可任意组合，一次生成一条 ffmpeg 命令）：") + "\n\n")
	for i, feat := range features {
		mark := uncheckedStyle.Render("[ ]")
		if isSelected(m.ops, feat.hint) {
			mark = checkedStyle.Render("[✔]")
		}
		cursor, style := "  ", normalStyle
		if i == m.featCursor {
			cursor = selectedMark.Render("▸ ")
			style = selectedStyle
		}
		b.WriteString(cursor + mark + " " + style.Render(feat.hint) + "\n")
		if d, ok := featureDescs[feat.hint]; ok && i == m.featCursor {
			b.WriteString("      " + descStyle.Render(d) + "\n")
		}
	}
	b.WriteString("\n  已选择: " + selectedStyle.Render(displaySel(m.ops)) + "\n")
	if m.featErr != "" {
		b.WriteString("  " + errorStyle.Render("× "+m.featErr) + "\n")
	}
	return b.String()
}

func isSelected(sel ffmpeg.Selection, hint string) bool {
	switch hint {
	case "视频转码":
		return sel.Convert
	case "添加字幕":
		return sel.AddSubtitle
	case "添加字体":
		return sel.AddFonts
	}
	return false
}

func (m model) advanceView() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render("视频转码 · 高级参数") + "\n")
	b.WriteString(subtitleStyle.Render("↑/↓ 选择，Enter 使用所选继续；d 删除选中的常用参数；常用参数也可在后续表单页按 ctrl+s 保存") + "\n\n")
	for i, p := range m.presets {
		cursor, style := "  ", normalStyle
		if i == m.presetCursor {
			cursor = selectedMark.Render("▸ ")
			style = selectedStyle
		}
		b.WriteString(cursor + style.Render(p) + "\n")
	}
	cursor, style := "  ", normalStyle
	if m.presetCursor == len(m.presets) {
		cursor = selectedMark.Render("▸ ")
		style = selectedStyle
	}
	b.WriteString(cursor + style.Render("(手动输入参数)") + "\n")
	if len(m.presets) == 0 {
		b.WriteString("  " + descStyle.Render("尚无常用参数，可选择手动输入，之后在表单页按 ctrl+s 保存") + "\n")
	}
	if m.statusMsg != "" {
		b.WriteString("\n  " + statusStyle.Render(m.statusMsg) + "\n")
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
		cursor, style := "  ", normalStyle
		if i == m.subCursor {
			cursor = selectedMark.Render("▸ ")
			style = selectedStyle
		}
		b.WriteString(fmt.Sprintf("%s%d. %s  语言: %s  标题: %s\n", cursor, i+1,
			style.Render("."+sub.Suffix), sub.Lang, sub.Title))
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
	b.WriteString(subtitleStyle.Render("↑/↓ 或 Tab 切换字段，Enter 编辑之下一项、最后一项直接提交，esc 返回") + "\n\n")

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
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render(displaySel(m.ops)+" · 命令预览") + "\n")
	switch {
	case m.planErr != nil:
		b.WriteString("  " + errorStyle.Render("× 生成命令失败: "+m.planErr.Error()) + "\n\n")
		b.WriteString("  按 Enter / esc 返回修改参数。\n")
	case !m.previewDone:
		b.WriteString("  " + subtitleStyle.Render(m.preview) + "\n")
	default:
		b.WriteString(m.vp.View())
	}
	return b.String()
}

func (m model) runningView() string {
	var b strings.Builder
	b.WriteString(taskTitleStyle.Render(displaySel(m.ops)+" · 执行中") + "\n")

	if m.total > 0 {
		b.WriteString("  " + m.bar.View() + "\n")
		b.WriteString(fmt.Sprintf("  进度: %d/%d 完成，%d 失败\n\n", m.done, m.total, m.failed))
	} else {
		b.WriteString("  " + subtitleStyle.Render("正在启动...") + "\n\n")
	}
	if m.current != "" {
		b.WriteString("  " + subtitleStyle.Render("当前: "+m.current) + "\n")
	}
	b.WriteString(m.logVp.View() + "\n")
	return b.String()
}

func (m model) resultView() string {
	var b strings.Builder
	if m.runErr != nil {
		b.WriteString("  " + errorStyle.Render(fmt.Sprintf("× 批处理失败: %v", m.runErr)) + "\n\n")
	} else {
		b.WriteString("  " + successStyle.Render(fmt.Sprintf("✔ 批处理完成：%d 个任务全部成功", m.done)) + "\n\n")
	}
	b.WriteString("  r 重新生成并执行    b 返回参数配置    q 退出\n\n")

	if len(m.logs) > 0 {
		b.WriteString(strings.Join(m.logs[max(0, len(m.logs)-15):], "\n"))
	}
	return b.String()
}

func (m model) footer() string {
	var hints []string
	switch m.phase {
	case phaseFeature:
		hints = []string{"↑/↓ 切换", "space 勾选", "Enter 下一步", "p 参数管理", "q 退出"}
	case phaseAdvance:
		hints = []string{"↑/↓ 选择", "Enter 使用", "d 删除", "p 参数管理", "esc 返回"}
	case phasePresets:
		hints = []string{"a 新增", "e 编辑", "d 删除", "shift+↑/↓ 排序", "esc 返回"}
	case phasePresetEdit:
		hints = []string{"Enter 保存", "esc 取消"}
	case phaseSubs:
		hints = []string{"a 新增", "e 编辑", "d 删除", "↑/↓ 选择", "Enter 继续", "esc 返回"}
	case phaseSubEdit:
		hints = []string{"↑/↓/Tab 切换", "Enter 保存", "esc 取消"}
	case phaseForm:
		hints = []string{"↑/↓/Tab 切换", "Enter 下一个/提交", "ctrl+s 保存参数预设", "esc 返回"}
	case phasePreview:
		hints = []string{"Enter 开始执行", "esc 返回", "↑/↓ 滚动"}
	case phaseRunning:
		hints = []string{"q/esc 中止并退出", "↑/↓ 滚动日志"}
	case phaseResult:
		hints = []string{"r 重新执行", "b 返回配置", "q 退出"}
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
