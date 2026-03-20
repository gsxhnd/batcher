package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/gsxhnd/batcher/utils"
)

type VideoBatchOption struct {
	InputPath      string
	InputFormat    string
	OutputPath     string
	OutputFormat   string
	FontsPath      string
	InputSubSuffix string
	InputSubNo     int
	InputSubTitle  string
	InputSubLang   string
	Advance        string
	Workers        int // 并发工作数
}

type VideoBatcher interface {
	GetVideosList() ([]string, error)         // 获取视频列表
	GetFontsList() ([]string, error)          // 获取字体列表
	GetFontsParams() ([]string, error)        // 获取字体参数
	GetConvertBatch() ([][]string, error)     // 获取转换视频命令
	GetAddFontsBatch() ([][]string, error)    // 获取添加字体命令
	GetAddSubtitleBatch() ([][]string, error) // 获取添加字幕命令
	ExecuteBatch(ctx context.Context, batchCmd [][]string) error
}

type videoBatch struct {
	option     *VideoBatchOption
	cmdBatches [][]string
}

var fontExtensions = []string{".ttf", ".otf", ".ttc"}

func NewVideoBatch(opt *VideoBatchOption) (VideoBatcher, error) {
	if opt == nil {
		return nil, fmt.Errorf("option cannot be nil")
	}
	if err := utils.MakeDir(opt.OutputPath); err != nil {
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

// ExecuteBatch 执行批处理命令，支持并发和 context 取消
func (vb *videoBatch) ExecuteBatch(ctx context.Context, cmdBatch [][]string) error {
	if len(cmdBatch) == 0 {
		return nil
	}

	// 单线程模式
	if vb.option.Workers == 1 {
		return vb.executeSequential(ctx, cmdBatch)
	}

	// 并发模式
	return vb.executeConcurrent(ctx, cmdBatch)
}

func (vb *videoBatch) executeSequential(ctx context.Context, cmdBatch [][]string) error {
	for _, args := range cmdBatch {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := vb.executeCommand(args); err != nil {
			return fmt.Errorf("execute command: %w", err)
		}
	}
	return nil
}

func (vb *videoBatch) executeConcurrent(ctx context.Context, cmdBatch [][]string) error {
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)

	// 使用信号量控制并发数
	sem := make(chan struct{}, vb.option.Workers)

	for _, args := range cmdBatch {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(a []string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()

				if err := vb.executeCommand(a); err != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("execute command: %w", err)
					})
				}
			}
		}(args)
	}

	wg.Wait()
	return firstErr
}

func (vb *videoBatch) executeCommand(args []string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ffmpeg.exe", args...)
	} else {
		cmd = exec.Command("ffmpeg", args...)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
