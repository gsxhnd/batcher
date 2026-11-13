package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func eventTypes(events []Event) []EventType {
	types := make([]EventType, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}
	return types
}

func TestRunnerEmitsLifecycleEvents(t *testing.T) {
	var events []Event
	r := &Runner{OnEvent: func(e Event) { events = append(events, e) }}
	err := r.Run(context.Background(), Call{Index: 2, Total: 5, Command: "ffmpeg -version", Args: []string{"-version"}})
	require.NoError(t, err)

	types := eventTypes(events)
	require.NotEmpty(t, types)
	assert.Equal(t, EventCommandStart, types[0])
	assert.Contains(t, types, EventLog)
	assert.Equal(t, EventCommandDone, types[len(types)-1])
	assert.Equal(t, "ffmpeg -version", events[0].Command)
	for _, e := range events {
		assert.Equal(t, 2, e.Index)
		assert.Equal(t, 5, e.Total)
	}
}

func TestRunnerExistingOutputExitZeroIsFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ffmpeg")
	body := "#!/bin/sh\necho \"File '/tmp/1.mkv' already exists. Exiting.\" >&2\necho \"Error opening output file /tmp/1.mkv.\" >&2\nexit 0\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	orig := ffmpegBin
	ffmpegBin = script
	t.Cleanup(func() { ffmpegBin = orig })

	var events []Event
	r := &Runner{OnEvent: func(e Event) { events = append(events, e) }}
	err := r.Run(context.Background(), Call{Total: 1, Args: []string{"-n", "-i", "a.mkv", "out.mkv"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已存在")
	assert.Contains(t, err.Error(), "already exists")
	require.NotEmpty(t, events)
	assert.Equal(t, EventCommandFailed, events[len(events)-1].Type)
	var logged bool
	for _, e := range events {
		if e.Type == EventLog && strings.Contains(e.Message, "already exists") {
			logged = true
		}
	}
	assert.True(t, logged, "stderr 里的已存在提示仍要出现在日志里")
}

func TestRunnerReportsFailure(t *testing.T) {
	var events []Event
	r := &Runner{OnEvent: func(e Event) { events = append(events, e) }}
	err := r.Run(context.Background(), Call{Total: 1, Args: []string{"-i", "not-exist-file.mp4", "-f", "null", "-"}})
	require.Error(t, err)
	require.NotEmpty(t, events)
	assert.Equal(t, EventCommandFailed, events[len(events)-1].Type)
}

func TestRunnerReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var events []Event
	r := &Runner{OnEvent: func(e Event) { events = append(events, e) }}
	err := r.Run(ctx, Call{Total: 1, Args: []string{"-version"}})
	assert.ErrorIs(t, err, context.Canceled)
	require.NotEmpty(t, events)
	assert.Equal(t, EventCommandCanceled, events[len(events)-1].Type)
}

func TestOutputExistsLineIgnoresInputPath(t *testing.T) {
	assert.False(t, outputExistsLine("Input #0, mov, from 'already exists.mp4'"))
	assert.True(t, outputExistsLine("File '/tmp/1.mkv' already exists. Exiting."))
	assert.ErrorIs(t, ErrOutputExists, ErrOutputExists)
	assert.True(t, IsOutputExists(ErrOutputExists))
}

func TestStderrTailKeepsExistsLineBeyondWindow(t *testing.T) {
	var s stderrTail
	s.add("File '/tmp/1.mkv' already exists. Exiting.")
	for i := range 20 {
		s.add(fmt.Sprintf("frame=%d", i))
	}
	// 「输出已存在」的行滚出最近几行后仍要保留，供失败时判断覆盖确认。
	require.Len(t, s.lines, stderrTailKeep)
	assert.Equal(t, "frame=12", s.lines[0])
	assert.Equal(t, "frame=19", s.last())
	assert.Equal(t, "File '/tmp/1.mkv' already exists. Exiting.", s.existsLine())
}

func TestLineWriterCapsLongLine(t *testing.T) {
	var lines []string
	w := newLineWriter(func(line string) { lines = append(lines, line) })
	payload := bytes.Repeat([]byte("a"), maxLineBytes+10)
	_, err := w.Write(payload)
	require.NoError(t, err)
	w.Flush()
	var total int
	for _, line := range lines {
		assert.LessOrEqual(t, len(line), maxLineBytes)
		total += len(line)
	}
	assert.Equal(t, maxLineBytes+10, total)
}

func TestRunnerWithoutListenerReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Runner{}
	assert.ErrorIs(t, r.Run(ctx, Call{Total: 1, Args: []string{"-version"}}), context.Canceled)
}
