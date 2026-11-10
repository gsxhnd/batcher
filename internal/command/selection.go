package command

import "strings"

// Operation 是单个操作的名称，用于日志与界面标题。
type Operation string

const (
	OperationConvert     Operation = "convert"
	OperationAddSubtitle Operation = "add_sub"
	OperationAddFonts    Operation = "add_fonts"
)

// Selection 描述一次批处理中启用哪些操作，三者可任意组合（需至少一项）。
type Selection struct {
	Convert     bool `json:"convert"`     // 转码：使用 Advance 自定义编码参数，输出 OutputFormat 格式
	AddSubtitle bool `json:"addSubtitle"` // 添加字幕：把与视频同名的字幕文件（可多条）封装进视频
	AddFonts    bool `json:"addFonts"`    // 添加字体：把 FontsPath 下的字体文件内嵌到视频
}

// Any 返回是否至少勾选了一项操作。
func (s Selection) Any() bool {
	return s.Convert || s.AddSubtitle || s.AddFonts
}

// String 返回选中操作的可读描述，用于日志与界面标题。
func (s Selection) String() string {
	var parts []string
	if s.Convert {
		parts = append(parts, string(OperationConvert))
	}
	if s.AddSubtitle {
		parts = append(parts, string(OperationAddSubtitle))
	}
	if s.AddFonts {
		parts = append(parts, string(OperationAddFonts))
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, "+")
}
