// Package tui 是 batcher 的交互式终端界面，基于 bubbletea v2 / bubbles v2 / lipgloss v2 构建，
// 复用 internal/command、internal/task、internal/ffmpeg 等核心包的规划、执行与事件能力，不重复实现业务逻辑。
package tui

import "charm.land/lipgloss/v2"

// 配色按深色终端设计：冷色强调、低饱和状态色，避免高亮把正文冲掉。
const (
	cAccent      = "73"  // 焦点边框、链接
	cAccentHi    = "159" // 选中文字
	cProgress    = "81"  // 进度条已完成段
	cText        = "252"
	cTextDim     = "246"
	cMuted       = "243"
	cBorder      = "240"
	cBorderDim   = "237"
	cDimBg       = "234" // 弹窗打开时压暗主界面
	cInput       = "255"
	cOnColor     = "231"
	cPlaceholder = "241"
	cLabel       = "152"
	cStepOk      = "157"
	cStepErr     = "224"
	cOk          = "114"
	cErr         = "210"
	cWarn        = "222"
	cInfo        = "110"
	cHeaderBg    = "236"
	cSelectBg    = "24"
	cKeyBg       = "238"
	cBadgeBg     = "31"
	cOkBg        = "22"
	cErrBg       = "52"
	cRunBg       = "23"
)

var (
	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cTextDim)).
			Padding(0, 1)

	taskTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cAccentHi))

	titleMarkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cAccent)).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cAccentHi))

	selectedMark = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cAccent)).
			Bold(true)

	// selectedRowStyle 铺满侧栏一行的选中底色。只用于整行渲染，避免套在已有颜色上被重置。
	selectedRowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(cInput)).
				Background(lipgloss.Color(cSelectBg)).
				Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cText))

	descStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cMuted))

	codeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cWarn))

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cLabel)).
			Width(16)

	labelFocusedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(cAccentHi)).
				Bold(true).
				Width(16)

	checkedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(cOk)).Bold(true)
	uncheckedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(cMuted))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cErr)).
			Bold(true)

	// errorToastStyle 右上角错误卡片：和面板一样的圆角边框，底色跟顶栏齐，只把字和边框标红。
	errorToastStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cErr)).
			Background(lipgloss.Color(cHeaderBg)).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(cErr)).
			BorderBackground(lipgloss.Color(cHeaderBg)).
			Padding(0, 1)

	// dragSelectStyle 鼠标拖选时盖住被选中的格子。
	dragSelectStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cOnColor)).
			Background(lipgloss.Color(cSelectBg))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cInfo))

	appBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cOnColor)).
			Background(lipgloss.Color(cBadgeBg)).
			Padding(0, 1)

	headerBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(cHeaderBg))

	stepActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cAccentHi)).
			Background(lipgloss.Color(cRunBg))

	stepTodoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cText)).
			Background(lipgloss.Color(cHeaderBg))

	stepOkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cStepOk)).
			Background(lipgloss.Color(cOkBg))

	stepErrStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cStepErr)).
			Background(lipgloss.Color(cErrBg))

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cBorder))

	gutterStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cBorderDim))

	helpKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cOnColor)).
			Background(lipgloss.Color(cKeyBg)).
			Padding(0, 1)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cTextDim))

	helpSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cMuted))

	footerPadStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(cHeaderBg))

	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color(cAccent))

	panelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(cAccentHi))

	panelTitleIdleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(cTextDim))

	logLineInfoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(cAccent))
	logLineDoneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(cOk))
	logLineFailedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(cErr))
	logLineDefaultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(cTextDim))

	// cmdPinStyle 执行中置顶完整 CLI 命令的高亮样式（暖色，与日志区分）。
	cmdPinStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(cWarn)).Bold(true)
)
