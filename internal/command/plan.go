package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/gsxhnd/batcher/internal/commandline"
	"github.com/gsxhnd/batcher/internal/media"
)

// Job 是一个输入视频对应的一次处理，参数直接交给 ffmpeg，不经 shell。
type Job struct {
	Input  string
	Output string
	Args   []string
}

// BatchPlan 保存配置与任务快照；调用方应只读使用 Jobs。
// 预览与执行共用该快照，不在执行时重新扫描或重新生成命令。
type BatchPlan struct {
	Jobs   []Job
	option VideoBatchOption
}

// planner 持有规范化后的配置副本，只做只读规划。
type planner struct {
	option *VideoBatchOption
}

// BuildPlan 扫描输入并生成任务，不写入文件、不创建目录，也不启动外部进程。
func BuildPlan(ctx context.Context, opt *VideoBatchOption, sel Selection) (*BatchPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := newPlanner(opt)
	if err != nil {
		return nil, err
	}
	jobs, err := p.planJobs(ctx, sel)
	if err != nil {
		return nil, fmt.Errorf("生成任务计划: %w", err)
	}
	return &BatchPlan{Jobs: jobs, option: *p.option}, nil
}

// Commands 返回各任务参数的副本。
func (p *BatchPlan) Commands() [][]string {
	cmds := make([][]string, 0, len(p.Jobs))
	for _, job := range p.Jobs {
		cmds = append(cmds, slices.Clone(job.Args))
	}
	return cmds
}

// Option 返回规划时使用的配置副本。
func (p *BatchPlan) Option() VideoBatchOption {
	option := p.option
	option.Subtitles = slices.Clone(p.option.Subtitles)
	return option
}

// Check 校验任务的输入输出路径；checkExisting 为真时，已存在的输出文件视为错误。
func (p *BatchPlan) Check(ctx context.Context, checkExisting bool) error {
	inputs, outputs := jobPaths(p.Jobs)
	return media.CheckPaths(ctx, inputs, outputs, checkExisting)
}

func jobPaths(jobs []Job) (inputs, outputs []string) {
	inputs = make([]string, 0, len(jobs))
	outputs = make([]string, 0, len(jobs))
	for _, job := range jobs {
		inputs = append(inputs, job.Input)
		outputs = append(outputs, job.Output)
	}
	return inputs, outputs
}

// newPlanner 校验并复制配置，不创建目录，也不修改调用方的配置。
func newPlanner(opt *VideoBatchOption) (*planner, error) {
	if opt == nil {
		return nil, fmt.Errorf("批处理配置不能为空")
	}
	if opt.OutputPath == "" {
		return nil, fmt.Errorf("输出目录不能为空")
	}
	if info, err := os.Stat(opt.OutputPath); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("输出路径不是目录: %s", opt.OutputPath)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("检查输出目录: %w", err)
	}
	option := *opt
	option.Subtitles = slices.Clone(opt.Subtitles)
	option.InputFormat = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(opt.InputFormat), "."))
	option.OutputFormat = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(opt.OutputFormat), "."))
	return &planner{option: &option}, nil
}

// fontsParams 为字体目录下的每个字体生成 -attach 参数段。
func (p *planner) fontsParams(ctx context.Context) ([]string, error) {
	if p.option.FontsPath == "" {
		return nil, nil
	}

	fontsList, err := media.Fonts(ctx, p.option.FontsPath)
	if err != nil {
		return nil, err
	}

	fontsCmdList := make([]string, 0, len(fontsList)*4)
	for _, fontPath := range fontsList {
		fontsCmdList = append(fontsCmdList,
			"-attach", fontPath,
			"-metadata:s:t:m:filename:"+filepath.Base(fontPath),
			"mimetype=application/x-truetype-font",
		)
	}

	return fontsCmdList, nil
}

// advanceArgs 解析输出侧的高级参数，不允许额外输入或覆盖输出文件。
func (p *planner) advanceArgs() ([]string, error) {
	args, err := commandline.Parse(p.option.Advance)
	if err != nil {
		return nil, fmt.Errorf("解析高级参数: %w", err)
	}
	for _, arg := range args {
		if arg == "-i" || arg == "-y" || arg == "-stdin" {
			return nil, fmt.Errorf("高级参数不允许使用 %s；输入由任务管理，已有输出文件不会覆盖", arg)
		}
	}
	return args, nil
}

// subtitleEntries 返回参与命令生成的字幕列表。
// 仅当 VideoBatchOption.Subtitles 为空时，回退为旧版单字幕字段（兼容 add_sub 流程）。
func (p *planner) subtitleEntries() []SubtitleOption {
	if len(p.option.Subtitles) > 0 {
		return p.option.Subtitles
	}
	if p.option.InputSubSuffix == "" {
		return nil
	}
	return []SubtitleOption{{
		Suffix: p.option.InputSubSuffix,
		Lang:   p.option.InputSubLang,
		Title:  p.option.InputSubTitle,
	}}
}

// planJobs 按选中的操作组合，为每个视频生成一条 ffmpeg 命令。
//
// 单一操作与组合操作使用同一个构建器，所有输入必须先于输出选项：
//
//	ffmpeg -nostdin -n -i <视频> [字幕输入] [流映射] [编码参数] [元数据/字体] <输出>
//
// 即一次处理即可完成转码 + 加多条字幕 + 加字体，无需跑多遍。
//
// 添加字幕支持多条：每条字幕是一个独立输入（-i <字幕> -map <n>），流序号从
// InputSubNo 开始递增。字幕文件按 <视频名>.<后缀> 或 <视频名>.<标记>.<后缀> 匹配。
func (p *planner) planJobs(ctx context.Context, sel Selection) ([]Job, error) {
	if !sel.Any() {
		return nil, fmt.Errorf("至少需要选择一个操作（转码/添加字幕/添加字体）")
	}
	if p.option.InputPath == "" || p.option.InputFormat == "" || p.option.OutputFormat == "" {
		return nil, fmt.Errorf("输入路径、输入后缀和输出后缀不能为空")
	}
	for _, suffix := range []string{p.option.InputFormat, p.option.OutputFormat} {
		if strings.ContainsAny(suffix, "/\\\x00") || strings.Trim(suffix, ".") == "" {
			return nil, fmt.Errorf("视频后缀不能包含路径或仅由点组成: %q", suffix)
		}
	}
	if p.option.InputSubNo < 0 {
		return nil, fmt.Errorf("字幕流起始序号不能为负数")
	}
	videos, err := media.Videos(ctx, p.option.InputPath, p.option.OutputPath, p.option.InputFormat)
	if err != nil {
		return nil, err
	}
	if len(videos) == 0 {
		return nil, nil
	}

	var fontsParams, advance []string
	if sel.AddFonts {
		if p.option.FontsPath == "" {
			return nil, fmt.Errorf("添加字体时必须填写字体目录")
		}
		fontsParams, err = p.fontsParams(ctx)
		if err != nil {
			return nil, err
		}
		if len(fontsParams) == 0 {
			return nil, fmt.Errorf("字体目录中未找到 ttf、otf 或 ttc 文件")
		}
	}
	if sel.Convert {
		advance, err = p.advanceArgs()
		if err != nil {
			return nil, err
		}
	}

	var subs []subtitleInput
	if sel.AddSubtitle {
		subs, err = p.subtitleInputs()
		if err != nil {
			return nil, err
		}
		if len(subs) == 0 {
			return nil, fmt.Errorf("添加字幕时至少需要一条有效字幕配置")
		}
	}

	outputs := media.OutputPaths(videos, p.option.OutputPath, p.option.OutputFormat)
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
				names, err = media.ListFiles(dir)
				if err != nil {
					return nil, err
				}
				directories[dir] = names
			}
			for _, sub := range subs {
				name, err := resolveSubtitle(names, media.Stem(videoPath), sub.Suffix)
				if err != nil {
					return nil, fmt.Errorf("匹配视频 %s 的字幕: %w", videoPath, err)
				}
				cmd = append(cmd, "-sub_charenc", "UTF-8", "-i", filepath.Join(dir, name))
			}
			subtitleOutput = p.subtitleOutputArgs(subs)
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
	inputPaths, outputPaths := jobPaths(jobs)
	if err := media.CheckPaths(ctx, inputPaths, outputPaths, false); err != nil {
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
func (p *planner) subtitleInputs() ([]subtitleInput, error) {
	entries := p.subtitleEntries()
	inputs := make([]subtitleInput, 0, len(entries))
	defaults := 0
	for _, sub := range entries {
		if sub.Suffix == "" {
			continue
		}
		inputs = append(inputs, subtitleInput{SubtitleOption: sub, streamNo: p.option.InputSubNo + len(inputs)})
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
func (p *planner) subtitleOutputArgs(subs []subtitleInput) []string {
	args := []string{"-map", "0"}
	if p.option.InputSubNo == 0 {
		args = append(args, "-map", "-0:s?")
	}
	for i := range subs {
		args = append(args, "-map", strconv.Itoa(i+1))
	}
	if p.option.InputSubNo == 0 {
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
