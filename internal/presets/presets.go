// Package presets 管理转码高级参数预设：内置预设（只读）+ 用户自定义预设。
//
// 用户预设保存为 <CLI 所在目录>/presets/advance_presets.json，目录在保存时自动创建；
// 可通过环境变量 BATCHER_PRESETS_DIR 覆盖存放目录（便于开发与测试）。
package presets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		return "", fmt.Errorf("resolve executable path: %w", err)
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
		return nil, fmt.Errorf("preset directory cannot be empty")
	}
	path := filepath.Join(dir, PresetFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read presets %s: %w", path, err)
	}
	var presets []Preset
	if err := json.Unmarshal(data, &presets); err != nil {
		return nil, fmt.Errorf("parse presets %s: %w", path, err)
	}
	return presets, nil
}

// SaveTo 覆写保存用户预设列表到指定目录（自动创建目录）。
// 参数为空的条目会被丢弃；参数相同的条目只保留第一条，名称与参数均去除首尾空白。
func SaveTo(dir string, presets []Preset) error {
	if dir == "" {
		return fmt.Errorf("preset directory cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create preset dir %s: %w", dir, err)
	}

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

	path := filepath.Join(dir, PresetFile)
	data, err := json.MarshalIndent(uniq, "", "  ")
	if err != nil {
		return fmt.Errorf("encode presets: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write presets %s: %w", path, err)
	}
	return nil
}

// Upsert 向预设列表追加一条预设（按参数文本去重，已存在则保持原条目不变），返回新列表。
func Upsert(existing []Preset, p Preset) []Preset {
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

// Trim 规范化一条高级参数字符串（去首尾空白并压缩连续空格）。
func Trim(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
