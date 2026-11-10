package ffmpeg

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/gsxhnd/batcher/internal/commandline"
)

// VideoBatcher 提供只读的命令规划与显式执行能力。
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

// NewVideoBatch 校验并复制配置，不创建目录，也不修改调用方的配置。
func NewVideoBatch(opt *VideoBatchOption) (VideoBatcher, error) {
	vb, err := newVideoBatch(opt)
	if err != nil {
		return nil, err
	}
	return vb, nil
}

func newVideoBatch(opt *VideoBatchOption) (*videoBatch, error) {
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
	option.Workers = max(opt.Workers, 1)
	return &videoBatch{option: &option}, nil
}

func (vb *videoBatch) GetVideosList() ([]string, error) {
	return vb.videosList(context.Background())
}

func (vb *videoBatch) videosList(ctx context.Context) ([]string, error) {
	input, err := filepath.Abs(vb.option.InputPath)
	if err != nil {
		return nil, fmt.Errorf("解析输入目录: %w", err)
	}
	output, err := filepath.Abs(vb.option.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("解析输出目录: %w", err)
	}
	exclude := ""
	if input != output {
		exclude = output
	}
	return walkFiles(ctx, vb.option.InputPath, exclude, func(name string) bool {
		return strings.EqualFold(filepath.Ext(name), "."+vb.option.InputFormat)
	})
}

func (vb *videoBatch) GetFontsList() ([]string, error) {
	return vb.fontsList(context.Background())
}

func (vb *videoBatch) fontsList(ctx context.Context) ([]string, error) {
	return walkFiles(ctx, vb.option.FontsPath, "", func(name string) bool {
		return slices.Contains(fontExtensions, strings.ToLower(filepath.Ext(name)))
	})
}

// walkFiles 递归遍历 root，返回文件名满足 match 的文件路径（跳过目录）。
func walkFiles(ctx context.Context, root, exclude string, match func(name string) bool) ([]string, error) {
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("遍历路径 %s: %w", path, err)
		}
		if entry.IsDir() {
			abs, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("解析路径 %s: %w", path, err)
			}
			if abs == exclude {
				return filepath.SkipDir
			}
		} else if entry.Type().IsRegular() && match(entry.Name()) {
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
	return vb.fontsParams(context.Background())
}

func (vb *videoBatch) fontsParams(ctx context.Context) ([]string, error) {
	if vb.option.FontsPath == "" {
		return nil, nil
	}

	fontsList, err := vb.fontsList(ctx)
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

func (vb *videoBatch) GetConvertBatch() ([][]string, error) {
	return vb.GetCombinedBatch(Selection{Convert: true})
}

func (vb *videoBatch) GetAddFontsBatch() ([][]string, error) {
	return vb.GetCombinedBatch(Selection{AddFonts: true})
}

func (vb *videoBatch) GetAddSubtitleBatch() ([][]string, error) {
	return vb.GetCombinedBatch(Selection{AddSubtitle: true})
}

// advanceArgs 解析输出侧的高级参数，不允许额外输入或覆盖输出文件。
func (vb *videoBatch) advanceArgs() ([]string, error) {
	args, err := commandline.Parse(vb.option.Advance)
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
func (vb *videoBatch) subtitleEntries() []SubtitleOption {
	if len(vb.option.Subtitles) > 0 {
		return vb.option.Subtitles
	}
	if vb.option.InputSubSuffix == "" {
		return nil
	}
	return []SubtitleOption{{
		Suffix: vb.option.InputSubSuffix,
		Lang:   vb.option.InputSubLang,
		Title:  vb.option.InputSubTitle,
	}}
}

func listFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取字幕目录 %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// outputPaths 为每个输入视频生成输出文件路径，同名输入追加 -1、-2 ... 后缀以避免覆盖。
func (vb *videoBatch) outputPaths(inputs []string) map[string]string {
	outputs := make(map[string]string, len(inputs))
	used := make(map[string]bool, len(inputs))
	next := make(map[string]int)

	for _, videoPath := range inputs {
		base := stem(videoPath)
		name := base
		for used[strings.ToLower(name)] {
			next[base]++
			name = fmt.Sprintf("%s-%d", base, next[base])
		}
		used[strings.ToLower(name)] = true

		outputs[videoPath] = filepath.Join(vb.option.OutputPath, name+"."+vb.option.OutputFormat)
	}
	return outputs
}

// stem 返回去掉目录与扩展名后的文件名。
func stem(path string) string {
	name, _ := strings.CutSuffix(filepath.Base(path), filepath.Ext(path))
	return name
}

// FormatCommand 将命令参数引用为可复制的命令；Windows 使用 PowerShell，其他系统使用 POSIX shell。
func FormatCommand(args []string) string {
	if runtime.GOOS == "windows" {
		return "ffmpeg " + commandline.JoinPowerShell(args)
	}
	return "ffmpeg " + commandline.Join(args)
}
