// Package presets 管理转码高级参数预设：内置预设（只读）+ 用户自定义预设。
//
// 用户预设保存为 <CLI 所在目录>/presets/advance_presets.json，目录在保存时自动创建；
// 可通过环境变量 BATCHER_PRESETS_DIR 覆盖存放目录（便于开发与测试）。
package presets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsxhnd/batcher/internal/commandline"
)

// PresetFile 用户预设文件名（位于预设文件夹内）。
const PresetFile = "advance_presets.json"

// Preset 一条用户自定义转码参数：Name 为界面上的标签（可为空），Args 为传给 ffmpeg 的参数。
type Preset struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

// UnmarshalJSON 兼容旧版文件：旧版每条预设只是一个参数字符串，读取后视为未命名预设。
func (p *Preset) UnmarshalJSON(data []byte) error {
	var args string
	if err := json.Unmarshal(data, &args); err == nil {
		*p = Preset{Args: args}
		return nil
	}
	// 使用不带方法的别名类型，避免递归调用本方法。
	type plain Preset
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = Preset(v)
	return nil
}

// envDir 允许通过环境变量覆盖预设目录，主要供开发与测试使用。
const envDir = "BATCHER_PRESETS_DIR"

// executablePath 返回当前进程可执行文件路径，测试中可替换。
var executablePath = os.Executable

// Dir 返回用户预设所在文件夹：默认为 CLI 可执行文件所在目录下的 presets/。
func Dir() (string, error) {
	if v := os.Getenv(envDir); v != "" {
		return v, nil
	}
	return dirFromExe()
}

// dirFromExe 由可执行文件路径推导默认预设目录。
func dirFromExe() (string, error) {
	exe, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("解析可执行文件路径: %w", err)
	}
	// 解析符号链接，保证 macOS 上返回真实的安装位置。
	if eval, err := filepath.EvalSymlinks(exe); err == nil {
		exe = eval
	}
	return filepath.Join(filepath.Dir(exe), "presets"), nil
}

// FilePath 返回用户预设文件完整路径（目录未必已创建）。
func FilePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, PresetFile), nil
}

// Load 读取用户预设列表；文件不存在时返回空列表（非错误）。
func Load() ([]Preset, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(dir)
}

// Save 覆写保存用户预设列表（自动创建预设文件夹、去重、保序）。
func Save(presets []Preset) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return SaveTo(dir, presets)
}

// LoadFrom 从指定目录读取用户预设列表；文件不存在时返回空列表（非错误）。
func LoadFrom(dir string) ([]Preset, error) {
	if dir == "" {
		return nil, fmt.Errorf("预设目录不能为空")
	}
	path := filepath.Join(dir, PresetFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取预设 %s: %w", path, err)
	}
	var presets []Preset
	if err := json.Unmarshal(data, &presets); err != nil {
		return nil, fmt.Errorf("解析预设 %s: %w", path, err)
	}
	return normalizePresets(presets), nil
}

// SaveTo 覆写保存用户预设列表到指定目录（自动创建目录）。
// 参数为空的条目会被丢弃；参数相同的条目只保留第一条，名称与参数均去除首尾空白。
func SaveTo(dir string, presets []Preset) (saveErr error) {
	if dir == "" {
		return fmt.Errorf("预设目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建预设目录 %s: %w", dir, err)
	}
	path := filepath.Join(dir, PresetFile)
	data, err := json.MarshalIndent(normalizePresets(presets), "", "  ")
	if err != nil {
		return fmt.Errorf("编码预设: %w", err)
	}
	file, err := os.CreateTemp(dir, ".advance_presets-*")
	if err != nil {
		return fmt.Errorf("创建预设临时文件: %w", err)
	}
	// 写入失败时关闭并清理临时文件，保留原始错误和清理错误。
	closed := false
	defer func() {
		if !closed {
			if err := file.Close(); err != nil {
				saveErr = errors.Join(saveErr, fmt.Errorf("关闭预设临时文件: %w", err))
			}
		}
		if saveErr != nil {
			if err := os.Remove(file.Name()); err != nil && !os.IsNotExist(err) {
				saveErr = errors.Join(saveErr, fmt.Errorf("清理预设临时文件: %w", err))
			}
		}
	}()
	if err := file.Chmod(0o644); err != nil {
		return fmt.Errorf("设置预设文件权限: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("写入预设临时文件: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步预设文件: %w", err)
	}
	err = file.Close()
	closed = true
	if err != nil {
		return fmt.Errorf("关闭预设文件: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("替换预设 %s: %w", path, err)
	}
	return nil
}

func normalizePresets(presets []Preset) []Preset {
	seen := make(map[string]struct{}, len(presets))
	uniq := make([]Preset, 0, len(presets))
	for _, p := range presets {
		p = Preset{Name: strings.TrimSpace(p.Name), Args: Trim(p.Args)}
		if p.Args == "" {
			continue
		}
		if _, ok := seen[p.Args]; ok {
			continue
		}
		seen[p.Args] = struct{}{}
		uniq = append(uniq, p)
	}
	return uniq
}

// Upsert 向预设列表追加一条预设（按参数文本去重，已存在则保持原条目不变），返回新列表。
func Upsert(existing []Preset, p Preset) []Preset {
	p.Name = strings.TrimSpace(p.Name)
	p.Args = Trim(p.Args)
	if p.Args == "" {
		return existing
	}
	for _, e := range existing {
		if e.Args == p.Args {
			return existing
		}
	}
	return append(existing, p)
}

// Trim 规范化高级参数，保留引号内的空白和转义语义。
func Trim(s string) string {
	return commandline.Normalize(s)
}
