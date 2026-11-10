package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFileItemsExtractsInputAndOutput(t *testing.T) {
	items := buildFileItems([]ffmpeg.Job{
		{Input: "src/a.mkv", Output: "out/a.webm", Args: []string{"-nostdin", "-n", "-i", "src/a.mkv", "-c", "copy", "out/a.webm"}},
		{Input: "src/b.mkv", Output: "out/b.mkv", Args: []string{"-nostdin", "-n", "-i", "src/b.mkv", "out/b.mkv"}},
	})
	require.Len(t, items, 2)
	assert.Equal(t, "src/a.mkv", items[0].input)
	assert.Equal(t, "out/a.webm", items[0].output)
	assert.True(t, strings.HasPrefix(items[0].cmd, "ffmpeg "), "cmd 应为格式化后的完整命令")
	assert.Equal(t, filePending, items[0].state)
}

func TestPlanMsgBuildsFileList(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.planSeq = 1

	next, _ := m.Update(planMsg{seq: 1, plan: testPlan("a.mkv", "b.mkv")})
	m = next.(model)

	assert.True(t, m.previewDone)
	require.Len(t, m.files, 2)
	assert.Equal(t, 0, m.fileCursor, "进入任务面板应选中“全部”")
	assert.Contains(t, m.previewSummary, "共生成 2 条命令")
}

// testPlan 提供显式输入/输出的计划，避免测试依赖命令参数位置。
func testPlan(inputs ...string) *ffmpeg.BatchPlan {
	plan := &ffmpeg.BatchPlan{}
	for _, input := range inputs {
		output := "o/" + input
		plan.Jobs = append(plan.Jobs, ffmpeg.Job{Input: input, Output: output, Args: []string{"-nostdin", "-n", "-i", input, output}})
	}
	return plan
}

func TestPlanMsgWithErrorKeepsEmptyFiles(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.planSeq = 1
	next, _ := m.Update(planMsg{seq: 1, err: assert.AnError})
	m = next.(model)

	assert.True(t, m.previewDone)
	assert.Error(t, m.planErr)
	assert.Empty(t, m.files)
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

// TestSchedulePlanDebounce 验证防抖序号与执行锁。
func TestSchedulePlanDebounce(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.planSeq = 0

	cmd := m.schedulePlan(planDebounceDelay)
	require.NotNil(t, cmd, "配置变化应安排重新生成")
	assert.Equal(t, 1, m.planSeq)
	assert.True(t, m.planLoading)

	// 执行中不再触发。
	m.running = true
	cmd = m.schedulePlan(planDebounceDelay)
	assert.Nil(t, cmd, "执行中不应安排生成")
	assert.Equal(t, 1, m.planSeq, "执行中序号不变")
}

// TestDoneMsgUnlocks 验证执行结束时解锁并保留配置以便重跑。
func TestDoneMsgUnlocks(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.running = true
	m.total = 2
	m.done = 2
	m.ops = ffmpeg.Selection{Convert: true}
	m.startAt = time.Now().Add(-time.Second)

	next, _ := m.Update(doneMsg{err: nil})
	m = next.(model)
	assert.False(t, m.running, "执行结束应解锁")
	assert.NotEmpty(t, m.statusMsg)
}
