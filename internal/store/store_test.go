package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTasksRoundTripAndPresets(t *testing.T) {
	db, err := OpenDir(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	b := &task.Batch{
		Version:   1,
		ID:        "20261111-120000-abcd",
		Name:      "demo",
		Status:    task.StatusPending,
		CreatedAt: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC),
		Selection: command.Selection{Convert: true},
		Option:    command.DefaultOption(),
		Items:     []*task.Item{{ID: "001", Input: "a.mp4", Output: "out/a.mkv", Status: task.StatusPending}},
	}
	require.NoError(t, db.SaveTask(b))
	got, err := db.LoadTask(b.ID)
	require.NoError(t, err)
	assert.Equal(t, b.Name, got.Name)
	assert.True(t, got.Selection.Convert)

	list, err := db.ListTasks()
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, db.DeleteTask(b.ID))
	_, err = db.LoadTask(b.ID)
	assert.Error(t, err)

	require.NoError(t, db.SavePresets([]presets.Preset{{Name: "高画质", Args: "-c:v libx265"}}))
	ps, err := db.LoadPresets()
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, "高画质", ps[0].Name)
}

func TestOpenImportsLegacyPresetsOnce(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "presets")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, presets.SaveTo(legacy, []presets.Preset{{Name: "旧", Args: "-c copy"}}))

	t.Setenv("BATCHER_DATA_DIR", root)
	t.Setenv("BATCHER_PRESETS_DIR", legacy)

	db, err := Open()
	require.NoError(t, err)
	ps, err := db.LoadPresets()
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, "-c copy", ps[0].Args)
	require.NoError(t, db.Close())

	require.NoError(t, presets.SaveTo(legacy, []presets.Preset{{Name: "更新", Args: "-c:v libx264"}}))
	db, err = Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ps, err = db.LoadPresets()
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, "-c copy", ps[0].Args, "导入只发生一次，之后以数据库为准")
}

func TestDirUsesExecutableFolder(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "batcher")
	require.NoError(t, os.WriteFile(exe, []byte("x"), 0o755))
	t.Setenv(envDir, "")
	orig := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = orig })

	got, err := Dir()
	require.NoError(t, err)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	assert.Equal(t, dir, got)
}
