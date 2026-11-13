package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrOutputExists 表示 ffmpeg 因输出文件已存在而停止。
var ErrOutputExists = errors.New("输出文件已存在")

// IsOutputExists 报告错误是否为输出文件已存在。
func IsOutputExists(err error) bool {
	return errors.Is(err, ErrOutputExists)
}

// cancelGrace 是取消时等待 ffmpeg 响应中断信号并正常收尾的时间，超时后强制结束。
const cancelGrace = 5 * time.Second

// progressArgs 让 ffmpeg 把 key=value 进度写到 stdout，并关闭 stderr 里的帧统计，
// 这样 stderr 只剩日志和错误，两路输出互不污染。
var progressArgs = []string{"-progress", "pipe:1", "-nostats"}

// ExecArgs 返回有事件监听时实际交给 ffmpeg 的参数：在计划参数前注入进度选项。
// 计划与预览里的命令保持干净、可直接复制，只有执行记录保存注入后的参数。
func ExecArgs(args []string) []string {
	return append(slices.Clone(progressArgs), args...)
}

// Call 描述一次 ffmpeg 调用：Index 与 Total 只用于事件定位，Command 是展示用的可复制命令。
type Call struct {
	Index   int
	Total   int
	Command string
	Args    []string
	// Input 是第一个输入文件，用于读取总时长以计算百分比；为空或读取失败时只报告已处理时长。
	Input string
}

// Runner 负责启动单个 ffmpeg 进程并把输出转成事件，不关心计划、队列和路径。
type Runner struct {
	// OnEvent 不为空时，ffmpeg 输出通过它回传，回调之间已串行化；
	// 为空时 ffmpeg 输出直接写到进程的 stdout/stderr，也不注入进度参数。
	OnEvent func(Event)

	mu sync.Mutex
}

// emit 串行化事件回调：stdout 与 stderr 各有一个拷贝 goroutine，会并发触发事件。
func (r *Runner) emit(e Event) {
	if r.OnEvent == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.OnEvent(e)
}

func (r *Runner) logLine(call Call, line string) {
	r.emit(Event{Type: EventLog, Index: call.Index, Total: call.Total, Message: line})
}

func newCommand(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	// 取消时先发中断信号让 ffmpeg 正常收尾；Windows 不能向子进程发送中断，直接结束。
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = cancelGrace
	return cmd
}

// Run 执行一条命令并阻塞到进程结束；ctx 取消时返回包装了 ctx.Err() 的错误。
func (r *Runner) Run(ctx context.Context, call Call) error {
	if r.OnEvent == nil {
		cmd := newCommand(ctx, call.Args)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("任务已取消: %w", ctx.Err())
			}
			return err
		}
		return nil
	}

	r.emit(Event{Type: EventCommandStart, Index: call.Index, Total: call.Total, Command: call.Command})

	// 读不到总时长（没装 ffprobe、文件没有时长）时降级为只报告已处理时长。
	var duration time.Duration
	if call.Input != "" {
		duration, _ = Duration(ctx, call.Input)
	}
	parser := newProgressParser(duration,
		func(p Progress) {
			r.emit(Event{Type: EventProgress, Index: call.Index, Total: call.Total, Progress: &p})
		},
		func(line string) { r.logLine(call, line) })
	stdout := newLineWriter(parser.handle)
	tail := &stderrTail{}
	stderr := newLineWriter(func(line string) {
		tail.add(line)
		r.logLine(call, line)
	})

	cmd := newCommand(ctx, ExecArgs(call.Args))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	stdout.Flush()
	stderr.Flush()
	// 输出已存在时，当前 ffmpeg 会把错误打到 stderr，进程退出码却是 0。
	// 不改写这条结果的话，任务会被记成成功。
	if ctx.Err() == nil {
		if line := tail.existsLine(); line != "" {
			err = fmt.Errorf("%w: %s", ErrOutputExists, line)
		}
	}

	if err != nil {
		eventType := EventCommandFailed
		if ctx.Err() != nil {
			err = fmt.Errorf("任务已取消: %w", ctx.Err())
			eventType = EventCommandCanceled
		} else if !IsOutputExists(err) {
			err = wrapFFmpegExit(err, tail.last())
		}
		r.emit(Event{Type: eventType, Index: call.Index, Total: call.Total, Command: call.Command, Message: err.Error()})
		return err
	}
	r.emit(Event{Type: EventCommandDone, Index: call.Index, Total: call.Total, Command: call.Command})
	return nil
}

const stderrTailKeep = 8

// stderrTail 只保留「输出已存在」的那一行和最近几行，避免长时间转码把 stderr 全部堆在内存里。
type stderrTail struct {
	mu     sync.Mutex
	exists string
	lines  []string
}

func (s *stderrTail) add(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exists == "" && outputExistsLine(line) {
		s.exists = line
	}
	s.lines = append(s.lines, line)
	if n := len(s.lines) - stderrTailKeep; n > 0 {
		// 用 slices.Delete 截掉前缀，释放已淘汰字符串的引用。
		s.lines = slices.Delete(s.lines, 0, n)
	}
}

func (s *stderrTail) existsLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exists
}

func (s *stderrTail) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) == 0 {
		return ""
	}
	return s.lines[len(s.lines)-1]
}

// outputExistsLine 只认 ffmpeg 的原文「File '...' already exists」，
// 避免输入路径里碰巧带有 already exists 时被当成输出冲突。
func outputExistsLine(line string) bool {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "already exists") {
		return false
	}
	return strings.Contains(lower, "file '") || strings.Contains(lower, "file \"")
}

func wrapFFmpegExit(err error, hint string) error {
	code := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}
	if hint != "" {
		return fmt.Errorf("ffmpeg 退出（%d）: %s: %w", code, hint, err)
	}
	return fmt.Errorf("ffmpeg 退出（%d）: %w", code, err)
}
