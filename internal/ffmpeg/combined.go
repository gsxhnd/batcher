package ffmpeg

import (
	"fmt"
	"path/filepath"
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
	case sel.Convert && !sel.AddSubtitle && !sel.AddFonts:
		return vb.GetConvertBatch()
	case sel.AddSubtitle && !sel.Convert && !sel.AddFonts && len(vb.option.Subtitles) == 0:
		// 未提供多条字幕列表时委托给持旧实现的单字幕批处理，保证与 CLI 完全一致。
		return vb.GetAddSubtitleBatch()
	case sel.AddFonts && !sel.Convert && !sel.AddSubtitle:
		return vb.GetAddFontsBatch()
	case !sel.Convert && !sel.AddSubtitle && !sel.AddFonts:
		return nil, fmt.Errorf("至少需要选择一个操作（转码/添加字幕/添加字体）")
	}

	videosList, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}
	if len(videosList) == 0 {
		return nil, nil
	}

	fontsParams, err := vb.GetFontsParams()
	if err != nil {
		return nil, err
	}

	// 添加注:字幕时一次性读取输入目录文件名列表，用于 1.chi.ass 这类带标记的字幕匹配。
	var subNames []string
	var subs []SubtitleOption
	if sel.AddSubtitle {
		subs, err = vb.subtitleEntries()
		if err != nil {
			return nil, err
		}
		subNames, err = vb.listInputFiles()
		if err != nil {
			return nil, err
		}
	}

	outputVideosMap := vb.filterOutput(videosList)

	batches := make([][]string, 0, len(videosList))
	for _, videoPath := range videosList {
		cmd := []string{"-i", videoPath}

		if sel.Convert && vb.option.Advance != "" {
			// 与 GetConvertBatch 一致：高级参数按空格拆分。
			cmd = append(cmd, strings.Split(vb.option.Advance, " ")...)
		}

		if sel.AddSubtitle {
			filename, _ := strings.CutSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
			// 每条有效字幕先作为输入追加（-sub_charenc 必须紧跟它对应的 -i）。
			for _, sub := range subs {
				if sub.Suffix == "" {
					continue
				}
				subPath := vb.matchSubtitle(subNames, filename, sub.Suffix)
				cmd = append(cmd, "-sub_charenc", "UTF-8", "-i", subPath)
			}
			// 保留视频与其内所有原流（视频、音轨、章节等）。
			cmd = append(cmd, "-map", "0")
			// 输入序号从 1 开始，与上一段循环中有效字幕的追加顺序一一对应。
			inputIndex := 1
			for _, sub := range subs {
				if sub.Suffix == "" {
					continue
				}
				cmd = append(cmd, "-map", strconv.Itoa(inputIndex))
				inputIndex++
			}
			// 字幕流序号从 InputSubNo 开始，按字幕条目顺序递增。
			n := 0
			for _, sub := range subs {
				if sub.Suffix == "" {
					continue
				}
				streamNo := vb.option.InputSubNo + n
				n++
				cmd = append(cmd,
					fmt.Sprintf("-metadata:s:s:%d", streamNo),
					fmt.Sprintf("language=%s", sub.Lang),
					fmt.Sprintf("-metadata:s:s:%d", streamNo),
					fmt.Sprintf("title=%s", sub.Title),
				)
			}
		}

		// 未开启转码时流媒体直接复制，保证添加字幕/字体不重新编码。
		if !sel.Convert {
			cmd = append(cmd, "-c", "copy")
		}

		if len(fontsParams) > 0 {
			cmd = append(cmd, fontsParams...)
		}

		cmd = append(cmd, outputVideosMap[videoPath])
		batches = append(batches, cmd)
	}
	return batches, nil
}
