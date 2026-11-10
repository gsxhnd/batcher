package ffmpeg

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
	writeFile(t, original, subtitle)
	writeFile(t, filepath.Join(fx.input, "episode.chi.srt"), subtitle)
	// 字体附件只检查封装，不验证字体渲染，使用临时占位内容。
	writeFile(t, filepath.Join(fx.fonts, "测试.ttf"), "字体附件占位内容")
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
	vb := &videoBatch{option: &VideoBatchOption{Workers: 1, OnEvent: func(e Event) {
		if e.Type == EventLog {
			logs += e.Message + "\n"
		}
	}}}
	require.NoError(t, vb.ExecuteBatch(ctx, [][]string{args}), logs)
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
		sel  Selection
	}{
		{"转码", Selection{Convert: true}},
		{"字幕", Selection{AddSubtitle: true}},
		{"字体", Selection{AddFonts: true}},
		{"转码与字幕", Selection{Convert: true, AddSubtitle: true}},
		{"转码与字体", Selection{Convert: true, AddFonts: true}},
		{"字幕与字体", Selection{AddSubtitle: true, AddFonts: true}},
		{"全部操作", Selection{Convert: true, AddSubtitle: true, AddFonts: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := DefaultOption()
			opt.InputPath, opt.InputFormat = fx.input, "mkv"
			opt.OutputPath = filepath.Join(t.TempDir(), "尚未创建")
			opt.FontsPath = fx.fonts
			opt.Advance = `-c:v mpeg4 -q:v 4 -c:a copy -vf "scale=16:16" -metadata title="标题 with  spaces"`
			opt.Subtitles = []SubtitleOption{{Suffix: "chi.srt", Lang: "chi", Title: "简体中文 with spaces", Default: true}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			plan, err := BuildPlan(ctx, &opt, tc.sel)
			require.NoError(t, err)
			require.Len(t, plan.Jobs, 1)
			assert.NoDirExists(t, opt.OutputPath)
			var logs string
			err = plan.Execute(ctx, func(e Event) {
				if e.Type == EventLog {
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
			assert.ErrorIs(t, plan.Execute(ctx, nil), os.ErrExist, "重跑不得静默覆盖已有文件")
			after, err := os.ReadFile(plan.Jobs[0].Output)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestBatchPlanExecutesSnapshot(t *testing.T) {
	fx := newMediaFixture(t)
	opt := DefaultOption()
	opt.InputPath, opt.InputFormat = fx.input, "mkv"
	opt.OutputPath = filepath.Join(t.TempDir(), "快照输出")
	opt.Advance = "-c copy"
	plan, err := BuildPlan(context.Background(), &opt, Selection{Convert: true})
	require.NoError(t, err)
	commands := plan.Commands()
	commands[0][0] = "调用方修改副本"
	assert.Equal(t, "-nostdin", plan.Jobs[0].Args[0])
	data, err := os.ReadFile(fx.video)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fx.input, "新增.mkv"), data, 0o644))
	opt.InputPath, opt.OutputPath, opt.Advance = "不存在的输入", "不能使用的新输出", "-y"
	var total int
	err = plan.Execute(context.Background(), func(e Event) {
		if e.Type == EventBatchStart {
			total = e.Total
		}
	})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.FileExists(t, plan.Jobs[0].Output)
	assert.NoFileExists(t, filepath.Join(filepath.Dir(plan.Jobs[0].Output), "新增.mkv"))
}

func TestServiceRunPaths(t *testing.T) {
	fx := newMediaFixture(t)
	for _, task := range []Task{TaskConvert, TaskAddSubtitle, TaskAddFonts, "组合"} {
		t.Run(string(task), func(t *testing.T) {
			opt := DefaultOption()
			opt.InputPath, opt.InputFormat, opt.FontsPath = fx.input, "mkv", fx.fonts
			opt.OutputPath = filepath.Join(t.TempDir(), "输出")
			opt.Advance = "-c copy"
			opt.InputSubSuffix = "chi.srt"
			var logs string
			opt.OnEvent = func(e Event) { logs += e.Message + "\n" }
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var err error
			if task == "组合" {
				err = RunCombined(ctx, &opt, Selection{Convert: true, AddSubtitle: true, AddFonts: true})
			} else {
				err = Run(ctx, task, &opt)
			}
			require.NoError(t, err, logs)
			assert.FileExists(t, filepath.Join(opt.OutputPath, "episode.mkv"))
			if task != "组合" {
				assert.ErrorIs(t, Run(ctx, task, &opt), os.ErrExist)
			}
		})
	}
	assert.Error(t, Run(context.Background(), "未知任务", nil))
	assert.Error(t, RunCombined(context.Background(), nil, Selection{Convert: true}))
}
