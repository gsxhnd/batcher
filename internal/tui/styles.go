// Package tui 是 batcher 的交互式终端界面，基于 bubbletea v2 / bubbles v2 / lipgloss v2 构建，
// 复用 internal/command、internal/task、internal/ffmpeg 等核心包的规划、执行与事件能力，不重复实现业务逻辑。
package tui

import "charm.land/lipgloss/v2"

var (
	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 1)

	taskTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))

	selectedMark = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39"))

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	descStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("117")).
			Width(16)

	labelFocusedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true).
				Width(16)

	checkedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	uncheckedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75"))

	appBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("39")).
			Padding(0, 1)

	stepActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))

	stepTodoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("236"))

	helpKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252"))

	helpDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	helpSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238"))

	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("74"))

	logLineInfoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	logLineDoneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	logLineFailedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	logLineDefaultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	// cmdPinStyle 执行中置顶完整 CLI 命令的高亮样式（橙色加粗，与日志区分）。
	cmdPinStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)
