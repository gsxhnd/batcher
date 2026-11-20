// Package media 负责输入输出文件的扫描与路径规划，只读文件系统，不启动外部进程。
package media

import (
	"context"
	"errors"
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

// Videos 列出 inputPath 下后缀为 format（不区分大小写）的视频。
// recursive 为真时递归全部子目录，否则只扫描第一层；
// 输出目录独立于输入目录时，其中的文件会被排除。
func Videos(ctx context.Context, inputPath, outputPath, format string, recursive bool) ([]string, error) {
	// 先确认路径存在，给一句短提示；遍历失败的原始错误里带着系统路径，
	// 界面表单已经能看到用户填的路径，提示里不再重复。
	if _, err := os.Stat(inputPath); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("输入路径不存在")
		}
		return nil, fmt.Errorf("检查输入路径: %w", err)
	}
	input, err := canonicalPath(inputPath)
	if err != nil {
		return nil, fmt.Errorf("解析输入目录: %w", err)
	}
	output, err := canonicalPath(outputPath)
	if err != nil {
		return nil, fmt.Errorf("解析输出目录: %w", err)
	}
	exclude := ""
	if !samePath(input, output) && !sameExistingFile(input, output) {
		exclude = outputPath
	}
	return WalkFiles(ctx, inputPath, exclude, recursive, func(name string) bool {
		return strings.EqualFold(filepath.Ext(name), "."+format)
	})
}

// Fonts 递归列出 dir 下的字体文件。
func Fonts(ctx context.Context, dir string) ([]string, error) {
	return WalkFiles(ctx, dir, "", true, func(name string) bool {
		return slices.Contains(FontExtensions, strings.ToLower(filepath.Ext(name)))
	})
}

// WalkFiles 遍历 root，返回文件名满足 match 的文件路径（跳过目录）。
// recursive 为真时递归全部子目录，为假时只看第一层（root 本身是文件时仍会匹配）。
// 根目录本身是符号链接时会跟着走。名为 ._ 或其它点开头的文件和目录会被跳过。
func WalkFiles(ctx context.Context, root, exclude string, recursive bool, match func(name string) bool) ([]string, error) {
	// 只有根本身是符号链接时才改变返回路径；规范化祖先仅用于比较，
	// 避免 macOS 上普通的 /var 临时目录无故变成 /private/var。
	if target, ok := symlinkDir(root); ok {
		root = target
	}
	excludeKey := ""
	var err error
	if exclude != "" {
		excludeKey, err = canonicalPath(exclude)
		if err != nil {
			return nil, fmt.Errorf("解析排除目录: %w", err)
		}
	}
	files := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// WalkDir 的错误（如 lstat/open）本身已带路径，不再包一层重复路径的前缀。
			return err
		}
		name := entry.Name()
		if path != root && strings.HasPrefix(name, ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			key, err := canonicalPath(path)
			if err != nil {
				return fmt.Errorf("解析路径 %s: %w", path, err)
			}
			if samePath(key, excludeKey) || exclude != "" && sameExistingFile(path, exclude) {
				return filepath.SkipDir
			}
			if !recursive && path != root {
				// 单层模式：不进入任何子目录。
				return filepath.SkipDir
			}
			return nil
		}
		if match(name) && regularFile(path, entry) {
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
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	return names, nil
}

func symlinkDir(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	stat, err := os.Stat(target)
	if err != nil || !stat.IsDir() {
		return "", false
	}
	return target, true
}

// canonicalPath 解析已有部分的符号链接；路径尚不存在时，从最近存在的父目录拼回剩余部分。
// 这样输入根目录和排除目录可以统一比较，输出目录还没创建也不影响规划。
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := abs
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			parts := []string{resolved}
			for i := len(missing) - 1; i >= 0; i-- {
				parts = append(parts, missing[i])
			}
			return filepath.Join(parts...), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func sameExistingFile(a, b string) bool {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false
	}
	bInfo, err := os.Stat(b)
	return err == nil && os.SameFile(aInfo, bInfo)
}

func regularFile(path string, entry fs.DirEntry) bool {
	if entry.Type().IsRegular() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
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
	inputInfos := make([]os.FileInfo, 0, len(inputs))
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := pathKey(input)
		if err != nil {
			return err
		}
		inputKeys[key] = true
		info, err := os.Stat(input)
		if err == nil {
			inputInfos = append(inputInfos, info)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查输入文件 %s: %w", input, err)
		}
	}
	seen := make(map[string]bool, len(outputs))
	seenInfos := make([]os.FileInfo, 0, len(outputs))
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
		outputInfo, statErr := os.Stat(output)
		if statErr != nil && !os.IsNotExist(statErr) {
			return fmt.Errorf("检查输出文件 %s: %w", output, statErr)
		}
		if statErr == nil {
			for _, inputInfo := range inputInfos {
				if os.SameFile(inputInfo, outputInfo) {
					return fmt.Errorf("输出路径与输入文件重合: %s", output)
				}
			}
			for _, seenInfo := range seenInfos {
				if os.SameFile(seenInfo, outputInfo) {
					return fmt.Errorf("多个任务指向同一输出文件: %s", output)
				}
			}
			seenInfos = append(seenInfos, outputInfo)
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
	abs, err := canonicalPath(path)
	if err != nil {
		return "", fmt.Errorf("解析文件路径 %s: %w", path, err)
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
}

// MakeDir 创建目录；路径已存在且为目录时直接成功。
func MakeDir(fullPath string) error {
	if fullPath == "" {
		return errors.New("目录路径不能为空")
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
