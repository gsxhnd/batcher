package ffmpeg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AdvancePresetFile 保存转码高级参数预设的文件名（位于 ~/.batcher/ 下）。
const AdvancePresetFile = "advance_presets.json"

// advancePresetPath 返回预设文件完整路径（~/.batcher/advance_presets.json），目录不存在则创建。
// home 为空时的行为交由 os.UserHomeDir 的错误反映。
func advancePresetPath(home string) (string, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
	}
	dir := filepath.Join(home, ".batcher")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir %s: %w", dir, err)
	}
	return filepath.Join(dir, AdvancePresetFile), nil
}

// LoadAdvancePresets 读取已保存的转码高级参数预设列表；文件不存在时返回空列表（非错误）。
func LoadAdvancePresets() ([]string, error) {
	return loadAdvancePresets("")
}

func loadAdvancePresets(home string) ([]string, error) {
	path, err := advancePresetPath(home)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read presets %s: %w", path, err)
	}
	var presets []string
	if err := json.Unmarshal(data, &presets); err != nil {
		return nil, fmt.Errorf("parse presets %s: %w", path, err)
	}
	return presets, nil
}

// UpsertPreset 向预设列表追加一条参数（按参数文本去重），返回新列表。
func UpsertPreset(existing []string, args string) []string {
	args = trimArgs(args)
	if args == "" {
		return existing
	}
	for _, p := range existing {
		if p == args {
			return existing
		}
	}
	return append(existing, args)
}

// SaveAdvancePresets 覆写保存预设列表（自动去重、保序）。
func SaveAdvancePresets(presets []string) error {
	return saveAdvancePresets("", presets)
}

func saveAdvancePresets(home string, presets []string) error {
	path, err := advancePresetPath(home)
	if err != nil {
		return err
	}
	// 去重、去掉空白项，保持插入顺序
	seen := make(map[string]struct{}, len(presets))
	uniq := make([]string, 0, len(presets))
	for _, p := range presets {
		p = trimArgs(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		uniq = append(uniq, p)
	}

	data, err := json.MarshalIndent(uniq, "", "  ")
	if err != nil {
		return fmt.Errorf("encode presets: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write presets %s: %w", path, err)
	}
	return nil
}

// trimArgs 规范化一条高级参数字符串（去首尾空白并压缩连续空格）。
func trimArgs(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
