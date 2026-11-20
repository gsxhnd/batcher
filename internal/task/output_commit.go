package task

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// createPartialOutput 用随机名和 O_EXCL 独占创建临时文件。
// 0666 会经过进程 umask，与 ffmpeg 直接创建正式文件时的权限语义一致。
func createPartialOutput(output string) (string, error) {
	dir, ext := filepath.Dir(output), filepath.Ext(output)
	for range 100 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", fmt.Errorf("生成临时输出名称: %w", err)
		}
		partial := filepath.Join(dir, ".batcher-"+hex.EncodeToString(suffix[:])+ext)
		file, err := os.OpenFile(partial, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("创建临时输出: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(partial)
			return "", fmt.Errorf("关闭临时输出: %w", err)
		}
		return partial, nil
	}
	return "", errors.New("无法分配唯一的临时输出路径")
}

// commitPartialOutput 确认 ffmpeg 确实生成了非空普通文件，再提交为正式输出。
// overwrite 只控制这一步；ffmpeg 本身始终只写同目录临时文件。
func commitPartialOutput(partial, output string, overwrite bool) error {
	if partial == "" {
		return errors.New("本次尝试没有临时输出路径")
	}
	info, err := os.Stat(partial)
	if err != nil {
		return fmt.Errorf("检查临时输出: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("临时输出不是普通文件")
	}
	if info.Size() == 0 {
		return errors.New("ffmpeg 未生成非空输出文件")
	}
	if err := replacePartialOutput(partial, output, overwrite); err != nil {
		return fmt.Errorf("提交正式输出: %w", err)
	}
	return nil
}
