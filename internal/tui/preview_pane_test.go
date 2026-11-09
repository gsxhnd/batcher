package tui

import (
	"strings"
	"testing"

	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFileItemsExtractsInputAndOutput(t *testing.T) {
	items := buildFileItems([][]string{
		{"-i", "src/a.mkv", "-c", "copy", "out/a.webm"},
		{"-i", "src/b.mkv", "-sub_charenc", "UTF-8", "out/b.mkv"},
	})
	require.Len(t, items, 2)
	assert.Equal(t, "src/a.mkv", items[0].input)
	assert.Equal(t, "out/a.webm", items[0].output)
	assert.True(t, strings.HasPrefix(items[0].cmd, "ffmpeg "), "cmd 应为格式化后的完整命令")
	assert.Equal(t, filePending, items[0].state)
}

func TestPlanMsgBuildsFileList(t *testing.T) {
	m := newModel()
	m.previewDone = false

	next, _ := m.Update(planMsg{cmds: [][]string{
		{"-i", "a.mkv", "-c", "copy", "o/a.mkv"},
		{"-i", "b.mkv", "-c", "copy", "o/b.mkv"},
	}, err: nil})
	m = next.(model)

	assert.True(t, m.previewDone)
	require.Len(t, m.files, 2)
	assert.Equal(t, 0, m.fileCursor, "进入预览时应选中“全部”")
	assert.Contains(t, m.previewSummary, "共生成 2 条命令")
}

func TestPlanMsgWithErrorKeepsEmptyFiles(t *testing.T) {
	m := newModel()
	next, _ := m.Update(planMsg{err: assert.AnError})
	m = next.(model)

	assert.True(t, m.previewDone)
	assert.Error(t, m.planErr)
	assert.Empty(t, m.files)
}

func TestHandleEventRoutesByFile(t *testing.T) {
	m := newModel()
	m.files = []fileItem{
		{input: "a.mkv", cmd: "ffmpeg -i a.mkv o/a.mkv"},
		{input: "b.mkv", cmd: "ffmpeg -i b.mkv o/b.mkv"},
	}
	m.fileFollow = true
	m.phase = phaseRunning

	m.handleEvent(ffmpeg.Event{Type: ffmpeg.EventBatchStart, Total: 2})
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
	m := newModel()
	m.phase = phaseRunning
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
