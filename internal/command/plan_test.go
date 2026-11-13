package command

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gsxhnd/batcher/internal/media"
	"github.com/gsxhnd/batcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildSelection 构造指定组合的 Selection。
func buildSelection(convert, sub, fonts bool) Selection {
	return Selection{Convert: convert, AddSubtitle: sub, AddFonts: fonts}
}

// fixtureOption 基于 fixture 目录构造一个组合流程的规划选项。
// fixture 的视频放在子目录 1/ 2/ 里，这里默认打开递归扫描。
func fixtureOption(fx testutil.Fixture) *VideoBatchOption {
	return &VideoBatchOption{
		InputPath:      fx.Root,
		InputFormat:    "mp4",
		Recursive:      true,
		InputSubSuffix: "ass",
		FontsPath:      fx.Dir1,
		OutputPath:     fx.Out + "/", // 保持与真实配置一致的末尾斜杠
		OutputFormat:   "mkv",
	}
}

// planCommands 规划并返回各任务的 ffmpeg 参数。
func planCommands(t *testing.T, opt *VideoBatchOption, sel Selection) [][]string {
	t.Helper()
	plan, err := BuildPlan(context.Background(), opt, sel)
	require.NoError(t, err)
	return plan.Commands()
}

// contains 判断 haystack 中是否存在连续的 needle 片段。
func contains(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func TestSelectionString(t *testing.T) {
	assert.Equal(t, "无", Selection{}.String())
	assert.Equal(t, "convert", buildSelection(true, false, false).String())
	assert.Equal(t, "convert+add_sub", buildSelection(true, true, false).String())
	assert.Equal(t, "convert+add_sub+add_fonts", buildSelection(true, true, true).String())
}

// TestPlanScanDepth 默认只扫描输入目录第一层，勾选递归后才进入子目录。
func TestPlanScanDepth(t *testing.T) {
	root := t.TempDir()
	top := filepath.Join(root, "a.mp4")
	nested := filepath.Join(root, "子", "b.mp4")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "子"), 0o755))
	testutil.WriteFile(t, top, "")
	testutil.WriteFile(t, nested, "")

	opt := &VideoBatchOption{
		InputPath:    root,
		InputFormat:  "mp4",
		OutputPath:   t.TempDir(),
		OutputFormat: "mkv",
	}
	plan, err := BuildPlan(context.Background(), opt, buildSelection(true, false, false))
	require.NoError(t, err)
	inputs, _ := jobPaths(plan.Jobs)
	assert.Equal(t, []string{top}, inputs, "默认单层，不进入子目录")

	opt.Recursive = true
	plan, err = BuildPlan(context.Background(), opt, buildSelection(true, false, false))
	require.NoError(t, err)
	inputs, _ = jobPaths(plan.Jobs)
	assert.ElementsMatch(t, []string{top, nested}, inputs, "勾选递归后进入子目录")
}

func TestNewPlanner(t *testing.T) {
	tests := []struct {
		name    string
		opt     *VideoBatchOption
		wantErr bool
	}{
		{"test_success", &VideoBatchOption{OutputPath: t.TempDir()}, false},
		{"test_nil_option", nil, true},
		{"test_empty_output", &VideoBatchOption{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newPlanner(tt.opt)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestFontsParams(t *testing.T) {
	fx := testutil.NewFixture(t)
	ctx := context.Background()

	p, err := newPlanner(&VideoBatchOption{OutputPath: fx.Out, FontsPath: fx.Root})
	require.NoError(t, err)
	got, err := p.fontsParams(ctx)
	require.NoError(t, err)
	assert.Equal(t, testutil.AttachCmd(fx.Fonts), got)

	p, err = newPlanner(&VideoBatchOption{OutputPath: fx.Out, FontsPath: "../111"})
	require.NoError(t, err)
	got, err = p.fontsParams(ctx)
	assert.Error(t, err)
	assert.Nil(t, got)

	p, err = newPlanner(&VideoBatchOption{OutputPath: fx.Out})
	require.NoError(t, err)
	got, err = p.fontsParams(ctx)
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestFontAttachUsesSlashPathAndBasenameMatcher(t *testing.T) {
	fx := testutil.NewFixture(t)
	p, err := newPlanner(&VideoBatchOption{OutputPath: fx.Out, FontsPath: fx.Root})
	require.NoError(t, err)
	got, err := p.fontsParams(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, got)
	for i := 0; i+3 < len(got); i += 4 {
		require.Equal(t, "-attach", got[i])
		attach := got[i+1]
		// ffmpeg 只按 "/" 取附件文件名；反斜杠会让 filename 变成整条路径，
		// 下面的按文件名匹配就会失效，mimetype 写不进去。
		assert.NotContains(t, attach, `\`, "字体路径必须使用正斜杠")
		assert.Equal(t, "-metadata:s:t:m:filename:"+path.Base(attach), got[i+2])
		assert.Equal(t, "mimetype=application/x-truetype-font", got[i+3])
	}
}

func TestBuildPlanConvertCommands(t *testing.T) {
	fx := testutil.NewFixture(t)
	outs := testutil.Outputs(fx, "mkv")
	plan, err := BuildPlan(context.Background(), fixtureOption(fx), buildSelection(true, false, false))
	require.NoError(t, err)
	require.Len(t, plan.Jobs, len(fx.MP4s))
	for i, job := range plan.Jobs {
		assert.Equal(t, fx.MP4s[i], job.Input)
		assert.Equal(t, outs[i], job.Output)
		assert.Equal(t, []string{"-nostdin", "-n", "-i", fx.MP4s[i], "-map", "0", "-c:s", "copy", "-c:t", "copy", "-c:d", "copy", outs[i]}, job.Args)
	}

	missing := fixtureOption(fx)
	missing.InputPath = "../111"
	_, err = BuildPlan(context.Background(), missing, buildSelection(true, false, false))
	assert.ErrorContains(t, err, "输入路径不存在")
}

// TestFontErrorsStayShort 字体相关的失败提示保持一句话，右上角 toast 一行放得下。
func TestFontErrorsStayShort(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.FontsPath = filepath.Join(fx.Root, "不存在的字体目录")
	_, err := BuildPlan(context.Background(), opt, buildSelection(false, false, true))
	assert.EqualError(t, err, "字体目录不存在")

	opt.FontsPath = t.TempDir()
	_, err = BuildPlan(context.Background(), opt, buildSelection(false, false, true))
	assert.EqualError(t, err, "字体目录里没有 ttf/otf/ttc 字体")
}

func TestBuildPlanAddFontsCommands(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.InputFormat, opt.FontsPath = "mkv", fx.Root
	outs := testutil.Outputs(fx, "mkv")
	var want [][]string
	for i, input := range fx.MKVs {
		cmd := []string{"-nostdin", "-n", "-i", input, "-map", "0", "-c", "copy"}
		cmd = append(cmd, testutil.AttachCmd(fx.Fonts)...)
		want = append(want, append(cmd, outs[i]))
	}
	assert.Equal(t, want, planCommands(t, opt, buildSelection(false, false, true)))
}

func TestBuildPlanCombinedCommands(t *testing.T) {
	fx := testutil.NewFixture(t)
	baseOpt := func() *VideoBatchOption {
		opt := fixtureOption(fx)
		opt.InputSubLang = "chi"
		opt.InputSubTitle = "Chinese"
		opt.Advance = "-c:v h265 -crf 20"
		return opt
	}
	subMaps := []string{"-map", "0", "-map", "-0:s?", "-map", "1", "-map", "0:s?"}

	// 字幕 + 字体（未转码）：一条命令同时带字幕参数、字体参数和 -c copy
	t.Run("sub+fonts without convert", func(t *testing.T) {
		cmds := planCommands(t, baseOpt(), buildSelection(false, true, true))
		assert.Len(t, cmds, len(fx.MP4s))
		for _, cmd := range cmds {
			assert.True(t, contains(cmd, subMaps))
			assert.True(t, contains(cmd, []string{"-c", "copy"}))
			assert.True(t, contains(cmd, []string{"-attach"}))
			assert.True(t, strings.HasPrefix(cmd[len(cmd)-1], fx.Out+string(filepath.Separator)))
		}
	})

	// 转码 + 字幕 + 字体：高级参数参与编码，不再追加 -c copy
	t.Run("convert+sub+fonts", func(t *testing.T) {
		cmds := planCommands(t, baseOpt(), buildSelection(true, true, true))
		assert.Len(t, cmds, len(fx.MP4s))
		for _, cmd := range cmds {
			assert.True(t, contains(cmd, []string{"-c:v", "h265", "-crf", "20"}))
			assert.True(t, contains(cmd, subMaps))
			assert.False(t, contains(cmd, []string{"-c", "copy"}))
			assert.True(t, contains(cmd, []string{"-attach"}))
		}
	})

	// 未勾选字体时，字体参数不应出现
	t.Run("convert+sub without fonts", func(t *testing.T) {
		opt := baseOpt()
		opt.FontsPath = ""
		cmds := planCommands(t, opt, buildSelection(true, true, false))
		assert.Len(t, cmds, len(fx.MP4s))
		for _, cmd := range cmds {
			assert.False(t, contains(cmd, []string{"-attach"}))
		}
	})
}

func TestFormattedPreviewLines(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.Advance = "-c:v h265"
	cmds := planCommands(t, opt, buildSelection(true, false, false))
	assert.Len(t, cmds, len(fx.MP4s))
	for _, cmd := range cmds {
		line := FormatCommand(cmd)
		assert.True(t, strings.HasPrefix(line, "ffmpeg -nostdin -n -i "), "bad line: %s", line)
		assert.Contains(t, line, "-c:v")
	}
}

func TestBuildPlanErrors(t *testing.T) {
	fx := testutil.NewFixture(t)
	_, err := BuildPlan(context.Background(), fixtureOption(fx), Selection{})
	assert.ErrorContains(t, err, "至少需要选择一个操作")

	_, err = BuildPlan(context.Background(), nil, buildSelection(true, false, false))
	assert.Error(t, err)
}

func TestPlanningIsReadOnly(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.OutputPath = filepath.Join(fx.Out, "尚未创建")
	opt.InputFormat = ".MP4" // 规范化只发生在副本上
	before := *opt
	_, err := BuildPlan(context.Background(), opt, buildSelection(true, false, false))
	require.NoError(t, err)
	assert.NoDirExists(t, fx.Out)
	assert.Equal(t, before, *opt, "规划不能修改调用方的配置")
}

func TestBatchPlanReturnsCopies(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.Subtitles = []SubtitleOption{{Suffix: "ass", Lang: "chi"}}
	plan, err := BuildPlan(context.Background(), opt, buildSelection(true, true, false))
	require.NoError(t, err)

	commands := plan.Commands()
	commands[0][0] = "调用方修改副本"
	assert.Equal(t, "-nostdin", plan.Jobs[0].Args[0])

	opt.Subtitles[0].Lang = "eng"
	got := plan.Option()
	assert.Equal(t, "chi", got.Subtitles[0].Lang, "快照不随调用方配置变化")
	got.Subtitles[0].Lang = "jpn"
	assert.Equal(t, "chi", plan.Option().Subtitles[0].Lang)
}

func TestBatchPlanCheckRejectsExistingOutput(t *testing.T) {
	fx := testutil.NewFixture(t)
	ctx := context.Background()
	plan, err := BuildPlan(ctx, fixtureOption(fx), buildSelection(true, false, false))
	require.NoError(t, err)
	require.NoError(t, plan.Check(ctx, true))

	require.NoError(t, os.MkdirAll(fx.Out, 0o755))
	testutil.WriteFile(t, plan.Jobs[0].Output, "")
	assert.ErrorIs(t, plan.Check(ctx, true), os.ErrExist)
	assert.NoError(t, plan.Check(ctx, false), "规划阶段不检查已存在的输出")
}

func TestPlanRejectsInputOutputOverlap(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.InputPath, opt.OutputPath, opt.OutputFormat = fx.Dir1, fx.Dir1, "mp4"
	_, err := BuildPlan(context.Background(), opt, buildSelection(true, false, false))
	require.ErrorContains(t, err, "输入文件重合")
}

func TestPlanMatchesSubtitlesInVideoDirectory(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.Subtitles = []SubtitleOption{{Suffix: "ass", Lang: "chi"}}
	// 未勾选字体时，不得读取遗留的字体路径。
	opt.FontsPath = filepath.Join(fx.Root, "不存在的字体目录")
	plan, err := BuildPlan(context.Background(), opt, buildSelection(true, true, false))
	require.NoError(t, err)
	for _, job := range plan.Jobs {
		subtitle := filepath.Join(filepath.Dir(job.Input), media.Stem(job.Input)+".ass")
		assert.True(t, contains(job.Args, []string{"-sub_charenc", "UTF-8", "-i", subtitle}))
		assert.NotContains(t, job.Args, "-attach")
	}
}

func TestAdvancedArgumentsAndInputOrder(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.Advance = `-c:v libx264 -metadata title="hello  world" -vf "drawtext=text='你好 世界'"`
	for _, cmd := range planCommands(t, opt, buildSelection(true, true, false)) {
		assert.Greater(t, slices.Index(cmd, "-c:v"), slices.Index(cmd, "-sub_charenc"))
		assert.Contains(t, cmd, "title=hello  world")
		assert.Contains(t, cmd, "drawtext=text='你好 世界'")
	}
	for _, args := range []string{`-vf "未闭合`, "-y", "-i other.mkv", "-stdin"} {
		t.Run(args, func(t *testing.T) {
			bad := fixtureOption(fx)
			bad.Advance = args
			_, err := BuildPlan(context.Background(), bad, buildSelection(true, false, false))
			assert.Error(t, err)
		})
	}
}

func TestPlanningCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := BuildPlan(ctx, nil, buildSelection(true, false, false))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRejectsFontsOutsideMatroska(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.OutputFormat = "mp4"
	_, err := BuildPlan(context.Background(), opt, buildSelection(false, false, true))
	assert.ErrorContains(t, err, "mkv")
}

func TestRejectsDuplicateFontNames(t *testing.T) {
	fx := testutil.NewFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.Dir1, "a.ttf"), []byte("x"), 0o644))
	nested := filepath.Join(fx.Dir1, "more")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "A.TTF"), []byte("y"), 0o644))
	opt := fixtureOption(fx)
	_, err := BuildPlan(context.Background(), opt, buildSelection(false, false, true))
	assert.ErrorContains(t, err, "字体文件名重复")
}

func TestRejectsInvalidSuffix(t *testing.T) {
	fx := testutil.NewFixture(t)
	for _, suffix := range []string{"../mkv", `..\\mkv`, ".."} {
		t.Run(suffix, func(t *testing.T) {
			opt := fixtureOption(fx)
			opt.OutputFormat = suffix
			_, err := BuildPlan(context.Background(), opt, buildSelection(true, false, false))
			assert.Error(t, err)
		})
	}
}
