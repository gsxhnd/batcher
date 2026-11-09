package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/gsxhnd/batcher/internal/utils"
)

type VideoBatchOption struct {
	InputPath      string `json:"inputPath"`
	InputFormat    string `json:"inputFormat"`
	OutputPath     string `json:"outputPath"`
	OutputFormat   string `json:"outputFormat"`
	FontsPath      string `json:"fontsPath"`
	InputSubSuffix string `json:"inputSubSuffix"`
	InputSubNo     int    `json:"inputSubNo"`
	InputSubTitle  string `json:"inputSubTitle"`
	InputSubLang   string `json:"inputSubLang"`
	// Subtitles 为多条字幕列表（TUI/GUI 组合流程使用）；为空时回退到 InputSub* 单字幕字段。
	Subtitles []SubtitleOption `json:"subtitles,omitempty"`
	Advance   string           `json:"advance"`
	Workers   int              `json:"workers"` // 并发工作数

	// OnEvent 不为空时，执行进度和 ffmpeg 输出会通过它回传（可能被多个 goroutine 并发调用，
	// 但调用之间已串行化）；为空时 ffmpeg 输出直接写到进程的 stdout/stderr。
	OnEvent func(Event) `json:"-"`
}

// DefaultOption 返回各字段的默认值，CLI 的 flag 默认值和 GUI 表单初始值都来自这里。
func DefaultOption() VideoBatchOption {
	return VideoBatchOption{
		InputPath:      "./",
		InputFormat:    "mp4",
		OutputPath:     "./result/",
		OutputFormat:   "mkv",
		InputSubSuffix: "ass",
		InputSubNo:     0,
		InputSubLang:   "chi",
		InputSubTitle:  "Chinese",
		Workers:        1,
	}
}

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

// SubtitleOption 描述一条字幕：文件按 <视频名>.<Suffix> 或 <视频名>.<其他>.<Suffix> 匹配，
// 例如视频 1.mp4 的字幕可以是 1.ass，也可以是 1.chi.ass。
type SubtitleOption struct {
	Suffix string `json:"suffix"` // 字幕扩展名，如 ass / srt
	Lang   string `json:"lang"`   // 语言缩写，如 chi / eng
	Title  string `json:"title"`  // 字幕轨道标题
}

// subtitleEntries 返回参与命令生成的字幕列表。
// 仅当 VideoBatchOption.Subtitles 为空时，回退为旧版单字幕字段（兼容 CLI add_sub）。
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

	vb.emit(Event{Type: EventBatchStart, Total: len(cmdBatch)})

	if vb.option.Workers <= 1 {
		return vb.executeSequential(ctx, cmdBatch)
	}
	return vb.executeConcurrent(ctx, cmdBatch)
}

func (vb *videoBatch) executeSequential(ctx context.Context, cmdBatch [][]string) error {
	for i, args := range cmdBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := vb.executeCommand(ctx, i, len(cmdBatch), args); err != nil {
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

	for i, args := range cmdBatch {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(i int, a []string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			if err := vb.executeCommand(ctx, i, len(cmdBatch), a); err != nil {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("execute command: %w", err)
				})
			}
		}(i, args)
	}

	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func (vb *videoBatch) executeCommand(ctx context.Context, index, total int, args []string) error {
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)

	if vb.option.OnEvent == nil {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	command := FormatCommand(args)
	vb.emit(Event{Type: EventCommandStart, Index: index, Total: total, Command: command})

	lw := newLineWriter(func(line string) {
		vb.emit(Event{Type: EventLog, Index: index, Total: total, Message: line})
	})
	cmd.Stdout = lw
	cmd.Stderr = lw

	err := cmd.Run()
	lw.Flush()

	if err != nil {
		vb.emit(Event{Type: EventCommandFailed, Index: index, Total: total, Command: command, Message: err.Error()})
		return err
	}
	vb.emit(Event{Type: EventCommandDone, Index: index, Total: total, Command: command})
	return nil
}

func (vb *videoBatch) emit(e Event) {
	if vb.option.OnEvent == nil {
		return
	}
	vb.eventMu.Lock()
	defer vb.eventMu.Unlock()
	vb.option.OnEvent(e)
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
