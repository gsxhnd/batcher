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
	bolt "go.etcd.io/bbolt"
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

func TestOpenSeedsBuiltinsOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BATCHER_DATA_DIR", root)
	t.Setenv("BATCHER_PRESETS_DIR", filepath.Join(root, "没有旧预设"))

	db, err := Open()
	require.NoError(t, err)
	ps, err := db.LoadPresets()
	require.NoError(t, err)
	require.Len(t, ps, len(presets.Builtins()))
	assert.Equal(t, presets.Builtins()[0].Args, ps[0].Args)
	kept := ps[1:]
	require.NoError(t, db.SavePresets(kept))
	require.NoError(t, db.Close())

	db, err = Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ps, err = db.LoadPresets()
	require.NoError(t, err)
	assert.Equal(t, kept, ps, "数据库里已经有预设时不再插入内置项")

	require.NoError(t, db.SavePresets(nil))
	require.NoError(t, db.Close())
	db, err = Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ps, err = db.LoadPresets()
	require.NoError(t, err)
	assert.Empty(t, ps, "用户删空之后不会把内置预设再写回去")
}

func TestOpenSeedsWhenMarkerExistsButListMissing(t *testing.T) {
	root := t.TempDir()
	db, err := OpenDir(root)
	require.NoError(t, err)
	require.NoError(t, db.markPresetsImported())
	require.NoError(t, db.Close())

	t.Setenv("BATCHER_DATA_DIR", root)
	t.Setenv("BATCHER_PRESETS_DIR", filepath.Join(root, "没有旧预设"))
	db, err = Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ps, err := db.LoadPresets()
	require.NoError(t, err)
	assert.Len(t, ps, len(presets.Builtins()))
}

func TestListTasksSkipsBadRecords(t *testing.T) {
	db, err := OpenDir(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	good := &task.Batch{
		Version:   1,
		ID:        "good",
		Status:    task.StatusPending,
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.SaveTask(good))
	require.NoError(t, db.bolt.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketTasks)
		if err := bucket.Put([]byte("bad-json"), []byte("{")); err != nil {
			return err
		}
		return bucket.Put([]byte("future"), []byte(`{"version":99,"id":"future"}`))
	}))

	list, err := db.ListTasks()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "good", list[0].ID)
	assert.Len(t, db.Warnings(), 2)
}

func TestOpenSkipsCorruptLegacyPresets(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "presets")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, presets.PresetFile), []byte("{"), 0o644))
	t.Setenv("BATCHER_DATA_DIR", root)
	t.Setenv("BATCHER_PRESETS_DIR", legacy)

	db, err := Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ps, err := db.LoadPresets()
	require.NoError(t, err)
	assert.Len(t, ps, len(presets.Builtins()))
	assert.NotEmpty(t, db.Warnings())
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
