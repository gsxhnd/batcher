package ffmpeg

import (
	"context"
	"fmt"
)

type Task string

const (
	TaskConvert     Task = "convert"
	TaskAddSubtitle Task = "add_sub"
	TaskAddFonts    Task = "add_fonts"
)

// Plan 根据任务类型生成 ffmpeg 参数列表，不会执行任何命令。
func Plan(task Task, opt *VideoBatchOption) (VideoBatcher, [][]string, error) {
	vb, err := NewVideoBatch(opt)
	if err != nil {
		return nil, nil, fmt.Errorf("create video batch: %w", err)
	}

	var cmds [][]string
	switch task {
	case TaskConvert:
		cmds, err = vb.GetConvertBatch()
	case TaskAddSubtitle:
		cmds, err = vb.GetAddSubtitleBatch()
	case TaskAddFonts:
		cmds, err = vb.GetAddFontsBatch()
	default:
		return nil, nil, fmt.Errorf("unknown task: %q", task)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("plan %s: %w", task, err)
	}
	return vb, cmds, nil
}

// Preview 返回将要执行的命令（仅格式化，不执行）。
func Preview(task Task, opt *VideoBatchOption) ([]string, error) {
	_, cmds, err := Plan(task, opt)
	if err != nil {
		return nil, err
	}
	return formatLines(cmds), nil
}

// Run 生成并执行批处理。
func Run(ctx context.Context, task Task, opt *VideoBatchOption) error {
	vb, cmds, err := Plan(task, opt)
	if err != nil {
		return err
	}
	if err := vb.ExecuteBatch(ctx, cmds); err != nil {
		return fmt.Errorf("execute batch: %w", err)
	}
	return nil
}

// PlanCombined 按操作组合生成 ffmpeg 参数列表，不会执行任何命令。
func PlanCombined(opt *VideoBatchOption, sel Selection) (VideoBatcher, [][]string, error) {
	vb, err := NewVideoBatch(opt)
	if err != nil {
		return nil, nil, fmt.Errorf("create video batch: %w", err)
	}
	cmds, err := vb.GetCombinedBatch(sel)
	if err != nil {
		return nil, nil, fmt.Errorf("plan %s: %w", sel, err)
	}
	return vb, cmds, nil
}

// PreviewCombined 返回操作组合将要执行的命令（仅格式化，不执行）。
func PreviewCombined(opt *VideoBatchOption, sel Selection) ([]string, error) {
	_, cmds, err := PlanCombined(opt, sel)
	if err != nil {
		return nil, err
	}
	return formatLines(cmds), nil
}

// formatLines 将命令参数列表格式化为可读的命令字符串列表。
func formatLines(cmds [][]string) []string {
	lines := make([]string, 0, len(cmds))
	for _, c := range cmds {
		lines = append(lines, FormatCommand(c))
	}
	return lines
}

// RunCombined 生成并执行操作组合的批处理。
func RunCombined(ctx context.Context, opt *VideoBatchOption, sel Selection) error {
	vb, cmds, err := PlanCombined(opt, sel)
	if err != nil {
		return err
	}
	if err := vb.ExecuteBatch(ctx, cmds); err != nil {
		return fmt.Errorf("execute batch: %w", err)
	}
	return nil
}
