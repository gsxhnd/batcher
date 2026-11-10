// Package media 负责输入输出文件的扫描与路径规划，只读文件系统，不启动外部进程。
package media

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// FontExtensions 是可内嵌的字体文件后缀。
var FontExtensions = []string{".ttf", ".otf", ".ttc"}

// Videos 递归列出 inputPath 下后缀为 format（不区分大小写）的视频；
// 输出目录独立于输入目录时，其中的文件会被排除。
func Videos(ctx context.Context, inputPath, outputPath, format string) ([]string, error) {
	input, err := filepath.Abs(inputPath)
	if err != nil {
		return nil, fmt.Errorf("解析输入目录: %w", err)
	}
	output, err := filepath.Abs(outputPath)
	if err != nil {
		return nil, fmt.Errorf("解析输出目录: %w", err)
	}
	exclude := ""
	if input != output {
		exclude = output
	}
	return WalkFiles(ctx, inputPath, exclude, func(name string) bool {
		return strings.EqualFold(filepath.Ext(name), "."+format)
	})
}

// Fonts 递归列出 dir 下的字体文件。
func Fonts(ctx context.Context, dir string) ([]string, error) {
	return WalkFiles(ctx, dir, "", func(name string) bool {
		return slices.Contains(FontExtensions, strings.ToLower(filepath.Ext(name)))
	})
}

// WalkFiles 递归遍历 root，返回文件名满足 match 的文件路径（跳过目录）。
func WalkFiles(ctx context.Context, root, exclude string, match func(name string) bool) ([]string, error) {
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

// ListFiles 返回 dir 下（不递归）的文件名，不含子目录。
func ListFiles(dir string) ([]string, error) {
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

// Stem 返回去掉目录与扩展名后的文件名。
func Stem(path string) string {
	name, _ := strings.CutSuffix(filepath.Base(path), filepath.Ext(path))
	return name
}

// OutputPaths 为每个输入视频生成输出文件路径，同名输入追加 -1、-2 ... 后缀以避免覆盖。
func OutputPaths(inputs []string, outputDir, format string) map[string]string {
	outputs := make(map[string]string, len(inputs))
	used := make(map[string]bool, len(inputs))
	next := make(map[string]int)

	for _, videoPath := range inputs {
		base := Stem(videoPath)
		name := base
		for used[strings.ToLower(name)] {
			next[base]++
			name = fmt.Sprintf("%s-%d", base, next[base])
		}
		used[strings.ToLower(name)] = true

		outputs[videoPath] = filepath.Join(outputDir, name+"."+format)
	}
	return outputs
}

// CheckPaths 校验一组任务的输入输出：输出不能与任何输入重合，也不能互相重名；
// checkExisting 为真时，已存在的输出文件同样视为错误（只有执行前才检查）。
func CheckPaths(ctx context.Context, inputs, outputs []string, checkExisting bool) error {
	inputKeys := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := pathKey(input)
		if err != nil {
			return err
		}
		inputKeys[key] = true
	}
	seen := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := pathKey(output)
		if err != nil {
			return err
		}
		if inputKeys[key] {
			return fmt.Errorf("输出路径与输入文件重合: %s", output)
		}
		if seen[key] {
			return fmt.Errorf("多个任务指向同一输出文件: %s", output)
		}
		seen[key] = true
		if checkExisting {
			if _, err := os.Lstat(output); err == nil {
				return fmt.Errorf("输出文件已存在，请先移动或删除后重试: %s: %w", output, os.ErrExist)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("检查输出文件 %s: %w", output, err)
			}
		}
	}
	return nil
}

func pathKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析文件路径 %s: %w", path, err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
}

// MakeDir 创建目录；路径已存在且为目录时直接成功。
func MakeDir(fullPath string) error {
	if fullPath == "" {
		return fmt.Errorf("目录路径不能为空")
	}

	info, err := os.Stat(fullPath)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("路径已存在但不是目录: %s", fullPath)
		}
		return nil
	}
	if os.IsNotExist(err) {
		if err := os.MkdirAll(fullPath, os.ModePerm); err != nil {
			return fmt.Errorf("创建目录: %w", err)
		}
		return nil
	}
	return fmt.Errorf("检查目录: %w", err)
}
