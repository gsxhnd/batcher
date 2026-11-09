package ffmpeg

import (
	"fmt"
	"os"
)

// VideoBatchOption 描述一次批量处理所需的全部参数。
type VideoBatchOption struct {
	InputPath      string `json:"inputPath"`
	InputFormat    string `json:"inputFormat"`
	OutputPath     string `json:"outputPath"`
	OutputFormat   string `json:"outputFormat"`
	FontsPath      string `json:"fontsPath"`
	InputSubSuffix string `json:"inputSubSuffix"`
	InputSubNo     int    `json:"inputSubNo"`
	InputSubTitle  string `json:"inputSubTitle"`
	InputSubLang   string `json:"inputSubLang"`
	// Subtitles 为多条字幕列表（TUI/GUI 组合流程使用）；为空时回退到 InputSub* 单字幕字段。
	Subtitles []SubtitleOption `json:"subtitles,omitempty"`
	Advance   string           `json:"advance"`
	Workers   int              `json:"workers"` // 并发工作数

	// OnEvent 不为空时，执行进度和 ffmpeg 输出会通过它回传（可能被多个 goroutine 并发调用，
	// 但调用之间已串行化）；为空时 ffmpeg 输出直接写到进程的 stdout/stderr。
	OnEvent func(Event) `json:"-"`
}

// DefaultOption 返回各字段的默认值，GUI 表单初始值来自这里。
func DefaultOption() VideoBatchOption {
	return VideoBatchOption{
		InputPath:      "./",
		InputFormat:    "mp4",
		OutputPath:     "./result/",
		OutputFormat:   "mkv",
		InputSubSuffix: "ass",
		InputSubNo:     0,
		InputSubLang:   "chi",
		InputSubTitle:  "Chinese",
		Workers:        1,
	}
}

// SubtitleOption 描述一条字幕：文件按 <视频名>.<Suffix> 或 <视频名>.<其他>.<Suffix> 匹配，
// 例如视频 1.mp4 的字幕可以是 1.ass，也可以是 1.chi.ass。
type SubtitleOption struct {
	Suffix string `json:"suffix"` // 字幕扩展名，如 ass / srt
	Lang   string `json:"lang"`   // 语言缩写，如 chi / eng
	Title  string `json:"title"`  // 字幕轨道标题
	// Default 标记该轨道为默认字幕流（mkv 中生成 -disposition:s:<n> default；
	// 其余轨道显式置 0，避免输出中出现多重默认轨道）。
	Default bool `json:"default,omitempty"`
}

// makeDir 创建目录；路径已存在且为目录时直接成功。
func makeDir(fullPath string) error {
	if fullPath == "" {
		return fmt.Errorf("path cannot be empty")
	}

	fs, err := os.Stat(fullPath)
	if err == nil {
		if !fs.IsDir() {
			return fmt.Errorf("path exists but is not a directory: %s", fullPath)
		}
		return nil
	}
	if os.IsNotExist(err) {
		if err := os.MkdirAll(fullPath, os.ModePerm); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		return nil
	}
	return fmt.Errorf("stat path: %w", err)
}
