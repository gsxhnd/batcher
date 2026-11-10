package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.NoDirExists(t, b.OutputDir, "保存前输出目录尚未创建")

	require.NoError(t, b.Save())
	assert.Equal(t, filepath.Join(b.OutputDir, ".batcher-"+b.ID+".json"), b.RecordPath())
	assert.FileExists(t, b.RecordPath())

	got, err := Load(b.RecordPath())
	require.NoError(t, err)
	assert.Equal(t, b, got)
}

func TestRecordJSONShape(t *testing.T) {
	b := sampleBatch(t)
	require.NoError(t, b.Save())
	data, err := os.ReadFile(b.RecordPath())
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

func TestSaveLeavesOnlyTheRecord(t *testing.T) {
	b := sampleBatch(t)
	for i := range 3 {
		b.Items[0].Attempts = i
		require.NoError(t, b.Save())
	}
	entries, err := os.ReadDir(b.OutputDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "原子替换后不能遗留临时文件")
	assert.Equal(t, filepath.Base(b.RecordPath()), entries[0].Name())

	got, err := Load(b.RecordPath())
	require.NoError(t, err)
	assert.Equal(t, 2, got.Items[0].Attempts)
}

func TestSaveRejectsInvalidOutputDir(t *testing.T) {
	b := sampleBatch(t)
	b.OutputDir = ""
	assert.Error(t, b.Save())

	file := filepath.Join(t.TempDir(), "占用")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	b.OutputDir = file
	assert.Error(t, b.Save())
}

func TestLoadRejectsBadRecords(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}
	_, err := Load(filepath.Join(dir, "不存在.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = Load(write("坏.json", "{不是 json"))
	assert.ErrorContains(t, err, "解析任务记录")
	_, err = Load(write("无版本.json", `{"id":"x"}`))
	assert.ErrorContains(t, err, "版本")
	_, err = Load(write("未来.json", `{"version":99}`))
	assert.ErrorContains(t, err, "版本")
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

func TestRecoverPersistsInterruptedStatus(t *testing.T) {
	b := sampleBatch(t)
	b.Status = StatusRunning
	b.Items[0].Status = StatusRunning
	require.NoError(t, b.Save())

	got, err := Recover(b.RecordPath())
	require.NoError(t, err)
	assert.Equal(t, StatusInterrupted, got.Status)
	assert.Equal(t, StatusInterrupted, got.Items[0].Status)

	onDisk, err := Load(b.RecordPath())
	require.NoError(t, err)
	assert.Equal(t, StatusInterrupted, onDisk.Status)
	assert.Equal(t, StatusInterrupted, onDisk.Items[0].Status)
}
