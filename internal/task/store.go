package task

import (
	"errors"
	"fmt"
	"sync"
)

// BatchStore 持久化主任务。由 bbolt 实现，队列与界面通过 BindStore 接上。
type BatchStore interface {
	SaveTask(*Batch) error
	LoadTask(id string) (*Batch, error)
	ListTasks() ([]*Batch, error)
	DeleteTask(id string) error
}

var (
	storeMu    sync.RWMutex
	batchStore BatchStore
)

// BindStore 设置任务存储。传 nil 表示卸下，之后 Save 会失败。
func BindStore(s BatchStore) {
	storeMu.Lock()
	batchStore = s
	storeMu.Unlock()
}

func currentStore() BatchStore {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return batchStore
}

// Save 把主任务写入当前存储。输出目录不在这里创建，仍只在执行时创建。
func (b *Batch) Save() error {
	s := currentStore()
	if s == nil {
		return errors.New("任务存储未初始化")
	}
	if err := s.SaveTask(b); err != nil {
		return fmt.Errorf("保存任务: %w", err)
	}
	return nil
}

// LoadTask 按 ID 从当前存储读取主任务。
func LoadTask(id string) (*Batch, error) {
	s := currentStore()
	if s == nil {
		return nil, errors.New("任务存储未初始化")
	}
	b, err := s.LoadTask(id)
	if err != nil {
		return nil, fmt.Errorf("读取任务: %w", err)
	}
	return b, nil
}

// ListTasks 返回存储中的全部主任务。
func ListTasks() ([]*Batch, error) {
	s := currentStore()
	if s == nil {
		return nil, errors.New("任务存储未初始化")
	}
	list, err := s.ListTasks()
	if err != nil {
		return nil, fmt.Errorf("列出任务: %w", err)
	}
	return list, nil
}

// RecoverStored 把仍标记为运行中或暂停的任务改为 interrupted 并写回。
// 在程序启动、队列开始之前调用，避免把崩溃前的状态当成还在执行。
func RecoverStored() error {
	list, err := ListTasks()
	if err != nil {
		return err
	}
	for _, b := range list {
		if b.MarkInterrupted() {
			if err := b.Save(); err != nil {
				return err
			}
		}
	}
	return nil
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
