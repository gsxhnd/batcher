package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// Run 启动交互式 TUI（备用屏幕全屏模式，由 View 的 AltScreen 开启）。
func Run() error {
	p := tea.NewProgram(newModel())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
