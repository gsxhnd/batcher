package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestViewsRender 确保每种面板状态的视图都能渲染（不 panic，输出非空）。
func TestViewsRender(t *testing.T) {
	cases := map[string]func(m *model){
		"操作面板":  func(m *model) { m.focus = panelOps },
		"预设面板":  func(m *model) { m.rows(); m.focus = panelPresets },
		"字幕面板":  func(m *model) { m.focus = panelSubs },
		"任务面板":  func(m *model) { m.focus = panelRun },
		"执行中":   func(m *model) { m.running = true; m.total = 2; m.focus = panelRun },
		"执行完成":  func(m *model) { m.total = 2; m.done = 2; m.focus = panelRun },
		"弹窗-预设": func(m *model) { m.openPresetModal(-1, presets.Preset{}) },
		"弹窗-创建": func(m *model) { m.openTaskModal() },
		"弹窗-确认": func(m *model) { m.openConfirmModal("删除", "确定？", nil) },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			isolatePresets(t)
			m := newModel()
			m.width, m.height = 100, 34
			setup(&m)
			out := m.render()
			require.NotEmpty(t, out, "视图不应为空")
		})
	}
}

// rows 填充测试用的文件列表。
func (m *model) rows() {
	m.files = []fileItem{
		{input: "a.mkv", cmd: "ffmpeg -i a.mkv o/a.mkv"},
		{input: "b.mkv", cmd: "ffmpeg -i b.mkv o/b.mkv"},
	}
}

// TestViewsRenderNarrow 窄终端下仍应正常渲染。
func TestViewsRenderNarrow(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 70, 24
	m.rows()
	m.focus = panelRun
	out := m.render()
	require.NotEmpty(t, out)
}

// TestWindowRows 光标在头部/尾部/中间时窗口都能正确取行。
func TestWindowRows(t *testing.T) {
	rows := []string{"r0", "r1", "r2", "r3", "r4"}
	assert.Equal(t, []string{"r0", "r1", "r2"}, windowRows(rows, 0, 3))
	assert.Equal(t, []string{"r2", "r3", "r4"}, windowRows(rows, 4, 3))
	assert.Equal(t, []string{"r0", "r1", "r2"}, windowRows(rows, 2, 3), "未超出底边时窗口不动，光标在窗口末行可见")
	assert.Empty(t, windowRows(rows, 0, 0), "高度为 0 时应无输出")
}

// TestDrawBoxWidthsAligned 盒子每一行（顶边/内容行/底边）显示宽度必须一致，
// 中文标题、超长标题、彩色内容点等各种情况都不得错位。
func TestDrawBoxWidthsAligned(t *testing.T) {
	cases := []struct {
		name  string
		w     int
		title string
		body  string
	}{
		{"短标题", 34, "1 · 操作", "▸ [✔] 视频转码\n"},
		{"中文全角标题", 40, "5 · 任务（12 个文件）", "1. .ass  chi\n"},
		{"标题超长", 20, "2 · 转码预设超长标题测试", "▸ 无损封装\n"},
		{"窄盒", 12, "3 · 字幕轨道", "（空）\n"},
		{"空内容", 30, "4 · 参数", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := drawBox(tc.w, tc.title, true, true, tc.body, 3)
			lines := strings.Split(out, "\n")
			require.NotEmpty(t, lines)
			require.Equal(t, tc.w, lipgloss.Width(lines[0]), "顶边宽度应等于盒子宽度")
			for i, ln := range lines {
				assert.Equal(t, tc.w, lipgloss.Width(ln), "第 %d 行宽度不一致: %q", i+1, ln)
			}
		})
	}
}

// TestErrorToastTopRight 以 × 开头的提示浮在右上角，普通提示仍留在面板里。
func TestErrorToastTopRight(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 30
	m.resizePanes()
	m.statusMsg = "× 入队失败: 目录不存在"
	out := m.render()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	require.NotEmpty(t, lines)
	for i, ln := range lines {
		assert.Equal(t, m.width, lipgloss.Width(ln), "第 %d 行宽度不一致", i+1)
	}
	found := -1
	for i, ln := range lines {
		if strings.Contains(ansi.Strip(ln), "入队失败") {
			found = i
			break
		}
	}
	require.NotEqual(t, -1, found, "错误提示应出现在画面上")
	assert.LessOrEqual(t, found, 2, "错误应靠近顶栏")
	assert.GreaterOrEqual(t, found, 1)
	assert.Contains(t, ansi.Strip(lines[found-1]), "╭", "错误提示应是圆角卡片")
	stripped := ansi.Strip(lines[found])
	idx := strings.Index(stripped, "入队失败")
	assert.Greater(t, idx, m.width/2, "错误文本应靠右，实际列 %d: %q", idx, stripped)

	m.statusMsg = "已重新排队"
	info := ansi.Strip(m.render())
	assert.Contains(t, info, "已重新排队")
	assert.NotContains(t, info, "入队失败")
}

// TestRenderRowsUniform 整页渲染的每一行显示宽度都应等于终端宽度。
func TestRenderRowsUniform(t *testing.T) {
	isolatePresets(t)
	base := newModel()
	for _, size := range [][2]int{{120, 30}, {100, 34}, {70, 24}, {96, 40}} {
		m := base
		m.width, m.height = size[0], size[1]
		m.files = []fileItem{{input: "ep01.mkv", cmd: "ffmpeg -i ep01.mkv o.mkv"}}
		out := m.render()
		for i, ln := range strings.Split(out, "\n") {
			assert.Equal(t, size[0], lipgloss.Width(ln), "第 %d 行宽度应为 %d: %q", i+1, size[0], ln)
		}
	}
}

func TestTaskModalFillsTheScreen(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.width, m.height = 120, 40
	m.openTaskModal()
	box := m.taskModalView()
	lines := strings.Split(strings.TrimSuffix(box, "\n"), "\n")
	require.NotEmpty(t, lines)
	assert.LessOrEqual(t, lipgloss.Width(lines[0]), 76, "创建弹窗不应铺满终端宽度")
	assert.GreaterOrEqual(t, lipgloss.Width(lines[0]), 60)
	assert.LessOrEqual(t, len(lines), 24, "创建弹窗不应铺满终端高度")
	assert.GreaterOrEqual(t, len(lines), 14)
	width := lipgloss.Width(lines[0])
	for i, ln := range lines {
		assert.Equal(t, width, lipgloss.Width(ln), "第 %d 行宽度不一致", i+1)
	}
	assert.Contains(t, box, "操作")
	assert.Contains(t, box, "源与输出")
	m.modal.focus = len(m.modal.fields) - 1
	lower := m.taskModalView()
	assert.Contains(t, lower, "字幕")
	out := m.render()
	for i, ln := range strings.Split(out, "\n") {
		assert.Equal(t, m.width, lipgloss.Width(ln), "压暗后的整页第 %d 行宽度不一致", i+1)
	}
	assert.Contains(t, out, "新建任务")
}
