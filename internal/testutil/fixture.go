// Package testutil 提供各包测试共用的临时占位文件。
//
// 这些文件不是真实媒体文件：命令规划的测试只校验“按文件名/后缀生成的命令”，
// 不会真正执行转码，因此内容为空即可。它们在 t.TempDir() 内生成、测试结束自动清理，
// 无需手工命令预先创建 testdata。
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fixture 描述临时占位测试文件。
type Fixture struct {
	Root string // 输入根目录，含 1/ 2/ 两个子目录
	Dir1 string // 子目录 1，也用作字体目录
	Dir2 string // 子目录 2
	Out  string // 输出目录

	MP4s  []string // 1/1.mp4, 1/2.mp4, 2/1.mp4, 2/2.mp4
	MKVs  []string // 1/1.mkv, 1/2.mkv, 2/1.mkv, 2/2.mkv
	Fonts []string // 1/1.ttf, 1/2.ttf, 2/1.ttf, 2/2.ttf
}

// NewFixture 在临时目录生成占位测试文件：
//
//	root/1/{1,2}.{mkv,mp4,ass,ttf}
//	root/2/{1,2}.{mkv,mp4,ass,ttf}
func NewFixture(t *testing.T) Fixture {
	t.Helper()
	root := t.TempDir()
	fx := Fixture{
		Root: root,
		Dir1: filepath.Join(root, "1"),
		Dir2: filepath.Join(root, "2"),
		Out:  filepath.Join(root, "output"),
	}
	names := []string{"1.mkv", "1.mp4", "1.ass", "1.ttf", "2.mkv", "2.mp4", "2.ass", "2.ttf"}
	for _, dir := range []string{fx.Dir1, fx.Dir2} {
		require.NoError(t, os.MkdirAll(dir, 0o755), "create fixture dir")
		for _, n := range names {
			WriteFile(t, filepath.Join(dir, n), "")
		}
	}
	fx.MP4s = []string{
		filepath.Join(fx.Dir1, "1.mp4"), filepath.Join(fx.Dir1, "2.mp4"),
		filepath.Join(fx.Dir2, "1.mp4"), filepath.Join(fx.Dir2, "2.mp4"),
	}
	fx.MKVs = []string{
		filepath.Join(fx.Dir1, "1.mkv"), filepath.Join(fx.Dir1, "2.mkv"),
		filepath.Join(fx.Dir2, "1.mkv"), filepath.Join(fx.Dir2, "2.mkv"),
	}
	fx.Fonts = []string{
		filepath.Join(fx.Dir1, "1.ttf"), filepath.Join(fx.Dir1, "2.ttf"),
		filepath.Join(fx.Dir2, "1.ttf"), filepath.Join(fx.Dir2, "2.ttf"),
	}
	return fx
}

// AttachCmd 生成 fonts 字体列表对应的 -attach 参数段期望值。
func AttachCmd(fonts []string) []string {
	cmd := make([]string, 0, len(fonts)*4)
	for _, fontPath := range fonts {
		cmd = append(cmd,
			"-attach", fontPath,
			"-metadata:s:t:m:filename:"+filepath.Base(fontPath),
			"mimetype=application/x-truetype-font",
		)
	}
	return cmd
}

// Outputs 生成输出路径期望值：1.mkv / 2.mkv / 1-1.mkv / 2-1.mkv（重名附加序号）。
func Outputs(fx Fixture, ext string) []string {
	return []string{
		filepath.Join(fx.Out, "1."+ext),
		filepath.Join(fx.Out, "2."+ext),
		filepath.Join(fx.Out, "1-1."+ext),
		filepath.Join(fx.Out, "2-1."+ext),
	}
}

// WriteFile 写入测试文件，失败时直接终止测试。
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
