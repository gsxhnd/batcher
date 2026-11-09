// Package rename 提供文件批量重命名的核心逻辑（与 CLI / GUI 无关）。
package rename

import "context"

type Option struct {
	InputPath string `json:"inputPath"`
	MD5       bool   `json:"md5"`
}

func DefaultOption() Option {
	return Option{InputPath: "./"}
}

// Run 执行重命名。目前仅为占位实现，尚未实际重命名文件。
func Run(ctx context.Context, opt *Option) error {
	return nil
}
