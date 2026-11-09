package tui

import (
	"testing"

	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/stretchr/testify/require"
)

// TestViewsRender 确保每个 phase 的视图都能渲染（不 panic，输出非空）。
func TestViewsRender(t *testing.T) {
	phases := []phase{
		phaseFeature, phaseAdvance, phaseSubs, phaseSubEdit,
		phaseForm, phasePreview, phaseRunning, phaseResult,
		phasePresets, phasePresetEdit,
	}
	for _, p := range phases {
		m := newModel()
		m.ops = ffmpeg.Selection{Convert: true, AddSubtitle: true}
		m.subs = []ffmpeg.SubtitleOption{{Suffix: "ass", Lang: "chi", Title: "中文"}}
		m.width, m.height = 100, 30
		m.phase = p
		if p == phaseRunning {
			m.total = 2
		}
		out := m.render()
		require.NotEmpty(t, out, "phase %d 视图不应为空", p)
	}
}

// TestAdvanceViewListsBuiltins 验证转码参数页展示内置预设，且文案区分内置与自定义。
func TestAdvanceViewListsBuiltins(t *testing.T) {
	m := newModel()
	m.ops = ffmpeg.Selection{Convert: true}
	m.width, m.height = 100, 30
	m.phase = phaseAdvance
	m.presets = []string{"-c:v mycustom -crf 30"}

	out := m.advanceView()
	require.Contains(t, out, "-c copy", "应展示第一条内置预设")
	require.Contains(t, out, "内置", "内置条目应有标记")
	require.Contains(t, out, "-c:v mycustom -crf 30", "应展示自定义预设")
	require.Contains(t, out, "(手动输入参数)")
}
