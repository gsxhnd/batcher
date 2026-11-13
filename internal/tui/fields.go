package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gsxhnd/batcher/internal/command"
)

type fieldKind int

const (
	fieldString fieldKind = iota
	fieldBool             // 勾选型字段：不吃文本输入，space 切换勾选状态
	fieldChoice           // 若干选项里选一个；新建任务里按 enter 展开列表
)

// fieldDef 描述一个表单项映射到 VideoBatchOption 的哪个配置。
type fieldDef struct {
	key   string
	label string
	kind  fieldKind
}

// subFieldDefs 是单条字幕的编辑字段。
var subFieldDefs = []fieldDef{
	{key: "sub_suffix", label: "字幕后缀"},
	{key: "sub_lang", label: "语言缩写"},
	{key: "sub_title", label: "轨道标题"},
	{key: "sub_default", label: "默认字幕流", kind: fieldBool},
}

// presetFieldDefs 是单条转码参数预设的编辑字段。
var presetFieldDefs = []fieldDef{
	{key: "preset_name", label: "预设名称"},
	{key: "preset_args", label: "高级参数"},
}

// field 保存表单项的运行时状态。
type field struct {
	def        fieldDef
	input      textinput.Model
	err        string
	checked    bool   // bool 字段（fieldBool）的勾选状态
	fallback   string // 输入为空时采用的默认值，以灰色占位显示，不写成白色正文
	options    []string
	choiceArgs []string // 与 options 对齐；空字符串表示不改写高级参数
	choice     int
	laidW      int // 已经设置过的输入框宽度，避免每次绘制都改宽度
}

func (f *field) useWidth(w int) {
	if f.laidW == w {
		return
	}
	f.laidW = w
	f.input.SetWidth(w)
}

// newField 构建一个可编辑的表单项，默认提示文字为字段标签。
func newField(def fieldDef) field {
	ti := textinput.New()
	ti.SetStyles(fieldInputStyles())
	ti.Prompt = "› "
	ti.Placeholder = def.label
	ti.CharLimit = 512
	return field{def: def, input: ti}
}

// fieldInputStyles 让输入框在浏览和编辑时都有清晰的提示符与光标。
func fieldInputStyles() textinput.Styles {
	s := textinput.DefaultDarkStyles()
	accent := lipgloss.Color(cAccent)
	s.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(cInput))
	s.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(cPlaceholder))
	s.Focused.Prompt = lipgloss.NewStyle().Foreground(accent).Bold(true)
	s.Blurred.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(cText))
	s.Blurred.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder))
	s.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(cTextDim))
	s.Cursor.Color = accent
	s.Cursor.Shape = tea.CursorBar
	return s
}

// useDefault 把默认值设为灰色占位。输入框保持空白，留空提交时再采用该值。
func (f *field) useDefault(v string) {
	f.fallback = v
	f.input.Placeholder = v
}

// resolved 返回用户输入；空白时回退到灰色占位里的默认值。
func (f *field) resolved() string {
	if v := f.Value(); v != "" {
		return v
	}
	return f.fallback
}

// applyDefaults 把有默认值的参数显示为灰色占位，并设置其余字段的提示文字。
func (f *field) applyDefaults(d *command.VideoBatchOption) {
	switch f.def.key {
	case "input_path":
		f.useDefault(d.InputPath)
	case "input_format":
		f.useDefault(d.InputFormat)
	case "output_path":
		f.useDefault(d.OutputPath)
	case "output_format":
		f.useDefault(d.OutputFormat)
	case "advance":
		f.input.Placeholder = "例如: -c:v libx265 -crf 26 -c:a copy"
	case "fonts_path":
		f.input.Placeholder = "字体文件所在文件夹"
	}
}

// subtitleDefaults 是新增字幕留空时写入的值。
// 后缀和语言跟命令默认值一致；标题沿用界面里原来的灰色提示「简体中文」。
func subtitleDefaults() command.SubtitleOption {
	d := command.DefaultOption()
	return command.SubtitleOption{
		Suffix: d.InputSubSuffix,
		Lang:   d.InputSubLang,
		Title:  "简体中文",
	}
}

// applySubDefaults 把字幕默认值显示为灰色占位，留空保存时采用这些值。
func (f *field) applySubDefaults() {
	d := subtitleDefaults()
	switch f.def.key {
	case "sub_suffix":
		f.useDefault(d.Suffix)
	case "sub_lang":
		f.useDefault(d.Lang)
	case "sub_title":
		f.useDefault(d.Title)
	}
}

// boolHint 返回 bool 字段旁边的操作提示文字。
func boolHint() string { return descStyle.Render("（space 勾选/取消）") }

// applyPresetDefaults 设置转码参数预设编辑字段的提示文字。
func (f *field) applyPresetDefaults() {
	switch f.def.key {
	case "preset_name":
		f.input.Placeholder = "例如: H.265 高画质（可留空，列表中显示参数）"
	case "preset_args":
		f.input.Placeholder = "例如: -c:v h265 -crf 20 -c:a copy"
	}
}

// isISO639Code 判断是否为 ISO 639-2 三字母小写语言码，MKV 的 language 标签要求此格式。
func isISO639Code(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// Value 返回文本项的当前值（常规空格符用于参数分隔）。
func (f *field) Value() string { return strings.TrimSpace(f.input.Value()) }

// fieldByKey 按字段键查找表单项。找不到时下标为 -1。
func fieldByKey(fields []field, key string) (*field, int) {
	for i := range fields {
		if fields[i].def.key == key {
			return &fields[i], i
		}
	}
	return nil, -1
}

func setStringOption(opt *command.VideoBatchOption, key, val string) {
	switch key {
	case "input_path":
		opt.InputPath = val
	case "input_format":
		opt.InputFormat = val
	case "output_path":
		opt.OutputPath = val
	case "output_format":
		opt.OutputFormat = val
	case "advance":
		opt.Advance = val
	case "fonts_path":
		opt.FontsPath = val
	}
}
