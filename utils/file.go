package utils

import (
	"fmt"
	"os"
)

// MakeDir 创建目录，如果目录不存在则创建
func MakeDir(fullPath string) error {
	if fullPath == "" {
		return fmt.Errorf("path cannot be empty")
	}

	fs, err := os.Stat(fullPath)
	if err == nil {
		// 路径已存在
		if !fs.IsDir() {
			return fmt.Errorf("path exists but is not a directory: %s", fullPath)
		}
		return nil
	}

	if os.IsNotExist(err) {
		if err := os.MkdirAll(fullPath, os.ModePerm); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		return nil
	}

	return fmt.Errorf("stat path: %w", err)
}
