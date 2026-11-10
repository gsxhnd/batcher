package ffmpeg

import (
	"context"
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

// Any 返回是否至少勾选了一项操作。
func (s Selection) Any() bool {
	return s.Convert || s.AddSubtitle || s.AddFonts
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
// 单一操作与组合操作使用同一个构建器，所有输入必须先于输出选项：
//
//	ffmpeg -nostdin -n -i <视频> [字幕输入] [流映射] [编码参数] [元数据/字体] <输出>
//
// 即一次处理即可完成转码 + 加多条字幕 + 加字体，无需跑多遍。
//
// 添加字幕支持多条：每条字幕是一个独立输入（-i <字幕> -map <n>），流序号从
// InputSubNo 开始递增。字幕文件按 <视频名>.<后缀> 或 <视频名>.<标记>.<后缀> 匹配。
func (vb *videoBatch) GetCombinedBatch(sel Selection) ([][]string, error) {
	jobs, err := vb.planJobs(context.Background(), sel)
	if err != nil {
		return nil, err
	}
	return (&BatchPlan{Jobs: jobs}).Commands(), nil
}

func (vb *videoBatch) planJobs(ctx context.Context, sel Selection) ([]Job, error) {
	if !sel.Any() {
		return nil, fmt.Errorf("至少需要选择一个操作（转码/添加字幕/添加字体）")
	}
	if vb.option.InputPath == "" || vb.option.InputFormat == "" || vb.option.OutputFormat == "" {
		return nil, fmt.Errorf("输入路径、输入后缀和输出后缀不能为空")
	}
	for _, suffix := range []string{vb.option.InputFormat, vb.option.OutputFormat} {
		if strings.ContainsAny(suffix, "/\\\x00") || strings.Trim(suffix, ".") == "" {
			return nil, fmt.Errorf("视频后缀不能包含路径或仅由点组成: %q", suffix)
		}
	}
	if vb.option.InputSubNo < 0 {
		return nil, fmt.Errorf("字幕流起始序号不能为负数")
	}
	videos, err := vb.videosList(ctx)
	if err != nil {
		return nil, err
	}
	if len(videos) == 0 {
		return nil, nil
	}

	var fontsParams, advance []string
	if sel.AddFonts {
		if vb.option.FontsPath == "" {
			return nil, fmt.Errorf("添加字体时必须填写字体目录")
		}
		fontsParams, err = vb.fontsParams(ctx)
		if err != nil {
			return nil, err
		}
		if len(fontsParams) == 0 {
			return nil, fmt.Errorf("字体目录中未找到 ttf、otf 或 ttc 文件")
		}
	}
	if sel.Convert {
		advance, err = vb.advanceArgs()
		if err != nil {
			return nil, err
		}
	}

	var subs []subtitleInput
	if sel.AddSubtitle {
		subs, err = vb.subtitleInputs()
		if err != nil {
			return nil, err
		}
		if len(subs) == 0 {
			return nil, fmt.Errorf("添加字幕时至少需要一条有效字幕配置")
		}
	}

	outputs := vb.outputPaths(videos)
	jobs := make([]Job, 0, len(videos))
	// 每个视频目录只读取一次，避免多视频、多字幕反复扫描同一目录。
	directories := make(map[string][]string)
	for _, videoPath := range videos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cmd := []string{"-nostdin", "-n", "-i", videoPath}
		var subtitleOutput []string
		if sel.AddSubtitle {
			dir := filepath.Dir(videoPath)
			names, ok := directories[dir]
			if !ok {
				names, err = listFiles(dir)
				if err != nil {
					return nil, err
				}
				directories[dir] = names
			}
			for _, sub := range subs {
				name, err := resolveSubtitle(names, stem(videoPath), sub.Suffix)
				if err != nil {
					return nil, fmt.Errorf("匹配视频 %s 的字幕: %w", videoPath, err)
				}
				cmd = append(cmd, "-sub_charenc", "UTF-8", "-i", filepath.Join(dir, name))
			}
			subtitleOutput = vb.subtitleOutputArgs(subs)
		} else {
			cmd = append(cmd, "-map", "0")
		}
		cmd = append(cmd, subtitleOutput...)
		if sel.Convert {
			// 保留原字幕、附件和数据流，不受容器默认编码器影响。
			cmd = append(cmd, "-c:s", "copy", "-c:t", "copy", "-c:d", "copy")
			cmd = append(cmd, advance...)
		} else {
			cmd = append(cmd, "-c", "copy")
		}
		cmd = append(cmd, fontsParams...)
		cmd = append(cmd, outputs[videoPath])
		jobs = append(jobs, Job{Input: videoPath, Output: outputs[videoPath], Args: cmd})
	}
	if err := validateJobPaths(ctx, jobs, false); err != nil {
		return nil, err
	}
	return jobs, nil
}

// resolveSubtitle 对缺失或歧义给出明确错误，不把问题留到 ffmpeg 执行时才发现。
func resolveSubtitle(names []string, base, suffix string) (string, error) {
	exact := base + "." + suffix
	var matches []string
	for _, name := range names {
		if name == exact {
			return name, nil
		}
		if strings.HasPrefix(name, base+".") && strings.HasSuffix(name, "."+suffix) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("未找到 %s", exact)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("后缀 %s 匹配到多个字幕（%s），请指定 chi.ass 等完整后缀", suffix, strings.Join(matches, "、"))
	}
}

// subtitleInput 是一条有效字幕（Suffix 非空）及其在输出中的流序号。
type subtitleInput struct {
	SubtitleOption
	streamNo int
}

// subtitleInputs 返回跳过空后缀后的有效字幕，并按顺序分配字幕流序号（从 InputSubNo 开始）。
func (vb *videoBatch) subtitleInputs() ([]subtitleInput, error) {
	entries := vb.subtitleEntries()
	inputs := make([]subtitleInput, 0, len(entries))
	defaults := 0
	for _, sub := range entries {
		if sub.Suffix == "" {
			continue
		}
		inputs = append(inputs, subtitleInput{SubtitleOption: sub, streamNo: vb.option.InputSubNo + len(inputs)})
		if sub.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return nil, fmt.Errorf("只能指定一条默认字幕轨道")
	}
	return inputs, nil
}

// subtitleOutputArgs 先保留非字幕流，再放新增字幕，最后保留原字幕。
// 默认序号因此不依赖原视频的字幕数量；显式设置 InputSubNo 时兼容旧版原字幕在前的映射。
func (vb *videoBatch) subtitleOutputArgs(subs []subtitleInput) []string {
	args := []string{"-map", "0"}
	if vb.option.InputSubNo == 0 {
		args = append(args, "-map", "-0:s?")
	}
	for i := range subs {
		args = append(args, "-map", strconv.Itoa(i+1))
	}
	if vb.option.InputSubNo == 0 {
		args = append(args, "-map", "0:s?")
	}

	// 第二遍：同时收集 metadata 参数和是否存在默认轨道，避免多次遍历。
	hasDefault := false
	for _, sub := range subs {
		args = append(args,
			fmt.Sprintf("-metadata:s:s:%d", sub.streamNo), fmt.Sprintf("language=%s", sub.Lang),
			fmt.Sprintf("-metadata:s:s:%d", sub.streamNo), fmt.Sprintf("title=%s", sub.Title),
		)
		if sub.Default {
			hasDefault = true
		}
	}

	// 有默认轨道时才写 disposition，其余轨道显式置 0 以防多重默认。
	if hasDefault {
		// 只清除原字幕的默认标记，保留 forced 等其他 disposition。
		args = append(args, "-disposition:s", "-default")
		for _, sub := range subs {
			flag := "0"
			if sub.Default {
				flag = "default"
			}
			args = append(args, fmt.Sprintf("-disposition:s:%d", sub.streamNo), flag)
		}
	}
	return args
}
