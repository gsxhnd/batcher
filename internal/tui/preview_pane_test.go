package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFileItemsExtractsInputAndOutput(t *testing.T) {
	items := buildFileItems([]command.Job{
		{Input: "src/a.mkv", Output: "out/a.webm", Args: []string{"-nostdin", "-n", "-i", "src/a.mkv", "-c", "copy", "out/a.webm"}},
		{Input: "src/b.mkv", Output: "out/b.mkv", Args: []string{"-nostdin", "-n", "-i", "src/b.mkv", "out/b.mkv"}},
	})
	require.Len(t, items, 2)
	assert.Equal(t, "src/a.mkv", items[0].input)
	assert.Equal(t, "out/a.webm", items[0].output)
	assert.True(t, strings.HasPrefix(items[0].cmd, "ffmpeg "), "cmd 应为格式化后的完整命令")
	assert.Equal(t, filePending, items[0].state)
}

func testPlan(inputs ...string) *command.BatchPlan {
	plan := &command.BatchPlan{}
	for _, input := range inputs {
		output := "o/" + input
		plan.Jobs = append(plan.Jobs, command.Job{Input: input, Output: output, Args: []string{"-nostdin", "-n", "-i", input, output}})
	}
	return plan
}

// testPlanOut 同 testPlan，但把每个任务的输出放到临时目录——
// 供入队（batch 记录写入输出目录）的测试使用，避免向测试进程的工作目录写文件。
func testPlanOut(t *testing.T, inputs ...string) *command.BatchPlan {
	t.Helper()
	plan := testPlan(inputs...)
	base := t.TempDir()
	for i := range plan.Jobs {
		plan.Jobs[i].Output = filepath.Join(base, fmt.Sprintf("out%d.mkv", i))
	}
	return plan
}

func TestHandleEventRoutesByFile(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.files = []fileItem{
		{input: "a.mkv", cmd: "ffmpeg -i a.mkv o/a.mkv"},
		{input: "b.mkv", cmd: "ffmpeg -i b.mkv o/b.mkv"},
	}
	m.fileFollow = true
	m.running = true

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandStart, Index: 0, Total: 2, Command: "ffmpeg -i a.mkv"})
	assert.Equal(t, fileRunning, m.files[0].state)
	assert.Equal(t, 1, m.fileCursor, "自动跟随应切到正在执行的文件")

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventLog, Index: 0, Total: 2, Message: "Stream #0"})
	require.Len(t, m.files[0].logs, 2, "命令开始行与输出行")
	assert.Empty(t, m.files[1].logs, "其他文件不应收到日志")

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandDone, Index: 0, Total: 2})
	assert.Equal(t, fileDone, m.files[0].state)
	assert.Equal(t, m.done, 1)

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventCommandFailed, Index: 1, Total: 2, Message: "boom"})
	assert.Equal(t, fileFailed, m.files[1].state)
	assert.Equal(t, m.failed, 1)
	assert.Contains(t, m.logs[len(m.logs)-1], "失败", "全局日志应记录失败")
}

func TestMoveFilePaneBoundsAndFollow(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.running = true
	m.files = []fileItem{
		{input: "a.mkv"}, {input: "b.mkv"}, {input: "c.mkv"},
	}
	m.fileFollow = true
	m.fileCursor = 0

	m.moveFilePane(1)
	assert.Equal(t, 1, m.fileCursor)
	assert.False(t, m.fileFollow, "手动切换应停止自动跟随")

	// 越界移动应停在边界
	m.moveFilePane(-5)
	assert.Equal(t, 0, m.fileCursor)
	m.moveFilePane(5)
	assert.Equal(t, 3, m.fileCursor)

	// f 恢复跟随正在执行的文件
	m.runIdx = 1
	m.files[1].state = fileRunning
	m.moveFilePane(-1)
	press(&m, "f")
	assert.Equal(t, 2, m.fileCursor, "f 应回到正在执行的文件")
	assert.True(t, m.fileFollow)
}

func TestDoneMsgUnlocks(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.running = true
	m.total = 2
	m.done = 2
	m.startAt = time.Now().Add(-time.Second)

	next, _ := m.Update(doneMsg{err: nil})
	m = next.(model)
	assert.False(t, m.running, "执行结束应解锁")
	assert.NotEmpty(t, m.statusMsg)
}
