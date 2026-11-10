package task

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/command"
	"github.com/gsxhnd/batcher/internal/ffmpeg"
	"github.com/gsxhnd/batcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mediaFixture struct {
	input string
	video string
	fonts string
}

// newMediaFixture 运行时生成微型媒体，不使用仓库内 testdata 或外部下载。
// 源文件含一个视频、两个音轨和一条默认字幕，用于检查流保留和新增字幕序号。
func newMediaFixture(t *testing.T) mediaFixture {
	t.Helper()
	root := t.TempDir()
	fx := mediaFixture{input: filepath.Join(root, "输入 视频"), fonts: filepath.Join(root, "字体")}
	require.NoError(t, os.MkdirAll(fx.input, 0o755))
	require.NoError(t, os.MkdirAll(fx.fonts, 0o755))
	fx.video = filepath.Join(fx.input, "episode.mkv")
	subtitle := "1\n00:00:00,000 --> 00:00:00,400\n测试字幕\n"
	original := filepath.Join(root, "原字幕.srt")
	testutil.WriteFile(t, original, subtitle)
	testutil.WriteFile(t, filepath.Join(fx.input, "episode.chi.srt"), subtitle)
	// 字体附件只检查封装，不验证字体渲染，使用临时占位内容。
	testutil.WriteFile(t, filepath.Join(fx.fonts, "测试.ttf"), "字体附件占位内容")
	runFixtureFFmpeg(t, []string{
		"-nostdin", "-n", "-v", "error",
		"-f", "lavfi", "-i", "color=c=black:s=32x32:r=2:d=0.5",
		"-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono",
		"-f", "srt", "-i", original,
		"-t", "0.5", "-map", "0:v:0", "-map", "1:a:0", "-map", "1:a:0", "-map", "2:s:0",
		"-c:v", "mpeg4", "-c:a", "pcm_s16le", "-c:s", "srt",
		"-metadata:s:s:0", "language=jpn", "-disposition:s:0", "default", fx.video,
	})
	return fx
}

func runFixtureFFmpeg(t *testing.T, args []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var logs string
	runner := &ffmpeg.Runner{OnEvent: func(e ffmpeg.Event) {
		if e.Type == ffmpeg.EventLog {
			logs += e.Message + "\n"
		}
	}}
	require.NoError(t, runner.Run(ctx, ffmpeg.Call{Total: 1, Args: args}), logs)
}

type probedStream struct {
	Type        string            `json:"codec_type"`
	Width       int               `json:"width"`
	Tags        map[string]string `json:"tags"`
	Disposition struct {
		Default int `json:"default"`
	} `json:"disposition"`
}

func probeStreams(t *testing.T, path string) []probedStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-show_streams", "-of", "json", path).Output()
	require.NoError(t, err)
	var result struct {
		Streams []probedStream `json:"streams"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	return result.Streams
}

func TestBatchPlanRealMediaCombinations(t *testing.T) {
	fx := newMediaFixture(t)
	for _, tc := range []struct {
		name string
		sel  command.Selection
	}{
		{"转码", command.Selection{Convert: true}},
		{"字幕", command.Selection{AddSubtitle: true}},
		{"字体", command.Selection{AddFonts: true}},
		{"转码与字幕", command.Selection{Convert: true, AddSubtitle: true}},
		{"转码与字体", command.Selection{Convert: true, AddFonts: true}},
		{"字幕与字体", command.Selection{AddSubtitle: true, AddFonts: true}},
		{"全部操作", command.Selection{Convert: true, AddSubtitle: true, AddFonts: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := command.DefaultOption()
			opt.InputPath, opt.InputFormat = fx.input, "mkv"
			opt.OutputPath = filepath.Join(t.TempDir(), "尚未创建")
			opt.FontsPath = fx.fonts
			opt.Advance = `-c:v mpeg4 -q:v 4 -c:a copy -vf "scale=16:16" -metadata title="标题 with  spaces"`
			opt.Subtitles = []command.SubtitleOption{{Suffix: "chi.srt", Lang: "chi", Title: "简体中文 with spaces", Default: true}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			plan, err := command.BuildPlan(ctx, &opt, tc.sel)
			require.NoError(t, err)
			require.Len(t, plan.Jobs, 1)
			assert.NoDirExists(t, opt.OutputPath)
			var logs string
			err = Execute(ctx, plan, func(e ffmpeg.Event) {
				if e.Type == ffmpeg.EventLog {
					logs += e.Message + "\n"
				}
			})
			require.NoError(t, err, logs)
			streams := probeStreams(t, plan.Jobs[0].Output)
			counts := map[string]int{}
			var subtitles []probedStream
			for _, stream := range streams {
				counts[stream.Type]++
				if stream.Type == "subtitle" {
					subtitles = append(subtitles, stream)
				}
				if stream.Type == "video" {
					wantWidth := 32
					if tc.sel.Convert {
						wantWidth = 16
					}
					assert.Equal(t, wantWidth, stream.Width)
				}
			}
			assert.Equal(t, 1, counts["video"])
			assert.Equal(t, 2, counts["audio"], "所有音轨必须保留")
			if tc.sel.AddSubtitle {
				require.Len(t, subtitles, 2)
				assert.Equal(t, "chi", subtitles[0].Tags["language"])
				assert.Equal(t, "简体中文 with spaces", subtitles[0].Tags["title"])
				assert.Equal(t, 1, subtitles[0].Disposition.Default)
				assert.Equal(t, "jpn", subtitles[1].Tags["language"])
				assert.Zero(t, subtitles[1].Disposition.Default, "原字幕不能保留第二个默认标记")
			} else {
				require.Len(t, subtitles, 1)
				assert.Equal(t, "jpn", subtitles[0].Tags["language"])
			}
			if tc.sel.AddFonts {
				assert.Equal(t, 1, counts["attachment"])
			} else {
				assert.Zero(t, counts["attachment"])
			}
			before, err := os.ReadFile(plan.Jobs[0].Output)
			require.NoError(t, err)
			assert.ErrorIs(t, Execute(ctx, plan, nil), os.ErrExist, "重跑不得静默覆盖已有文件")
			after, err := os.ReadFile(plan.Jobs[0].Output)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestBatchPlanExecutesSnapshot(t *testing.T) {
	fx := newMediaFixture(t)
	opt := command.DefaultOption()
	opt.InputPath, opt.InputFormat = fx.input, "mkv"
	opt.OutputPath = filepath.Join(t.TempDir(), "快照输出")
	opt.Advance = "-c copy"
	plan, err := command.BuildPlan(context.Background(), &opt, command.Selection{Convert: true})
	require.NoError(t, err)
	data, err := os.ReadFile(fx.video)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fx.input, "新增.mkv"), data, 0o644))
	opt.InputPath, opt.OutputPath, opt.Advance = "不存在的输入", "不能使用的新输出", "-y"
	var total int
	err = Execute(context.Background(), plan, func(e ffmpeg.Event) {
		if e.Type == ffmpeg.EventBatchStart {
			total = e.Total
		}
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.FileExists(t, plan.Jobs[0].Output)
	assert.NoFileExists(t, filepath.Join(filepath.Dir(plan.Jobs[0].Output), "新增.mkv"))
}

func TestAddingFontsPreservesExistingAttachmentMetadata(t *testing.T) {
	fx := newMediaFixture(t)
	root := t.TempDir()
	attachment := filepath.Join(root, "原附件.bin")
	testutil.WriteFile(t, attachment, "原附件内容")
	source := filepath.Join(root, "source.mkv")
	runFixtureFFmpeg(t, []string{
		"-nostdin", "-n", "-v", "error", "-i", fx.video, "-map", "0", "-c", "copy",
		"-attach", attachment, "-metadata:s:t:0", "mimetype=application/octet-stream", source,
	})
	opt := command.DefaultOption()
	opt.InputPath, opt.InputFormat, opt.FontsPath = source, "mkv", fx.fonts
	opt.OutputPath = t.TempDir()
	plan, err := command.BuildPlan(context.Background(), &opt, command.Selection{AddFonts: true})
	require.NoError(t, err)
	var logs string
	err = Execute(context.Background(), plan, func(e ffmpeg.Event) { logs += e.Message + "\n" })
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
