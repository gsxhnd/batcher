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

// TestPinnedCmdScrollsWhenTooLong 命令过长时命令区高度固定、可上下滚动：
// Tab 切换命令/输出滚动焦点，输出区保留可读行数，y 复制的仍是完整命令。
func TestPinnedCmdScrollsWhenTooLong(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 24
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.total = 1
	m.current = "ffmpeg -i a.mkv " + strings.Repeat("-metadata a=b ", 200) + "out/a.mkv"
	m.roster = []rosterRow{{batchID: "b", itemID: "001", title: "a.mkv", cmd: m.current, state: fileRunning}}
	m.fileCursor = 0
	m.refreshRunHeights()
	m.syncLogPane()

	// 高度计算与渲染一致，主体不超出盒子；输出视口保留多行。
	body := m.taskDetail()
	limit := m.bodyH() - 2
	assert.LessOrEqual(t, len(strings.Split(strings.TrimSuffix(body, "\n"), "\n")), limit)
	assert.Greater(t, m.logVp.Height(), 2, "长命令不应把输出区挤到最少行数")
	assert.True(t, m.cmdOverflow(), "长命令超出命令区固定高度")
	assert.Contains(t, m.render(), "Tab 后可上下翻")

	// Tab 聚焦命令区后，↑/↓ 与 pgdn 滚动命令区。
	press(&m, "tab")
	assert.True(t, m.cmdFocus)
	press(&m, "down")
	assert.Equal(t, 1, m.cmdVp.YOffset(), "↓ 应滚动命令区")
	press(&m, "pgdown")
	assert.Greater(t, m.cmdVp.YOffset(), 1, "pgdown 应继续向下翻命令")
	assert.Equal(t, m.current, m.pinnedCmdText(), "y 复制的仍是完整命令")

	// Tab 切回输出后，滚动键不再动命令区。
	offset := m.cmdVp.YOffset()
	press(&m, "tab")
	assert.False(t, m.cmdFocus)
	press(&m, "pgdown")
	assert.Equal(t, offset, m.cmdVp.YOffset(), "输出焦点不滚命令区")
	assert.Less(t, m.logVp.YOffset(), m.logVp.Height(), "输出区仍在自己的视口内滚动")

	// esc 退出命令焦点后，↑/↓ 恢复移动左侧列表。
	press(&m, "tab")
	press(&m, "esc")
	assert.False(t, m.cmdFocus)
	assert.NotContains(t, m.render(), "超出部分已收起", "不再使用收起提示")
}

// TestPinnedCmdShortCommandNoScroll 命令不长时命令区完整显示，滚动键不动它。
func TestPinnedCmdShortCommandNoScroll(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 34
	m.resizePanes()
	m.focus = panelRun
	m.running = true
	m.total = 1
	m.current = "ffmpeg -i a.mkv -c copy out/a.mkv"
	m.roster = []rosterRow{{batchID: "b", itemID: "001", title: "a.mkv", cmd: m.current, state: fileRunning}}
	m.fileCursor = 0
	m.refreshRunHeights()
	m.syncLogPane()

	assert.False(t, m.cmdOverflow(), "短命令不超出固定高度")
	assert.Contains(t, m.taskDetail(), "out/a.mkv", "短命令尾部完整显示")
	press(&m, "tab")
	press(&m, "pgdown")
	assert.Equal(t, 0, m.cmdVp.YOffset(), "内容不超高度时滚动无效")
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

// TestRunPinHMatchesPinnedCmdBlock 回归：runPinH 必须等于 pinnedCmdBlock 的实际渲染行数，
// 否则 refreshRunHeights 算出的输出视口高度会与渲染不符（底部留白或顶出盒子）。
// 覆盖命令为空、短命令（不足固定高度，需补空行）、超长命令，以及多种窗口宽度。
func TestRunPinHMatchesPinnedCmdBlock(t *testing.T) {
	isolatePresets(t)
	longCmd := "ffmpeg -nostdin -n -i /very/long/path/to/video.mkv -map 0 -map -0:s? " +
		strings.Repeat("-metadata:s:s:0 title=Chinese ", 20) + "/out/result.mkv"
	cmds := []struct {
		name string
		cmd  string
	}{
		{"空命令", ""},
		{"短命令", "ffmpeg -i a.mkv -c copy out/a.mkv"},
		{"长命令", longCmd},
	}
	for _, width := range []int{80, 100, 140, 200} {
		for _, tc := range cmds {
			t.Run(fmt.Sprintf("%d/%s", width, tc.name), func(t *testing.T) {
				m := newModel()
				m.width, m.height = width, 34
				m.resizePanes()
				m.running = true
				m.activeBatch = "b1"
				m.roster = []rosterRow{{batchID: "b1", itemID: "001", title: "a.mkv", cmd: tc.cmd, state: fileRunning}}
				m.fileCursor = 0
				m.files = []fileItem{{cmd: tc.cmd, state: fileRunning}}
				m.filesBatch = "b1"

				rendered := strings.Count(strings.TrimSuffix(m.pinnedCmdBlock(), "\n"), "\n") + 1
				assert.Equal(t, rendered, m.runPinH(), "runPinH 应与实际渲染行数一致")
			})
		}
	}
}

// Tab 从命令区切回输出后，行滚动与翻页都作用于输出；esc 仍逐层退出焦点。
func TestTabRestoresOutputScrolling(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 100, 24
	m.focus = panelRun
	m.running, m.total = true, 1
	m.filesBatch = "b"
	longCmd := "ffmpeg -i a.mkv " + strings.Repeat("-metadata a=b ", 80) + "out/a.mkv"
	logs := make([]string, 30)
	for i := range logs {
		logs[i] = fmt.Sprintf("输出 %02d", i+1)
	}
	m.files = []fileItem{{
		input: "a.mkv", cmd: longCmd, state: fileRunning,
		attempts: []attemptLog{{n: 1, status: fileRunning, lines: logs}},
	}}
	m.roster = []rosterRow{{batchID: "b", itemID: "001", attempt: 1, title: "第 1 次", cmd: longCmd}}
	m.fileCursor = 0
	m.logFollow = false
	m.resizePanes()
	m.syncLogPane()
	require.Positive(t, m.logVp.TotalLineCount())

	press(&m, "tab")
	require.True(t, m.cmdFocus)
	press(&m, "down")
	assert.Equal(t, 1, m.cmdVp.YOffset())
	press(&m, "pgdown")
	assert.Greater(t, m.cmdVp.YOffset(), 1)

	press(&m, "tab")
	assert.False(t, m.cmdFocus)
	assert.True(t, m.outputFocus, "切回输出区必须设置 outputFocus")
	cmdOffset := m.cmdVp.YOffset()
	press(&m, "down")
	assert.Equal(t, 1, m.logVp.YOffset(), "上下键应滚动输出而不是移动任务列表")
	assert.Equal(t, 0, m.fileCursor)
	assert.Equal(t, cmdOffset, m.cmdVp.YOffset())
	press(&m, "pgdown")
	assert.Greater(t, m.logVp.YOffset(), 1)
	before := m.logVp.YOffset()
	press(&m, "up")
	assert.Less(t, m.logVp.YOffset(), before)

	press(&m, "tab")
	assert.True(t, m.cmdFocus)
	assert.True(t, m.outputFocus, "命令焦点覆盖在输出查看之上")
	press(&m, "esc")
	assert.False(t, m.cmdFocus)
	assert.True(t, m.outputFocus, "第一次 esc 只退出命令焦点")
	press(&m, "esc")
	assert.False(t, m.outputFocus, "第二次 esc 才退出输出查看")
}
