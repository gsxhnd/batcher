package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/presets"
)

type phase int

const (
	phaseFeature    phase = iota // 勾选要执行的操作
	phaseAdvance                 // 转码参数选择（内置/自定义预设）
	phaseSubs                    // 字幕列表管理
	phaseSubEdit                 // 单条字幕编辑
	phaseForm                    // 填写主参数
	phasePreview                 // 预览命令
	phaseRunning                 // 执行中
	phaseResult                  // 结果
	phasePresets                 // 转码参数管理列表（自定义预设）
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
	"视频转码": "按“自定义参数 + 输出后缀”转码；参数可在下一步选择/保存常用预设",
	"添加字幕": "把源目录下匹配视频名的字幕文件封装进视频，支持多条字幕",
	"添加字体": "把字体文件夹中的字体文件内嵌到视频（mkv 附带流）",
}

// 文件任务的执行状态（左侧列表标记用）。
const (
	filePending = ""        // 待执行
	fileRunning = "running" // 执行中
	fileDone    = "done"    // 完成
	fileFailed  = "failed"  // 失败
)

// fileItem 描述预览/执行界面左侧列表中的一个源文件任务。
type fileItem struct {
	input  string   // 源文件路径
	output string   // 输出文件路径
	cmd    string   // 已格式化的完整 ffmpeg 命令
	state  string   // filePending / fileRunning / fileDone / fileFailed
	logs   []string // 该任务执行时的 ffmpeg 输出（按行，带样式）
}

type model struct {
	width, height int

	phase phase

	// 操作组合勾选
	ops        ffmpeg.Selection
	featCursor int
	featErr    string

	// 转码参数预设：内置（只读）在前，用户自定义在后
	builtins      []presets.Builtin
	presets       []string
	presetCursor  int
	manualAdvance string
	statusMsg     string // 界面上的临时提示（保存成功等）

	// 转码参数管理页（仅管理用户自定义预设）
	mgrCursor    int
	mgrEditIndex int             // 当前编辑的预设下标，-1 表示新增
	mgrInput     textinput.Model // 编辑中的参数文本
	mgrErr       string
	mgrReturn    phase // 管理页返回的界面（操作页或转码页）

	// 删除自定义预设的二次确认：记录待删除的下标，-1 表示无待确认项
	delPendingIdx int

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
	vp             viewport.Model
	previewDone    bool
	previewSummary string // Plan 汇总文本（全部命令列表），左侧选中“全部”时展示
	planErr        error
	spin           spinner.Model

	// 预览/执行共用的左右分栏：左侧源文件列表，右侧命令/输出
	files      []fileItem
	fileCursor int  // 左侧光标：0 = 全部汇总，1.. = 第 i 个源文件
	fileFollow bool // 执行时右侧自动跟随正在执行的文件（手动切换后停止）
	runIdx     int  // 正在执行的文件下标（files 内），-1 表示当前无执行中任务
	leftW      int  // 左侧文件栏宽度
	rightW     int  // 右侧内容区宽度

	// 执行
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

	// 结果
	runErr error
}

func newModel() model {
	return model{
		phase:    phaseFeature,
		bar:      progress.New(progress.WithDefaultBlend()),
		spin:     spinner.New(spinner.WithSpinner(spinner.Dot)),
		builtins: presets.Builtins(),
		runIdx:   -1,

		delPendingIdx: -1,
	}
}

// presetCount 返回转码参数页完整列表长度：内置 + 自定义 + 手动输入。
func (m model) presetCount() int {
	return len(m.builtins) + len(m.presets) + 1
}

// presetHintText 返回用户预设文件路径，用于状态提示。
func presetHintText() string {
	path, err := presets.FilePath()
	if err != nil {
		return "预设文件夹（无法解析位置: " + err.Error() + "）"
	}
	return path
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

type tickMsg struct{}

// ---------- 命令 ----------

// maxLogLines 限制日志缓冲行数，避免长任务占用过多内存。
const maxLogLines = 1000

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

// tickCmd 每秒触发一次重绘，用于执行页显示实时耗时。
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

// ---------- Update ----------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
		m.resizePanes()
		// 尺寸变化后按新宽度重新折行刷新两侧视口。
		m.syncPreview()
		m.syncLogPane()
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
			return m, nil
		}
		m.planErr = nil
		m.files = buildFileItems(msg.cmds)
		m.fileCursor = 0
		m.runIdx = -1
		var b strings.Builder
		b.WriteString(fmt.Sprintf("已选择：%s，共生成 %d 条命令（%d 个视频）\n", displaySel(m.ops), len(msg.cmds), len(msg.cmds)))
		if m.ops.AddSubtitle {
			var suffixes []string
			for _, s := range m.subs {
				suffixes = append(suffixes, s.Suffix)
			}
			fmt.Fprintf(&b, "字幕（%d 条）：.%s\n", len(m.subs), strings.Join(suffixes, " / ."))
		}
		b.WriteString("确认无误后按 Enter 开始执行；左侧 ↑/↓ 切换文件查看对应命令。\n\n")
		for i, c := range msg.cmds {
			fmt.Fprintf(&b, "[%d/%d] %s\n\n", i+1, len(msg.cmds), ffmpeg.FormatCommand(c))
		}
		m.previewSummary = b.String()
		m.syncPreview()
		m.vp.GotoTop()
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
		m.elapsed = time.Since(m.startAt).Round(time.Second)
		m.phase = phaseResult
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
		if m.phase == phasePreview && !m.previewDone {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil
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

// handleEvent 将 ffmpeg 事件按文件路由到左侧列表与右侧日志，更新进度，返回进度条动画命令。
func (m *model) handleEvent(e ffmpeg.Event) tea.Cmd {
	switch e.Type {
	case ffmpeg.EventBatchStart:
		m.total = e.Total
		m.appendLog(fmt.Sprintf("开始执行批处理，共 %d 个任务", e.Total), logLineInfoStyle)
	case ffmpeg.EventCommandStart:
		m.current = e.Command
		m.runIdx = e.Index
		line := fmt.Sprintf("▶ [%d/%d] %s", e.Index+1, e.Total, e.Command)
		m.appendLog(line, logLineInfoStyle)
		if it := m.fileAt(e.Index); it != nil {
			it.state = fileRunning
			it.logs = appendFileLog(it.logs, line, logLineInfoStyle)
		}
		if m.fileFollow {
			m.fileCursor = e.Index + 1
		}
	case ffmpeg.EventLog:
		m.appendLog("    "+e.Message, logLineDefaultStyle)
		if it := m.fileAt(e.Index); it != nil {
			it.logs = appendFileLog(it.logs, "    "+e.Message, logLineDefaultStyle)
		}
	case ffmpeg.EventCommandDone:
		m.done++
		m.current = ""
		line := fmt.Sprintf("✔ [%d/%d] 完成", e.Index+1, e.Total)
		m.appendLog(line, logLineDoneStyle)
		if it := m.fileAt(e.Index); it != nil {
			it.state = fileDone
			it.logs = appendFileLog(it.logs, line, logLineDoneStyle)
		}
	case ffmpeg.EventCommandFailed:
		m.failed++
		m.current = ""
		line := fmt.Sprintf("✘ [%d/%d] 失败: %s", e.Index+1, e.Total, e.Message)
		m.appendLog(line, logLineFailedStyle)
		if it := m.fileAt(e.Index); it != nil {
			it.state = fileFailed
			it.logs = appendFileLog(it.logs, line, logLineFailedStyle)
		}
	}
	var cmd tea.Cmd
	if m.total > 0 {
		cmd = m.bar.SetPercent(float64(m.done+m.failed) / float64(m.total))
	}
	m.syncLogPane()
	return cmd
}

// fileAt 返回指定下标的文件条目，越界时返回 nil（事件 Index 与 files 顺序一致）。
func (m model) fileAt(idx int) *fileItem {
	if idx >= 0 && idx < len(m.files) {
		return &m.files[idx]
	}
	return nil
}

// appendFileLog 向单个文件的日志缓冲追加一行（带样式），超过上限时丢弃旧行。
func appendFileLog(logs []string, line string, s lipgloss.Style) []string {
	logs = append(logs, s.Render(line))
	if len(logs) > maxLogLines {
		logs = logs[len(logs)-maxLogLines:]
	}
	return logs
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
	m.logs = append(m.logs, s.Render(line))
	if len(m.logs) > maxLogLines {
		m.logs = m.logs[len(m.logs)-maxLogLines:]
	}
}

// ---------- 左右分栏（预览/执行共用） ----------

// bodyH 返回面板内部可用行数（去掉顶栏 1、底栏 2 与面板上下边框 2）。
func (m model) bodyH() int {
	return max(m.height-5, 3)
}

// resizePanes 依据终端宽高计算左右分栏与视口尺寸。
func (m *model) resizePanes() {
	// 面板内容宽度：终端宽 - 面板左右边框 2 - 内边距 2。
	contentW := max(m.width-6, 10)
	leftW := min(28, max(18, contentW*2/7))
	// 保证右侧内容区至少 36 列，窄终端时压缩左侧。
	if right := contentW - leftW - 1; right < 36 {
		leftW = max(14, contentW-37)
	}
	m.leftW = max(leftW, 10)
	m.rightW = max(contentW-m.leftW-1, 10)

	m.vp.SetWidth(m.rightW)
	m.vp.SetHeight(max(m.bodyH()-1, 3))
	m.logVp.SetWidth(m.rightW)
	m.logVp.SetHeight(max(m.bodyH()-2, 3))
	m.bar.SetWidth(max(m.rightW-4, 10))
}

// moveFilePane 移动左侧文件列表光标（0 为“全部”汇总项）并刷新右侧内容。
// 执行/结果阶段的手动切换会停止自动跟随。
func (m *model) moveFilePane(delta int) tea.Cmd {
	if len(m.files) == 0 {
		return nil
	}
	next := min(len(m.files), max(0, m.fileCursor+delta))
	if next == m.fileCursor {
		return nil
	}
	m.fileCursor = next
	if m.phase == phaseRunning {
		m.fileFollow = false
	}
	m.syncPreview()
	m.vp.GotoTop()
	m.syncLogPane()
	return nil
}

// previewContent 返回右侧预览視口的原始文本：选中“全部”时为汇总列表，否则为该文件命令。
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
	m.vp.SetContent(wrapText(m.previewContent(), m.rightW))
}

// syncLogPane 按左侧选中项刷新右侧日志视图并贴底，跟随最新输出。
func (m *model) syncLogPane() {
	if len(m.files) == 0 {
		m.logVp.SetContent("")
		return
	}
	var s string
	if idx := m.fileCursor - 1; idx < 0 {
		s = strings.Join(m.logs, "\n")
	} else if idx < len(m.files) {
		s = strings.Join(m.files[idx].logs, "\n")
	}
	m.logVp.SetContent(wrapText(s, m.rightW))
	m.logVp.GotoBottom()
}

// quitCleanup 退出前取消仍在执行的批处理。
func (m model) quitCleanup() {
	if m.cancel != nil {
		m.cancel()
	}
}
