package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/command"
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
		"参数面板":  func(m *model) { m.focus = panelForm },
		"任务面板":  func(m *model) { m.focus = panelRun },
		"执行中":   func(m *model) { m.running = true; m.total = 2; m.focus = panelRun },
		"执行完成":  func(m *model) { m.total = 2; m.done = 2; m.focus = panelRun },
		"计划失败":  func(m *model) { m.planErr = assertAnError(); m.focus = panelRun },
		"弹窗-预设": func(m *model) { m.openPresetModal(-1, presets.Preset{}) },
		"弹窗-创建": func(m *model) { m.openTaskModal() },
		"弹窗-确认": func(m *model) { m.openConfirmModal("删除", "确定？", nil) },
		"表单编辑":  func(m *model) { m.focus = panelForm; m.editing = true },
		"勾选组合":  func(m *model) { m.rows(); m.focus = panelOps },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			isolatePresets(t)
			m := newModel()
			m.width, m.height = 100, 34
			m.ops = command.Selection{Convert: true, AddSubtitle: true}
			m.subs = []command.SubtitleOption{{Suffix: "ass", Lang: "chi", Title: "中文"}}
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
	m.previewDone = true
}

func assertAnError() error { return &fakeErr{} }

type fakeErr struct{}

func (e *fakeErr) Error() string { return "测试错误" }

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

func TestFormBodyShowsSelectedFieldAndItsFiles(t *testing.T) {
	isolatePresets(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.mp4"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "不要列出.txt"), []byte("x"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.mp4"), []byte("x"), 0o644))

	m := newModel()
	m.width, m.height = 100, 34
	m.resizePanes()
	m.focus = panelForm
	m.ops = command.Selection{Convert: true, AddFonts: true}
	m.rebuildFields()
	setField(&m, "input_path", dir)
	setField(&m, "input_format", "mp4")
	setField(&m, "output_path", t.TempDir())
	m.focusNo = 0
	loadDirList(t, &m)

	body := m.formBody()
	assert.Contains(t, body, "源视频路径")
	assert.Contains(t, body, "a.mp4")
	assert.Contains(t, body, filepath.Join("sub", "b.mp4"))
	assert.NotContains(t, body, "不要列出.txt")
	assert.NotContains(t, body, "输出目录")
	assert.NotContains(t, body, "字体文件夹")

	for i := range m.fields {
		if m.fields[i].def.key == "output_format" {
			m.focusNo = i
		}
	}
	body = m.formBody()
	assert.Contains(t, body, "输出后缀")
	assert.NotContains(t, body, "a.mp4", "非文件夹参数不显示文件列表")
	assert.NotContains(t, body, "源视频路径")

	fontDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fontDir, "a.ttf"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fontDir, "no.mp4"), []byte("x"), 0o644))
	setField(&m, "fonts_path", fontDir)
	for i := range m.fields {
		if m.fields[i].def.key == "fonts_path" {
			m.focusNo = i
		}
	}
	loadDirList(t, &m)
	body = m.formBody()
	assert.Contains(t, body, "字体文件夹")
	assert.Contains(t, body, "a.ttf")
	assert.NotContains(t, body, "no.mp4")
	assert.NotContains(t, body, "源视频路径")

	next, _ := m.Update(dirListMsg{seq: m.dirSeq - 1, files: []string{"gone.mp4"}})
	m = next.(model)
	assert.NotContains(t, m.formBody(), "gone.mp4")
}

func setField(m *model, key, value string) {
	f, ok := findField(&m.fields, key)
	if !ok {
		panic("缺少字段 " + key)
	}
	f.input.SetValue(value)
}

func loadDirList(t *testing.T, m *model) {
	t.Helper()
	cmd := m.scheduleDirList(0)
	require.NotNil(t, cmd)
	next, _ := m.Update(cmd())
	*m = next.(model)
}

// TestPresetsPaneScrolls 长列表超出面板高度时按光标滚动窗口，光标行始终可见。
func TestPresetsPaneScrolls(t *testing.T) {
	isolatePresets(t)
	m := newModel()
	m.presets = []presets.Preset{
		{Args: "custom-aa"}, {Args: "custom-bb"}, {Args: "custom-cc"}, {Args: "custom-dd"},
	}
	m.focus = panelPresets
	m.presetCursor = m.presetUserOffset() + 3 // 最后一条自定义预设

	out := m.presetsPane(3)
	require.Contains(t, out, "custom-bb", "窗口应包含光标前的条目")
	require.Contains(t, out, "custom-cc")
	require.Contains(t, out, "custom-dd", "光标行必须可见")
	require.NotContains(t, out, "custom-aa", "窗口外的早期条目应被滚出")
}

// TestPanelHeightsFillBody 焦点面板展开、其余折叠 2 行，总内容行数须恰好填满主体区。
func TestPanelHeightsFillBody(t *testing.T) {
	isolatePresets(t)
	base := newModel()
	for _, h := range []int{24, 30, 40} {
		m := base
		m.width, m.height = 120, h
		for _, focus := range []panel{panelOps, panelPresets, panelSubs, panelForm, panelRun} {
			sub := m
			sub.focus = focus
			hs := sub.panelHeights()
			sum := 0
			for _, v := range hs {
				sum += v
			}
			assert.Equal(t, sub.bodyH()-int(panelCount)*2, sum, "height=%d focus=%d", h, focus)
			assert.GreaterOrEqual(t, hs[focus], 3, "聚焦面板至少 3 行内容")
		}
	}
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

// TestRenderRowsUniform 整页渲染的每一行显示宽度都应等于终端宽度。
func TestRenderRowsUniform(t *testing.T) {
	isolatePresets(t)
	base := newModel()
	for _, size := range [][2]int{{120, 30}, {100, 34}, {70, 24}, {96, 40}} {
		m := base
		m.width, m.height = size[0], size[1]
		m.ops = command.Selection{Convert: true, AddSubtitle: true}
		m.rebuildFields()
		m.subs = []command.SubtitleOption{{Suffix: "ass", Lang: "chi"}}
		m.previewDone = true
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
