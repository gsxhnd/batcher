package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
)

type fieldKind int

const (
	fieldString fieldKind = iota
	fieldInt
)

// fieldDef 描述一个表单项映射到 VideoBatchOption 的哪个配置。
type fieldDef struct {
	key      string
	label    string
	kind     fieldKind
	valueInt int
}

// fieldsFor 根据勾选的操作组合返回主参数表单的字段。
// 多条字幕与转码参数预设分别在独立的界面管理，不出现在本表单。
func fieldsFor(sel ffmpeg.Selection) []fieldDef {
	defs := []fieldDef{
		{key: "input_path", label: "源视频路径"},
		{key: "input_format", label: "源视频后缀"},
		{key: "output_path", label: "输出目录"},
		{key: "output_format", label: "输出后缀"},
	}
	if sel.Convert {
		defs = append(defs, fieldDef{key: "advance", label: "高级自定义参数"})
	}
	if sel.AddFonts {
		defs = append(defs, fieldDef{key: "fonts_path", label: "字体文件夹"})
	}
	defs = append(defs, fieldDef{key: "workers", label: "并发工作数", kind: fieldInt, valueInt: 1})
	return defs
}

// subFieldDefs 是单条字幕的编辑字段。
var subFieldDefs = []fieldDef{
	{key: "sub_suffix", label: "字幕后缀"},
	{key: "sub_lang", label: "语言缩写"},
	{key: "sub_title", label: "轨道标题"},
}

// field 保存表单项的运行时状态。
type field struct {
	def   fieldDef
	input textinput.Model
	err   string
}

// newField 根据字段定义和默认值构建一个可编辑的表单项；不支持的定义返回空输入并使用 placeholder。
func newField(def fieldDef) field {
	ti := textinput.New()
	ti.Placeholder = def.label
	ti.CharLimit = 512
	return field{def: def, input: ti}
}

// applyDefaults 按配置默认值填充字段。
func (f *field) applyDefaults(d *ffmpeg.VideoBatchOption) {
	switch f.def.key {
	case "input_path":
		f.input.SetValue(d.InputPath)
	case "input_format":
		f.input.SetValue(d.InputFormat)
	case "output_path":
		f.input.SetValue(d.OutputPath)
	case "output_format":
		f.input.SetValue(d.OutputFormat)
	case "workers":
		f.input.SetValue(strconv.Itoa(d.Workers))
	}
	switch f.def.key {
	case "advance":
		f.input.Placeholder = "例如: -c:v h265 -crf 20 -c:a copy（可留空，ctrl+s 可保存为常用参数）"
	case "fonts_path":
		f.input.Placeholder = "字体文件所在文件夹"
	case "sub_suffix":
		f.input.Placeholder = "ass / srt；多语言文件可填 chi.ass / eng.ass"
	case "sub_lang":
		f.input.Placeholder = "chi / eng / jpn（ISO 639-2 三字母码）"
	case "sub_title":
		f.input.Placeholder = "简体中文"
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

// buildOption 将主表单当前值写回 ffmpeg.VideoBatchOption 并校验。
func (m *model) buildOption() (*ffmpeg.VideoBatchOption, error) {
	opt := ffmpeg.DefaultOption()
	for i := range m.fields {
		f := &m.fields[i]
		f.err = ""
		switch f.def.kind {
		case fieldInt:
			n, err := strconv.Atoi(f.Value())
			if err != nil {
				f.err = "需为数字"
				continue
			}
			if n < 0 {
				f.err = "不能为负数"
				continue
			}
			setIntOption(&opt, f.def.key, n)
		default:
			setStringOption(&opt, f.def.key, f.Value())
		}
	}

	// 与 CLI add_fonts 子命令 --input_fonts_path Required 的行为保持一致。
	if m.ops.AddFonts && opt.FontsPath == "" {
		for i := range m.fields {
			if m.fields[i].def.key == "fonts_path" {
				m.fields[i].err = "必填（已勾选添加字体）"
			}
		}
		return nil, fmt.Errorf("勾选了添加字体，必须填写字体文件夹")
	}

	for i := range m.fields {
		if m.fields[i].err != "" {
			return nil, fmt.Errorf("%s: %s", m.fields[i].def.label, m.fields[i].err)
		}
	}

	// 多条字幕列表由字幕管理界面维护。
	opt.Subtitles = append([]ffmpeg.SubtitleOption(nil), m.subs...)
	return &opt, nil
}

func setStringOption(opt *ffmpeg.VideoBatchOption, key, val string) {
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

func setIntOption(opt *ffmpeg.VideoBatchOption, key string, val int) {
	switch key {
	case "workers":
		opt.Workers = val
	}
}

// displaySel 把操作组合转成中文描述。
func displaySel(sel ffmpeg.Selection) string {
	if sel.String() == "无" {
		return "无"
	}
	var labels []string
	if sel.Convert {
		labels = append(labels, "视频转码")
	}
	if sel.AddSubtitle {
		labels = append(labels, "添加字幕")
	}
	if sel.AddFonts {
		labels = append(labels, "添加字体")
	}
	return strings.Join(labels, " + ")
}
