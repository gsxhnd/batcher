package command

import (
	"testing"

	"github.com/gsxhnd/batcher/internal/commandline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"simple", []string{"-i", "input.mp4"}, "ffmpeg -i input.mp4"},
		{"empty", []string{}, "ffmpeg "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatCommand(tt.args))
		})
	}
}

func TestFormatCommandQuotesArguments(t *testing.T) {
	args := []string{"-i", "a b's;$(test).mkv", "-metadata", "title=中文  标题", "out file.mkv"}
	got := FormatCommand(args)
	assert.Contains(t, got, "ffmpeg ")
	// POSIX 规范化的参数必须可往返还原；Windows 的 PowerShell 引用另有独立测试。
	parsed, err := commandline.Parse(commandline.Join(args))
	require.NoError(t, err)
	assert.Equal(t, args, parsed)
}
