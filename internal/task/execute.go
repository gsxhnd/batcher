// Package task 负责把规划好的批处理按顺序交给 ffmpeg 执行。
// 依赖方向：task -> {command, media, ffmpeg}，规划与进程管理互不依赖。
package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/media"
)

// Execute 顺序执行计划快照：同一时刻只运行一个 ffmpeg 进程，单条失败不会中断其余命令。
// 执行前重新检查路径，默认拒绝覆盖已有文件；输出目录只在这里（执行时）才创建。
func Execute(ctx context.Context, plan *command.BatchPlan, onEvent func(ffmpeg.Event)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := plan.Check(ctx, true); err != nil {
		return err
	}
	if len(plan.Jobs) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if outDir := plan.Option().OutputPath; outDir != "" {
		if err := media.MakeDir(outDir); err != nil {
			return fmt.Errorf("创建输出目录: %w", err)
		}
	}

	runner := &ffmpeg.Runner{OnEvent: onEvent}
	runner.Emit(ffmpeg.Event{Type: ffmpeg.EventBatchStart, Total: len(plan.Jobs)})

	var errs []error
	for i, job := range plan.Jobs {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		call := ffmpeg.Call{
			Index:   i,
			Total:   len(plan.Jobs),
			Command: command.FormatCommand(job.Args),
			Args:    job.Args,
			Input:   job.Input,
		}
		if err := runner.Run(ctx, call); err != nil {
			errs = append(errs, fmt.Errorf("执行第 %d 条命令: %w", i+1, err))
		}
	}
	return errors.Join(errs...)
}
