//go:build !windows

package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverwriteProbeCancellationKeepsOldOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "等待输入.mkv")
	// 没有写入端的 FIFO 会让 ffprobe 阻塞在打开输入，取消必须发生在 ffmpeg 启动前。
	require.NoError(t, syscall.Mkfifo(input, 0o600))
	output := filepath.Join(root, "输出", "result.mkv")
	writeOldOutput(t, output)
	plan := oneJobPlan(input, output, "-nostdin", "-n", "-i", input, "-c", "copy", output)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	probeStarted := make(chan struct{})
	var once sync.Once
	q := NewQueue(ctx, func(e QueueEvent) {
		if e.Type == QueueFFmpeg && e.FFmpegEvt != nil && e.FFmpegEvt.Type == ffmpeg.EventCommandStart {
			once.Do(func() { close(probeStarted) })
		}
	})
	defer q.Stop()
	b := startOverwriteAttempt(t, q, plan)
	select {
	case <-probeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("等待 ffprobe 阶段开始超时")
	}
	cancel()

	done := waitBatchTerminal(t, q, b.ID, 10*time.Second)
	assert.Equal(t, StatusCanceled, done.Status)
	assert.Equal(t, StatusCanceled, done.Items[0].Status)
	assert.False(t, done.Items[0].PartialOutput)
	assert.Empty(t, done.Items[0].PartialPath)
	assertOldOutputKept(t, output)
	assertNoTemporaryOutputs(t, output)
	entries, err := os.ReadDir(filepath.Dir(output))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "取消后只留下旧正式输出")
}

func TestNoReplaceCommitRejectsExistingOutput(t *testing.T) {
	dir := t.TempDir()
	partial := filepath.Join(dir, ".batcher-test.mkv")
	output := filepath.Join(dir, "result.mkv")
	require.NoError(t, os.WriteFile(partial, []byte("新输出"), 0o600))
	require.NoError(t, os.WriteFile(output, []byte("已有输出"), 0o600))

	err := replacePartialOutput(partial, output, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrExist)
	for path, want := range map[string]string{partial: "新输出", output: "已有输出"} {
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		assert.Equal(t, want, string(data))
	}
}

func TestCommitKeepsFormalOutputWhenPartialCleanupFails(t *testing.T) {
	dir := t.TempDir()
	partial := filepath.Join(dir, ".batcher-test.mkv")
	output := filepath.Join(dir, "result.mkv")
	require.NoError(t, os.WriteFile(partial, []byte("完整输出"), 0o600))
	original := removePartialFile
	removePartialFile = func(string) error { return errors.New("模拟清理失败") }
	t.Cleanup(func() { removePartialFile = original })

	err := linkPartialOutput(partial, output)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "正式输出已保留")
	for _, path := range []string{partial, output} {
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr, "异常回滚不能按路径删除文件: %s", path)
		assert.Equal(t, "完整输出", string(data))
	}
}

func TestPartialOutputUsesProcessUmask(t *testing.T) {
	umask := syscall.Umask(0)
	syscall.Umask(umask)
	output := filepath.Join(t.TempDir(), "result.mkv")
	partial, err := (&Queue{}).prepareOutput(output, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(partial) })
	info, err := os.Stat(partial)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666&^umask), info.Mode().Perm(), "临时输出权限必须沿用当前 umask")
}

func TestTemporaryOutputDoesNotRepeatLongStem(t *testing.T) {
	output := filepath.Join(t.TempDir(), strings.Repeat("a", 240)+".mkv")
	q := &Queue{}
	partial, err := q.prepareOutput(output, false)
	require.NoError(t, err, "正式文件名合法时，临时文件名也不能超过文件系统限制")
	require.NoError(t, os.Remove(partial))
}
