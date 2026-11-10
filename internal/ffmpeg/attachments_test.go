package ffmpeg

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddingFontsPreservesExistingAttachmentMetadata(t *testing.T) {
	fx := newMediaFixture(t)
	root := t.TempDir()
	attachment := filepath.Join(root, "原附件.bin")
	writeFile(t, attachment, "原附件内容")
	source := filepath.Join(root, "source.mkv")
	runFixtureFFmpeg(t, []string{
		"-nostdin", "-n", "-v", "error", "-i", fx.video, "-map", "0", "-c", "copy",
		"-attach", attachment, "-metadata:s:t:0", "mimetype=application/octet-stream", source,
	})
	opt := DefaultOption()
	opt.InputPath, opt.InputFormat, opt.FontsPath = source, "mkv", fx.fonts
	opt.OutputPath = t.TempDir()
	plan, err := BuildPlan(context.Background(), &opt, Selection{AddFonts: true})
	require.NoError(t, err)
	var logs string
	err = plan.Execute(context.Background(), func(e Event) { logs += e.Message + "\n" })
	require.NoError(t, err, logs)
	attachments := make(map[string]string)
	for _, stream := range probeStreams(t, plan.Jobs[0].Output) {
		if stream.Type == "attachment" {
			attachments[stream.Tags["filename"]] = stream.Tags["mimetype"]
		}
	}
	assert.Equal(t, map[string]string{
		"原附件.bin": "application/octet-stream",
		"测试.ttf":  "application/x-truetype-font",
	}, attachments)
}
