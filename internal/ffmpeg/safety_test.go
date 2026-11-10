package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gsxhnd/batcher/internal/commandline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanningIsReadOnly(t *testing.T) {
	for _, method := range []string{"构造", "单操作计划", "单操作预览", "组合计划", "组合预览", "任务快照"} {
		t.Run(method, func(t *testing.T) {
			fx := newFixture(t)
			opt := fixtureOption(fx)
			opt.OutputPath = filepath.Join(fx.out, "尚未创建")
			opt.Workers = 0
			before := *opt
			var err error
			switch method {
			case "构造":
				_, err = NewVideoBatch(opt)
			case "单操作计划":
				_, _, err = Plan(TaskConvert, opt)
			case "单操作预览":
				_, err = Preview(TaskConvert, opt)
			case "组合计划":
				_, _, err = PlanCombined(opt, Selection{Convert: true})
			case "组合预览":
				_, err = PreviewCombined(opt, Selection{Convert: true})
			case "任务快照":
				_, err = BuildPlan(context.Background(), opt, Selection{Convert: true})
			}
			require.NoError(t, err)
			assert.NoDirExists(t, fx.out)
			assert.Equal(t, before, *opt, "规划不能修改调用方的配置")
		})
	}
}

func TestOutputPathsRemainUnique(t *testing.T) {
	for _, names := range [][]string{{"a", "a", "a", "a"}, {"a-1", "a", "a", "A"}, {"a", "a", "a-1", "a"}} {
		t.Run(strings.Join(names, "/"), func(t *testing.T) {
			root := t.TempDir()
			vb := &videoBatch{option: &VideoBatchOption{OutputPath: root, OutputFormat: "mkv"}}
			var inputs []string
			for i, name := range names {
				inputs = append(inputs, filepath.Join(root, fmt.Sprint(i), name+".mp4"))
			}
			paths := vb.outputPaths(inputs)
			used := make(map[string]bool)
			for _, input := range inputs {
				output := strings.ToLower(paths[input])
				assert.False(t, used[output], "输出不能重名: %s", output)
				used[output] = true
			}
		})
	}
}

func TestScanExcludesOutputsAndAcceptsUppercase(t *testing.T) {
	fx := newFixture(t)
	require.NoError(t, os.MkdirAll(fx.out, 0o755))
	writeFile(t, filepath.Join(fx.out, "旧结果.MP4"), "")
	uppercase := filepath.Join(fx.root, "大写.MP4")
	writeFile(t, uppercase, "")
	vb, err := NewVideoBatch(fixtureOption(fx))
	require.NoError(t, err)
	got, err := vb.GetVideosList()
	require.NoError(t, err)
	assert.ElementsMatch(t, append(slices.Clone(fx.mp4s), uppercase), got)
}

func TestPlanRejectsInputOutputOverlap(t *testing.T) {
	fx := newFixture(t)
	opt := fixtureOption(fx)
	opt.InputPath, opt.OutputPath, opt.OutputFormat = fx.dir1, fx.dir1, "mp4"
	_, err := BuildPlan(context.Background(), opt, Selection{Convert: true})
	require.ErrorContains(t, err, "输入文件重合")
}

func TestPlanMatchesSubtitlesInVideoDirectory(t *testing.T) {
	fx := newFixture(t)
	opt := fixtureOption(fx)
	opt.Subtitles = []SubtitleOption{{Suffix: "ass", Lang: "chi"}}
	// 未勾选字体时，不得读取遗留的字体路径。
	opt.FontsPath = filepath.Join(fx.root, "不存在的字体目录")
	plan, err := BuildPlan(context.Background(), opt, Selection{Convert: true, AddSubtitle: true})
	require.NoError(t, err)
	for _, job := range plan.Jobs {
		subtitle := filepath.Join(filepath.Dir(job.Input), stem(job.Input)+".ass")
		assert.True(t, contains(job.Args, []string{"-sub_charenc", "UTF-8", "-i", subtitle}))
		assert.NotContains(t, job.Args, "-attach")
	}
}

func TestSubtitleResolutionErrors(t *testing.T) {
	cases := []struct {
		name   string
		names  []string
		suffix string
		want   string
		err    string
	}{
		{"精确优先", []string{"a.ass", "a.chi.ass", "a.eng.ass"}, "ass", "a.ass", ""},
		{"唯一候选", []string{"a.chi.ass"}, "ass", "a.chi.ass", ""},
		{"完整后缀", []string{"a.chi.ass", "a.eng.ass"}, "eng.ass", "a.eng.ass", ""},
		{"缺失", nil, "ass", "", "未找到"},
		{"歧义", []string{"a.chi.ass", "a.eng.ass"}, "ass", "", "多个字幕"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSubtitle(tc.names, "a", tc.suffix)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestAdvancedArgumentsAndInputOrder(t *testing.T) {
	fx := newFixture(t)
	opt := fixtureOption(fx)
	opt.Advance = `-c:v libx264 -metadata title="hello  world" -vf "drawtext=text='你好 世界'"`
	plan, err := BuildPlan(context.Background(), opt, Selection{Convert: true, AddSubtitle: true})
	require.NoError(t, err)
	for _, job := range plan.Jobs {
		assert.Greater(t, slices.Index(job.Args, "-c:v"), slices.Index(job.Args, "-sub_charenc"))
		assert.Contains(t, job.Args, "title=hello  world")
		assert.Contains(t, job.Args, "drawtext=text='你好 世界'")
	}
	for _, args := range []string{`-vf "未闭合`, "-y", "-i other.mkv", "-stdin"} {
		t.Run(args, func(t *testing.T) {
			opt.Advance = args
			_, err := BuildPlan(context.Background(), opt, Selection{Convert: true})
			assert.Error(t, err)
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

func TestExecuteContinuesAfterFailure(t *testing.T) {
	for _, workers := range []int{1, 3} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			var started, done, failed, active, peak int
			vb := &videoBatch{option: &VideoBatchOption{Workers: workers, OnEvent: func(e Event) {
				switch e.Type {
				case EventCommandStart:
					started++
					active++
					peak = max(peak, active)
				case EventCommandDone:
					done++
					active--
				case EventCommandFailed:
					failed++
					active--
				}
			}}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := vb.ExecuteBatch(ctx, [][]string{{"-batcher-invalid"}, {"-version"}, {"-version"}, {"-version"}})
			require.Error(t, err)
			assert.Equal(t, 4, started)
			assert.Equal(t, 3, done)
			assert.Equal(t, 1, failed)
			assert.Zero(t, active)
			assert.LessOrEqual(t, peak, workers)
		})
	}
}

func TestExecuteCancellationStopsQueuedCommands(t *testing.T) {
	for _, workers := range []int{1, 3} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, canceled := 0, 0
			vb := &videoBatch{option: &VideoBatchOption{Workers: workers, OnEvent: func(e Event) {
				if e.Type == EventCommandStart {
					started++
					cancel()
				}
				if e.Type == EventCommandCanceled {
					canceled++
				}
			}}}
			err := vb.ExecuteBatch(ctx, [][]string{{"-version"}, {"-version"}, {"-version"}, {"-version"}})
			assert.True(t, errors.Is(err, context.Canceled))
			assert.LessOrEqual(t, started, workers)
			assert.Equal(t, started, canceled)
		})
	}
}

func TestPlanningCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := BuildPlan(ctx, nil, Selection{Convert: true})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRejectsInvalidSuffixAndMultipleDefaults(t *testing.T) {
	fx := newFixture(t)
	for _, suffix := range []string{"../mkv", `..\mkv`, ".."} {
		t.Run(suffix, func(t *testing.T) {
			opt := fixtureOption(fx)
			opt.OutputFormat = suffix
			_, err := BuildPlan(context.Background(), opt, Selection{Convert: true})
			assert.Error(t, err)
		})
	}
	opt := fixtureOption(fx)
	opt.Subtitles = []SubtitleOption{{Suffix: "ass", Default: true}, {Suffix: "srt", Default: true}}
	_, err := BuildPlan(context.Background(), opt, Selection{AddSubtitle: true})
	assert.ErrorContains(t, err, "只能指定一条")
}
