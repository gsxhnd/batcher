package ffmpeg

import (
	"bytes"
	"strings"
	"sync"
)

// maxLineBytes 是一行输出的上限。超过后先交出已有内容，避免没有换行的流把缓冲撑大。
const maxLineBytes = 64 << 10

type EventType string

const (
	EventCommandStart    EventType = "command_start"
	EventCommandDone     EventType = "command_done"
	EventCommandFailed   EventType = "command_failed"
	EventCommandCanceled EventType = "command_canceled"
	EventLog             EventType = "log"
	EventProgress        EventType = "progress"
)

// Event 描述批处理执行过程中的状态变化，供 TUI 展示进度。
type Event struct {
	Type    EventType `json:"type"`
	Index   int       `json:"index"`
	Total   int       `json:"total"`
	Command string    `json:"command,omitempty"`
	Message string    `json:"message,omitempty"`
	// Progress 仅在 EventProgress 事件中有值。
	Progress *Progress `json:"progress,omitempty"`
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
	n := len(p)
	w.mu.Lock()
	defer w.mu.Unlock()

	for len(p) > 0 {
		i := bytes.IndexAny(p, "\r\n")
		if i < 0 {
			w.appendLocked(p)
			break
		}
		w.appendLocked(p[:i])
		w.flushLocked()
		if p[i] == '\r' && i+1 < len(p) && p[i+1] == '\n' {
			p = p[i+2:]
			continue
		}
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) appendLocked(p []byte) {
	for len(p) > 0 {
		room := maxLineBytes - w.buf.Len()
		if room <= 0 {
			w.flushLocked()
			room = maxLineBytes
		}
		n := min(room, len(p))
		w.buf.Write(p[:n])
		p = p[n:]
	}
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
