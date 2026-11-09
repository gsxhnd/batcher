package presets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFrom_FileNotExists(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadFrom(dir)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSaveToAndLoadFrom(t *testing.T) {
	dir := t.TempDir()

	want := []string{"-c:v h265 -crf 20 -c:a copy", "-c:v libx264 -preset fast"}
	require.NoError(t, SaveTo(dir, want))

	// 保存后应创建预设文件夹与文件
	assert.DirExists(t, dir)
	assert.FileExists(t, filepath.Join(dir, PresetFile))

	got, err := LoadFrom(dir)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSaveToNormalizesAndDedupes(t *testing.T) {
	dir := t.TempDir()

	in := []string{
		"  -c:v   h265  -crf 20  ",
		"-c:v h265 -crf 20", // 规范化后与前一条重复
		"",
		"   ",
		"-c copy",
	}
	require.NoError(t, SaveTo(dir, in))

	got, err := LoadFrom(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"-c:v h265 -crf 20", "-c copy"}, got, "应去空白、去重、保序")
}

func TestUpsert(t *testing.T) {
	base := []string{"-c:v h265 -crf 20 -c:a copy"}

	got := Upsert(base, "  -c:v   h265  -crf 20  -c:a copy ")
	assert.Equal(t, base, got, "重复参数不应重复添加")

	got = Upsert(base, "-c:v nvenc -cq 23")
	assert.Len(t, got, 2)

	assert.Equal(t, base, Upsert(base, "   "), "空白参数不应添加")
}

func TestBuiltins(t *testing.T) {
	bs := Builtins()
	require.NotEmpty(t, bs)
	for _, b := range bs {
		assert.NotEmpty(t, b.Name)
		assert.NotEmpty(t, b.Args)
		assert.Equal(t, "-", b.Args[:1])
	}

	// 修改副本不应影响内置列表
	bs[0].Args = "mutated"
	assert.Equal(t, "-c copy", Builtins()[0].Args)
}

func TestDirEnvOverride(t *testing.T) {
	t.Setenv(envDir, t.TempDir())
	dir, err := Dir()
	require.NoError(t, err)
	assert.DirExists(t, dir)
}

func TestDirDefaultExecDir(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "batcher")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv(envDir, "")

	orig := executablePath
	executablePath = func() (string, error) { return exe, nil }
	defer func() { executablePath = orig }()

	got, err := Dir()
	require.NoError(t, err)
	// EvalSymlinks 会把 macOS 的 /var 解析为 /private/var，预期值同样解析后再比较。
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	assert.Equal(t, filepath.Join(dir, "presets"), got)
}
