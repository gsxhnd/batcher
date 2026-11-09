package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	option     *VideoBatchOption
	cmdBatches [][]string
	eventMu    sync.Mutex
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

	return &videoBatch{
		option:     opt,
		cmdBatches: make([][]string, 0),
	}, nil
}

func (vb *videoBatch) GetVideosList() ([]string, error) {
	videosList := make([]string, 0)
	err := filepath.Walk(vb.option.InputPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk path %s: %w", path, err)
		}

		if fi.IsDir() {
			return nil
		}

		fileExt := filepath.Ext(fi.Name())
		if fileExt == "."+vb.option.InputFormat {
			videosList = append(videosList, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return videosList, nil
}

func (vb *videoBatch) GetFontsList() ([]string, error) {
	fontsList := make([]string, 0)
	err := filepath.Walk(vb.option.FontsPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk path %s: %w", path, err)
		}

		if fi.IsDir() {
			return nil
		}

		fileExt := filepath.Ext(fi.Name())
		for _, ext := range fontExtensions {
			if fileExt == ext {
				fontsList = append(fontsList, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fontsList, nil
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
	videosList, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}

	outputVideosMap := vb.filterOutput(videosList)

	for _, videoPath := range videosList {
		cmd := []string{"-i", videoPath}
		if vb.option.Advance != "" {
			adv := strings.Split(vb.option.Advance, " ")
			cmd = append(cmd, adv...)
		}
		cmd = append(cmd, outputVideosMap[videoPath])
		vb.cmdBatches = append(vb.cmdBatches, cmd)
	}

	return vb.cmdBatches, nil
}

func (vb *videoBatch) GetAddFontsBatch() ([][]string, error) {
	videosList, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}

	fontCmd, err := vb.GetFontsParams()
	if err != nil {
		return nil, err
	}

	outputVideosMap := vb.filterOutput(videosList)
	for _, videoPath := range videosList {
		batchCmd := []string{"-i", videoPath, "-c", "copy"}
		batchCmd = append(batchCmd, fontCmd...)
		batchCmd = append(batchCmd, outputVideosMap[videoPath])
		vb.cmdBatches = append(vb.cmdBatches, batchCmd)
	}

	return vb.cmdBatches, nil
}

func (vb *videoBatch) GetAddSubtitleBatch() ([][]string, error) {
	videosList, err := vb.GetVideosList()
	if err != nil {
		return nil, err
	}

	fontsParams, err := vb.GetFontsParams()
	if err != nil {
		return nil, err
	}

	outputVideosMap := vb.filterOutput(videosList)

	for _, videoPath := range videosList {
		filename, _ := strings.CutSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
		sourceSubtitle := filepath.Join(vb.option.InputPath, filename+"."+vb.option.InputSubSuffix)

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
		if len(fontsParams) > 0 {
			cmd = append(cmd, fontsParams...)
		}
		cmd = append(cmd, outputVideosMap[videoPath])
		vb.cmdBatches = append(vb.cmdBatches, cmd)
	}

	return vb.cmdBatches, nil
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
// <base>.*.<suffix> 排序后的第一个匹配。全部未命中时返回旧版约定路径（与
// GetAddSubtitleBatch 行为一致），由 ffmpeg 在运行时报错。
func (vb *videoBatch) matchSubtitle(names []string, base, suffix string) string {
	if suffix == "" {
		return ""
	}
	exact := base + "." + suffix
	if found, ok := findSubName(names, base, suffix); ok {
		return filepath.Join(vb.option.InputPath, found)
	}
	return filepath.Join(vb.option.InputPath, exact)
}

// findSubName 优先找精确名，再找 <base> 开头且以 .<suffix> 结尾的名字（排除与精确名相同的）。
func findSubName(names []string, base, suffix string) (string, bool) {
	exact := base + "." + suffix
	for _, n := range names {
		if n == exact {
			return n, true
		}
	}
	for _, n := range names {
		if n != exact && strings.HasPrefix(n, base+".") && strings.HasSuffix(n, "."+suffix) {
			return n, true
		}
	}
	return "", false
}

// filterOutput 生成输出文件路径映射，处理重名文件
func (vb *videoBatch) filterOutput(input []string) map[string]string {
	output := make(map[string]string, len(input))
	seenNames := make(map[string]int) // 记录已出现的文件名计数

	for _, videoPath := range input {
		filename, _ := strings.CutSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))

		// 处理重名
		if count, exists := seenNames[filename]; exists {
			filename = fmt.Sprintf("%s-%d", filename, count)
		}
		seenNames[filename]++

		output[videoPath] = filepath.Join(vb.option.OutputPath, filename+"."+vb.option.OutputFormat)
	}
	return output
}

// FormatCommand 将命令参数格式化为字符串（用于日志输出）
func FormatCommand(args []string) string {
	return "ffmpeg " + strings.Join(args, " ")
}
