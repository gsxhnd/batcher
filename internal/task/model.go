package task

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
)

// recordVersion 是任务记录 JSON 的 schema 版本，字段有不兼容变化时递增。
const recordVersion = 1

// maxStderrTail 限制每个子任务落盘的日志行数：只保留末尾若干行，避免记录文件膨胀。
const maxStderrTail = 50

// Status 是主任务或子任务的状态。
type Status string

const (
	StatusPending     Status = "pending"
	StatusRunning     Status = "running"
	StatusPaused      Status = "paused" // 仅主任务：子任务失败后等待重试、跳过或中止
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusSkipped     Status = "skipped" // 仅子任务
	StatusCanceled    Status = "canceled"
	StatusInterrupted Status = "interrupted" // 进程退出或崩溃时仍处于运行中
)

// Terminal 返回该状态是否已结束（不会再自行变化）。
func (s Status) Terminal() bool {
	switch s {
	case StatusDone, StatusFailed, StatusSkipped, StatusCanceled, StatusInterrupted:
		return true
	}
	return false
}

// Item 是子任务：一个视频文件对应的一条 ffmpeg 命令。
type Item struct {
	ID     string `json:"id"`
	Input  string `json:"input"`
	Output string `json:"output"`
	// Args 是实际交给 ffmpeg 的参数（含注入的进度选项）；PlanArgs 是计划与预览里的原始参数。
	Args     []string `json:"args"`
	PlanArgs []string `json:"planArgs"`
	Status   Status   `json:"status"`
	// Attempts 是这个子任务已经开始执行的次数，含正在进行的这一次。
	Attempts int `json:"attempts"`
	// Removed 为真时子任务已从队列列表删除：执行会跳过，计数与界面不再统计。
	Removed bool `json:"removed,omitempty"`
	// PartialOutput 在子任务启动前置真、成功后置回假：崩溃后据此判断输出是不是半成品。
	PartialOutput bool     `json:"partialOutput"`
	DurationMs    int64    `json:"durationMs"`
	Error         string   `json:"error,omitempty"`
	StderrTail    []string `json:"stderrTail,omitempty"`
}

// AddStderr 追加一行日志，只保留末尾 maxStderrTail 行。
func (it *Item) AddStderr(line string) {
	it.StderrTail = append(it.StderrTail, line)
	if n := len(it.StderrTail) - maxStderrTail; n > 0 {
		it.StderrTail = slices.Delete(it.StderrTail, 0, n)
	}
}

// Batch 是主任务：对应一次入队的输入文件夹，持有其下全部子任务。
type Batch struct {
	Version    int                      `json:"version"`
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	InputDir   string                   `json:"inputDir"`
	OutputDir  string                   `json:"outputDir"`
	Selection  command.Selection        `json:"selection"`
	Option     command.VideoBatchOption `json:"option"`
	Status     Status                   `json:"status"`
	CreatedAt  time.Time                `json:"createdAt"`
	StartedAt  time.Time                `json:"startedAt,omitzero"`
	FinishedAt time.Time                `json:"finishedAt,omitzero"`
	Items      []*Item                  `json:"items"`
}

// Counts 统计各状态的子任务数量。
type Counts struct {
	Total, Pending, Running, Done, Failed, Skipped, Canceled, Interrupted int
}

// Counts 返回当前各状态的子任务数量。
func (b *Batch) Counts() Counts {
	c := Counts{}
	for _, it := range b.Items {
		if it.Removed {
			continue
		}
		c.Total++
		switch it.Status {
		case StatusPending:
			c.Pending++
		case StatusRunning:
			c.Running++
		case StatusDone:
			c.Done++
		case StatusFailed:
			c.Failed++
		case StatusSkipped:
			c.Skipped++
		case StatusCanceled:
			c.Canceled++
		case StatusInterrupted:
			c.Interrupted++
		}
	}
	return c
}

// NewID 生成按时间排序且不重复的主任务 ID。
func NewID(now time.Time) string {
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:])
}

// NewBatch 用计划快照创建主任务，所有子任务初始为 pending。
// 配置与参数都是副本，不与计划共享；输入输出目录记为绝对路径，任务记录落在输出目录里。
func NewBatch(id string, plan *command.BatchPlan, sel command.Selection, now time.Time) *Batch {
	opt := plan.Option()
	inputDir := absPath(opt.InputPath)
	outputDir := opt.OutputPath
	if outputDir == "" && len(plan.Jobs) > 0 {
		// 手工构造的计划可能没有携带 option；记录跟随第一个子任务的输出目录。
		outputDir = filepath.Dir(plan.Jobs[0].Output)
	}
	b := &Batch{
		Version:   recordVersion,
		ID:        id,
		Name:      filepath.Base(inputDir),
		InputDir:  inputDir,
		OutputDir: absPath(outputDir),
		Selection: sel,
		Option:    opt,
		Status:    StatusPending,
		CreatedAt: now,
		Items:     make([]*Item, 0, len(plan.Jobs)),
	}
	for i, job := range plan.Jobs {
		b.Items = append(b.Items, &Item{
			ID:       fmt.Sprintf("%03d", i+1),
			Input:    job.Input,
			Output:   job.Output,
			Args:     ffmpeg.ExecArgs(job.Args),
			PlanArgs: slices.Clone(job.Args),
			Status:   StatusPending,
		})
	}
	return b
}

func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
