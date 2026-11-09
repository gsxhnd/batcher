package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// version/buildDate/gitCommit 由 GoReleaser 在发布时通过 ldflags 注入；
// 本地直接 go run 时保持默认值，标题栏不显示版本号。
var (
	version   = ""
	buildDate = ""
	gitCommit = ""
)

// badgeVersion 返回标题栏徽标中的版本号后缀，未注入版本时为空。
func badgeVersion() string {
	if version == "" {
		return ""
	}
	return " " + version
}

// Run 启动交互式 TUI（备用屏幕全屏模式，由 View 的 AltScreen 开启）。
func Run() error {
	p := tea.NewProgram(newModel())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
