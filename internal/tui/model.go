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
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/store"
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
	input       string          // 源文件路径
	output      string          // 输出文件路径
	cmd         string          // 已格式化的完整 ffmpeg 命令
	state       string          // filePending / fileRunning / fileDone / fileFailed / fileCanceled
	logs        []string        // 该任务执行时的 ffmpeg 输出（按行，带样式）
	attempts    []attemptLog    // 按次数分开的输出，左侧列表挂在子任务下面
	progress    ffmpeg.Progress // 当前这次执行的 ffmpeg 进度
	hasProgress bool            // 收到过进度快照；重试开始时清掉，避免沿用上一次
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

	focus panel // 当前聚焦的面板

	// 转码参数预设：内置（只读）在前，用户自定义在后。
	builtins     []presets.Builtin
	presets      []presets.Preset
	presetCursor int
	statusMsg    string        // 界面上的临时提示；以 × 开头的错误显示在右上角
	sel          textSelection // 鼠标拖选的范围，松手后写入剪贴板

	// 弹窗（创建任务、预设管理、编辑与确认），非空时按键全部交给弹窗
	modal        *modal
	db           *store.DB
	savedList    []*task.Batch
	liveBatches  map[string]*task.Batch // syncRoster 刷新的队列快照，界面读取不再逐条加锁拷贝
	forget       map[string]struct{}    // 要从列表和数据库删掉的主任务
	creating     bool
	createSeq    int
	createCancel context.CancelFunc
	createSel    command.Selection

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
	activeBatch string    // 正在执行或暂停中的批次 ID
	pausedBatch string    // 非空时队列处于暂停等待决策（批次 ID）
	pausedAt    time.Time // 非零时处于暂停等待决策，计时冻结到该时刻
	queue       *task.Queue

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
		focus: panelRun,
		bar: progress.New(
			progress.WithColors(lipgloss.Color(cProgress), lipgloss.Color(cOk)),
			progress.WithScaled(true),
		),
		builtins: presets.Builtins(),
		runIdx:   -1,
	}
	user, err := presets.Load()
	if err != nil {
		m.setStatus("× 读取自定义预设失败: " + err.Error())
	}
	m.presets = user
	m.bar.EmptyColor = lipgloss.Color(cKeyBg)
	m.bar.PercentageStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(cText)).Bold(true)
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

// presetHintText 返回任务数据库路径，预设和任务都写在这里。
func presetHintText() string {
	dir, err := store.Dir()
	if err != nil {
		return "数据库（无法解析位置: " + err.Error() + "）"
	}
	return filepath.Join(dir, store.FileName)
}

// setFocus 切换聚焦面板。离开任务面板时停止自动跟随。
func (m *model) setFocus(p panel) {
	if p < 0 || p >= panelCount || m.focus == p {
		return
	}
	if m.focus == panelRun && p != panelRun {
		m.fileFollow = false
		m.outputFocus = false
	}
	m.focus = p
	m.refreshRunHeights()
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

// createPlanMsg 是新建任务弹窗里一次目录扫描的结果。
type createPlanMsg struct {
	seq  int
	plan *command.BatchPlan
	err  error
}

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
	case createPlanMsg:
		if msg.seq != m.createSeq {
			return m, nil
		}
		m.creating = false
		if m.modal == nil || m.modal.kind != modalTask {
			return m, nil
		}
		if msg.err != nil {
			m.modal.err = msg.err.Error()
			return m, nil
		}
		if msg.plan == nil || len(msg.plan.Jobs) == 0 {
			m.modal.err = "未找到匹配的视频文件"
			return m, nil
		}
		plan := msg.plan
		sel := m.createSel
		m.openConfirmModal("确认加入队列", enqueueInputSummary(plan), func(m *model) tea.Cmd {
			return m.enqueue(plan, sel)
		})
		return m, nil

	case tea.WindowSizeMsg:
		m.clearSelection()
		m.width, m.height = msg.Width, msg.Height
		m.resizePanes()
		m.resizeModal()
		m.syncLogPane()
		return m, nil

	case queueEventMsg:
		// 无条件重挂监听：队列事件流不依赖执行状态（此前空闲或批次收尾时监听断链，
		// 导致入队后界面永远看不到执行）。不变式：只有 Init 与本分支挂 listenEvents，
		// 其他消息一律不得挂，保证任意时刻至多一个监听器在消费队列事件。
		if m.evCh == nil {
			return m, m.handleQueueEvent(msg.evt)
		}
		return m, tea.Batch(m.handleQueueEvent(msg.evt), listenEvents(m.ctx, m.evCh))

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

	case tea.MouseClickMsg:
		return m, m.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		return m, m.handleMouseMotion(msg)
	case tea.MouseReleaseMsg:
		return m, m.handleMouseRelease(msg)
	case tea.MouseWheelMsg:
		m.clearSelection()
		return m, m.handleMouse(msg)
	case tea.PasteMsg:
		m.clearSelection()
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		m.clearSelection()
		if key.String() == "ctrl+c" {
			// 真正的停止放在 Run 退出之后：这里同步 Stop 会在事件通道塞满时把界面卡死。
			return m, tea.Quit
		}
		return m, m.handleKey(key)
	}
	if m.modal != nil {
		return m, m.updateModal(msg)
	}
	return m, nil
}

// handleKey 按当前状态分发按键：弹窗 > 输出查看 > 执行中取消 > 全局键 > 任务列表。
func (m *model) handleKey(key tea.KeyPressMsg) tea.Cmd {
	if m.modal != nil {
		return m.updateModal(key)
	}
	s := key.String()

	if m.outputFocus {
		switch s {
		case "esc":
			m.leaveOutput()
			return nil
		case "enter", "tab", "shift+tab":
			return nil
		}
	}

	if m.running || m.pausedBatch != "" {
		switch s {
		case "q", "esc":
			if s == "esc" && m.outputFocus {
				m.leaveOutput()
				return nil
			}
			if m.pausedBatch != "" {
				if m.queue != nil {
					m.queue.Decide(task.DecisionAbort)
				}
				m.clearPause("已发送中止指令")
				return nil
			}
			if m.queue != nil {
				m.queue.CancelCurrent()
				m.setStatus("已发送取消请求，等待正在执行的任务结束…")
			}
			return nil
		}
	}

	switch s {
	case "a":
		return m.openTaskModal()
	case "p":
		m.openPresetList()
		return nil
	case "q":
		return tea.Quit
	case "?":
		m.openHelpModal()
		return nil
	}
	return m.updateRunPanel(key)
}

// handleMouse 处理鼠标滚轮事件：任务面板滚动命令/日志，帮助弹窗滚动帮助文本，其余面板忽略。
func (m *model) handleMouse(msg tea.MouseWheelMsg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case m.modal != nil && (m.modal.kind == modalHelp || (m.modal.kind == modalConfirm && m.modal.vp.Height() > 0)):
		m.modal.vp, cmd = m.modal.vp.Update(msg)
	case m.modal != nil:
		return nil
	case m.running || m.queueViewActive():
		m.logVp, cmd = m.logVp.Update(msg)
		m.logFollow = m.logVp.AtBottom()
	}
	return cmd
}

// handleEvent 将 ffmpeg 事件写入全局日志与对应文件的日志，并更新进度条。
// 事件 Index 与 files 顺序一致，因此可直接定位到源文件条目。
func (m *model) handleEvent(e ffmpeg.Event) tea.Cmd {
	switch e.Type {
	case ffmpeg.EventProgress:
		// 进度不写日志。只更新当前子任务的快照，界面在下一次渲染时画出它自己的进度条。
		if e.Progress == nil || e.Index < 0 || e.Index >= len(m.files) {
			return nil
		}
		before := m.runBarH()
		m.files[e.Index].progress = *e.Progress
		m.files[e.Index].hasProgress = true
		if m.width > 0 && m.runBarH() != before {
			m.refreshRunHeights()
		}
		return nil
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
	if idx < 0 || idx >= len(m.files) {
		m.appendLog(line, s)
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
	it.logs = appendRendered(it.logs, rendered)
	m.logs = appendRendered(m.logs, rendered)
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
	return appendRendered(logs, s.Render(line))
}

func appendRendered(logs []string, rendered string) []string {
	logs = append(logs, rendered)
	if len(logs) > maxLogLines {
		logs = logs[len(logs)-maxLogLines:]
	}
	return logs
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

	m.logVp.SetWidth(max(m.rightW-2, 10))
	m.logVp.SetHeight(max(m.bodyH()-2, 3))
	m.bar.SetWidth(max(m.rightW-4, 10))
	// 视口实际高度按视图构成重算（预览头部/置顶命令/进度条占用的行数）。
	m.refreshRunHeights()
}

// 主任务把剩余高度留给子任务状态，子任务把剩余高度留给输出。
func (m *model) refreshRunHeights() {
	inner := max(m.bodyH()-2, 2)
	if h := m.narrowFilePaneH(); h > 0 {
		inner = max(inner-h-1, 2)
	}
	if m.running || m.queueViewActive() {
		m.logVp.SetHeight(max(inner-m.taskChromeH(), 2))
	}
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
	m.setStatus("")
}

// openOutput 进入当前子任务的输出查看。
func (m *model) openOutput() {
	m.outputFocus = true
	m.fileFollow = false
	m.logFollow = true
	m.pinRosterCursor()
	m.syncLogPane()
	m.logVp.GotoBottom()
	m.setStatus("↑/↓ 滚动输出，esc 返回")
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
	if row, ok := m.focusedRosterRow(); ok && m.queue != nil {
		if snap, ok := m.batchByID(row.batchID); ok {
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
	if row, ok := m.focusedRosterRow(); ok && row.itemID != "" {
		return fileStateText(row.state)
	}
	if m.runIdx >= 0 && m.runIdx < len(m.files) {
		return fileStateText(m.files[m.runIdx].state)
	}
	return "等待"
}

// focusedFileIndex 返回当前子任务在 files 中的下标。主任务行没有对应文件。
func (m model) focusedFileIndex() int {
	if row, ok := m.focusedRosterRow(); ok {
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
	if row, ok := m.focusedRosterRow(); ok && row.attempt > 0 {
		return row.attempt
	}
	if idx := m.focusedFileIndex(); idx >= 0 {
		if n := m.attemptNumber(idx); n > 0 {
			return n
		}
	}
	row, ok := m.focusedRosterRow()
	if m.queue == nil || !ok {
		return 0
	}
	snap, ok := m.batchByID(row.batchID)
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
		if snap, ok := m.batchByID(m.activeBatch); ok && idx < len(snap.Items) && snap.Items[idx].Attempts > n {
			n = snap.Items[idx].Attempts
		}
	}
	it.attempts = append(it.attempts, attemptLog{n: n, status: fileRunning})
	it.progress = ffmpeg.Progress{}
	it.hasProgress = false
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
					break
				}
				return a.lines
			}
		}
	}
	if m.queue != nil {
		if snap, ok := m.batchByID(row.batchID); ok {
			for _, it := range snap.Items {
				if it.ID != row.itemID {
					continue
				}
				n := row.attempt
				if n >= 1 && n <= len(it.AttemptLogs) && len(it.AttemptLogs[n-1]) > 0 {
					out := make([]string, len(it.AttemptLogs[n-1]))
					for i, line := range it.AttemptLogs[n-1] {
						style := logLineDefaultStyle
						if i == len(it.AttemptLogs[n-1])-1 && it.Status == task.StatusFailed && n == it.Attempts {
							style = logLineFailedStyle
						}
						out[i] = style.Render(line)
					}
					return out
				}
				if n == it.Attempts && len(it.StderrTail) > 0 {
					out := make([]string, len(it.StderrTail))
					for i, line := range it.StderrTail {
						out[i] = logLineDefaultStyle.Render(line)
					}
					return out
				}
				if n == it.Attempts && it.Error != "" {
					return []string{logLineFailedStyle.Render(it.Error)}
				}
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
		if a.status != fileFailed {
			continue
		}
		for _, ln := range m.oneAttemptLines(rosterRow{batchID: row.batchID, itemID: row.itemID, attempt: a.n}) {
			if strings.Contains(ln, "这次输出未保留") || strings.Contains(ln, "（无输出）") {
				continue
			}
			lines = append(lines, ln)
		}
	}
	return lines
}

// batchItemLines 是主任务下的子任务状态，不含 ffmpeg 输出。
func (m model) batchItemLines(batchID string) []string {
	if m.queue == nil {
		return []string{descStyle.Render("暂无子任务")}
	}
	snap, ok := m.batchByID(batchID)
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
		if label := m.itemProgressLabel(batchID, it.ID, it.Status); label != "" {
			line += "  " + label
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

// quitCleanup 退出前取消仍在执行的批处理并停止队列。
func (m model) quitCleanup() {
	if m.createCancel != nil {
		m.createCancel()
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.queue != nil {
		m.queue.Stop()
	}
}

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

// itemAttemptsOpen 表示这个子任务的次数是否展开。正在执行或已经失败时默认展开，方便直接看输出。
func (m *model) itemAttemptsOpen(b *task.Batch, it *task.Item) bool {
	if m.itemExpanded != nil {
		if v, ok := m.itemExpanded[b.ID+"\n"+it.ID]; ok {
			return v
		}
	}
	if it.Status == task.StatusRunning || it.Status == task.StatusFailed {
		return true
	}
	return false
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
	snap, ok := m.batchByID(batchID)
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

// syncRosterFromStore 重新读取数据库后再建列表。启动、删除和任务移出队列时用它。
func (m *model) syncRosterFromStore() {
	m.reloadSaved()
	m.syncRoster()
}

// syncRoster 从队列快照和已保存的任务重建左侧列表（含展开的子任务行），
// 并尽量把光标恢复到钉住的行。队列里的状态优先于数据库里的副本。
func (m *model) syncRoster() {
	rows := make([]rosterRow, 0, 8)
	for _, b := range m.rosterBatches() {
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
			cmd := command.FormatCommand(it.PlanArgs)
			rows = append(rows, rosterRow{
				batchID: b.ID,
				itemID:  it.ID,
				title:   filepath.Base(it.Input),
				state:   itemState(it.Status),
				cmd:     cmd,
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
					cmd:     cmd,
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
		m.setStatus("批处理已取消，配置与日志已保留")
	case err != nil:
		m.setStatus("× 批处理已结束: " + err.Error())
	default:
		m.setStatus("✔ 批处理完成")
	}
	m.current = ""
	m.runIdx = -1
	m.activeBatch = ""
	m.pausedBatch = ""
	m.logDirty = false
	m.logRefreshScheduled = false
	m.refreshRunHeights()
	m.syncLogPane()
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

// liveItemProgress 返回光标所在、且正在执行的那一次的 ffmpeg 进度。
// 主任务行、已结束的子任务、以及不是当前这一次的历史输出都不带实时进度。
func (m model) liveItemProgress() (ffmpeg.Progress, bool) {
	return m.fileProgress(m.progressFileIndex())
}

// progressFileIndex 定位当前要展示进度的子任务。没有队列列表时用正在执行的文件。
func (m model) progressFileIndex() int {
	if row, ok := m.focusedRosterRow(); ok {
		if row.itemID == "" || row.batchID != m.filesBatch {
			return -1
		}
		idx := itemIndexOf(row.itemID)
		if row.attempt > 0 && row.attempt != m.attemptNumber(idx) {
			return -1
		}
		return idx
	}
	if m.runIdx >= 0 && m.runIdx < len(m.files) {
		return m.runIdx
	}
	return -1
}

// fileProgress 返回仍在执行的子任务进度。结束后不再把它当成当前进度。
func (m model) fileProgress(idx int) (ffmpeg.Progress, bool) {
	if idx < 0 || idx >= len(m.files) || !m.files[idx].hasProgress || m.files[idx].state != fileRunning {
		return ffmpeg.Progress{}, false
	}
	return m.files[idx].progress, true
}

// itemProgressLabel 给列表一行用的短进度：有总时长时是百分比，否则是已处理时长。
func (m model) itemProgressLabel(batchID, itemID string, status task.Status) string {
	if status != task.StatusRunning || batchID != m.filesBatch {
		return ""
	}
	p, ok := m.fileProgress(itemIndexOf(itemID))
	if !ok {
		return ""
	}
	return progressLabel(p)
}

// focusedSnapshot 返回光标所在主任务；没有队列列表时返回当前正在执行的批次。
func (m model) focusedSnapshot() (*task.Batch, bool) {
	id := m.activeBatch
	if row, ok := m.focusedRosterRow(); ok {
		id = row.batchID
	}
	if id == "" {
		return nil, false
	}
	return m.batchByID(id)
}

// batchByID 先取队列中的实时副本，否则取数据库里的任务。
func (m model) batchByID(id string) (*task.Batch, bool) {
	if id == "" {
		return nil, false
	}
	if b, ok := m.liveBatches[id]; ok && b != nil {
		return b, true
	}
	if m.queue != nil {
		if b, ok := m.queue.BatchCopy(id); ok {
			return b, true
		}
	}
	for _, b := range m.savedList {
		if b != nil && b.ID == id {
			return b, true
		}
	}
	return nil, false
}

// rosterBatches 返回左侧列表要展示的主任务：队列中的在前，仅存在于数据库的历史任务在后。
func (m *model) refreshLiveBatches() {
	if m.queue == nil {
		m.liveBatches = nil
		return
	}
	copies := m.queue.Copies()
	m.liveBatches = make(map[string]*task.Batch, len(copies))
	for _, b := range copies {
		if b != nil {
			m.liveBatches[b.ID] = b
		}
	}
}

func (m *model) rosterBatches() []*task.Batch {
	var out []*task.Batch
	seen := map[string]bool{}
	m.liveBatches = nil
	if m.queue != nil {
		copies := m.queue.Copies()
		m.liveBatches = make(map[string]*task.Batch, len(copies))
		for _, b := range copies {
			if b == nil {
				continue
			}
			m.liveBatches[b.ID] = b
			seen[b.ID] = true
			out = append(out, b)
		}
	}
	for _, b := range m.savedList {
		if b == nil || seen[b.ID] {
			continue
		}
		out = append(out, b)
	}
	return out
}

// reloadSaved 从数据库重新读取任务列表。未打开数据库时保持原样。
func (m *model) reloadSaved() {
	if m.db == nil {
		return
	}
	list, err := m.db.ListTasks()
	if err != nil {
		m.setStatus("× 读取任务失败: " + err.Error())
		return
	}
	m.savedList = list
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

// setStatus 更新临时提示，并按它是否占详情行数重算滚动区高度。
func (m *model) setStatus(msg string) {
	if m.statusMsg == msg {
		return
	}
	m.statusMsg = msg
	if m.width > 0 {
		m.refreshRunHeights()
	}
}

// clearPause 在决策已经发出后结束界面上的暂停态，避免连按被当成下一次决策。
func (m *model) clearPause(msg string) {
	m.pausedBatch = ""
	m.resumeTimer()
	m.setStatus(msg)
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
		fs = append(fs, fileItem{
			input:  it.Input,
			output: it.Output,
			cmd:    command.FormatCommand(it.PlanArgs),
			state:  itemState(it.Status),
		})
	}
	return fs
}

// handleQueueEvent 把队列事件映射到既有执行状态。ffmpeg 日志与进度都走 handleEvent；
// 起停状态仍由队列子任务事件驱动，避免和进度快照重复计数。
func (m *model) handleQueueEvent(e task.QueueEvent) tea.Cmd {
	m.refreshLiveBatches()
	if e.Type == task.QueueStoreError {
		msg := "保存任务记录失败"
		if e.Err != nil {
			msg += ": " + e.Err.Error()
		}
		m.setStatus("× " + msg)
		return nil
	}
	if e.Type == task.QueueFFmpeg {
		if !m.running || e.BatchID != m.activeBatch || e.FFmpegEvt == nil {
			return nil
		}
		switch e.FFmpegEvt.Type {
		case ffmpeg.EventLog, ffmpeg.EventProgress:
			evt := *e.FFmpegEvt
			// 执行器发出的下标可能是 0；以子任务 ID 为准，避免第二条任务的输出写进第一条。
			if idx := itemIndexOf(e.ItemID); idx >= 0 {
				evt.Index = idx
			}
			if m.total > 0 {
				evt.Total = m.total
			}
			return m.handleEvent(evt)
		default:
			return nil
		}
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
			// 正在看这个子任务时，重试后光标跟到新的一次；浏览其他任务时不抢走光标。
			if m.fileFollow || (m.rosterPinBatch == e.BatchID && m.rosterPinItem == e.ItemID) {
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
		if e.Err != nil && task.IsOutputExists(e.Err.Error()) {
			m.setStatus("输出文件已存在，任务已暂停：r 重试 / o 覆盖 / s 跳过 / x 中止")
		} else {
			m.setStatus("任务已暂停：r 重试 / s 跳过 / x 中止")
		}
	case task.QueueBatchDone, task.QueueBatchFailed, task.QueueBatchCanceled:
		// 只有正在执行时才收尾；取消队列里等待中的其他批次只刷新列表。
		if m.running {
			cmd = m.applyBatchFinish(e)
		}
	case task.QueueStopped:
		// 由 quitCleanup 或程序 ctx 触发，无需界面处理。
	}
	if e.Type == task.QueueBatchRemoved {
		m.dropForgotten(e.BatchID)
		m.syncRosterFromStore()
	} else {
		m.syncRoster()
	}
	m.syncLogPane()
	return cmd
}

// dropForgotten 在队列已经移除主任务之后，把它从数据库里删掉。
func (m *model) dropForgotten(id string) {
	if _, ok := m.forget[id]; !ok {
		return
	}
	delete(m.forget, id)
	if m.db == nil {
		return
	}
	if err := m.db.DeleteTask(id); err != nil {
		m.setStatus("× 删除任务记录失败: " + err.Error())
	}
}

// applyBatchStart 从队列快照重建文件列表并初始化执行状态。
func (m *model) applyBatchStart(batchID string) tea.Cmd {
	if batchID == "" {
		return nil
	}
	snap, ok := m.batchByID(batchID)
	if !ok {
		return nil
	}
	m.activeBatch = batchID
	m.pausedBatch = ""
	m.pausedAt = time.Time{}
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
	if snap, ok := m.batchByID(e.BatchID); ok {
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
			err = errors.New("主任务失败")
		}
	}
	return m.handleDone(err)
}

func clamp(v, lo, hi int) int { return min(hi, max(lo, v)) }
