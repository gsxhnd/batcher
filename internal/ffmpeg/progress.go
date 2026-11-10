package ffmpeg

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Progress 是 ffmpeg -progress 输出的一个进度快照。
type Progress struct {
	OutTime   time.Duration `json:"outTime"`             // 已处理的媒体时长
	Duration  time.Duration `json:"duration,omitempty"`  // 输入总时长，未知时为 0
	Speed     float64       `json:"speed,omitempty"`     // 处理速度倍率，未知时为 0
	FPS       float64       `json:"fps,omitempty"`       // 当前帧率，未知时为 0
	TotalSize int64         `json:"totalSize,omitempty"` // 已写出的字节数
	Done      bool          `json:"done,omitempty"`      // progress=end
}

// Percent 返回 0 到 1 之间的完成比例；总时长未知时 ok 为 false。
func (p Progress) Percent() (ratio float64, ok bool) {
	if p.Duration <= 0 {
		return 0, false
	}
	ratio = float64(p.OutTime) / float64(p.Duration)
	return min(max(ratio, 0), 1), true
}

// progressParser 解析 -progress 输出的 key=value 行：以 progress=continue|end 结束一个进度块，
// 每个块提交一次快照；不是进度键的行（例如 -version 的输出）按日志处理。
type progressParser struct {
	duration   time.Duration
	cur        Progress
	onProgress func(Progress)
	onLog      func(string)
}

func newProgressParser(duration time.Duration, onProgress func(Progress), onLog func(string)) *progressParser {
	return &progressParser{duration: duration, cur: Progress{Duration: duration}, onProgress: onProgress, onLog: onLog}
}

func (p *progressParser) handle(line string) {
	key, value, ok := strings.Cut(line, "=")
	if !ok || !isProgressKey(key) {
		p.onLog(line)
		return
	}
	value = strings.TrimSpace(value)
	switch key {
	// out_time_ms 实际也是微秒，命名有历史问题，只用 out_time_us。
	case "out_time_us":
		if n, err := strconv.ParseInt(value, 10, 64); err == nil && n > 0 {
			p.cur.OutTime = time.Duration(n) * time.Microsecond
		}
	case "speed":
		p.cur.Speed = parsePositiveFloat(strings.TrimSuffix(value, "x"))
	case "fps":
		p.cur.FPS = parsePositiveFloat(value)
	case "total_size":
		if n, err := strconv.ParseInt(value, 10, 64); err == nil && n > 0 {
			p.cur.TotalSize = n
		}
	case "progress":
		p.cur.Done = value == "end"
		p.onProgress(p.cur)
		p.cur = Progress{Duration: p.duration}
	}
}

// parsePositiveFloat 解析正数，N/A、负数、无穷与解析失败都返回 0。
func parsePositiveFloat(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || !(f > 0) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func isProgressKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
