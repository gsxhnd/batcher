package task

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memStore 是测试用的内存任务存储，避免 task 测试再去打开 bbolt（会和 store 包形成导入环）。
type memStore struct {
	mu    sync.Mutex
	tasks map[string]*Batch
}

func (s *memStore) SaveTask(b *Batch) error {
	if b == nil || b.ID == "" {
		return fmt.Errorf("任务 ID 不能为空")
	}
	cp, err := CloneBatch(b)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks == nil {
		s.tasks = map[string]*Batch{}
	}
	s.tasks[b.ID] = cp
	return nil
}

func (s *memStore) LoadTask(id string) (*Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.tasks[id]
	if !ok {
		return nil, errNotFound(id)
	}
	return CloneBatch(b)
}

func (s *memStore) ListTasks() ([]*Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Batch, 0, len(s.tasks))
	for _, b := range s.tasks {
		cp, err := CloneBatch(b)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}

func (s *memStore) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
	return nil
}

type errNotFound string

func (e errNotFound) Error() string { return "未找到任务 " + string(e) }

func TestMain(m *testing.M) {
	BindStore(&memStore{})
	os.Exit(m.Run())
}

func sampleBatch(t *testing.T) *Batch {
	t.Helper()
	plan, _ := fixturePlan(t)
	return NewBatch("20261110-080000-abcd1234", plan, command.Selection{Convert: true}, fixedNow)
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	b := sampleBatch(t)
	b.Status = StatusRunning
	b.StartedAt = fixedNow.Add(time.Second)
	b.Items[0].Status = StatusFailed
	b.Items[0].Attempts = 2
	b.Items[0].Error = "退出码 1"
	b.Items[0].DurationMs = 1500
	b.Items[0].AddStderr("Invalid data found")
	assert.NoDirExists(t, b.OutputDir, "保存任务不应创建输出目录")

	require.NoError(t, b.Save())
	got, err := LoadTask(b.ID)
	require.NoError(t, err)
	assert.Equal(t, b, got)
}

func TestRecordJSONShape(t *testing.T) {
	b := sampleBatch(t)
	b.ID = "20261110-080001-shape"
	require.NoError(t, b.Save())
	got, err := LoadTask(b.ID)
	require.NoError(t, err)

	data, err := json.Marshal(got)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.EqualValues(t, recordVersion, raw["version"])
	assert.Equal(t, "pending", raw["status"])
	assert.Equal(t, map[string]any{"convert": true, "addSubtitle": false, "addFonts": false}, raw["selection"])
	assert.NotContains(t, raw, "startedAt", "未开始时不写时间字段")

	items, ok := raw["items"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, items)
	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	for _, key := range []string{"id", "input", "output", "args", "planArgs", "status", "attempts", "partialOutput", "durationMs"} {
		assert.Contains(t, first, key)
	}
	option, ok := raw["option"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, option, "OnEvent")
}

func TestSaveOverwritesSameTask(t *testing.T) {
	b := sampleBatch(t)
	b.ID = "20261110-080002-overwrite"
	for i := range 3 {
		b.Items[0].Attempts = i
		require.NoError(t, b.Save())
	}
	got, err := LoadTask(b.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Items[0].Attempts)
	assert.NoDirExists(t, b.OutputDir)
}

func TestSaveRequiresStore(t *testing.T) {
	prev := currentStore()
	BindStore(nil)
	t.Cleanup(func() { BindStore(prev) })
	b := sampleBatch(t)
	assert.Error(t, b.Save())
	_, err := LoadTask(b.ID)
	assert.Error(t, err)
}

func TestLoadRejectsBadRecords(t *testing.T) {
	_, err := LoadTask("不存在的任务")
	assert.ErrorContains(t, err, "未找到任务")
}

func TestRecoverPersistsInterruptedStatus(t *testing.T) {
	b := sampleBatch(t)
	b.ID = "20261110-080003-recover"
	b.Status = StatusRunning
	b.Items[0].Status = StatusRunning
	require.NoError(t, b.Save())

	require.NoError(t, RecoverStored())
	got, err := LoadTask(b.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusInterrupted, got.Status)
	assert.Equal(t, StatusInterrupted, got.Items[0].Status)
}

func TestMarkInterrupted(t *testing.T) {
	b := sampleBatch(t)
	require.GreaterOrEqual(t, len(b.Items), 3)
	b.Status = StatusRunning
	b.Items[0].Status = StatusDone
	b.Items[1].Status = StatusRunning
	b.Items[1].PartialOutput = true

	assert.True(t, b.MarkInterrupted())
	assert.Equal(t, StatusInterrupted, b.Status)
	assert.Equal(t, StatusDone, b.Items[0].Status, "已完成的成品保持不变")
	assert.Equal(t, StatusInterrupted, b.Items[1].Status)
	assert.True(t, b.Items[1].PartialOutput, "保留半成品标记，供重试时清理")
	assert.Equal(t, StatusPending, b.Items[2].Status)
	assert.False(t, b.MarkInterrupted(), "重复标记不再产生变化")

	done := sampleBatch(t)
	done.Status = StatusDone
	assert.False(t, done.MarkInterrupted())

	paused := sampleBatch(t)
	paused.Status = StatusPaused
	assert.True(t, paused.MarkInterrupted())
	assert.Equal(t, StatusInterrupted, paused.Status)
}
