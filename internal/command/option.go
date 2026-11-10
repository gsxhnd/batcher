// Package command 把配置与操作组合规划成 ffmpeg 命令，只读规划：
// 不创建目录、不写文件，也不启动外部进程。
package command

// VideoBatchOption 描述一次批量处理所需的全部参数。
type VideoBatchOption struct {
	InputPath      string `json:"inputPath"`
	InputFormat    string `json:"inputFormat"`
	OutputPath     string `json:"outputPath"`
	OutputFormat   string `json:"outputFormat"`
	FontsPath      string `json:"fontsPath"`
	InputSubSuffix string `json:"inputSubSuffix"`
	// InputSubNo 默认 0，新增字幕排在原字幕之前；非 0 时兼容旧版显式指定原字幕数量的映射。
	InputSubNo    int    `json:"inputSubNo"`
	InputSubTitle string `json:"inputSubTitle"`
	InputSubLang  string `json:"inputSubLang"`
	// Subtitles 为多条字幕列表；为空时回退到 InputSub* 单字幕字段。
	Subtitles []SubtitleOption `json:"subtitles,omitempty"`
	Advance   string           `json:"advance"`
}

// DefaultOption 返回各字段的默认值，TUI 表单初始值来自这里。
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
