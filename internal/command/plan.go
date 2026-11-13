package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
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
// 错误消息保持精简：界面表单里已经能看到用户填的路径，不再重复。
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
		return nil, err
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
		return nil, errors.New("批处理配置不能为空")
	}
	if opt.OutputPath == "" {
		return nil, errors.New("输出目录不能为空")
	}
	if info, err := os.Stat(opt.OutputPath); err == nil && !info.IsDir() {
		return nil, errors.New("输出路径不是目录")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("检查输出目录: %w", err)
	}
	option := *opt
	option.Subtitles = slices.Clone(opt.Subtitles)
	option.InputFormat = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(opt.InputFormat), "."))
	option.OutputFormat = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(opt.OutputFormat), "."))
	for _, item := range []*string{&option.InputPath, &option.OutputPath, &option.FontsPath} {
		if *item == "" {
			continue
		}
		abs, err := filepath.Abs(*item)
		if err != nil {
			return nil, fmt.Errorf("解析路径 %s: %w", *item, err)
		}
		*item = abs
	}
	return &planner{option: &option}, nil
}

// fontsParams 为字体目录下的每个字体生成 -attach 参数段。
func (p *planner) fontsParams(ctx context.Context) ([]string, error) {
	if p.option.FontsPath == "" {
		return nil, nil
	}

	// 先确认目录存在，给出一句短提示；遍历错误会带上一长串系统路径，
	// 表单里已经能看到用户填的目录，toast 里不需要重复。
	if _, err := os.Stat(p.option.FontsPath); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("字体目录不存在")
		}
		return nil, fmt.Errorf("检查字体目录: %w", err)
	}
	fontsList, err := media.Fonts(ctx, p.option.FontsPath)
	if err != nil {
		return nil, err
	}

	fontsCmdList := make([]string, 0, len(fontsList)*4)
	seenName := make(map[string]string, len(fontsList))
	for _, fontPath := range fontsList {
		// ffmpeg 只按 "/" 拆分 -attach 的路径来生成附件 filename 元数据：Windows 的
		// 反斜杠会让 filename 变成整条路径（如 fonts\黑体.ttf），按文件名匹配的
		// -metadata:s:t:m:filename: 就会落空，mimetype 不会被写入，封装因此失败。
		attachPath := filepath.ToSlash(fontPath)
		base := strings.ToLower(path.Base(attachPath))
		if prev, ok := seenName[base]; ok {
			return nil, fmt.Errorf("字体文件名重复：%s 与 %s", path.Base(prev), path.Base(fontPath))
		}
		seenName[base] = fontPath
		fontsCmdList = append(fontsCmdList,
			"-attach", attachPath,
			"-metadata:s:t:m:filename:"+path.Base(attachPath),
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
			return nil, fmt.Errorf("高级参数不允许使用 %s", arg)
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
	if err := p.checkPlanInputs(sel); err != nil {
		return nil, err
	}
	videos, err := media.Videos(ctx, p.option.InputPath, p.option.OutputPath, p.option.InputFormat)
	if err != nil {
		return nil, err
	}
	if len(videos) == 0 {
		return nil, nil
	}
	fontsParams, advance, subs, err := p.planExtras(ctx, sel)
	if err != nil {
		return nil, err
	}
	jobs, err := p.buildJobs(ctx, sel, videos, fontsParams, advance, subs)
	if err != nil {
		return nil, err
	}
	inputPaths, outputPaths := jobPaths(jobs)
	if err := media.CheckPaths(ctx, inputPaths, outputPaths, false); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (p *planner) checkPlanInputs(sel Selection) error {
	if !sel.Any() {
		return errors.New("至少需要选择一个操作")
	}
	if p.option.InputPath == "" || p.option.InputFormat == "" || p.option.OutputFormat == "" {
		return errors.New("输入路径、输入后缀和输出后缀不能为空")
	}
	for _, suffix := range []string{p.option.InputFormat, p.option.OutputFormat} {
		if strings.ContainsAny(suffix, "/\\\x00") || strings.Trim(suffix, ".") == "" {
			return fmt.Errorf("视频后缀不能包含路径或仅由点组成: %q", suffix)
		}
	}
	if p.option.InputSubNo < 0 {
		return errors.New("字幕流起始序号不能为负数")
	}
	return nil
}

func (p *planner) planExtras(ctx context.Context, sel Selection) (fontsParams, advance []string, subs []subtitleInput, err error) {
	if sel.AddFonts {
		switch p.option.OutputFormat {
		case "mkv", "mka":
		default:
			return nil, nil, nil, fmt.Errorf("内嵌字体只支持 mkv/mka，当前是 %s", p.option.OutputFormat)
		}
		if p.option.FontsPath == "" {
			return nil, nil, nil, errors.New("添加字体时必须填写字体目录")
		}
		fontsParams, err = p.fontsParams(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(fontsParams) == 0 {
			return nil, nil, nil, errors.New("字体目录里没有 ttf/otf/ttc 字体")
		}
	}
	if sel.Convert {
		advance, err = p.advanceArgs()
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if sel.AddSubtitle {
		subs, err = p.subtitleInputs()
		if err != nil {
			return nil, nil, nil, err
		}
		if len(subs) == 0 {
			return nil, nil, nil, errors.New("添加字幕时至少需要一条有效字幕配置")
		}
	}
	return fontsParams, advance, subs, nil
}

// dirInfo 缓存一个视频目录的文件列表与视频主文件名，规划期间只计算一次。
type dirInfo struct {
	names []string
	stems []string
}

func (p *planner) buildJobs(ctx context.Context, sel Selection, videos, fontsParams, advance []string, subs []subtitleInput) ([]Job, error) {
	outputs := media.OutputPaths(videos, p.option.OutputPath, p.option.OutputFormat)
	jobs := make([]Job, 0, len(videos))
	// 每个视频目录只读取一次，目录文件列表与视频主文件名都只算一遍，
	// 避免多视频、多字幕反复扫描同一目录或重复遍历全部视频。
	dirs := make(map[string]*dirInfo)
	for _, videoPath := range videos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cmd := []string{"-nostdin", "-n", "-i", videoPath}
		var subtitleOutput []string
		if sel.AddSubtitle {
			dir := filepath.Dir(videoPath)
			info, ok := dirs[dir]
			if !ok {
				names, err := media.ListFiles(dir)
				if err != nil {
					return nil, err
				}
				info = &dirInfo{names: names, stems: videoStems(videos, dir)}
				dirs[dir] = info
			}
			for _, sub := range subs {
				name, err := resolveSubtitle(info.names, media.Stem(videoPath), sub.Suffix, info.stems)
				if err != nil {
					return nil, fmt.Errorf("匹配 %s 的字幕: %w", filepath.Base(videoPath), err)
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
	return jobs, nil
}

func videoStems(videos []string, dir string) []string {
	stems := make([]string, 0, len(videos))
	for _, video := range videos {
		if filepath.Dir(video) == dir {
			stems = append(stems, media.Stem(video))
		}
	}
	return stems
}

// resolveSubtitle 对缺失或歧义给出明确错误，不把问题留到 ffmpeg 执行时才发现。
// stems 是同一目录里的视频主文件名。更长的视频名（Movie 与 Movie.Extended）不会抢走对方的字幕。
func resolveSubtitle(names []string, base, suffix string, stems []string) (string, error) {
	suffix = strings.TrimLeft(strings.TrimSpace(suffix), ".")
	exact := base + "." + suffix
	owned := make(map[string]struct{}, len(stems))
	for _, stem := range stems {
		owned[strings.ToLower(stem)] = struct{}{}
	}
	var matches []string
	for _, name := range names {
		if strings.EqualFold(name, exact) {
			return name, nil
		}
		if !hasFoldPrefix(name, base+".") || !hasFoldSuffix(name, "."+suffix) {
			continue
		}
		middle := name[len(base)+1 : len(name)-len(suffix)-1]
		if _, ok := owned[strings.ToLower(base+"."+middle)]; ok {
			continue
		}
		matches = append(matches, name)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("未找到 %s", exact)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("后缀 %s 匹配到多个字幕，请用完整后缀", suffix)
	}
}

func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func hasFoldSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
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
		return nil, errors.New("只能指定一条默认字幕轨道")
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
