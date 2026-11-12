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
