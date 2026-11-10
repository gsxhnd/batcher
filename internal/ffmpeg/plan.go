package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Job 是一个输入视频对应的一次处理，参数直接交给 ffmpeg，不经 shell。
type Job struct {
	Input  string
	Output string
	Args   []string
}

// BatchPlan 保存配置与任务快照；调用方应只读使用 Jobs。
// 预览与执行共用该快照，不在执行时重新扫描或重新生成命令。
type BatchPlan struct {
	Jobs   []Job
	option VideoBatchOption
}

// BuildPlan 扫描输入并生成任务，不写入文件、不创建目录，也不启动外部进程。
func BuildPlan(ctx context.Context, opt *VideoBatchOption, sel Selection) (*BatchPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vb, err := newVideoBatch(opt)
	if err != nil {
		return nil, err
	}
	jobs, err := vb.planJobs(ctx, sel)
	if err != nil {
		return nil, fmt.Errorf("生成任务计划: %w", err)
	}
	return &BatchPlan{Jobs: jobs, option: *vb.option}, nil
}

// Commands 返回各任务参数的副本。
func (p *BatchPlan) Commands() [][]string {
	cmds := make([][]string, 0, len(p.Jobs))
	for _, job := range p.Jobs {
		cmds = append(cmds, slices.Clone(job.Args))
	}
	return cmds
}

// Execute 执行当前快照，默认拒绝覆盖已有文件；只有 ExecuteBatch 会启动 ffmpeg。
func (p *BatchPlan) Execute(ctx context.Context, onEvent func(Event)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateJobPaths(ctx, p.Jobs, true); err != nil {
		return err
	}
	option := p.option
	option.OnEvent = onEvent
	vb := &videoBatch{option: &option}
	return vb.ExecuteBatch(ctx, p.Commands())
}

func validateJobPaths(ctx context.Context, jobs []Job, checkExisting bool) error {
	inputs := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := pathKey(job.Input)
		if err != nil {
			return err
		}
		inputs[path] = true
	}
	outputs := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := pathKey(job.Output)
		if err != nil {
			return err
		}
		if inputs[path] {
			return fmt.Errorf("输出路径与输入文件重合: %s", job.Output)
		}
		if outputs[path] {
			return fmt.Errorf("多个任务指向同一输出文件: %s", job.Output)
		}
		outputs[path] = true
		if checkExisting {
			if _, err := os.Lstat(job.Output); err == nil {
				return fmt.Errorf("输出文件已存在，请先移动或删除后重试: %s: %w", job.Output, os.ErrExist)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("检查输出文件 %s: %w", job.Output, err)
			}
		}
	}
	return nil
}

func pathKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析文件路径 %s: %w", path, err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
}
