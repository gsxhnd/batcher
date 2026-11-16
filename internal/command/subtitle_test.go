package command

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gsxhnd/batcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subtitleFixture 布置一个临时目录：1.mp4 + 两条带语言标记命名的字幕。
func subtitleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "1.mp4"), "video")
	testutil.WriteFile(t, filepath.Join(dir, "1.chi.ass"), "sub1")
	testutil.WriteFile(t, filepath.Join(dir, "1.eng.ass"), "sub2")
	return dir
}

func TestMultiSubtitleCommand(t *testing.T) {
	tmp := subtitleFixture(t)
	opt := &VideoBatchOption{
		InputPath:    tmp,
		InputFormat:  "mp4",
		OutputPath:   tmp, // 输出仅生成命令，不执行
		OutputFormat: "mkv",
		InputSubNo:   1, // 序号从 1 开始，验证偏移
		// 语言标记命名的字幕用带标记的后缀（如 chi.ass）精确匹配
		Subtitles: []SubtitleOption{
			{Suffix: "chi.ass", Lang: "chi", Title: "简体中文"},
			{Suffix: "eng.ass", Lang: "eng", Title: "English"},
			{Suffix: "", Lang: "jpn", Title: "空后缀应被跳过"},
		},
	}
	cmds := planCommands(t, opt, Selection{AddSubtitle: true})
	require.Len(t, cmds, 1)
	cmd := cmds[0]

	// 两条字幕各追加一个输入，sub_charenc 紧跟其 -i
	assert.True(t, contains(cmd, []string{"-sub_charenc", "UTF-8", "-i", filepath.Join(tmp, "1.chi.ass")}))
	assert.True(t, contains(cmd, []string{"-sub_charenc", "UTF-8", "-i", filepath.Join(tmp, "1.eng.ass")}))
	// 保留主视频流 + 两条字幕流
	assert.True(t, contains(cmd, []string{"-map", "0", "-map", "1", "-map", "2"}))
	// 流序号从 InputSubNo=1 开始递增，跳过空后缀条目
	assert.True(t, contains(cmd, []string{"-metadata:s:s:1", "language=chi"}))
	assert.True(t, contains(cmd, []string{"-metadata:s:s:1", "title=简体中文"}))
	assert.True(t, contains(cmd, []string{"-metadata:s:s:2", "language=eng"}))
	assert.True(t, contains(cmd, []string{"-metadata:s:s:2", "title=English"}))
	// 未转码 → -c copy
	assert.True(t, contains(cmd, []string{"-c", "copy"}))
	// 空后缀字幕不生成命令片段
	assert.False(t, contains(cmd, []string{"language=jpn"}))
}

// 勾选默认轨道的置 default，其余字幕流显式置 0；全部未勾选时不生成 disposition 参数。
func TestSubtitleDisposition(t *testing.T) {
	tmp := subtitleFixture(t)
	buildOpt := func(subs []SubtitleOption) *VideoBatchOption {
		return &VideoBatchOption{
			InputPath:    tmp,
			InputFormat:  "mp4",
			OutputPath:   t.TempDir(), // 仅为生成命令，不真正写出
			OutputFormat: "mkv",
			InputSubNo:   1,
			Subtitles:    subs,
		}
	}

	t.Run("标记默认轨道", func(t *testing.T) {
		cmds := planCommands(t, buildOpt([]SubtitleOption{
			{Suffix: "chi.ass", Lang: "chi", Title: "简体中文"},
			{Suffix: "eng.ass", Lang: "eng", Title: "English", Default: true},
		}), Selection{AddSubtitle: true})
		require.Len(t, cmds, 1)
		assert.True(t, contains(cmds[0], []string{"-disposition:s:1", "0"}), "未勾选的流应显式置 0")
		assert.True(t, contains(cmds[0], []string{"-disposition:s:2", "default"}), "勾选的流应为默认轨道")
	})

	t.Run("未标记默认轨道", func(t *testing.T) {
		cmds := planCommands(t, buildOpt([]SubtitleOption{
			{Suffix: "chi.ass", Lang: "chi", Title: "简体中文"},
			{Suffix: "eng.ass", Lang: "eng", Title: "English"},
		}), Selection{AddSubtitle: true})
		require.Len(t, cmds, 1)
		assert.False(t, contains(cmds[0], []string{"-disposition"}), "未勾选默认时不应输出 disposition")
	})
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
		{"大小写", []string{"A.ASS"}, "ass", "A.ASS", ""},
		{"前导点", []string{"a.ass"}, ".ass", "a.ass", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSubtitle(tc.names, "a", tc.suffix, nil)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

// 视频主文件名自带标记（Movie.chi）、字幕后缀又以同一标记开头（chi.ass）时，
// 前缀 "Movie.chi." 与后缀 ".chi.ass" 在 Movie.chi.ass 里重叠，不能因切片越界 panic。
func TestResolveSubtitleOverlappingPrefixAndSuffix(t *testing.T) {
	require.NotPanics(t, func() {
		got, err := resolveSubtitle([]string{"Movie.chi.ass"}, "Movie.chi", "chi.ass", []string{"Movie.chi"})
		require.ErrorContains(t, err, "未找到")
		assert.Empty(t, got)
	})

	require.NotPanics(t, func() {
		got, err := resolveSubtitle([]string{"Movie.chi.chi.ass"}, "Movie.chi", "chi.ass", []string{"Movie.chi"})
		require.NoError(t, err)
		assert.Equal(t, "Movie.chi.chi.ass", got, "前后缀不重叠时仍按标记段匹配")
	})
}

func TestResolveSubtitleSkipsLongerVideoName(t *testing.T) {
	got, err := resolveSubtitle([]string{"Movie.Extended.ass"}, "Movie", "ass", []string{"Movie", "Movie.Extended"})
	require.ErrorContains(t, err, "未找到")
	assert.Empty(t, got)

	got, err = resolveSubtitle([]string{"Movie.Extended.ass"}, "Movie.Extended", "ass", []string{"Movie", "Movie.Extended"})
	require.NoError(t, err)
	assert.Equal(t, "Movie.Extended.ass", got)
}

func TestRejectsMultipleDefaultSubtitles(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.Subtitles = []SubtitleOption{{Suffix: "ass", Default: true}, {Suffix: "srt", Default: true}}
	_, err := BuildPlan(context.Background(), opt, Selection{AddSubtitle: true})
	assert.ErrorContains(t, err, "只能指定一条")
}

// TestBuildPlanSkipsMissingSubtitles 没找到字幕不阻断创建：命令里跳过该字幕，
// 只映射配上的部分，流序号按实际带上的字幕重新分配，原因记入 SubtitleNotes。
func TestBuildPlanSkipsMissingSubtitles(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "1.mp4"), "")
	testutil.WriteFile(t, filepath.Join(dir, "1.chi.ass"), "")
	testutil.WriteFile(t, filepath.Join(dir, "1.eng.srt"), "")
	testutil.WriteFile(t, filepath.Join(dir, "2.mp4"), "")
	testutil.WriteFile(t, filepath.Join(dir, "2.chi.ass"), "") // 2.eng.srt 缺失
	out := t.TempDir()
	opt := &VideoBatchOption{
		InputPath:    dir,
		InputFormat:  "mp4",
		OutputPath:   out,
		OutputFormat: "mkv",
		Subtitles: []SubtitleOption{
			{Suffix: "chi.ass", Lang: "chi", Title: "简体中文"},
			{Suffix: "eng.srt", Lang: "eng", Title: "English"},
		},
	}
	plan, err := BuildPlan(context.Background(), opt, Selection{AddSubtitle: true})
	require.NoError(t, err)
	require.Len(t, plan.Jobs, 2)

	// 1.mp4：两条字幕都配上，映射与流序号不变。
	cmd1 := plan.Jobs[0].Args
	assert.Empty(t, plan.Jobs[0].SubtitleNotes)
	assert.True(t, contains(cmd1, []string{"-sub_charenc", "UTF-8", "-i", filepath.Join(dir, "1.chi.ass")}))
	assert.True(t, contains(cmd1, []string{"-sub_charenc", "UTF-8", "-i", filepath.Join(dir, "1.eng.srt")}))
	assert.True(t, contains(cmd1, []string{"-map", "1", "-map", "2"}))
	assert.True(t, contains(cmd1, []string{"-metadata:s:s:0", "language=chi"}))
	assert.True(t, contains(cmd1, []string{"-metadata:s:s:1", "language=eng"}))

	// 2.mp4：只有 chi 配上；eng 记录提示，映射只带一条，序号回到 0。
	cmd2 := plan.Jobs[1].Args
	require.Len(t, plan.Jobs[1].SubtitleNotes, 1)
	assert.Contains(t, plan.Jobs[1].SubtitleNotes[0], "未找到 2.eng.srt")
	assert.True(t, contains(cmd2, []string{"-sub_charenc", "UTF-8", "-i", filepath.Join(dir, "2.chi.ass")}))
	assert.False(t, contains(cmd2, []string{filepath.Join(dir, "2.eng.srt")}))
	assert.True(t, contains(cmd2, []string{"-map", "1"}))
	assert.False(t, contains(cmd2, []string{"-map", "2"}))
	assert.True(t, contains(cmd2, []string{"-metadata:s:s:0", "language=chi"}))
	assert.False(t, contains(cmd2, []string{"language=eng"}))
}

// TestBuildPlanWithoutAnySubtitleStillPlans 全部字幕都没找到时照样创建，
// 命令退化为纯封装：-map 0 保留原有流，没有字幕输入，提示逐条记录。
func TestBuildPlanWithoutAnySubtitleStillPlans(t *testing.T) {
	fx := testutil.NewFixture(t)
	opt := fixtureOption(fx)
	opt.Subtitles = []SubtitleOption{{Suffix: "srt", Lang: "eng"}}
	plan, err := BuildPlan(context.Background(), opt, buildSelection(false, true, false))
	require.NoError(t, err)
	require.Len(t, plan.Jobs, len(fx.MP4s))
	outs := testutil.Outputs(fx, "mkv")
	for i, job := range plan.Jobs {
		require.Len(t, job.SubtitleNotes, 1)
		assert.Contains(t, job.SubtitleNotes[0], "未找到")
		assert.Equal(t,
			[]string{"-nostdin", "-n", "-i", fx.MP4s[i], "-map", "0", "-c", "copy", outs[i]},
			job.Args, "没配上字幕的命令退化为纯封装")
	}
}

// TestBuildPlanAmbiguousSubtitleRecorded 后缀匹配到多个候选同样不阻断创建：
// 跳过该字幕并记录原因，由入队确认提示用户改用完整后缀。
func TestBuildPlanAmbiguousSubtitleRecorded(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "1.mp4"), "")
	testutil.WriteFile(t, filepath.Join(dir, "1.chi.ass"), "")
	testutil.WriteFile(t, filepath.Join(dir, "1.eng.ass"), "")
	opt := &VideoBatchOption{
		InputPath:    dir,
		InputFormat:  "mp4",
		OutputPath:   t.TempDir(),
		OutputFormat: "mkv",
		Subtitles:    []SubtitleOption{{Suffix: "ass", Lang: "chi"}},
	}
	plan, err := BuildPlan(context.Background(), opt, Selection{AddSubtitle: true})
	require.NoError(t, err)
	require.Len(t, plan.Jobs, 1)
	require.Len(t, plan.Jobs[0].SubtitleNotes, 1)
	assert.Contains(t, plan.Jobs[0].SubtitleNotes[0], "多个字幕")
	assert.False(t, contains(plan.Jobs[0].Args, []string{"-sub_charenc", "UTF-8"}))
	assert.True(t, contains(plan.Jobs[0].Args, []string{"-map", "0"}))
}
