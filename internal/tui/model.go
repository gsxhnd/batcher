package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/task"
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
	selected func(command.Selection) bool
	toggle   func(*command.Selection)
}

var features = []feature{
	{
		label:    "视频转码",
		desc:     "按“自定义参数 + 输出后缀”转码；参数在本面板选择/保存常用预设",
		selected: func(s command.Selection) bool { return s.Convert },
		toggle:   func(s *command.Selection) { s.Convert = !s.Convert },
	},
	{
		label:    "添加字幕",
		desc:     "把源目录下匹配视频名的字幕文件封装进视频，支持多条字幕",
		selected: func(s command.Selection) bool { return s.AddSubtitle },
		toggle:   func(s *command.Selection) { s.AddSubtitle = !s.AddSubtitle },
	},
	{
		label:    "添加字体",
		desc:     "把字体文件夹中的字体文件内嵌到视频（mkv 附带流）",
		selected: func(s command.Selection) bool { return s.AddFonts },
		toggle:   func(s *command.Selection) { s.AddFonts = !s.AddFonts },
	},
}

// 文件任务的执行状态（左侧列表标记用）。
const (
	filePending  = ""         // 待执行
	fileRunning  = "running"  // 执行中
	filePaused   = "paused"   // 主任务暂停，等待重试/跳过/中止
	fileDone     = "done"     // 完成
	fileFailed   = "failed"   // 失败
	fileCanceled = "canceled" // 已取消
	fileSkipped  = "skipped"  // 已跳过
)

// fileItem 描述任务面板文件列表中的一个源文件任务。
type fileItem struct {
	input    string       // 源文件路径
	output   string       // 输出文件路径
	cmd      string       // 已格式化的完整 ffmpeg 命令
	state    string       // filePending / fileRunning / fileDone / fileFailed / fileCanceled
	logs     []string     // 该任务执行时的 ffmpeg 输出（按行，带样式）
	attempts []attemptLog // 按次数分开的输出，左侧列表挂在子任务下面
}

// attemptLog 是子任务的一次执行。
type attemptLog struct {
	n      int
	status string
	lines  []string
}

// rosterRow 是任务面板左侧列表的一行：主任务、子任务，或子任务下的一次执行。
type rosterRow struct {
	batchID string // 所属主任务 ID
	itemID  string // 子任务 ID；空表示主任务行
	attempt int    // 第几次；0 表示不是次数行
	title   string // 展示名
	state   string // filePending 等
	cmd     string // 子任务和次数行带完整命令；主任务行为空
}

type model struct {
	width, height int
	ctx           context.Context // 程序生命周期；退出时取消所有后台工作

	focus panel // 当前聚焦的面板（1-5）

	// 操作组合勾选
	ops        command.Selection
	featCursor int
	featErr    string

	// 转码参数预设：内置（只读）在前，用户自定义在后，最后是“手动输入”选项
	builtins      []presets.Builtin
	presets       []presets.Preset
	presetCursor  int
	manualAdvance string
	statusMsg     string // 界面上的临时提示（保存成功等）

	// 字幕列表
	subs      []command.SubtitleOption
	subCursor int

	// 主参数表单（进入编辑态后全局快捷键失效）
	fields      []field
	fieldValues map[string]string // 保留暂时隐藏字段以及显式清空的值
	focusNo     int               // 表单内的字段下标
	editing     bool              // 是否处于表单输入状态

	// 弹窗（预设/字幕编辑、删除确认），非空时按键全部交给弹窗
	modal *modal

	// 实时命令预览：配置变化后防抖生成，planSeq 丢弃过期结果
	planSeq      int
	planLoading  bool
	previewDone  bool
	planErr      error
	planGenerate bool // 防抖 tick 到期后是否需要重新生成
	planCancel   context.CancelFunc
	plan         *command.BatchPlan

	// 命令预览/执行日志共用的右侧视口
	vp             viewport.Model
	previewSummary string // Plan 汇总文本（全部命令列表）
	spin           spinner.Model

	// 任务面板：左侧文件列表，右侧命令/日志
	files      []fileItem
	fileCursor int  // 光标：0 = 全部汇总，1.. = 第 i 个源文件
	fileFollow bool // 执行时右侧自动跟随正在执行的文件（手动切换后停止）
	// outputFocus 为真时，上下键滚动当前这一次的输出，而不是移动左侧列表。
	// 只有 esc 退出这一层。
	outputFocus bool
	runIdx      int // 正在执行的文件下标（files 内），-1 表示当前无执行中任务
	leftW       int // 左侧面板栏宽度
	rightW      int // 右侧内容区宽度

	// 执行
	running             bool
	total               int
	done                int
	failed              int
	canceled            int
	skipped             int
	current             string
	logs                []string
	logVp               viewport.Model
	bar                 progress.Model
	startAt             time.Time     // 开始执行时间（用于显示耗时）
	elapsed             time.Duration // 批处理总耗时（结束时记录）
	logFollow           bool          // 日志是否贴底跟随最新输出（用户向上滚动后暂停）
	cancel              context.CancelFunc
	evCh                chan tea.Msg // 状态、日志与结束结果有序传递
	eventing            bool
	runErr              error
	runID               int
	logDirty            bool
	logRefreshScheduled bool

	// 任务队列
	activeBatch    string             // 正在执行或暂停中的批次 ID
	pausedBatch    string             // 非空时队列处于暂停等待决策（批次 ID）
	pausedAt       time.Time          // 非零时处于暂停等待决策，计时冻结到该时刻
	pendingPlan    *command.BatchPlan // 运行期间重新生成的计划，批次结束后应用
	pendingPlanErr error
	queue          *task.Queue

	// 队列视图：主任务 + 可展开的子任务 + 子任务下可展开的每一次。
	roster           []rosterRow
	expanded         map[string]bool // 主任务 ID → 是否展开子任务（未记录时：运行/暂停的主任务默认展开）
	itemExpanded     map[string]bool // 主任务 ID + 子任务 ID → 是否展开次数
	rosterPinBatch   string          // 光标钉住的主任务
	rosterPinItem    string          // 光标钉住的子任务（空 = 钉在主任务行）
	rosterPinAttempt int             // 光标钉住的次数；0 = 钉在主任务或子任务行
	filesBatch       string          // files/logs 所属的主任务 ID
}

func newModel() model {
	m := model{
		ctx:   context.Background(),
		focus: panelOps,
		bar: progress.New(
			progress.WithColors(lipgloss.Color("81"), lipgloss.Color("114")),
			progress.WithScaled(true),
		),
		spin: spinner.New(
			spinner.WithSpinner(spinner.Dot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("81"))),
		),
		builtins: presets.Builtins(),
		runIdx:   -1,
	}
	user, err := presets.Load()
	if err != nil {
		m.statusMsg = "× 读取自定义预设失败: " + err.Error()
	}
	m.presets = user
	m.bar.EmptyColor = lipgloss.Color("238")
	m.bar.PercentageStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	m.rebuildFields()
	return m
}

// initQueue 在拥有最终 ctx 与事件通道后启动队列消费者；事件写入缓冲 channel，
// 由 listenEvents 转发进 Bubble Tea 主循环。
func (m *model) initQueue(ch chan tea.Msg) {
	m.evCh = ch
	ctx := m.ctx // 捕获一次，回调不读 model 字段，避免与 Update 循环的值拷贝竞态
	m.queue = task.NewQueue(ctx, func(e task.QueueEvent) {
		select {
		case ch <- queueEventMsg{evt: e}:
		case <-ctx.Done():
		}
	})
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

// panelEnabled 判断面板当前是否可用：未勾选对应操作的面板变灰不可进入。
// 执行期间不锁定面板：允许随时修改配置并入队后续任务。
func (m model) panelEnabled(p panel) bool {
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
		// 离开任务面板时取消自动跟随，并退出输出查看。
		m.fileFollow = false
		m.outputFocus = false
	}
	m.focus = p
	m.refreshRunHeights()
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
	// 从程序启动起就常驻监听队列事件通道；只有 queueEventMsg 消费后重挂下一轮。
	if m.evCh == nil {
		return nil
	}
	return listenEvents(m.ctx, m.evCh)
}

// ---------- 消息 ----------

type planMsg struct {
	seq  int
	plan *command.BatchPlan
	err  error
}

// planDebounceMsg 是配置变化后的防抖时钟：到期且序号仍是最新时才重新生成命令。
type planDebounceMsg struct{ seq int }

type eventMsg struct {
	runID int
	event ffmpeg.Event
}

type doneMsg struct {
	runID int
	err   error
}

type logRefreshMsg struct{ runID int }

type tickMsg struct{ runID int }

// queueEventMsg 把 task.QueueEvent 安全地送进 Bubble Tea 主循环。
type queueEventMsg struct{ evt task.QueueEvent }

// ---------- 命令 ----------

// maxLogLines 限制日志缓冲行数，避免长任务占用过多内存。
const maxLogLines = 1000

// planDebounceDelay 配置变化后等待多少毫秒再重新生成命令列表。
const planDebounceDelay = 300 * time.Millisecond

func planCmd(ctx context.Context, opt *command.VideoBatchOption, sel command.Selection, seq int) tea.Cmd {
	return func() tea.Msg {
		plan, err := command.BuildPlan(ctx, opt, sel)
		return planMsg{seq: seq, plan: plan, err: err}
	}
}

func listenEvents(ctx context.Context, ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-ch:
			return msg
		}
	}
}

// tickCmd 每秒触发一次重绘，用于任务面板显示实时耗时。
func tickCmd(runID int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{runID: runID} })
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
		return m, m.applyPlan(msg.err, msg.plan)

	case queueEventMsg:
		// 无条件重挂监听：队列事件流不依赖执行状态（此前空闲或批次收尾时监听断链，
		// 导致入队后界面永远看不到执行）。不变式：只有 Init 与本分支挂 listenEvents，
		// 其他消息一律不得挂，保证任意时刻至多一个监听器在消费队列事件。
		if m.evCh == nil {
			return m, m.handleQueueEvent(msg.evt)
		}
		return m, tea.Batch(m.handleQueueEvent(msg.evt), listenEvents(m.ctx, m.evCh))

	case planDebounceMsg:
		if msg.seq == m.planSeq && m.planGenerate {
			return m, m.runPlan()
		}
		return m, nil

	case eventMsg:
		if !m.running || msg.runID != m.runID {
			return m, nil
		}
		return m, m.handleEvent(msg.event)

	case doneMsg:
		if !m.running || msg.runID != m.runID {
			return m, nil
		}
		return m, m.handleDone(msg.err)

	case tickMsg:
		if m.eventing && msg.runID == m.runID {
			return m, tickCmd(m.runID)
		}
		return m, nil

	case logRefreshMsg:
		if msg.runID == m.runID {
			m.logRefreshScheduled = false
			if m.logDirty {
				m.syncLogPane()
				m.logDirty = false
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.planLoading {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.MouseWheelMsg:
		return m, m.handleMouse(msg)
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.String() == "ctrl+c" {
			m.quitCleanup()
			return m, tea.Quit
		}
		return m, m.handleKey(key)
	}
	if m.modal != nil {
		return m, m.updateModal(msg)
	}
	if m.editing {
		return m, m.updateFormEdit(msg)
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

	if m.outputFocus {
		switch s {
		case "esc":
			m.leaveOutput()
			return nil
		case "enter", "tab", "shift+tab":
			return nil
		}
	}

	if m.running {
		// 运行期间允许查看其他面板与编辑配置；q/esc 取消当前批次，队列继续处理后续任务。
		switch s {
		case "q", "esc":
			if s == "esc" && m.outputFocus {
				m.leaveOutput()
				return nil
			}
			if m.pausedBatch != "" {
				// 暂停中等待决策：q/esc 视作中止当前主任务。
				m.queue.Decide(task.DecisionAbort)
				m.statusMsg = "已发送中止指令"
				return nil
			}
			if m.queue != nil {
				m.queue.CancelCurrent()
				m.statusMsg = "已发送取消请求，等待正在执行的任务结束…"
			}
			return nil
		case "1", "2", "3", "4", "5", "tab", "shift+tab", "shift+down", "shift+up", "down", "up", "j", "k", "?", "x":
			// 面板切换、焦点移动、帮助与入队（x 运行中被守卫拦截）交给下方全局分发。
		default:
			return m.updateRunPanel(key)
		}
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
		if m.focus == panelRun && m.queueViewActive() {
			m.statusMsg = "任务列表中按 x 不会新建主任务"
			return nil
		}
		if m.focus != panelRun {
			// 进入任务列表只展示参数，不入队。入队要在任务面板里再按一次 x。
			m.setFocus(panelRun)
			m.statusMsg = "已进入任务列表。按 x 弹出确认后才会入队"
			return nil
		}
		return m.tryStartRun()
	case "enter":
		// 进入任务列表只查看预览或操作已有任务，不入队。入队只走 x。
		if m.focus == panelRun {
			return m.updateRunPanel(key)
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

// handleMouse 处理鼠标滚轮事件：任务面板滚动命令/日志，帮助弹窗滚动帮助文本，其余面板忽略。
func (m *model) handleMouse(msg tea.MouseWheelMsg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case m.modal != nil && m.modal.kind == modalHelp:
		m.modal.vp, cmd = m.modal.vp.Update(msg)
	case m.focus == panelRun && (m.running || m.queueViewActive()):
		m.logVp, cmd = m.logVp.Update(msg)
		m.logFollow = m.logVp.AtBottom()
	case m.focus == panelRun && m.previewDone && m.planErr == nil:
		m.vp, cmd = m.vp.Update(msg)
	}
	return cmd
}

// handleEvent 将 ffmpeg 事件写入全局日志与对应文件的日志，并更新进度条。
// 事件 Index 与 files 顺序一致，因此可直接定位到源文件条目。
func (m *model) handleEvent(e ffmpeg.Event) tea.Cmd {
	switch e.Type {
	case ffmpeg.EventProgress:
		return nil // 进度事件不写日志，避免刷屏
	case ffmpeg.EventBatchStart:
		m.total = e.Total
		m.appendLog(fmt.Sprintf("开始执行批处理，共 %d 个任务", e.Total), logLineInfoStyle)
	case ffmpeg.EventCommandStart:
		m.current = e.Command
		m.runIdx = e.Index
		m.beginAttempt(e.Index)
		n := m.attemptNumber(e.Index)
		line := fmt.Sprintf("▶ 第 %d 次开始", n)
		m.recordLine(e.Index, line, fileRunning, logLineInfoStyle)
		if m.fileFollow {
			m.fileCursor = e.Index + 1
		}
	case ffmpeg.EventLog:
		m.recordLine(e.Index, "    "+e.Message, "", logLineDefaultStyle)
	case ffmpeg.EventCommandDone:
		m.recordLine(e.Index, fmt.Sprintf("✔ [%d/%d] 完成", e.Index+1, e.Total), fileDone, logLineDoneStyle)
	case ffmpeg.EventCommandFailed:
		m.recordLine(e.Index, fmt.Sprintf("✘ [%d/%d] 失败: %s", e.Index+1, e.Total, e.Message), fileFailed, logLineFailedStyle)
	case ffmpeg.EventCommandCanceled:
		m.recordLine(e.Index, fmt.Sprintf("− [%d/%d] 已取消", e.Index+1, e.Total), fileCanceled, logLineInfoStyle)
	}
	if e.Type == ffmpeg.EventLog {
		m.logDirty = true
		if m.logRefreshScheduled {
			return nil
		}
		m.logRefreshScheduled = true
		runID := m.runID
		return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return logRefreshMsg{runID: runID} })
	}
	if e.Type == ffmpeg.EventCommandDone || e.Type == ffmpeg.EventCommandFailed || e.Type == ffmpeg.EventCommandCanceled {
		if e.Index == m.runIdx {
			m.current, m.runIdx = "", -1
			for i := len(m.files) - 1; i >= 0; i-- {
				if m.files[i].state == fileRunning {
					m.current, m.runIdx = m.files[i].cmd, i
					if m.fileFollow {
						m.fileCursor = i + 1
					}
					break
				}
			}
		}
	}
	// 当前命令变化时置顶固定区域行数会变，日志视口高度需同步重算。
	if m.running {
		m.refreshRunHeights()
	}
	m.syncLogPane()
	m.logDirty = false
	return nil
}

// recordLine 把一行输出同时写入全局日志与第 idx 个文件的日志；state 为空时不修改文件状态。
func (m *model) recordLine(idx int, line, state string, s lipgloss.Style) {
	m.appendLog(line, s)
	if idx < 0 || idx >= len(m.files) {
		return
	}
	it := &m.files[idx]
	if state != "" && state != it.state {
		// 同一子任务重试时先离开失败态，避免失败和完成各算一次进度。
		m.adjustCount(it.state, -1)
		m.adjustCount(state, 1)
		it.state = state
	}
	rendered := s.Render(line)
	it.logs = appendFileLog(it.logs, line, s)
	if n := len(it.attempts); n > 0 {
		a := &it.attempts[n-1]
		if state != "" {
			a.status = state
		}
		a.lines = append(a.lines, rendered)
		if len(a.lines) > maxLogLines {
			a.lines = a.lines[len(a.lines)-maxLogLines:]
		}
	}
}

// adjustCount 按文件状态增减进度计数。空状态与执行中不计入结果。
func (m *model) adjustCount(state string, delta int) {
	switch state {
	case fileDone:
		m.done += delta
	case fileFailed:
		m.failed += delta
	case fileCanceled:
		m.canceled += delta
	case fileSkipped:
		m.skipped += delta
	}
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
			tok := "." + s.Suffix
			if s.Default {
				tok = "(默认)" + tok
			}
			suffixes = append(suffixes, tok)
		}
		fmt.Fprintf(&b, "字幕（%d 条）：%s\n", len(m.subs), strings.Join(suffixes, " / "))
	}
	b.WriteString("尚未加入队列。在任务面板确认参数后按 x 入队；↑/↓ 切换文件查看对应命令。\n\n")
	for i, c := range cmds {
		fmt.Fprintf(&b, "[%d/%d] %s\n\n", i+1, len(cmds), command.FormatCommand(c))
	}
	return b.String()
}

// buildFileItems 直接使用计划中的结构化任务，不依赖 ffmpeg 参数的位置。
func buildFileItems(jobs []command.Job) []fileItem {
	items := make([]fileItem, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, fileItem{input: job.Input, output: job.Output, cmd: command.FormatCommand(job.Args)})
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
	// 运行期间也允许重新生成：结果暂存，批次结束后应用。
	// 重新生成即回到预览态：清除队列视图。
	m.roster = nil
	m.rosterPinBatch, m.rosterPinItem, m.rosterPinAttempt = "", "", 0
	m.filesBatch = ""
	if m.planCancel != nil {
		m.planCancel()
		m.planCancel = nil
	}
	m.plan = nil
	m.planGenerate = true
	m.planLoading = true
	m.planSeq++
	seq := m.planSeq
	return tea.Tick(delay, func(time.Time) tea.Msg { return planDebounceMsg{seq: seq} })
}

// runPlan 根据当前配置生成命令列表。配置不完整/非法时把原因展示在任务面板，不报询问弹窗。
func (m *model) runPlan() tea.Cmd {
	m.planGenerate = false
	if !m.ops.Any() {
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
	ctx, cancel := context.WithCancel(m.ctx)
	m.planCancel = cancel
	if !m.previewDone {
		m.previewSummary = ""
	}
	return tea.Batch(planCmd(ctx, opt, m.ops, m.planSeq), m.spin.Tick)
}

// ---------- 左右分栏尺寸与视口同步 ----------

// bodyH 返回主体区可用行数（去掉顶栏 1、底栏 2）。
func (m model) bodyH() int {
	return max(m.height-3, 3)
}

// sideW 左侧面板栏宽度，rightW 右侧内容区宽度。
func (m *model) resizePanes() {
	const gutter = 1
	sideW := 34
	// 保证右侧内容区至少 46 列，窄终端时压缩左侧。左右之间留一列间隔。
	if right := m.width - sideW - gutter; right < 46 {
		sideW = max(24, m.width-46-gutter)
	}
	m.leftW = min(sideW, max(12, m.width-14))
	m.rightW = max(m.width-m.leftW-gutter, 12)
	if m.width < narrowWidth {
		m.leftW, m.rightW = m.width, m.width
	}

	m.vp.SetWidth(max(m.rightW-2, 10))
	m.logVp.SetWidth(max(m.rightW-2, 10))
	m.vp.SetHeight(max(m.bodyH()-2, 3))
	m.logVp.SetHeight(max(m.bodyH()-2, 3))
	m.bar.SetWidth(max(m.rightW-4, 10))
	// 视口实际高度按视图构成重算（预览头部/置顶命令/进度条占用的行数）。
	m.refreshRunHeights()
}

// previewHeadH 预览态命令区中位于视口之前的行数，runBody 的上方信息须与此保持一致。
func (m model) previewHeadH() int {
	if !m.previewDone || m.planErr != nil {
		return 1 // 加载提示或失败说明各占 1-2 行，视口未显示
	}
	if m.total == 0 {
		return 0 // 首次预览直接渲染视口
	}
	h := 3 // 上次执行结果行 + 提示行 + "\n\n" 空行
	if m.runErr != nil {
		h++
	}
	if m.statusMsg != "" {
		h += 2 // statusSuffix 追加在视口之后的提示块
	}
	return h
}

// refreshRunHeights 按当前视图构成设置预览/日志视口高度。
// 主任务把剩余高度留给子任务状态，子任务把剩余高度留给输出。
func (m *model) refreshRunHeights() {
	inner := max(m.bodyH()-2, 2)
	if h := m.narrowFilePaneH(); h > 0 {
		inner = max(inner-h-1, 2)
	}
	if m.running || m.queueViewActive() {
		m.logVp.SetHeight(max(inner-m.taskChromeH(), 2))
		return
	}
	m.vp.SetHeight(max(inner-m.previewHeadH(), 1))
}

// narrowFilePaneH 是窄终端任务列表的实际行数，须给命令与日志保留空间。
func (m model) narrowFilePaneH() int {
	rows := len(m.files)
	if len(m.roster) > 0 {
		rows = len(m.roster)
	}
	if m.width >= narrowWidth || m.focus != panelRun || rows == 0 {
		return 0
	}
	return min(rows+1, max(1, min(6, m.bodyH()/4)))
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
		hs[i] = min(max(h-1, 0), 2)
		h -= hs[i]
	}
	hs[m.focus] = h
	return hs
}

// moveFilePane 移动任务面板左侧列表光标并刷新右侧内容；手动切换会停止自动跟随。
// 空闲时为预览文件列表（0 为“全部”），队列视图激活后为批次/子任务行。
func (m *model) moveFilePane(delta int) {
	hi := len(m.files) // 预览列表含“全部”项，可到 len(files)
	if len(m.roster) > 0 {
		hi = len(m.roster) - 1 // 队列列表无“全部”项
	}
	if hi < 0 {
		return
	}
	next := min(hi, max(0, m.fileCursor+delta))
	if next == m.fileCursor {
		return
	}
	m.fileCursor = next
	if m.running {
		m.fileFollow = false
	}
	m.pinRosterCursor()
	if m.width > 0 && (m.running || len(m.roster) > 0) {
		m.refreshRunHeights()
	}
	m.syncPreview()
	m.vp.GotoTop()
	m.syncLogPane()
}

// pinRosterCursor 记录光标所在行，供事件重建列表后恢复位置。
func (m *model) pinRosterCursor() {
	if len(m.roster) == 0 {
		return
	}
	if idx := clamp(m.fileCursor, 0, len(m.roster)-1); idx < len(m.roster) {
		m.rosterPinBatch, m.rosterPinItem, m.rosterPinAttempt = m.roster[idx].batchID, m.roster[idx].itemID, m.roster[idx].attempt
	}
	m.fileCursor = clamp(m.fileCursor, 0, max(len(m.roster)-1, 0))
}

// toggleRosterExpand 展开或折叠光标所在的主任务。
func (m *model) toggleRosterExpand() {
	if len(m.roster) == 0 {
		return
	}
	row := m.roster[clamp(m.fileCursor, 0, len(m.roster)-1)]
	if row.itemID != "" {
		return
	}
	if m.expanded == nil {
		m.expanded = make(map[string]bool)
	}
	open := false
	if m.fileCursor+1 < len(m.roster) {
		next := m.roster[m.fileCursor+1]
		open = next.batchID == row.batchID && next.itemID != ""
	}
	m.expanded[row.batchID] = !open
	m.rosterPinAttempt = 0
	m.pinRosterCursor()
	m.syncRoster()
}

// toggleItemAttempts 展开或折叠光标所在子任务下面的每一次。
func (m *model) toggleItemAttempts(row rosterRow) {
	if m.itemExpanded == nil {
		m.itemExpanded = make(map[string]bool)
	}
	open := false
	if m.fileCursor+1 < len(m.roster) {
		next := m.roster[m.fileCursor+1]
		open = next.batchID == row.batchID && next.itemID == row.itemID && next.attempt > 0
	}
	m.itemExpanded[row.batchID+"\n"+row.itemID] = !open
	m.rosterPinAttempt = 0
	m.pinRosterCursor()
	m.syncRoster()
}

// leaveOutput 退出子任务输出查看，上下键恢复为移动左侧列表。
func (m *model) leaveOutput() {
	m.outputFocus = false
	m.statusMsg = ""
}

// openOutput 进入当前子任务的输出查看。
func (m *model) openOutput() {
	m.outputFocus = true
	m.fileFollow = false
	m.logFollow = true
	m.pinRosterCursor()
	m.syncLogPane()
	m.logVp.GotoBottom()
	m.statusMsg = "↑/↓ 滚动输出，esc 返回"
}

// scrollOutput 在输出查看中按行滚动，离开底部后暂停跟随新输出。
func (m *model) scrollOutput(delta int) {
	if delta < 0 {
		m.logVp.ScrollUp(-delta)
	} else if delta > 0 {
		m.logVp.ScrollDown(delta)
	}
	m.logFollow = m.logVp.AtBottom()
}
func (m *model) followRunningItem() {
	m.outputFocus = false
	if len(m.roster) == 0 {
		if m.runIdx >= 0 && m.runIdx < len(m.files) && m.files[m.runIdx].state == fileRunning {
			m.fileFollow = true
			m.fileCursor = m.runIdx + 1
			m.logFollow = true
			m.syncLogPane()
		}
		return
	}
	for i, row := range m.roster {
		if row.state == fileRunning && row.attempt > 0 {
			m.fileCursor = i
			m.rosterPinBatch, m.rosterPinItem, m.rosterPinAttempt = row.batchID, row.itemID, row.attempt
			m.fileFollow = true
			m.logFollow = true
			m.syncLogPane()
			return
		}
	}
	for i, row := range m.roster {
		if row.state == fileRunning && row.itemID != "" {
			m.fileCursor = i
			m.rosterPinBatch, m.rosterPinItem, m.rosterPinAttempt = row.batchID, row.itemID, 0
			m.fileFollow = true
			m.logFollow = true
			m.syncLogPane()
			return
		}
	}
}

// queueViewActive 表示任务面板右侧当前展示队列视图（批次+子任务）
// 而不是命令预览；从入队到用户重新生成配置前保持为真。
func (m model) queueViewActive() bool {
	return len(m.roster) > 0
}

// previewContent 返回右侧预览视口的原始文本：选中“全部”时为汇总列表，否则为该文件命令。
func (m model) previewContent() string {
	if idx := m.fileCursor - 1; idx >= 0 && idx < len(m.files) {
		it := m.files[idx]
		var b strings.Builder
		fmt.Fprintf(&b, "输出: %s\n\n", it.output)
		b.WriteString(it.cmd)
		if len(it.logs) > 0 {
			b.WriteString("\n\n执行日志\n")
			b.WriteString(strings.Join(it.logs, "\n"))
		}
		return b.String()
	}
	for _, item := range m.files {
		if len(item.logs) > 0 {
			return m.previewSummary + "\n执行日志\n" + strings.Join(m.logs, "\n")
		}
	}
	return m.previewSummary
}

// syncPreview 按左侧选中项刷新右侧预览内容（长命令按内容区宽度折行）。
// 队列视图激活时不动预览视口：右侧由日志/批次摘要接管。
func (m *model) syncPreview() {
	if len(m.roster) > 0 || !m.previewDone || m.planErr != nil {
		return
	}
	if m.running {
		// 执行时段的右侧视口由日志接管。
		return
	}
	m.vp.SetContent(wrapText(m.previewContent(), m.rightW-2))
}

// syncLogPane 按左侧选中项刷新右侧日志视图。logFollow 为真时贴底跟随最新输出；
// 用户向上滚动查看历史后暂停跟随，回到底部再自动恢复。
// 队列视图激活且未运行时，批次/子任务行显示该子任务的错误尾部；批次行为摘要。
func (m *model) syncLogPane() {
	if len(m.roster) > 0 {
		m.syncRosterLogPane()
		return
	}
	var lines []string
	switch idx := m.fileCursor - 1; {
	case len(m.files) == 0:
	case idx < 0:
		lines = m.logs
	case idx < len(m.files):
		lines = m.files[idx].logs
	}
	m.setLogContent(wrapText(strings.Join(lines, "\n"), m.rightW-2))
}

// syncRosterLogPane 按批次列表光标刷新右侧输出区。
// 主任务行不放子任务日志；子任务行只放该任务自己的 ffmpeg 输出。
func (m *model) syncRosterLogPane() {
	if len(m.roster) == 0 {
		return
	}
	row := m.roster[clamp(m.fileCursor, 0, len(m.roster)-1)]
	var lines []string
	switch {
	case row.itemID == "":
		lines = m.batchItemLines(row.batchID)
	case row.attempt > 0:
		m.setLogContent(wrapText(strings.Join(m.oneAttemptLines(row), "\n"), m.rightW-2))
		return
	case row.batchID == m.filesBatch:
		lines = m.attemptListLines(row)
	default:
		lines = m.attemptListLines(row)
	}
	m.setLogContent(wrapText(strings.Join(lines, "\n"), m.rightW-2))
}

// setLogContent 写入输出视口。
func (m *model) setLogContent(content string) {
	m.logVp.SetContent(content)
	if m.logFollow {
		m.logVp.GotoBottom()
	}
}

// subtaskStatusLine 是子任务固定区的一行状态，不随输出滚动。
func (m model) subtaskStatusLine() string {
	text := "状态：" + m.focusedItemStateText()
	if n := m.focusedAttemptNumber(); n > 0 {
		text += fmt.Sprintf(" · 第 %d 次", n)
	}
	if m.queue != nil && len(m.roster) > 0 && m.fileCursor >= 0 && m.fileCursor < len(m.roster) {
		row := m.roster[m.fileCursor]
		if snap, ok := m.queue.BatchCopy(row.batchID); ok {
			for _, it := range snap.Items {
				if it.ID != row.itemID || it.Status != task.StatusFailed || it.Error == "" {
					continue
				}
				errLine := it.Error
				if i := strings.IndexByte(errLine, '\n'); i >= 0 {
					errLine = errLine[:i]
				}
				text += "  " + errLine
				break
			}
		}
	}
	return "  " + descStyle.Render(truncateText(text, max(m.rightW-4, 4))) + "\n"
}

// focusedItemStateText 返回当前子任务的中文状态。
func (m model) focusedItemStateText() string {
	if len(m.roster) > 0 && m.fileCursor >= 0 && m.fileCursor < len(m.roster) && m.roster[m.fileCursor].itemID != "" {
		return fileStateText(m.roster[m.fileCursor].state)
	}
	if m.runIdx >= 0 && m.runIdx < len(m.files) {
		return fileStateText(m.files[m.runIdx].state)
	}
	return "等待"
}

// focusedFileIndex 返回当前子任务在 files 中的下标。主任务行没有对应文件。
func (m model) focusedFileIndex() int {
	if len(m.roster) > 0 && m.fileCursor >= 0 && m.fileCursor < len(m.roster) {
		row := m.roster[m.fileCursor]
		if row.itemID != "" && row.batchID == m.filesBatch {
			if idx := itemIndexOf(row.itemID); idx >= 0 && idx < len(m.files) {
				return idx
			}
		}
		return -1
	}
	if m.runIdx >= 0 && m.runIdx < len(m.files) {
		return m.runIdx
	}
	return -1
}

// focusedAttemptNumber 返回光标这一次的序号；停在子任务上时返回最近一次。
func (m model) focusedAttemptNumber() int {
	if len(m.roster) > 0 && m.fileCursor >= 0 && m.fileCursor < len(m.roster) && m.roster[m.fileCursor].attempt > 0 {
		return m.roster[m.fileCursor].attempt
	}
	if idx := m.focusedFileIndex(); idx >= 0 {
		if n := m.attemptNumber(idx); n > 0 {
			return n
		}
	}
	if m.queue == nil || len(m.roster) == 0 || m.fileCursor < 0 || m.fileCursor >= len(m.roster) {
		return 0
	}
	row := m.roster[m.fileCursor]
	snap, ok := m.queue.BatchCopy(row.batchID)
	if !ok {
		return 0
	}
	for _, it := range snap.Items {
		if it.ID == row.itemID {
			return it.Attempts
		}
	}
	return 0
}

// attemptNumber 返回该文件最后一次执行的序号。
func (m model) attemptNumber(idx int) int {
	if idx < 0 || idx >= len(m.files) || len(m.files[idx].attempts) == 0 {
		return 0
	}
	return m.files[idx].attempts[len(m.files[idx].attempts)-1].n
}

// beginAttempt 为一次新的执行记一节输出。
func (m *model) beginAttempt(idx int) {
	if idx < 0 || idx >= len(m.files) {
		return
	}
	it := &m.files[idx]
	n := len(it.attempts) + 1
	if m.queue != nil && m.activeBatch != "" {
		if snap, ok := m.queue.BatchCopy(m.activeBatch); ok && idx < len(snap.Items) && snap.Items[idx].Attempts > n {
			n = snap.Items[idx].Attempts
		}
	}
	it.attempts = append(it.attempts, attemptLog{n: n, status: fileRunning})
	m.logFollow = true
}

// oneAttemptLines 返回左侧选中的这一次的输出。
func (m model) oneAttemptLines(row rosterRow) []string {
	if row.batchID == m.filesBatch {
		if idx := itemIndexOf(row.itemID); idx >= 0 && idx < len(m.files) {
			for _, a := range m.files[idx].attempts {
				if a.n != row.attempt {
					continue
				}
				if len(a.lines) == 0 {
					return []string{descStyle.Render("（无输出）")}
				}
				return a.lines
			}
		}
	}
	if m.queue != nil {
		if snap, ok := m.queue.BatchCopy(row.batchID); ok {
			for _, it := range snap.Items {
				if it.ID != row.itemID || row.attempt != it.Attempts {
					continue
				}
				if len(it.StderrTail) == 0 {
					return []string{descStyle.Render("（无输出）")}
				}
				return it.StderrTail
			}
		}
	}
	return []string{descStyle.Render("这次输出未保留")}
}

// attemptListLines 是子任务右侧的次数列表，不含 ffmpeg 输出。
func (m model) attemptListLines(row rosterRow) []string {
	snaps := m.attemptSnapshots(row.batchID, row.itemID)
	if len(snaps) == 0 {
		return []string{descStyle.Render("还没有执行")}
	}
	lines := make([]string, 0, len(snaps))
	for _, a := range snaps {
		lines = append(lines, descStyle.Render(fmt.Sprintf("第 %d 次  %s", a.n, fileStateText(a.status))))
	}
	return lines
}

// batchItemLines 是主任务下的子任务状态，不含 ffmpeg 输出。
func (m model) batchItemLines(batchID string) []string {
	if m.queue == nil {
		return []string{descStyle.Render("暂无子任务")}
	}
	snap, ok := m.queue.BatchCopy(batchID)
	if !ok || snap == nil {
		return []string{descStyle.Render("任务已不在队列中")}
	}
	var lines []string
	for _, it := range snap.Items {
		if it.Removed {
			continue
		}
		line := itemStateText(it.Status) + "  " + filepath.Base(it.Input)
		if it.Attempts > 0 {
			line += fmt.Sprintf("  第 %d 次", it.Attempts)
		}
		style := descStyle
		switch it.Status {
		case task.StatusFailed:
			style = logLineFailedStyle
		case task.StatusDone:
			style = logLineDoneStyle
		case task.StatusRunning:
			style = subtitleStyle
		}
		lines = append(lines, style.Render(line))
	}
	if len(lines) == 0 {
		lines = append(lines, descStyle.Render("没有子任务"))
	}
	return lines
}

// fileStateText 把界面里的文件状态转成中文。
func fileStateText(state string) string {
	switch state {
	case fileRunning:
		return "执行中"
	case filePaused:
		return "已暂停"
	case fileDone:
		return "已完成"
	case fileFailed:
		return "失败"
	case fileSkipped:
		return "已跳过"
	case fileCanceled:
		return "已取消"
	default:
		return "等待"
	}
}

// itemStateText 返回子任务状态的中文名。
func itemStateText(s task.Status) string {
	switch s {
	case task.StatusRunning:
		return "执行中"
	case task.StatusDone:
		return "已完成"
	case task.StatusFailed:
		return "失败"
	case task.StatusSkipped:
		return "已跳过"
	case task.StatusCanceled:
		return "已取消"
	case task.StatusInterrupted:
		return "已中断"
	default:
		return "等待"
	}
}

// historyLines 返回历史/等待中批次的只读内容（不保留完整日志，只展示状态与错误尾部）。
func (m *model) historyLines(row rosterRow) []string {
	b, ok := m.queue.BatchCopy(row.batchID)
	if !ok {
		return nil
	}
	var lines []string
	if row.itemID == "" {
		c := b.Counts()
		lines = append(lines,
			descStyle.Render(fmt.Sprintf("状态：%s（完成 %d / 跳过 %d / 失败 %d / 取消 %d，共 %d）",
				batchStateText(b.Status), c.Done, c.Skipped, c.Failed, c.Canceled, c.Total)),
			descStyle.Render("输入目录: "+b.InputDir),
			descStyle.Render("输出目录: "+b.OutputDir),
		)
		return lines
	}
	for _, it := range b.Items {
		if it.ID == row.itemID {
			lines = append(lines, descStyle.Render("输出: "+it.Output))
			if it.Error != "" {
				lines = append(lines, logLineFailedStyle.Render("错误: "+it.Error))
			}
			if it.Attempts > 0 {
				lines = append(lines, descStyle.Render(fmt.Sprintf("状态 %s · 第 %d 次", itemStateText(it.Status), it.Attempts)))
			} else {
				lines = append(lines, descStyle.Render("状态 "+itemStateText(it.Status)))
			}
			lines = append(lines, descStyle.Render("── 最后 50 行输出 ──"))
			for _, l := range it.StderrTail {
				lines = append(lines, l)
			}
			break
		}
	}
	return lines
}

// quitCleanup 退出前取消仍在执行的批处理并停止队列。
func (m model) quitCleanup() {
	if m.planCancel != nil {
		m.planCancel()
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.queue != nil {
		m.queue.Stop()
	}
}

// ---------- 表单 ----------

// rebuildFields 按当前操作组合重建主表单。保留同名字段的已填值，
// 高级参数字段优先使用当前选中的预设参数。
func (m *model) rebuildFields() {
	defs := fieldsFor(m.ops)
	if m.fieldValues == nil {
		m.fieldValues = make(map[string]string)
	}
	for _, f := range m.fields {
		m.fieldValues[f.def.key] = f.input.Value()
	}

	base := command.DefaultOption()
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
		if v, ok := m.fieldValues[def.key]; ok {
			nf.input.SetValue(v)
		}
		m.fields = append(m.fields, nf)
	}
	m.focusNo = min(m.focusNo, max(len(m.fields)-1, 0))
}

// ---------- 队列事件 ----------

// itemState 把子任务状态转成文件列表标记所用的常量。
func itemState(s task.Status) string {
	switch s {
	case task.StatusRunning:
		return fileRunning
	case task.StatusDone:
		return fileDone
	case task.StatusFailed:
		return fileFailed
	case task.StatusCanceled, task.StatusInterrupted:
		return fileCanceled
	case task.StatusSkipped:
		return fileSkipped
	default:
		return filePending
	}
}

// batchState 汇总主任务行使用的状态标记。
func batchState(s task.Status) string {
	switch s {
	case task.StatusRunning:
		return fileRunning
	case task.StatusPaused:
		return filePaused
	case task.StatusDone:
		return fileDone
	case task.StatusFailed:
		return fileFailed
	case task.StatusCanceled, task.StatusInterrupted:
		return fileCanceled
	default:
		return filePending
	}
}

// batchStateText 批次行的中文状态词。
func batchStateText(s task.Status) string {
	switch s {
	case task.StatusRunning:
		return "执行中"
	case task.StatusPaused:
		return "已暂停"
	case task.StatusDone:
		return "已完成"
	case task.StatusFailed:
		return "失败"
	case task.StatusCanceled:
		return "已取消"
	case task.StatusInterrupted:
		return "已中断"
	default:
		return "等待执行"
	}
}

// itemAttemptsOpen 表示这个子任务的次数是否展开。正在执行，或暂停在失败的子任务上时默认展开。
func (m *model) itemAttemptsOpen(b *task.Batch, it *task.Item) bool {
	if m.itemExpanded != nil {
		if v, ok := m.itemExpanded[b.ID+"\n"+it.ID]; ok {
			return v
		}
	}
	if it.Status == task.StatusRunning {
		return true
	}
	return b.Status == task.StatusPaused && it.Status == task.StatusFailed
}

// attemptSnap 是左侧次数行需要的序号和状态。
type attemptSnap struct {
	n      int
	status string
}

// attemptSnapshots 返回一个子任务的各次执行。当前批次优先用界面里记下的输出。
func (m model) attemptSnapshots(batchID, itemID string) []attemptSnap {
	if batchID == m.filesBatch {
		if idx := itemIndexOf(itemID); idx >= 0 && idx < len(m.files) && len(m.files[idx].attempts) > 0 {
			out := make([]attemptSnap, len(m.files[idx].attempts))
			for i, a := range m.files[idx].attempts {
				st := a.status
				if st == "" {
					st = filePending
				}
				out[i] = attemptSnap{n: a.n, status: st}
			}
			return out
		}
	}
	if m.queue == nil {
		return nil
	}
	snap, ok := m.queue.BatchCopy(batchID)
	if !ok {
		return nil
	}
	for _, it := range snap.Items {
		if it.ID != itemID || it.Attempts <= 0 {
			continue
		}
		out := make([]attemptSnap, it.Attempts)
		for i := 1; i <= it.Attempts; i++ {
			st := fileFailed
			if i == it.Attempts {
				st = itemState(it.Status)
			}
			out[i-1] = attemptSnap{n: i, status: st}
		}
		return out
	}
	return nil
}

// syncRoster 从队列快照重建任务面板左侧的批次列表（含展开的子任务行），
// 并尽量把光标恢复到钉住的行；队列未就绪时清空列表回到预览文件列表。
func (m *model) syncRoster() {
	if m.queue == nil {
		m.roster = nil
		return
	}
	rows := make([]rosterRow, 0, 8)
	for _, ptr := range m.queue.Snapshot() {
		b, ok := m.queue.BatchCopy(ptr.ID)
		if !ok {
			continue
		}
		c := b.Counts()
		settled := c.Done + c.Skipped + c.Canceled
		title := fmt.Sprintf("%s（%s %d/%d）", b.Name, batchStateText(b.Status), settled, c.Total)
		if c.Failed > 0 {
			title = fmt.Sprintf("%s（%s %d/%d 失败%d）", b.Name, batchStateText(b.Status), settled, c.Total, c.Failed)
		}
		rows = append(rows, rosterRow{
			batchID: b.ID,
			title:   title,
			state:   batchState(b.Status),
		})
		explanded, seen := m.expanded[b.ID]
		if !seen {
			explanded = b.Status == task.StatusRunning || b.Status == task.StatusPaused
		}
		if !explanded {
			continue
		}
		for _, it := range b.Items {
			if it.Removed {
				continue
			}
			rows = append(rows, rosterRow{
				batchID: b.ID,
				itemID:  it.ID,
				title:   filepath.Base(it.Input),
				state:   itemState(it.Status),
				cmd:     command.FormatCommand(it.PlanArgs),
			})
			if !m.itemAttemptsOpen(b, it) {
				continue
			}
			for _, a := range m.attemptSnapshots(b.ID, it.ID) {
				rows = append(rows, rosterRow{
					batchID: b.ID,
					itemID:  it.ID,
					attempt: a.n,
					title:   fmt.Sprintf("第 %d 次", a.n),
					state:   a.status,
					cmd:     command.FormatCommand(it.PlanArgs),
				})
			}
		}
	}
	m.roster = rows
	// 恢复光标到钉住的行；找不到时夹取到有效范围。
	exact, fallback := -1, -1
	for i, row := range rows {
		if row.batchID != m.rosterPinBatch || row.itemID != m.rosterPinItem {
			continue
		}
		if row.attempt == 0 {
			fallback = i
		}
		if row.attempt == m.rosterPinAttempt {
			exact = i
			break
		}
	}
	switch {
	case exact >= 0:
		m.fileCursor = exact
	case fallback >= 0:
		m.fileCursor = fallback
	default:
		m.fileCursor = clamp(m.fileCursor, 0, max(len(rows)-1, 0))
	}
	if m.outputFocus {
		rowOK := m.fileCursor >= 0 && m.fileCursor < len(m.roster) && m.roster[m.fileCursor].attempt > 0
		if !rowOK {
			m.outputFocus = false
		}
	}
	if m.width > 0 && (m.running || len(rows) > 0) {
		m.refreshRunHeights()
	}
}

// applyPlan 保存最新生成的计划；运行期间先暂存到 pendingPlan，批次结束后再应用，
// 避免执行中的事件与文件列表下标对不上。
func (m *model) applyPlan(err error, plan *command.BatchPlan) tea.Cmd {
	if m.planCancel != nil {
		m.planCancel()
		m.planCancel = nil
	}
	m.planLoading = false
	m.previewDone = true
	if m.running {
		m.pendingPlan, m.pendingPlanErr = plan, err
		if err != nil {
			m.statusMsg = "× 新配置生成失败：" + err.Error()
		} else if plan != nil {
			m.statusMsg = "配置已更新，后续批次将使用新配置"
		}
		return nil
	}
	m.plan = plan
	if err != nil {
		m.planErr = err
		m.files = nil
		m.fileCursor = 0
		m.runIdx = -1
		m.previewSummary = ""
		return nil
	}
	m.planErr = nil
	if plan == nil {
		return nil
	}
	m.files = buildFileItems(plan.Jobs)
	m.fileCursor = 0
	m.runIdx = -1
	m.previewSummary = m.summaryText(plan.Commands())
	m.refreshRunHeights()
	m.syncPreview()
	m.vp.GotoTop()
	return nil
}

// applyPendingPlan 在批次结束后应用运行期间暂存的计划。
func (m *model) applyPendingPlan() {
	if m.pendingPlan == nil && m.pendingPlanErr == nil {
		return
	}
	m.applyPlan(m.pendingPlanErr, m.pendingPlan)
	m.pendingPlan, m.pendingPlanErr = nil, nil
}

// handleDone 收尾一次执行：更新计数与提示，应用运行期间暂存的计划。
func (m *model) handleDone(err error) tea.Cmd {
	m.eventing = false
	m.runErr = err
	m.elapsed = m.elapsedNow().Round(time.Second)
	m.running = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		for i := range m.files {
			if m.files[i].state == filePending || m.files[i].state == fileRunning {
				m.files[i].state = fileCanceled
				m.canceled++
			}
		}
		m.statusMsg = "批处理已取消，配置与日志已保留"
	case err != nil:
		m.statusMsg = "× 批处理已结束: " + err.Error()
	default:
		m.statusMsg = "✔ 批处理完成。按 x 进入任务列表，确认参数后再按 x 入队"
	}
	m.current = ""
	m.runIdx = -1
	m.activeBatch = ""
	m.pausedBatch = ""
	m.logDirty = false
	m.logRefreshScheduled = false
	m.refreshRunHeights()
	m.syncPreview()
	m.syncLogPane()
	m.applyPendingPlan()
	return nil
}

// progressNumbers 返回当前应展示的进度。队列视图以光标所在主任务的快照为准，
// 完成数只计完成、跳过和取消；失败单独显示，重试不会把同一次失败再算进去。
func (m model) progressNumbers() (settled, failed, skipped, canceled, total int, paused bool) {
	if snap, ok := m.focusedSnapshot(); ok {
		c := snap.Counts()
		return c.Done + c.Skipped + c.Canceled, c.Failed, c.Skipped, c.Canceled, c.Total, snap.Status == task.StatusPaused
	}
	return m.done + m.skipped + m.canceled, m.failed, m.skipped, m.canceled, m.total, m.pausedBatch != ""
}

// focusedSnapshot 返回光标所在主任务；没有队列列表时返回当前正在执行的批次。
func (m model) focusedSnapshot() (*task.Batch, bool) {
	if m.queue == nil {
		return nil, false
	}
	id := m.activeBatch
	if len(m.roster) > 0 && m.fileCursor >= 0 && m.fileCursor < len(m.roster) {
		id = m.roster[m.fileCursor].batchID
	}
	if id == "" {
		return nil, false
	}
	return m.queue.BatchCopy(id)
}

// focusedBatchRow 返回光标所在的主任务行。
func (m model) focusedBatchRow() (rosterRow, bool) {
	row, ok := m.focusedRosterRow()
	if !ok || row.itemID != "" {
		return rosterRow{}, false
	}
	return row, true
}

// focusedItemRow 返回光标所在的子任务行（不是某一次）。
func (m model) focusedItemRow() (rosterRow, bool) {
	row, ok := m.focusedRosterRow()
	if !ok || row.itemID == "" || row.attempt > 0 {
		return rosterRow{}, false
	}
	return row, true
}

// focusedAttemptRow 返回光标所在的一次执行。
func (m model) focusedAttemptRow() (rosterRow, bool) {
	row, ok := m.focusedRosterRow()
	if !ok || row.attempt <= 0 {
		return rosterRow{}, false
	}
	return row, true
}

func (m model) focusedRosterRow() (rosterRow, bool) {
	if len(m.roster) == 0 || m.fileCursor < 0 || m.fileCursor >= len(m.roster) {
		return rosterRow{}, false
	}
	return m.roster[m.fileCursor], true
}

// queueEventWhenIdle 报告该事件在没有批次执行时也要刷新任务列表。
func queueEventWhenIdle(t task.QueueEventType) bool {
	switch t {
	case task.QueueBatchAdded, task.QueueBatchStarted, task.QueueBatchRemoved, task.QueueStopped,
		task.QueueBatchCanceled, task.QueueBatchDone, task.QueueBatchFailed, task.QueueItemCanceled:
		return true
	default:
		return false
	}
}

// elapsedNow 返回当前执行耗时；暂停等待决策期间冻结到暂停时刻，
// 恢复执行时把暂停时长从起点扣除，保证“子任务失败等待决策”不算进任务耗时。
func (m model) elapsedNow() time.Duration {
	end := time.Now()
	if !m.pausedAt.IsZero() {
		end = m.pausedAt
	}
	return end.Sub(m.startAt)
}

// clearPause 在决策已经发出后结束界面上的暂停态，避免连按被当成下一次决策。
func (m *model) clearPause(msg string) {
	m.pausedBatch = ""
	m.resumeTimer()
	m.statusMsg = msg
}

// pauseTimer 在进入暂停等待决策时冻结计时。
func (m *model) pauseTimer() {
	if m.pausedAt.IsZero() {
		m.pausedAt = time.Now()
	}
}

// resumeTimer 在暂停结束（恢复执行或批次收尾）时把暂停时长从起点扣除。
func (m *model) resumeTimer() {
	if m.pausedAt.IsZero() {
		return
	}
	m.startAt = m.startAt.Add(time.Since(m.pausedAt))
	m.pausedAt = time.Time{}
}

// itemIndexOf 把子任务 ID（"001" 形式）转换成文件列表下标。
func itemIndexOf(itemID string) int {
	n, err := strconv.Atoi(strings.TrimSpace(itemID))
	if err != nil {
		return -1
	}
	return n - 1
}

// buildQueueItems 把队列批次快照转换成任务面板的文件列表。
func buildQueueItems(items []*task.Item) []fileItem {
	fs := make([]fileItem, 0, len(items))
	for _, it := range items {
		state := filePending
		switch it.Status {
		case task.StatusRunning:
			state = fileRunning
		case task.StatusDone:
			state = fileDone
		case task.StatusFailed:
			state = fileFailed
		case task.StatusCanceled, task.StatusInterrupted:
			state = fileCanceled
		case task.StatusSkipped:
			state = fileSkipped
		}
		fs = append(fs, fileItem{
			input:  it.Input,
			output: it.Output,
			cmd:    command.FormatCommand(it.PlanArgs),
			state:  state,
		})
	}
	return fs
}

// handleQueueEvent 把队列事件映射到既有执行状态；ffmpeg 日志走原 handleEvent，
// 进度事件暂不渲染（状态/日志已足够），起停状态由队列子任务事件驱动，避免重复计数。
func (m *model) handleQueueEvent(e task.QueueEvent) tea.Cmd {
	if e.Type == task.QueueFFmpeg {
		if !m.running || e.BatchID != m.activeBatch || e.FFmpegEvt == nil {
			return nil
		}
		if e.FFmpegEvt.Type == ffmpeg.EventLog {
			evt := *e.FFmpegEvt
			// 执行器发出的日志下标可能是 0；以子任务 ID 为准，避免第二条任务的输出写进第一条。
			if idx := itemIndexOf(e.ItemID); idx >= 0 {
				evt.Index = idx
			}
			if m.total > 0 {
				evt.Total = m.total
			}
			return m.handleEvent(evt)
		}
		return nil
	}
	// 入队、开始、删除、取消以及子任务删除都要在空闲时刷新列表。
	// 正在执行的批次的结束事件仍走下面的收尾；其他批次的结束只刷新列表。
	if !m.running && !queueEventWhenIdle(e.Type) {
		return nil
	}
	var cmd tea.Cmd
	switch e.Type {
	case task.QueueBatchAdded:
		// 入队时的状态提示由 tryStartRun 设置，这里不再覆盖。
	case task.QueueBatchStarted:
		cmd = m.applyBatchStart(e.BatchID)
		// 默认把光标钉在新批次的批次行上，保持其展开视图。
		m.rosterPinBatch, m.rosterPinItem, m.rosterPinAttempt = e.BatchID, "", 0
	case task.QueueItemStarted:
		// 决策（重试/跳过）已生效，队列恢复执行：把暂停时长从总耗时扣除。
		m.pausedBatch = ""
		m.resumeTimer()
		if idx := itemIndexOf(e.ItemID); idx >= 0 && idx < len(m.files) {
			cmd = m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandStart, Index: idx, Total: m.total, Command: m.files[idx].cmd})
			if m.fileFollow {
				m.rosterPinBatch, m.rosterPinItem = e.BatchID, e.ItemID
				m.rosterPinAttempt = m.attemptNumber(idx)
			}
		}
	case task.QueueItemDone:
		if idx := itemIndexOf(e.ItemID); idx >= 0 && idx < len(m.files) {
			cmd = m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandDone, Index: idx, Total: m.total})
		}
	case task.QueueItemFailed:
		if idx := itemIndexOf(e.ItemID); idx >= 0 && idx < len(m.files) {
			msg := ""
			if e.Err != nil {
				msg = e.Err.Error()
			}
			cmd = m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandFailed, Index: idx, Total: m.total, Message: msg})
		}
	case task.QueueItemCanceled:
		if m.running && e.BatchID == m.activeBatch {
			if idx := itemIndexOf(e.ItemID); idx >= 0 && idx < len(m.files) {
				cmd = m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandCanceled, Index: idx, Total: m.total})
			}
		}
	case task.QueueItemSkipped:
		m.resumeTimer()
		m.pausedBatch = ""
		if idx := itemIndexOf(e.ItemID); idx >= 0 && idx < len(m.files) {
			m.recordLine(idx, fmt.Sprintf("− [%d/%d] 已跳过", idx+1, m.total), fileSkipped, logLineInfoStyle)
			m.syncLogPane()
		}
	case task.QueueItemRetry:
		// 次数在下一次开始时记入新的一节输出，这里只结束暂停。
		m.pausedBatch = ""
		m.resumeTimer()
	case task.QueueBatchPaused:
		m.pausedBatch = e.BatchID
		m.pauseTimer()
		m.statusMsg = "任务已暂停：r 重试 / d 删除 / c 取消"
	case task.QueueBatchDone, task.QueueBatchFailed, task.QueueBatchCanceled:
		// 只有正在执行时才收尾；取消队列里等待中的其他批次只刷新列表。
		if m.running {
			cmd = m.applyBatchFinish(e)
		}
	case task.QueueStopped:
		// 由 quitCleanup 或程序 ctx 触发，无需界面处理。
	}
	m.syncRoster()
	m.syncLogPane()
	return cmd
}

// applyBatchStart 从队列快照重建文件列表并初始化执行状态。
func (m *model) applyBatchStart(batchID string) tea.Cmd {
	if batchID == "" {
		return nil
	}
	snap, ok := m.queue.BatchCopy(batchID)
	if !ok {
		return nil
	}
	m.activeBatch = batchID
	m.pausedBatch = ""
	m.pausedAt = time.Time{}
	if m.planCancel != nil {
		m.planCancel()
		m.planCancel = nil
	}
	m.planSeq++ // 丢弃旧预览结果
	m.runID++
	m.running = true
	m.eventing = true
	m.runErr = nil
	m.total = snap.Counts().Total
	m.done, m.failed, m.canceled, m.skipped = 0, 0, 0, 0
	m.logs = nil
	m.logDirty, m.logRefreshScheduled = false, false
	m.current = ""
	m.runIdx = -1
	m.files = buildQueueItems(snap.Items)
	m.fileCursor = 0
	m.filesBatch = batchID
	m.fileFollow = true
	m.startAt = time.Now()
	m.elapsed = 0
	m.appendLog(fmt.Sprintf("▶ 开始执行批次，共 %d 个任务", m.total), logLineInfoStyle)
	m.setFocus(panelRun)
	m.refreshRunHeights()
	m.syncLogPane()
	m.logVp.GotoBottom()
	return tickCmd(m.runID)
}

// applyBatchFinish 用队列快照对账文件状态与计数，再走统一收尾；
// 取消的批次按被取消提示，暗示的未启动子任务也同步为 canceled。
func (m *model) applyBatchFinish(e task.QueueEvent) tea.Cmd {
	if m.running && e.BatchID != m.activeBatch {
		return nil // 结束的是旧批次消息，忽略
	}
	m.resumeTimer()
	if snap, ok := m.queue.BatchCopy(e.BatchID); ok {
		states := buildQueueItems(snap.Items)
		for i := range states {
			if i >= len(m.files) {
				break
			}
			m.files[i].state = states[i].state
		}
		// 按快照状态修正对账：事件计数可能遗漏未启动的子任务，删除的子任务不再计入总数。
		c := snap.Counts()
		m.done, m.failed, m.skipped = c.Done, c.Failed, c.Skipped
		m.canceled = c.Canceled
		if c.Total > 0 {
			m.total = c.Total
		}
	}
	var err error
	switch e.Type {
	case task.QueueBatchDone:
		err = nil
	case task.QueueBatchCanceled:
		err = context.Canceled
	default:
		if e.Err != nil {
			err = e.Err
		} else {
			err = fmt.Errorf("主任务失败")
		}
	}
	return m.handleDone(err)
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
