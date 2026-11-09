package ffmpeg

import (
	"fmt"
	"strconv"
	"strings"
)

// Selection 描述一次批处理中启用哪些操作，三者可任意组合（需至少一项）。
type Selection struct {
	Convert     bool // 转码：使用 Advance 自定义编码参数，输出 OutputFormat 格式
	AddSubtitle bool // 添加字幕：把与视频同名的字幕文件（可多条）封装进视频
	AddFonts    bool // 添加字体：把 FontsPath 下的字体文件内嵌到视频
}

// String 返回选中操作的可读描述，用于日志与界面标题。
func (s Selection) String() string {
	var parts []string
	if s.Convert {
		parts = append(parts, string(TaskConvert))
	}
	if s.AddSubtitle {
		parts = append(parts, string(TaskAddSubtitle))
	}
	if s.AddFonts {
		parts = append(parts, string(TaskAddFonts))
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, "+")
}

// GetCombinedBatch 按选中的操作组合，为每个视频生成一条 ffmpeg 命令。
//
// 单一操作时直接复用对应的既有批处理；组合时单条命令同时完成：
//
//	ffmpeg -i <视频> [高级参数] [字幕参数] [字体参数] [-c copy(未开启转码时)] <输出>
//
// 即一次处理即可完成转码 + 加多条字幕 + 加字体，无需跑多遍。
//
// 添加字幕支持多条：每条字幕是一个独立输入（-i <字幕> -map <n>），流序号从
// InputSubNo 开始递增。字幕文件按 <视频名>.<后缀> 或 <视频名>.<标记>.<后缀> 匹配。
func (vb *videoBatch) GetCombinedBatch(sel Selection) ([][]string, error) {
	switch {
	case !sel.Convert && !sel.AddSubtitle && !sel.AddFonts:
		return nil, fmt.Errorf("至少需要选择一个操作（转码/添加字幕/添加字体）")
	case sel.Convert && !sel.AddSubtitle && !sel.AddFonts:
		return vb.GetConvertBatch()
	case sel.AddSubtitle && !sel.Convert && !sel.AddFonts && len(vb.option.Subtitles) == 0:
		// 未提供多条字幕列表时委托给持旧实现的单字幕批处理，保证与 CLI 完全一致。
		return vb.GetAddSubtitleBatch()
	case sel.AddFonts && !sel.Convert && !sel.AddSubtitle:
		return vb.GetAddFontsBatch()
	}

	videos, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}
	if len(videos) == 0 {
		return nil, nil
	}

	fontsParams, err := vb.GetFontsParams()
	if err != nil {
		return nil, err
	}

	var subs []subtitleInput
	var subNames []string
	if sel.AddSubtitle {
		subs, err = vb.subtitleInputs()
		if err != nil {
			return nil, err
		}
		subNames, err = vb.listInputFiles()
		if err != nil {
			return nil, err
		}
	}

	outputs := vb.outputPaths(videos)
	batches := make([][]string, 0, len(videos))
	for _, videoPath := range videos {
		cmd := []string{"-i", videoPath}

		if sel.Convert {
			cmd = append(cmd, vb.advanceArgs()...)
		}
		if sel.AddSubtitle {
			cmd = append(cmd, vb.subtitleArgs(subs, subNames, stem(videoPath))...)
		}
		// 未开启转码时流媒体直接复制，保证添加字幕/字体不重新编码。
		if !sel.Convert {
			cmd = append(cmd, "-c", "copy")
		}
		cmd = append(cmd, fontsParams...)
		cmd = append(cmd, outputs[videoPath])
		batches = append(batches, cmd)
	}
	return batches, nil
}

// subtitleInput 是一条有效字幕（Suffix 非空）及其在输出中的流序号。
type subtitleInput struct {
	SubtitleOption
	streamNo int
}

// subtitleInputs 返回跳过空后缀后的有效字幕，并按顺序分配字幕流序号（从 InputSubNo 开始）。
func (vb *videoBatch) subtitleInputs() ([]subtitleInput, error) {
	entries, err := vb.subtitleEntries()
	if err != nil {
		return nil, err
	}
	inputs := make([]subtitleInput, 0, len(entries))
	for _, sub := range entries {
		if sub.Suffix == "" {
			continue
		}
		inputs = append(inputs, subtitleInput{SubtitleOption: sub, streamNo: vb.option.InputSubNo + len(inputs)})
	}
	return inputs, nil
}

// subtitleArgs 生成单个视频的字幕参数：每条字幕作为独立输入（-sub_charenc 紧跟对应的 -i），
// 再映射视频自身的全部流与各字幕流，最后写入字幕语言与标题元数据。
func (vb *videoBatch) subtitleArgs(subs []subtitleInput, names []string, base string) []string {
	var args []string
	for _, sub := range subs {
		args = append(args, "-sub_charenc", "UTF-8", "-i", vb.matchSubtitle(names, base, sub.Suffix))
	}
	// 保留视频与其内所有原流（视频、音轨、章节等）。
	args = append(args, "-map", "0")
	for i := range subs {
		// 输入 0 为视频，字幕输入从 1 开始。
		args = append(args, "-map", strconv.Itoa(i+1))
	}
	for _, sub := range subs {
		args = append(args,
			fmt.Sprintf("-metadata:s:s:%d", sub.streamNo),
			fmt.Sprintf("language=%s", sub.Lang),
			fmt.Sprintf("-metadata:s:s:%d", sub.streamNo),
			fmt.Sprintf("title=%s", sub.Title),
		)
	}
	return args
}
