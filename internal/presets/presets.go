// Package presets 管理转码高级参数预设。
//
// 正在使用的预设存在可执行文件旁的 batcher.db 里，由 Bind 接上存储。
// advance_presets.json 只用于第一次打开时导入旧文件，以及文件层测试；
// 目录可用 BATCHER_PRESETS_DIR 覆盖。
package presets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// Store 持久化自定义预设。由 bbolt 实现，界面启动时 Bind。
type Store interface {
	LoadPresets() ([]Preset, error)
	SavePresets([]Preset) error
}

var (
	storeMu sync.RWMutex
	active  Store
)

// Bind 设置预设存储。传 nil 时 Load 返回空列表，Save 返回错误。
func Bind(s Store) {
	storeMu.Lock()
	active = s
	storeMu.Unlock()
}

func currentStore() Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return active
}

// Load 读取自定义预设。存储未接上时返回空列表。
func Load() ([]Preset, error) {
	s := currentStore()
	if s == nil {
		return nil, nil
	}
	list, err := s.LoadPresets()
	if err != nil {
		return nil, err
	}
	return normalizePresets(list), nil
}

// Save 去重后写入当前预设存储，保序。
func Save(list []Preset) error {
	s := currentStore()
	if s == nil {
		return errors.New("预设存储未初始化")
	}
	if err := s.SavePresets(normalizePresets(list)); err != nil {
		return fmt.Errorf("保存预设: %w", err)
	}
	return nil
}

// LoadFrom 从指定目录读取用户预设列表；文件不存在时返回空列表（非错误）。
func LoadFrom(dir string) ([]Preset, error) {
	if dir == "" {
		return nil, errors.New("预设目录不能为空")
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
		return errors.New("预设目录不能为空")
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
