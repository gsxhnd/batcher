package ffmpeg

import (
	"strings"
	"sync"
)

type EventType string

const (
	EventBatchStart      EventType = "batch_start"
	EventCommandStart    EventType = "command_start"
	EventCommandDone     EventType = "command_done"
	EventCommandFailed   EventType = "command_failed"
	EventCommandCanceled EventType = "command_canceled"
	EventLog             EventType = "log"
)

// Event 描述批处理执行过程中的状态变化，供 TUI 展示进度。
type Event struct {
	Type    EventType `json:"type"`
	Index   int       `json:"index"`
	Total   int       `json:"total"`
	Command string    `json:"command,omitempty"`
	Message string    `json:"message,omitempty"`
}

// lineWriter 将写入的数据按行（\n 或 \r）切分后回调，用于捕获 ffmpeg 输出。
type lineWriter struct {
	mu   sync.Mutex
	buf  strings.Builder
	emit func(line string)
}

func newLineWriter(emit func(line string)) *lineWriter {
	return &lineWriter{emit: emit}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, b := range p {
		if b == '\n' || b == '\r' {
			w.flushLocked()
			continue
		}
		w.buf.WriteByte(b)
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
}

func (w *lineWriter) flushLocked() {
	line := strings.TrimSpace(w.buf.String())
	w.buf.Reset()
	if line != "" {
		w.emit(line)
	}
}
