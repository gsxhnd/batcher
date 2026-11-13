package task

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
)

// recordVersion 是任务记录 JSON 的 schema 版本，字段有不兼容变化时递增。
const recordVersion = 1

// SupportedRecord 报告记录版本是否为本程序能读取的范围。
func SupportedRecord(version int) bool {
	return version >= 1 && version <= recordVersion
}

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
	// PlanArgs 是计划里的原始参数。执行时再注入进度选项，不另存一份。
	PlanArgs []string `json:"planArgs"`
	Status   Status   `json:"status"`
	// Attempts 是这个子任务已经开始执行的次数，含正在进行的这一次。
	Attempts int `json:"attempts"`
	// Removed 为真时子任务已从队列列表删除：执行会跳过，计数与界面不再统计。
	Removed bool `json:"removed,omitempty"`
	// PartialOutput 在子任务启动前置真、成功后置回假：崩溃后据此判断输出是不是半成品。
	PartialOutput bool `json:"partialOutput"`
	// Overwrite 为真时允许替换已有输出。只在界面确认后置位。
	Overwrite  bool     `json:"overwrite,omitempty"`
	DurationMs int64    `json:"durationMs"`
	Error      string   `json:"error,omitempty"`
	StderrTail []string `json:"stderrTail,omitempty"`
	// AttemptLogs 按执行次数保存输出。下标 0 是第 1 次，失败次数展开时用它显示输出。
	AttemptLogs [][]string `json:"attemptLogs,omitempty"`
}

// IsOutputExists 判断错误是不是「输出文件已经存在」。
// ffmpeg 遇到 -n 且目标已存在时会打印这条错误，但退出码仍是 0。
func IsOutputExists(msg string) bool {
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "已存在") || strings.Contains(strings.ToLower(msg), "already exists")
}

// BeginAttemptLog 为新的一次执行准备一段输出。调用方需已持有队列锁。
func (it *Item) BeginAttemptLog() {
	it.AttemptLogs = append(it.AttemptLogs, nil)
}

// AppendAttemptLog 把一行输出写入当前这一次，只保留末尾 maxStderrTail 行。调用方需已持有队列锁。
func (it *Item) AppendAttemptLog(line string) {
	if line == "" {
		return
	}
	if len(it.AttemptLogs) == 0 {
		it.AttemptLogs = append(it.AttemptLogs, nil)
	}
	last := len(it.AttemptLogs) - 1
	it.AttemptLogs[last] = append(it.AttemptLogs[last], line)
	if n := len(it.AttemptLogs[last]) - maxStderrTail; n > 0 {
		// 用 slices.Delete 截掉前缀，释放已淘汰字符串的引用。
		it.AttemptLogs[last] = slices.Delete(it.AttemptLogs[last], 0, n)
	}
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
	if opt.InputPath != "" {
		opt.InputPath = inputDir
	}
	outputDir = absPath(outputDir)
	if opt.OutputPath != "" {
		opt.OutputPath = outputDir
	}
	b := &Batch{
		Version:   recordVersion,
		ID:        id,
		Name:      filepath.Base(inputDir),
		InputDir:  inputDir,
		OutputDir: outputDir,
		Selection: sel,
		Option:    opt,
		Status:    StatusPending,
		CreatedAt: now,
		Items:     make([]*Item, 0, len(plan.Jobs)),
	}
	for i, job := range plan.Jobs {
		input := job.Input
		output := job.Output
		if input != "" {
			input = absPath(input)
		}
		if output != "" {
			output = absPath(output)
		}
		planArgs := absArgs(job.Args, job.Input, input, job.Output, output)
		b.Items = append(b.Items, &Item{
			ID:       fmt.Sprintf("%03d", i+1),
			Input:    input,
			Output:   output,
			PlanArgs: planArgs,
			Status:   StatusPending,
		})
	}
	return b
}

// absArgs 把参数里与输入、输出完全相同的路径换成绝对路径。
func absArgs(args []string, fromTo ...string) []string {
	out := slices.Clone(args)
	for i := 0; i+1 < len(fromTo); i += 2 {
		from, to := fromTo[i], fromTo[i+1]
		if from == "" || from == to {
			continue
		}
		for j, arg := range out {
			if arg == from {
				out[j] = to
			}
		}
	}
	return out
}

func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
