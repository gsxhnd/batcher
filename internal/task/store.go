package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gsxhnd/batcher/internal/media"
)

const (
	recordPrefix = ".batcher-"
	recordSuffix = ".json"
)

// RecordPath 返回任务记录文件的路径：主任务输出目录下的 .batcher-<ID>.json。
func (b *Batch) RecordPath() string {
	return filepath.Join(b.OutputDir, recordPrefix+b.ID+recordSuffix)
}

// Save 把主任务的完整状态写入记录文件，用同目录临时文件加 rename 保证原子替换。
// 只应在执行期间由队列的单个执行 goroutine 调用；输出目录不存在时在这里创建，规划阶段不会写盘。
func (b *Batch) Save() error {
	if b.OutputDir == "" {
		return fmt.Errorf("输出目录不能为空")
	}
	return b.save(b.RecordPath())
}

func (b *Batch) save(path string) error {
	if err := media.MakeDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("创建记录目录: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("编码任务记录: %w", err)
	}
	return writeAtomic(path, append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), recordPrefix+"*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时记录文件: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(action string, err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("%s: %w", action, err)
	}
	_ = tmp.Chmod(0o644)
	if _, err := tmp.Write(data); err != nil {
		return fail("写入任务记录", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("同步任务记录", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("关闭任务记录: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("替换任务记录: %w", err)
	}
	return nil
}

// Load 读取任务记录，只读取不修改；schema 版本缺失或高于当前版本时返回错误。
func Load(path string) (*Batch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取任务记录 %s: %w", path, err)
	}
	var b Batch
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("解析任务记录 %s: %w", path, err)
	}
	if b.Version < 1 || b.Version > recordVersion {
		return nil, fmt.Errorf("不支持的任务记录版本 %d: %s", b.Version, path)
	}
	return &b, nil
}

// MarkInterrupted 把仍处于运行中或暂停的主任务与运行中的子任务标为 interrupted，返回是否有变化。
// 已完成的成品、尚未开始的子任务和 PartialOutput 标记保持不变，供之后重试时清理半成品。
func (b *Batch) MarkInterrupted() bool {
	changed := false
	for _, it := range b.Items {
		if it.Status == StatusRunning {
			it.Status = StatusInterrupted
			changed = true
		}
	}
	if b.Status == StatusRunning || b.Status == StatusPaused {
		b.Status = StatusInterrupted
		changed = true
	}
	return changed
}

// Recover 读取上次遗留的记录；若上次异常退出，则标为 interrupted 并写回原文件。
func Recover(path string) (*Batch, error) {
	b, err := Load(path)
	if err != nil {
		return nil, err
	}
	if b.MarkInterrupted() {
		if err := b.save(path); err != nil {
			return nil, err
		}
	}
	return b, nil
}
