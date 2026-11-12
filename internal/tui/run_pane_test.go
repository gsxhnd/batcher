package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyPress 构造单字符按键消息。
func keyPress(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
}

// teaWheelUp / teaWheelDown 构造滚轮消息。
func teaWheelUp() tea.Msg   { return tea.MouseWheelMsg{Button: tea.MouseWheelUp} }
func teaWheelDown() tea.Msg { return tea.MouseWheelMsg{Button: tea.MouseWheelDown} }

// TestRefreshRunVpHeights 回归：视口必须设置高度，否则预览/日志渲染为空。
func TestRefreshRunVpHeights(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 34
	m.resizePanes()
	assert.Positive(t, m.logVp.Height(), "日志视口应有高度")

	// 执行中的子任务：日志高度 = 主体内容 - 固定区（命令、进度、分隔线）。
	m.running = true
	m.total = 3
	m.current = "ffmpeg -i a.mkv -c copy out/a.mkv"
	m.resizePanes()
	inner := max(m.bodyH()-2, 2)
	assert.Equal(t, inner-m.taskChromeH(), m.logVp.Height())
	assert.GreaterOrEqual(t, m.logVp.Height(), 2, "日志视口至少保留 2 行")
}

// TestPinnedCmdRender 执行中主体最上方应有固定的完整命令区。
func TestPinnedCmdRender(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 34
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.current = "ffmpeg -i /very/long/path/a.mkv -c copy " + strings.Repeat("-metadata a=b ", 20) + "out/a.mkv"

	out := m.render()
	require.Contains(t, out, "固定 · 当前任务完整命令", "应显示置顶命令区标题")
	require.Contains(t, out, "ffmpeg -i /very/long/path/a.mkv", "完整命令应显示（含前段）")
	require.Contains(t, out, "out/a.mkv", "长命令折行后结尾不应缺失")
}

// TestLogFollowManualScroll 验证日志跟随与手动滚动暂停/恢复。
func TestLogFollowManualScroll(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 20
	m.resizePanes()
	m.running = true
	m.files = []fileItem{{input: "a.mkv", cmd: "ffmpeg -i a.mkv o.mkv"}}
	m.logFollow = true
	m.refreshRunHeights()
	m.logVp.SetHeight(3)

	const total = 20
	for i := 0; i < total; i++ {
		m.appendLog(fmt.Sprintf("输出行 %02d", i+1), logLineDefaultStyle)
	}
	m.syncLogPane()
	assert.Positive(t, m.logVp.YOffset(), "跟随模式应贴到最后")

	// 用户向上滚动后停止跟随：新输出不再强制贴底。
	press(&m, "pgup")
	assert.False(t, m.logFollow, "向上滚动后应暂停跟随")
	before := m.logVp.YOffset()
	m.appendLog("新的输出", logLineDefaultStyle)
	m.syncLogPane()
	assert.Equal(t, before, m.logVp.YOffset(), "新输出不应打断查看历史")

	// 回到底部自动恢复跟随。
	press(&m, "end")
	assert.True(t, m.logFollow, "end 回到底部后应恢复跟随")
	after := m.logVp.YOffset()
	m.appendLog("底部后的输出", logLineDefaultStyle)
	m.syncLogPane()
	assert.Greater(t, m.logVp.YOffset(), after, "贴底跟随继续工作")
}

// TestCopyShownCommand 验证 y 复制命令与提示信息。
func TestCopyShownCommand(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 34
	m.rows() // 填充预览文件列表
	m.focus = panelRun
	m.fileCursor = 1

	// 队列为空时没有可复制的命令。
	m2 := newModel()
	m2.focus = panelRun
	cmd := m2.updateRunPanel(keyPress("y"))
	assert.Nil(t, cmd)
	assert.Contains(t, m2.statusMsg, "尚无命令")

	// 执行中复制置顶命令
	m.running = true
	m.current = "ffmpeg -i a.mkv out/a.mkv"
	cmd = m.updateRunPanel(keyPress("y"))
	require.NotNil(t, cmd, "应返回写入剪贴板的命令")
	assert.Contains(t, m.statusMsg, "完整命令")

	// 没有队列行时不从预览文件列表复制。
	m.running = false
	m.current = ""
	cmd = m.updateRunPanel(keyPress("y"))
	assert.Nil(t, cmd)
	assert.Contains(t, m.statusMsg, "尚无命令")
}

// TestMouseWheelPausesFollow 滚轮向上滚动后应暂停日志跟随，回到底部恢复。
func TestMouseWheelPausesFollow(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 20
	m.resizePanes()
	m.running = true
	m.focus = panelRun
	m.files = []fileItem{{input: "a.mkv", cmd: "ffmpeg -i a.mkv o.mkv"}}
	m.logFollow = true
	m.refreshRunHeights()
	m.logVp.SetHeight(3)

	for i := 0; i < 30; i++ {
		m.appendLog(fmt.Sprintf("输出 %02d", i+1), logLineDefaultStyle)
	}
	m.syncLogPane()
	require.Positive(t, m.logVp.YOffset())

	before := m.logVp.YOffset()
	next, _ := m.Update(teaWheelUp())
	m = next.(model)
	assert.False(t, m.logFollow, "滚轮向上应暂停跟随")
	assert.Less(t, m.logVp.YOffset(), before, "滚轮向上应滚动内容")

	// 滚轮回顶部计数器后恢复跟随。
	for range 10 {
		next, _ = m.Update(teaWheelDown())
		m = next.(model)
		if m.logFollow {
			break
		}
	}
	assert.True(t, m.logFollow, "滚轮回到底部后应恢复跟随")
}

func TestSubtaskDetailFitsTheBox(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 34
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.total = 1
	m.filesBatch = "b"
	m.files = []fileItem{{input: "a.mkv", cmd: "ffmpeg -i a.mkv -c copy " + strings.Repeat("pad ", 30) + "o.mkv"}}
	m.roster = []rosterRow{{batchID: "b", itemID: "001", title: "a.mkv", cmd: m.files[0].cmd, state: fileRunning}}
	m.fileCursor = 0
	m.refreshRunHeights()
	m.syncLogPane()
	check := func(name string) {
		t.Helper()
		m.refreshRunHeights()
		m.syncLogPane()
		body := m.taskDetail()
		limit := m.bodyH() - 2
		n := len(strings.Split(strings.TrimSuffix(body, "\n"), "\n"))
		t.Logf("%s lines=%d limit=%d chrome=%d vp=%d", name, n, limit, m.taskChromeH(), m.logVp.Height())
		assert.LessOrEqual(t, n, limit, name)
		assert.NotContains(t, m.render(), "行已折叠", name)
	}
	check("子任务")
	m.statusMsg = "已重新排队"
	check("有提示")
	m.height = 16
	m.resizePanes()
	check("矮窗口")
	m.roster = []rosterRow{{batchID: "b", title: "主任务"}}
	m.fileCursor = 0
	check("主任务")
}
