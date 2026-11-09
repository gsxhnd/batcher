package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// ExecuteBatch 执行批处理命令，支持并发和 context 取消
func (vb *videoBatch) ExecuteBatch(ctx context.Context, cmdBatch [][]string) error {
	if len(cmdBatch) == 0 {
		return nil
	}

	vb.emit(Event{Type: EventBatchStart, Total: len(cmdBatch)})

	if vb.option.Workers <= 1 {
		return vb.executeSequential(ctx, cmdBatch)
	}
	return vb.executeConcurrent(ctx, cmdBatch)
}

func (vb *videoBatch) executeSequential(ctx context.Context, cmdBatch [][]string) error {
	for i, args := range cmdBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := vb.executeCommand(ctx, i, len(cmdBatch), args); err != nil {
			return fmt.Errorf("execute command: %w", err)
		}
	}
	return nil
}

func (vb *videoBatch) executeConcurrent(ctx context.Context, cmdBatch [][]string) error {
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)

	// 使用信号量控制并发数
	sem := make(chan struct{}, vb.option.Workers)

	for i, args := range cmdBatch {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(i int, a []string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			if err := vb.executeCommand(ctx, i, len(cmdBatch), a); err != nil {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("execute command: %w", err)
				})
			}
		}(i, args)
	}

	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func (vb *videoBatch) executeCommand(ctx context.Context, index, total int, args []string) error {
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)

	if vb.option.OnEvent == nil {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	command := FormatCommand(args)
	vb.emit(Event{Type: EventCommandStart, Index: index, Total: total, Command: command})

	lw := newLineWriter(func(line string) {
		vb.emit(Event{Type: EventLog, Index: index, Total: total, Message: line})
	})
	cmd.Stdout = lw
	cmd.Stderr = lw

	err := cmd.Run()
	lw.Flush()

	if err != nil {
		vb.emit(Event{Type: EventCommandFailed, Index: index, Total: total, Command: command, Message: err.Error()})
		return err
	}
	vb.emit(Event{Type: EventCommandDone, Index: index, Total: total, Command: command})
	return nil
}

func (vb *videoBatch) emit(e Event) {
	if vb.option.OnEvent == nil {
		return
	}
	vb.eventMu.Lock()
	defer vb.eventMu.Unlock()
	vb.option.OnEvent(e)
}
