package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	m := newModel()
	m.ctx = ctx
	m.initQueue(make(chan tea.Msg, 256))
	p := tea.NewProgram(m, tea.WithContext(ctx))
	final, err := p.Run()
	canceled := ctx.Err() != nil
	cancel()
	if final, ok := final.(model); ok {
		// 停止任务队列并等待消费者退出：已启动的子任务完成取消与资源回收，记录落盘。
		final.quitCleanup()
	}
	if err != nil && (!errors.Is(err, tea.ErrProgramKilled) || !canceled || errors.Is(err, tea.ErrProgramPanic)) {
		return fmt.Errorf("运行终端界面: %w", err)
	}
	return nil
}
