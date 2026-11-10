package ffmpeg

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgressParserEmitsOneSnapshotPerBlock(t *testing.T) {
	var got []Progress
	var logs []string
	p := newProgressParser(10*time.Second, func(s Progress) { got = append(got, s) }, func(l string) { logs = append(logs, l) })
	for _, line := range []string{
		"frame=5", "fps=24.50", "stream_0_0_q=-1.0", "total_size=2048",
		"out_time_us=2500000", "out_time_ms=2500000", "out_time=00:00:02.500000",
		"speed=1.25x", "progress=continue",
		"configuration: --prefix=/opt/ffmpeg",
		"out_time_us=N/A", "speed=N/A", "progress=end",
	} {
		p.handle(line)
	}
	require.Len(t, got, 2)
	assert.Equal(t, 2500*time.Millisecond, got[0].OutTime)
	assert.InDelta(t, 1.25, got[0].Speed, 1e-9)
	assert.InDelta(t, 24.5, got[0].FPS, 1e-9)
	assert.Equal(t, int64(2048), got[0].TotalSize)
	assert.False(t, got[0].Done)
	ratio, ok := got[0].Percent()
	require.True(t, ok)
	assert.InDelta(t, 0.25, ratio, 1e-9)
	assert.True(t, got[1].Done)
	assert.Zero(t, got[1].OutTime, "N/A 不能沿用上一块的值")
	assert.Zero(t, got[1].Speed)
	assert.Equal(t, []string{"configuration: --prefix=/opt/ffmpeg"}, logs)
}

func TestProgressPercent(t *testing.T) {
	_, ok := Progress{OutTime: time.Second}.Percent()
	assert.False(t, ok, "总时长未知时不给百分比")
	ratio, ok := Progress{OutTime: 3 * time.Second, Duration: 2 * time.Second}.Percent()
	assert.True(t, ok)
	assert.Equal(t, 1.0, ratio, "超出总时长时按 100% 截断")
}

func TestParsePositiveFloat(t *testing.T) {
	assert.InDelta(t, 1.5, parsePositiveFloat("1.5"), 1e-9)
	for _, s := range []string{"N/A", "", "-1", "0", "inf", "nan"} {
		assert.Zero(t, parsePositiveFloat(s), s)
	}
	assert.False(t, math.IsNaN(parsePositiveFloat("nan")))
}

func TestParseDuration(t *testing.T) {
	got, err := parseDuration("12.500000\n")
	require.NoError(t, err)
	assert.Equal(t, 12500*time.Millisecond, got)
	for _, s := range []string{"N/A\n", "", "0.000000", "-1", "inf", "nan", "1e12"} {
		_, err := parseDuration(s)
		assert.Error(t, err, s)
	}
}

func TestDurationReadsRealMedia(t *testing.T) {
	src := makeSource(t)
	got, err := Duration(context.Background(), src)
	require.NoError(t, err)
	assert.InDelta(t, 2.0, got.Seconds(), 0.6)

	_, err = Duration(context.Background(), filepath.Join(t.TempDir(), "不存在.mkv"))
	assert.Error(t, err)
}

func TestDurationWithoutFFprobe(t *testing.T) {
	useMissingFFprobe(t)
	_, err := Duration(context.Background(), "任意.mkv")
	assert.Error(t, err)
}
