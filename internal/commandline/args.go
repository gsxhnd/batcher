// Package commandline 解析和展示参数，不启动 shell，也不展开变量、通配符或命令替换。
package commandline

import (
	"fmt"
	"strings"
	"unicode"
)

// Parse 按空白分隔参数，支持单/双引号和对空白、引号、反斜杠的转义。
// 其余反斜杠原样保留，兼容 Windows 路径和 ffmpeg 滤镜表达式。
func Parse(s string) ([]string, error) {
	var (
		args   []string
		value  strings.Builder
		quote  rune
		active bool
	)
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote == '\'' {
			if r == quote {
				quote = 0
			} else {
				value.WriteRune(r)
			}
			continue
		}
		if r == '\\' && i+1 < len(runes) {
			next := runes[i+1]
			if next == '\\' || next == '"' || (quote == 0 && (next == '\'' || unicode.IsSpace(next))) {
				value.WriteRune(next)
				active = true
				i++
				continue
			}
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				value.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote, active = r, true
		case unicode.IsSpace(r):
			if active {
				args = append(args, value.String())
				value.Reset()
				active = false
			}
		default:
			value.WriteRune(r)
			active = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("参数中的引号未闭合")
	}
	if active {
		args = append(args, value.String())
	}
	return args, nil
}

// Join 将参数引用为 POSIX shell 可复制的文本；解析该文本可还原原参数。
func Join(args []string) string { return join(args, false) }

// JoinPowerShell 将参数引用为 PowerShell 可复制的文本。
func JoinPowerShell(args []string) string { return join(args, true) }

func join(args []string, powershell bool) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if isBare(arg) && (!powershell || !strings.ContainsAny(arg, "@%,")) {
			quoted[i] = arg
			continue
		}
		escaped := strings.ReplaceAll(arg, "'", "'\\''")
		if powershell {
			escaped = strings.ReplaceAll(arg, "'", "''")
		}
		quoted[i] = "'" + escaped + "'"
	}
	return strings.Join(quoted, " ")
}

func isBare(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r) {
			continue
		}
		return false
	}
	return true
}

// Normalize 规范化参数间的空白，但保留参数内部的空白与转义语义。
// 尚未输入完整的参数只去除首尾空白，交由调用方显示解析错误。
func Normalize(s string) string {
	args, err := Parse(s)
	if err != nil {
		return strings.TrimSpace(s)
	}
	return Join(args)
}
