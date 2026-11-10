package ffmpeg

import (
	"context"
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

func TestRunnerWithoutListenerReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Runner{}
	assert.ErrorIs(t, r.Run(ctx, Call{Total: 1, Args: []string{"-version"}}), context.Canceled)
}
