package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
)

type VideoBatcher interface {
	GetVideosList() ([]string, error)                   // 获取视频列表
	GetFontsList() ([]string, error)                    // 获取字体列表
	GetFontsParams() ([]string, error)                  // 获取字体参数
	GetConvertBatch() ([][]string, error)               // 获取转换视频命令
	GetAddFontsBatch() ([][]string, error)              // 获取添加字体命令
	GetAddSubtitleBatch() ([][]string, error)           // 获取添加字幕命令
	GetCombinedBatch(sel Selection) ([][]string, error) // 按操作组合生成命令
	ExecuteBatch(ctx context.Context, batchCmd [][]string) error
}

type videoBatch struct {
	option  *VideoBatchOption
	eventMu sync.Mutex
}

// ffmpegBin 是调用的可执行文件，测试中可替换。
var ffmpegBin = func() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}()

var fontExtensions = []string{".ttf", ".otf", ".ttc"}

func NewVideoBatch(opt *VideoBatchOption) (VideoBatcher, error) {
	if opt == nil {
		return nil, fmt.Errorf("option cannot be nil")
	}
	if err := makeDir(opt.OutputPath); err != nil {
		return nil, fmt.Errorf("create output path: %w", err)
	}

	// 默认使用单线程
	if opt.Workers <= 0 {
		opt.Workers = 1
	}

	return &videoBatch{option: opt}, nil
}

func (vb *videoBatch) GetVideosList() ([]string, error) {
	return walkFiles(vb.option.InputPath, func(name string) bool {
		return filepath.Ext(name) == "."+vb.option.InputFormat
	})
}

func (vb *videoBatch) GetFontsList() ([]string, error) {
	return walkFiles(vb.option.FontsPath, func(name string) bool {
		return slices.Contains(fontExtensions, filepath.Ext(name))
	})
}

// walkFiles 递归遍历 root，返回文件名满足 match 的文件路径（跳过目录）。
func walkFiles(root string, match func(name string) bool) ([]string, error) {
	files := make([]string, 0)
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk path %s: %w", path, err)
		}
		if !fi.IsDir() && match(fi.Name()) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (vb *videoBatch) GetFontsParams() ([]string, error) {
	if vb.option.FontsPath == "" {
		return nil, nil
	}

	fontsList, err := vb.GetFontsList()
	if err != nil {
		return nil, err
	}

	fontsCmdList := make([]string, 0, len(fontsList)*4)
	for i, fontPath := range fontsList {
		fontsCmdList = append(fontsCmdList,
			"-attach", fontPath,
			fmt.Sprintf("-metadata:s:t:%d", i),
			"mimetype=application/x-truetype-font",
		)
	}

	return fontsCmdList, nil
}

func (vb *videoBatch) GetConvertBatch() ([][]string, error) {
	videos, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}

	outputs := vb.outputPaths(videos)
	batches := make([][]string, 0, len(videos))
	for _, videoPath := range videos {
		cmd := []string{"-i", videoPath}
		cmd = append(cmd, vb.advanceArgs()...)
		cmd = append(cmd, outputs[videoPath])
		batches = append(batches, cmd)
	}

	return batches, nil
}

func (vb *videoBatch) GetAddFontsBatch() ([][]string, error) {
	videos, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}

	fontParams, err := vb.GetFontsParams()
	if err != nil {
		return nil, err
	}

	outputs := vb.outputPaths(videos)
	batches := make([][]string, 0, len(videos))
	for _, videoPath := range videos {
		cmd := []string{"-i", videoPath, "-c", "copy"}
		cmd = append(cmd, fontParams...)
		cmd = append(cmd, outputs[videoPath])
		batches = append(batches, cmd)
	}

	return batches, nil
}

func (vb *videoBatch) GetAddSubtitleBatch() ([][]string, error) {
	videos, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}

	fontsParams, err := vb.GetFontsParams()
	if err != nil {
		return nil, err
	}

	outputs := vb.outputPaths(videos)
	batches := make([][]string, 0, len(videos))
	for _, videoPath := range videos {
		sourceSubtitle := filepath.Join(vb.option.InputPath, stem(videoPath)+"."+vb.option.InputSubSuffix)

		cmd := []string{
			"-i", videoPath,
			"-sub_charenc", "UTF-8",
			"-i", sourceSubtitle,
			"-map", "0", "-map", "1",
			fmt.Sprintf("-metadata:s:s:%d", vb.option.InputSubNo),
			fmt.Sprintf("language=%s", vb.option.InputSubLang),
			fmt.Sprintf("-metadata:s:s:%d", vb.option.InputSubNo),
			fmt.Sprintf("title=%s", vb.option.InputSubTitle),
			"-c", "copy",
		}
		cmd = append(cmd, fontsParams...)
		cmd = append(cmd, outputs[videoPath])
		batches = append(batches, cmd)
	}

	return batches, nil
}

// advanceArgs 把高级参数按空格拆分为 ffmpeg 参数；未设置时返回 nil。
func (vb *videoBatch) advanceArgs() []string {
	if vb.option.Advance == "" {
		return nil
	}
	return strings.Split(vb.option.Advance, " ")
}

// subtitleEntries 返回参与命令生成的字幕列表。
// 仅当 VideoBatchOption.Subtitles 为空时，回退为旧版单字幕字段（兼容 add_sub 流程）。
func (vb *videoBatch) subtitleEntries() ([]SubtitleOption, error) {
	if len(vb.option.Subtitles) > 0 {
		return vb.option.Subtitles, nil
	}
	if vb.option.InputSubSuffix == "" {
		return nil, nil
	}
	return []SubtitleOption{{
		Suffix: vb.option.InputSubSuffix,
		Lang:   vb.option.InputSubLang,
		Title:  vb.option.InputSubTitle,
	}}, nil
}

// listInputFiles 返回 InputPath 下（非递归）的文件名列表，用于把 1.chi.ass 之类的字幕匹配到视频。
func (vb *videoBatch) listInputFiles() ([]string, error) {
	entries, err := os.ReadDir(vb.option.InputPath)
	if err != nil {
		return nil, fmt.Errorf("read subtitle directory %s: %w", vb.option.InputPath, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// matchSubtitle 查找视频对应的字幕文件：优先精确 <base>.<suffix>，其次目录中按
// <base>.*.<suffix> 排序后的第一个匹配。全部未命中时返回精确路径，由 ffmpeg 在运行时报错。
func (vb *videoBatch) matchSubtitle(names []string, base, suffix string) string {
	if suffix == "" {
		return ""
	}
	if found, ok := findSubName(names, base, suffix); ok {
		return filepath.Join(vb.option.InputPath, found)
	}
	return filepath.Join(vb.option.InputPath, base+"."+suffix)
}

// findSubName 优先找精确名，再找 <base> 开头且以 .<suffix> 结尾的名字（排除与精确名相同的）。
func findSubName(names []string, base, suffix string) (string, bool) {
	exact := base + "." + suffix
	if slices.Contains(names, exact) {
		return exact, true
	}
	for _, n := range names {
		if strings.HasPrefix(n, base+".") && strings.HasSuffix(n, "."+suffix) {
			return n, true
		}
	}
	return "", false
}

// outputPaths 为每个输入视频生成输出文件路径，同名输入追加 -1、-2 ... 后缀以避免覆盖。
func (vb *videoBatch) outputPaths(inputs []string) map[string]string {
	outputs := make(map[string]string, len(inputs))
	seenNames := make(map[string]int)

	for _, videoPath := range inputs {
		name := stem(videoPath)
		if count, exists := seenNames[name]; exists {
			name = fmt.Sprintf("%s-%d", name, count)
		}
		seenNames[name]++

		outputs[videoPath] = filepath.Join(vb.option.OutputPath, name+"."+vb.option.OutputFormat)
	}
	return outputs
}

// stem 返回去掉目录与扩展名后的文件名。
func stem(path string) string {
	name, _ := strings.CutSuffix(filepath.Base(path), filepath.Ext(path))
	return name
}

// FormatCommand 将命令参数格式化为字符串（用于日志输出）
func FormatCommand(args []string) string {
	return "ffmpeg " + strings.Join(args, " ")
}
