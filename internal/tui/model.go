package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
)

// panel 描述左右分栏布局中的各个面板（左侧小方块按 1-5 编号）。
type panel int

const (
	panelOps     panel = iota // [1] 操作组合勾选
	panelPresets              // [2] 转码参数预设（内置/自定义/手动输入）
	panelSubs                 // [3] 字幕轨道列表
	panelForm                 // [4] 主参数表单
	panelRun                  // [5] 任务：文件列表 + 命令预览/执行日志

	panelCount
)

// feature 描述组合选择面板中的一个可勾选操作。
type feature struct {
	label    string
	desc     string
	selected func(ffmpeg.Selection) bool
	toggle   func(*ffmpeg.Selection)
}

var features = []feature{
	{
		label:    "视频转码",
		desc:     "按“自定义参数 + 输出后缀”转码；参数在本面板选择/保存常用预设",
		selected: func(s ffmpeg.Selection) bool { return s.Convert },
		toggle:   func(s *ffmpeg.Selection) { s.Convert = !s.Convert },
	},
	{
		label:    "添加字幕",
		desc:     "把源目录下匹配视频名的字幕文件封装进视频，支持多条字幕",
		selected: func(s ffmpeg.Selection) bool { return s.AddSubtitle },
		toggle:   func(s *ffmpeg.Selection) { s.AddSubtitle = !s.AddSubtitle },
	},
	{
		label:    "添加字体",
		desc:     "把字体文件夹中的字体文件内嵌到视频（mkv 附带流）",
		selected: func(s ffmpeg.Selection) bool { return s.AddFonts },
		toggle:   func(s *ffmpeg.Selection) { s.AddFonts = !s.AddFonts },
	},
}

// 文件任务的执行状态（左侧列表标记用）。
const (
	filePending = ""        // 待执行
	fileRunning = "running" // 执行中
	fileDone    = "done"    // 完成
	fileFailed  = "failed"  // 失败
)

// fileItem 描述任务面板文件列表中的一个源文件任务。
type fileItem struct {
	input  string   // 源文件路径
	output string   // 输出文件路径
	cmd    string   // 已格式化的完整 ffmpeg 命令
	state  string   // filePending / fileRunning / fileDone / fileFailed
	logs   []string // 该任务执行时的 ffmpeg 输出（按行，带样式）
}

type model struct {
	width, height int

	focus panel // 当前聚焦的面板（1-5）

	// 操作组合勾选
	ops        ffmpeg.Selection
	featCursor int
	featErr    string

	// 转码参数预设：内置（只读）在前，用户自定义在后，最后是“手动输入”选项
	builtins      []presets.Builtin
	presets       []presets.Preset
	presetCursor  int
	manualAdvance string
	statusMsg     string // 界面上的临时提示（保存成功等）

	// 字幕列表
	subs      []ffmpeg.SubtitleOption
	subCursor int

	// 主参数表单（进入编辑态后全局快捷键失效）
	fields  []field
	focusNo int  // 表单内的字段下标
	editing bool // 是否处于表单输入状态

	// 弹窗（预设/字幕编辑、删除确认），非空时按键全部交给弹窗
	modal *modal

	// 实时命令预览：配置变化后防抖生成，planSeq 丢弃过期结果
	planSeq      int
	planLoading  bool
	previewDone  bool
	planErr      error
	planGenerate bool // 防抖 tick 到期后是否需要重新生成

	// 命令预览/执行日志共用的右侧视口
	vp             viewport.Model
	previewSummary string // Plan 汇总文本（全部命令列表）
	spin           spinner.Model

	// 任务面板：左侧文件列表，右侧命令/日志
	files      []fileItem
	fileCursor int  // 光标：0 = 全部汇总，1.. = 第 i 个源文件
	fileFollow bool // 执行时右侧自动跟随正在执行的文件（手动切换后停止）
	runIdx     int  // 正在执行的文件下标（files 内），-1 表示当前无执行中任务
	leftW      int  // 左侧面板栏宽度
	rightW     int  // 右侧内容区宽度

	// 执行
	running  bool
	opt      *ffmpeg.VideoBatchOption
	total    int
	done     int
	failed   int
	current  string
	logs     []string
	logVp    viewport.Model
	bar      progress.Model
	startAt  time.Time     // 开始执行时间（用于显示耗时）
	elapsed  time.Duration // 批处理总耗时（结束时记录）
	cancel   context.CancelFunc
	evCh     chan ffmpeg.Event
	doneCh   chan error
	eventing bool
	runErr   error
}

func newModel() model {
	m := model{
		focus:    panelOps,
		bar:      progress.New(progress.WithDefaultBlend()),
		spin:     spinner.New(spinner.WithSpinner(spinner.Dot)),
		builtins: presets.Builtins(),
		runIdx:   -1,
	}
	// 启动时读取自定义预设（面板直接展示，无需单独的管理页）。
	user, err := presets.Load()
	if err != nil {
		m.statusMsg = "× 读取自定义预设失败: " + err.Error()
	}
	m.presets = user
	m.rebuildFields()
	return m
}

// presetCount 返回转码预设列表完整长度：内置 + 自定义 + 手动输入。
func (m model) presetCount() int {
	return len(m.builtins) + len(m.presets) + 1
}

// presetUserOffset 返回自定义预设区在完整列表中的起始下标。
func (m model) presetUserOffset() int { return len(m.builtins) }

// presetHintText 返回用户预设文件路径，用于状态提示。
func presetHintText() string {
	path, err := presets.FilePath()
	if err != nil {
		return "预设文件夹（无法解析位置: " + err.Error() + "）"
	}
	return path
}

// panelEnabled 判断面板当前是否可用：未勾选对应操作的面板变灰不可进入；
// 执行期间只允许停留在任务面板。
func (m model) panelEnabled(p panel) bool {
	if m.running {
		return p == panelRun
	}
	switch p {
	case panelPresets:
		return m.ops.Convert
	case panelSubs:
		return m.ops.AddSubtitle
	default:
		return true
	}
}

// setFocus 切换聚焦面板；目标不可用时给出提示并保持原焦点。
func (m *model) setFocus(p panel) {
	if p < 0 || p >= panelCount {
		return
	}
	if !m.panelEnabled(p) {
		switch p {
		case panelPresets:
			m.statusMsg = "未勾选「视频转码」，转码预设面板不可用"
		case panelSubs:
			m.statusMsg = "未勾选「添加字幕」，字幕面板不可用"
		case panelRun:
			m.statusMsg = "执行中请停留在任务面板"
		}
		return
	}
	if m.editing {
		m.blurForm()
	}
	if m.focus == panelRun && p != panelRun {
		// 离开任务面板时取消自动跟随（下次执行前重新开启）。
		m.fileFollow = false
	}
	m.focus = p
}

// nextPanel 按 tab 方向循环切换到下一个可用面板。
func (m *model) nextPanel(delta int) {
	for i := 1; i <= int(panelCount); i++ {
		p := (int(m.focus) + delta*i%int(panelCount) + int(panelCount)) % int(panelCount)
		if m.panelEnabled(panel(p)) {
			m.setFocus(panel(p))
			return
		}
	}
}

// ---------- tea.Model ----------

func (m model) Init() tea.Cmd {
	return nil
}

// ---------- 消息 ----------

type planMsg struct {
	seq  int
	cmds [][]string
	err  error
}

// planDebounceMsg 是配置变化后的防抖时钟：到期且序号仍是最新时才重新生成命令。
type planDebounceMsg struct{ seq int }

type eventMsg struct{ event ffmpeg.Event }

type doneMsg struct{ err error }

type batchStartedMsg struct{}

type tickMsg struct{}

// ---------- 命令 ----------

// maxLogLines 限制日志缓冲行数，避免长任务占用过多内存。
const maxLogLines = 1000

// planDebounceDelay 配置变化后等待多少毫秒再重新生成命令列表。
const planDebounceDelay = 300 * time.Millisecond

func planCmd(opt *ffmpeg.VideoBatchOption, sel ffmpeg.Selection, seq int) tea.Cmd {
	return func() tea.Msg {
		_, cmds, err := ffmpeg.PlanCombined(opt, sel)
		return planMsg{seq: seq, cmds: cmds, err: err}
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

// tickCmd 每秒触发一次重绘，用于任务面板显示实时耗时。
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

// ---------- Update ----------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizePanes()
		m.resizeModal()
		// 尺寸变化后按新宽度重新折行刷新两侧视口。
		m.syncPreview()
		m.syncLogPane()
		return m, nil

	case planMsg:
		if msg.seq != m.planSeq {
			return m, nil // 过期的 Plan 结果直接丢弃
		}
		m.planLoading = false
		m.previewDone = true
		if msg.err != nil {
			m.planErr = msg.err
			m.files = nil
			m.fileCursor = 0
			m.runIdx = -1
			m.previewSummary = ""
			return m, nil
		}
		m.planErr = nil
		m.files = buildFileItems(msg.cmds)
		m.fileCursor = 0
		m.runIdx = -1
		m.previewSummary = m.summaryText(msg.cmds)
		m.syncPreview()
		m.vp.GotoTop()
		return m, nil

	case planDebounceMsg:
		if msg.seq == m.planSeq && m.planGenerate {
			return m, m.runPlan()
		}
		return m, nil

	case eventMsg:
		cmds := []tea.Cmd{m.handleEvent(msg.event)}
		if m.eventing {
			cmds = append(cmds, listenEvents(m.evCh))
		}
		return m, tea.Batch(cmds...)

	case doneMsg:
		m.eventing = false
		m.runErr = msg.err
		m.elapsed = time.Since(m.startAt).Round(time.Second)
		m.running = false
		if msg.err != nil {
			m.statusMsg = "× 批处理已结束: " + msg.err.Error()
		} else {
			m.statusMsg = "✔ 批处理完成，可直接修改配置后按 x 重跑"
		}
		return m, nil

	case batchStartedMsg:
		m.eventing = true
		return m, tea.Batch(listenEvents(m.evCh), listenDone(m.doneCh), tickCmd())

	case tickMsg:
		if m.eventing {
			return m, tickCmd()
		}
		return m, nil

	case spinner.TickMsg:
		if m.planLoading {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.String() == "ctrl+c" {
			m.quitCleanup()
			return m, tea.Quit
		}
		return m, m.handleKey(key)
	}
	return m, nil
}

// handleKey 按当前状态分发按键：弹窗 > 表单编辑 > 执行中查看 > 全局键 > 面板按键。
func (m *model) handleKey(key tea.KeyPressMsg) tea.Cmd {
	if m.modal != nil {
		return m.updateModal(key)
	}
	s := key.String()

	if m.editing {
		return m.updateFormEdit(key)
	}

	if m.running {
		// 运行期间只允许查看任务面板；q/esc 取消执行并保留界面。
		switch s {
		case "q", "esc":
			if m.cancel != nil {
				m.cancel()
				m.statusMsg = "已发送取消请求，等待正在执行的任务结束…"
			}
			return nil
		}
		return m.updateRunPanel(key)
	}

	switch s {
	case "1":
		m.setFocus(panelOps)
		return nil
	case "2":
		m.setFocus(panelPresets)
		return nil
	case "3":
		m.setFocus(panelSubs)
		return nil
	case "4":
		m.setFocus(panelForm)
		return nil
	case "5":
		m.setFocus(panelRun)
		return nil
	case "tab":
		m.nextPanel(1)
		return nil
	case "shift+tab":
		m.nextPanel(-1)
		return nil
	case "q":
		return tea.Quit
	case "?":
		m.openHelpModal()
		return nil
	case "x":
		return m.tryStartRun()
	case "enter":
		if m.focus == panelRun {
			return m.tryStartRun()
		}
	}

	if !m.panelEnabled(m.focus) {
		// 聚焦面板已被禁用（例如执行结束取消勾选转码），不响应面板内按键。
		return nil
	}
	switch m.focus {
	case panelOps:
		return m.updateOpsPanel(key)
	case panelPresets:
		return m.updatePresetsPanel(key)
	case panelSubs:
		return m.updateSubsPanel(key)
	case panelForm:
		return m.updateFormPanel(key)
	case panelRun:
		return m.updateRunPanel(key)
	}
	return nil
}

// handleEvent 将 ffmpeg 事件写入全局日志与对应文件的日志，并更新进度条。
// 事件 Index 与 files 顺序一致，因此可直接定位到源文件条目。
func (m *model) handleEvent(e ffmpeg.Event) tea.Cmd {
	switch e.Type {
	case ffmpeg.EventBatchStart:
		m.total = e.Total
		m.appendLog(fmt.Sprintf("开始执行批处理，共 %d 个任务", e.Total), logLineInfoStyle)
	case ffmpeg.EventCommandStart:
		m.current = e.Command
		m.runIdx = e.Index
		line := fmt.Sprintf("▶ [%d/%d] %s", e.Index+1, e.Total, e.Command)
		m.recordLine(e.Index, line, fileRunning, logLineInfoStyle)
		if m.fileFollow {
			m.fileCursor = e.Index + 1
		}
	case ffmpeg.EventLog:
		m.recordLine(e.Index, "    "+e.Message, "", logLineDefaultStyle)
	case ffmpeg.EventCommandDone:
		m.done++
		m.current = ""
		m.recordLine(e.Index, fmt.Sprintf("✔ [%d/%d] 完成", e.Index+1, e.Total), fileDone, logLineDoneStyle)
	case ffmpeg.EventCommandFailed:
		m.failed++
		m.current = ""
		m.recordLine(e.Index, fmt.Sprintf("✘ [%d/%d] 失败: %s", e.Index+1, e.Total, e.Message), fileFailed, logLineFailedStyle)
	}
	var cmd tea.Cmd
	if m.total > 0 {
		cmd = m.bar.SetPercent(float64(m.done+m.failed) / float64(m.total))
	}
	m.syncLogPane()
	return cmd
}

// recordLine 把一行输出同时写入全局日志与第 idx 个文件的日志；state 为空时不修改文件状态。
func (m *model) recordLine(idx int, line, state string, s lipgloss.Style) {
	m.appendLog(line, s)
	if idx < 0 || idx >= len(m.files) {
		return
	}
	it := &m.files[idx]
	if state != "" {
		it.state = state
	}
	it.logs = appendFileLog(it.logs, line, s)
}

// appendFileLog 向单个文件的日志缓冲追加一行（带样式），超过上限时丢弃旧行。
func appendFileLog(logs []string, line string, s lipgloss.Style) []string {
	logs = append(logs, s.Render(line))
	if len(logs) > maxLogLines {
		logs = logs[len(logs)-maxLogLines:]
	}
	return logs
}

// summaryText 生成任务面板“全部”视图的汇总文本：概要、字幕列表与全部命令。
func (m model) summaryText(cmds [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "已选择：%s，共生成 %d 条命令（%d 个视频）\n", displaySel(m.ops), len(cmds), len(cmds))
	if m.ops.AddSubtitle {
		suffixes := make([]string, 0, len(m.subs))
		for _, s := range m.subs {
			suffixes = append(suffixes, s.Suffix)
		}
		fmt.Fprintf(&b, "字幕（%d 条）：.%s\n", len(m.subs), strings.Join(suffixes, " / ."))
	}
	b.WriteString("按 x 开始执行；↑/↓ 切换文件查看对应命令。\n\n")
	for i, c := range cmds {
		fmt.Fprintf(&b, "[%d/%d] %s\n\n", i+1, len(cmds), ffmpeg.FormatCommand(c))
	}
	return b.String()
}

// buildFileItems 从 Plan 生成的命令列表提取源文件与其命令。
// 每条命令固定以 "-i <源文件>" 开头、以输出路径结尾（见 internal/ffmpeg 各批处理生成逻辑）。
func buildFileItems(cmds [][]string) []fileItem {
	items := make([]fileItem, 0, len(cmds))
	for _, args := range cmds {
		it := fileItem{cmd: ffmpeg.FormatCommand(args)}
		if len(args) > 0 {
			it.output = args[len(args)-1]
		}
		if len(args) >= 2 && args[0] == "-i" {
			it.input = args[1]
		}
		items = append(items, it)
	}
	return items
}

func (m *model) appendLog(line string, s lipgloss.Style) {
	m.logs = appendFileLog(m.logs, line, s)
}

// ---------- 实时命令预览（防抖） ----------

// schedulePlan 记录需要重新生成命令，并在 delay 后触发一次生成；
// 配置密集变化时只有最后一次生效，避免反复扫描目录。
func (m *model) schedulePlan(delay time.Duration) tea.Cmd {
	if m.running {
		// 执行中锁定配置，结束后如配置被改动可在停止时重新生成。
		return nil
	}
	m.planGenerate = true
	m.planLoading = true
	m.planSeq++
	seq := m.planSeq
	return tea.Tick(delay, func(time.Time) tea.Msg { return planDebounceMsg{seq: seq} })
}

// runPlan 根据当前配置生成命令列表。配置不完整/非法时把原因展示在任务面板，不报询问弹窗。
func (m *model) runPlan() tea.Cmd {
	m.planGenerate = false
	if m.ops.String() == "无" {
		m.planErr = fmt.Errorf("请在面板 1 勾选至少一项操作")
		m.planLoading = false
		return nil
	}
	opt, err := m.buildOption()
	if err != nil {
		m.planErr = err
		m.planLoading = false
		return nil
	}
	if m.ops.AddSubtitle && len(m.subs) == 0 {
		m.planErr = fmt.Errorf("已勾选添加字幕，但字幕列表为空（在面板 3 按 n 新增）")
		m.planLoading = false
		return nil
	}
	m.opt = opt
	if !m.previewDone {
		m.previewSummary = ""
	}
	return tea.Batch(planCmd(m.opt, m.ops, m.planSeq), m.spin.Tick)
}

// ---------- 左右分栏尺寸与视口同步 ----------

// bodyH 返回主体区可用行数（去掉顶栏 1、底栏 2）。
func (m model) bodyH() int {
	return max(m.height-3, 3)
}

// sideW 左侧面板栏宽度，rightW 右侧内容区宽度。
func (m *model) resizePanes() {
	sideW := 34
	// 保证右侧内容区至少 46 列，窄终端时压缩左侧。
	if right := m.width - sideW - 2; right < 46 {
		sideW = max(24, m.width-48)
	}
	m.leftW = min(sideW, max(12, m.width-14))
	m.rightW = max(m.width-m.leftW-2, 12)

	m.vp.SetWidth(max(m.rightW-2, 10))
	m.logVp.SetWidth(max(m.rightW-2, 10))
	m.bar.SetWidth(max(m.rightW-4, 10))
}

// panelHeights 返回左侧五个面板的内容行数分配（面板边框每格占 2 行）。
// 未聚焦的面板折叠为 2 行（光标行随窗口滚动保持可见），聚焦面板占据剩余空间。
func (m model) panelHeights() [panelCount]int {
	content := max(m.bodyH()-int(panelCount)*2, 0)
	hs := [panelCount]int{}
	h := content
	for i := range hs {
		if panel(i) == m.focus {
			continue // 聚焦面板取剩余全部空间，最后统一赋值
		}
		hs[i] = min(content, 2)
		h -= hs[i]
	}
	hs[m.focus] = max(h, 1)
	return hs
}

// moveFilePane 移动任务面板左侧文件列表光标（0 为“全部”汇总项）并刷新右侧内容。
// 执行阶段的手动切换会停止自动跟随。
func (m *model) moveFilePane(delta int) {
	if len(m.files) == 0 {
		return
	}
	next := min(len(m.files), max(0, m.fileCursor+delta))
	if next == m.fileCursor {
		return
	}
	m.fileCursor = next
	if m.running {
		m.fileFollow = false
	}
	m.syncPreview()
	m.vp.GotoTop()
	m.syncLogPane()
}

// previewContent 返回右侧预览视口的原始文本：选中“全部”时为汇总列表，否则为该文件命令。
func (m model) previewContent() string {
	if idx := m.fileCursor - 1; idx >= 0 && idx < len(m.files) {
		it := m.files[idx]
		var b strings.Builder
		fmt.Fprintf(&b, "输出: %s\n\n", it.output)
		b.WriteString(it.cmd)
		return b.String()
	}
	return m.previewSummary
}

// syncPreview 按左侧选中项刷新右侧预览内容（长命令按内容区宽度折行）。
func (m *model) syncPreview() {
	if !m.previewDone || m.planErr != nil {
		return
	}
	if m.running {
		// 执行时段的右侧视口由日志接管。
		return
	}
	m.vp.SetContent(wrapText(m.previewContent(), m.rightW-2))
}

// syncLogPane 按左侧选中项刷新右侧日志视图并贴底，跟随最新输出。
func (m *model) syncLogPane() {
	var lines []string
	switch idx := m.fileCursor - 1; {
	case len(m.files) == 0:
	case idx < 0:
		lines = m.logs
	case idx < len(m.files):
		lines = m.files[idx].logs
	}
	m.logVp.SetContent(wrapText(strings.Join(lines, "\n"), m.rightW-2))
	m.logVp.GotoBottom()
}

// quitCleanup 退出前取消仍在执行的批处理。
func (m model) quitCleanup() {
	if m.cancel != nil {
		m.cancel()
	}
}

// ---------- 表单 ----------

// rebuildFields 按当前操作组合重建主表单。保留同名字段的已填值，
// 高级参数字段优先使用当前选中的预设参数。
func (m *model) rebuildFields() {
	defs := fieldsFor(m.ops)
	old := make(map[string]string, len(m.fields))
	for _, f := range m.fields {
		old[f.def.key] = f.input.Value()
	}

	base := ffmpeg.DefaultOption()
	m.fields = make([]field, 0, len(defs))
	for _, def := range defs {
		nf := newField(def)
		nf.applyDefaults(&base)
		switch def.key {
		case "advance":
			if m.manualAdvance != "" {
				nf.input.SetValue(m.manualAdvance)
			}
		case "fonts_path":
			nf.input.Placeholder = "字体文件所在文件夹"
		}
		if v, ok := old[def.key]; ok && v != "" {
			nf.input.SetValue(v)
		}
		m.fields = append(m.fields, nf)
	}
	m.focusNo = min(m.focusNo, max(len(m.fields)-1, 0))
}

// focusFormNo 聚焦 fields[focusNo] 并失焦其余字段，返回聚焦输入框的光标闪烁命令。
func (m *model) focusFormNo(no int) tea.Cmd {
	m.focusNo = clamp(no, 0, len(m.fields)-1)
	return focusField(m.fields, m.focusNo)
}

// blurForm 退出表单输入状态：使所有输入框失焦（隐藏光标），保留已填内容。
func (m *model) blurForm() {
	if !m.editing {
		return
	}
	m.editing = false
	for i := range m.fields {
		m.fields[i].input.Blur()
	}
}

// beginFormEdit 进入面板 [4] 的输入状态并聚焦当前字段。
func (m *model) beginFormEdit() tea.Cmd {
	m.editing = true
	return m.focusFormNo(m.focusNo)
}

func clamp(v, lo, hi int) int { return min(hi, max(lo, v)) }
