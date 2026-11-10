package ffmpeg

import (
	"context"
	"errors"
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if vb.option.OutputPath != "" {
		if err := makeDir(vb.option.OutputPath); err != nil {
			return fmt.Errorf("创建输出目录: %w", err)
		}
	}

	vb.emit(Event{Type: EventBatchStart, Total: len(cmdBatch)})

	if vb.option.Workers <= 1 {
		return vb.executeSequential(ctx, cmdBatch)
	}
	return vb.executeConcurrent(ctx, cmdBatch)
}

func (vb *videoBatch) executeSequential(ctx context.Context, cmdBatch [][]string) error {
	var errs []error
	for i, args := range cmdBatch {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := vb.executeCommand(ctx, i, len(cmdBatch), args); err != nil {
			errs = append(errs, fmt.Errorf("执行第 %d 条命令: %w", i+1, err))
		}
	}
	return errors.Join(errs...)
}

func (vb *videoBatch) executeConcurrent(ctx context.Context, cmdBatch [][]string) error {
	var wg sync.WaitGroup
	jobs := make(chan int)
	errs := make([]error, len(cmdBatch))
	// 固定数量的 worker，避免为大量待执行文件创建同等数量的 goroutine。
	for range min(vb.option.Workers, len(cmdBatch)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := vb.executeCommand(ctx, i, len(cmdBatch), cmdBatch[i]); err != nil {
					// 每个下标只有一个 worker 写入，等待全部结束后统一读取。
					errs[i] = fmt.Errorf("执行第 %d 条命令: %w", i+1, err)
				}
			}
		}()
	}
	submit := func() {
		for i := range cmdBatch {
			select {
			case <-ctx.Done():
				return
			case jobs <- i:
			}
		}
	}
	submit()
	close(jobs)
	wg.Wait()
	return errors.Join(append(errs, ctx.Err())...)
}

func (vb *videoBatch) executeCommand(ctx context.Context, index, total int, args []string) error {
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)

	if vb.option.OnEvent == nil {
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
		eventType := EventCommandFailed
		if ctx.Err() != nil {
			err = fmt.Errorf("任务已取消: %w", ctx.Err())
			eventType = EventCommandCanceled
		}
		vb.emit(Event{Type: eventType, Index: index, Total: total, Command: command, Message: err.Error()})
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
