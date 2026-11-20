package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gsxhnd/batcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideos(t *testing.T) {
	fx := testutil.NewFixture(t)
	tests := []struct {
		name      string
		input     string
		format    string
		recursive bool
		want      []string
		wantErr   bool
	}{
		{"test_mkv", fx.Root, "mkv", true, fx.MKVs, false},
		{"test_mp4", fx.Root, "mp4", true, fx.MP4s, false},
		{"test_not_exist_path", "../111", "mp4", true, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Videos(context.Background(), tt.input, "", tt.format, tt.recursive)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestVideosSingleLevelSkipsSubdirectories 单层模式只收第一层的视频。
func TestVideosSingleLevelSkipsSubdirectories(t *testing.T) {
	fx := testutil.NewFixture(t)
	root := filepath.Join(fx.Root, "顶层")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "子"), 0o755))
	top := filepath.Join(root, "a.mp4")
	nested := filepath.Join(root, "子", "b.mp4")
	testutil.WriteFile(t, top, "")
	testutil.WriteFile(t, nested, "")

	got, err := Videos(context.Background(), root, "", "mp4", false)
	require.NoError(t, err)
	assert.Equal(t, []string{top}, got)

	got, err = Videos(context.Background(), root, "", "mp4", true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{top, nested}, got)
}

func TestFonts(t *testing.T) {
	fx := testutil.NewFixture(t)
	got, err := Fonts(context.Background(), fx.Root)
	require.NoError(t, err)
	assert.Equal(t, fx.Fonts, got)

	_, err = Fonts(context.Background(), "../111")
	assert.Error(t, err)
}

// TestFontsAcceptUppercaseExtensions 字体后缀不区分大小写。
func TestFontsAcceptUppercaseExtensions(t *testing.T) {
	tmp := t.TempDir()
	want := []string{
		filepath.Join(tmp, "黑体.TTF"),
		filepath.Join(tmp, "宋体.OTF"),
		filepath.Join(tmp, "楷体.TTC"),
	}
	for _, p := range want {
		testutil.WriteFile(t, p, "")
	}
	// 非字体后缀不应出现，大写的不认识后缀也不行。
	testutil.WriteFile(t, filepath.Join(tmp, "说明.TXT"), "")
	testutil.WriteFile(t, filepath.Join(tmp, "字体.WOFF2"), "")

	got, err := Fonts(context.Background(), tmp)
	require.NoError(t, err)
	assert.ElementsMatch(t, want, got)
}

func TestOutputPaths(t *testing.T) {
	fx := testutil.NewFixture(t)
	got := OutputPaths(fx.MP4s, "../output", "mkv")
	assert.Equal(t, map[string]string{
		fx.MP4s[0]: filepath.Join("../output", "1.mkv"),
		fx.MP4s[1]: filepath.Join("../output", "2.mkv"),
		fx.MP4s[2]: filepath.Join("../output", "1-1.mkv"),
		fx.MP4s[3]: filepath.Join("../output", "2-1.mkv"),
	}, got)
}

func TestOutputPathsRemainUnique(t *testing.T) {
	for _, names := range [][]string{{"a", "a", "a", "a"}, {"a-1", "a", "a", "A"}, {"a", "a", "a-1", "a"}} {
		t.Run(strings.Join(names, "/"), func(t *testing.T) {
			root := t.TempDir()
			var inputs []string
			for i, name := range names {
				inputs = append(inputs, filepath.Join(root, fmt.Sprint(i), name+".mp4"))
			}
			paths := OutputPaths(inputs, root, "mkv")
			used := make(map[string]bool)
			for _, input := range inputs {
				output := strings.ToLower(paths[input])
				assert.False(t, used[output], "输出不能重名: %s", output)
				used[output] = true
			}
		})
	}
}

func TestVideosExcludeOutputsAndAcceptUppercase(t *testing.T) {
	fx := testutil.NewFixture(t)
	require.NoError(t, os.MkdirAll(fx.Out, 0o755))
	testutil.WriteFile(t, filepath.Join(fx.Out, "旧结果.MP4"), "")
	uppercase := filepath.Join(fx.Root, "大写.MP4")
	testutil.WriteFile(t, uppercase, "")
	got, err := Videos(context.Background(), fx.Root, fx.Out+"/", "mp4", true)
	require.NoError(t, err)
	assert.ElementsMatch(t, append(slices.Clone(fx.MP4s), uppercase), got)
}

func TestVideosExcludeCanonicalOutputDirectory(t *testing.T) {
	root := t.TempDir()
	realOutput := filepath.Join(root, "实际输出")
	require.NoError(t, os.Mkdir(realOutput, 0o755))
	input := filepath.Join(root, "输入.mp4")
	oldOutput := filepath.Join(realOutput, "旧结果.mp4")
	testutil.WriteFile(t, input, "")
	testutil.WriteFile(t, oldOutput, "")

	links := t.TempDir()
	rootLink := filepath.Join(links, "输入链接")
	outputLink := filepath.Join(links, "输出链接")
	require.NoError(t, os.Symlink(root, rootLink))
	require.NoError(t, os.Symlink(realOutput, outputLink))

	got, err := Videos(context.Background(), rootLink, outputLink, "mp4", true)
	require.NoError(t, err)
	wantInput, err := filepath.EvalSymlinks(input)
	require.NoError(t, err)
	assert.Equal(t, []string{wantInput}, got, "根目录和输出目录的符号链接都必须按真实路径比较")
}

func TestCanonicalPathResolvesMissingOutputFromExistingParent(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "真实目录")
	require.NoError(t, os.Mkdir(realParent, 0o755))
	link := filepath.Join(root, "目录链接")
	require.NoError(t, os.Symlink(realParent, link))
	missing := filepath.Join(link, "尚未创建", "输出")

	got, err := canonicalPath(missing)
	require.NoError(t, err)
	resolvedParent, err := filepath.EvalSymlinks(realParent)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolvedParent, "尚未创建", "输出"), got)
	assert.NoDirExists(t, filepath.Join(realParent, "尚未创建"), "规范化路径不能创建目录")
}

func TestListFiles(t *testing.T) {
	tmp := t.TempDir()
	testutil.WriteFile(t, filepath.Join(tmp, "1.mp4"), "video")
	testutil.WriteFile(t, filepath.Join(tmp, "1.chi.ass"), "sub1")
	testutil.WriteFile(t, filepath.Join(tmp, "1.eng.ass"), "sub2")
	testutil.WriteFile(t, filepath.Join(tmp, "2.srt"), "sub3")
	testutil.WriteFile(t, filepath.Join(tmp, "._1.mp4"), "junk")
	require.NoError(t, os.Mkdir(filepath.Join(tmp, "子目录"), 0o755))

	names, err := ListFiles(tmp)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1.mp4", "1.chi.ass", "1.eng.ass", "2.srt"}, names)
}

func TestWalkSkipsAppleDoubleAndFollowsFileSymlink(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "a.mp4")
	testutil.WriteFile(t, video, "v")
	testutil.WriteFile(t, filepath.Join(root, "._a.mp4"), "junk")
	link := filepath.Join(root, "b.mp4")
	require.NoError(t, os.Symlink(video, link))

	// 文件都在根目录，单层与递归结果一致；隐藏文件与符号链接的处理对两种模式都生效。
	for _, recursive := range []bool{false, true} {
		got, err := Videos(context.Background(), root, "", "mp4", recursive)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{video, link}, got)
	}
}

func TestCheckPaths(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	input := filepath.Join(root, "a.mp4")
	existing := filepath.Join(root, "b.mkv")
	testutil.WriteFile(t, existing, "")
	fresh := filepath.Join(root, "c.mkv")

	assert.NoError(t, CheckPaths(ctx, []string{input}, []string{fresh}, true))
	assert.ErrorContains(t, CheckPaths(ctx, []string{input}, []string{input}, false), "输入文件重合")
	assert.ErrorContains(t, CheckPaths(ctx, []string{input}, []string{fresh, fresh}, false), "同一输出文件")
	assert.NoError(t, CheckPaths(ctx, []string{input}, []string{existing}, false), "规划阶段不检查已存在的输出")
	assert.ErrorIs(t, CheckPaths(ctx, []string{input}, []string{existing}, true), os.ErrExist)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	assert.ErrorIs(t, CheckPaths(canceled, []string{input}, []string{fresh}, false), context.Canceled)
}

func TestCheckPathsRejectsHardLinkAliases(t *testing.T) {
	ctx := context.Background()
	t.Run("输入与输出", func(t *testing.T) {
		root := t.TempDir()
		input := filepath.Join(root, "输入.mp4")
		output := filepath.Join(root, "输出.mkv")
		testutil.WriteFile(t, input, "video")
		require.NoError(t, os.Link(input, output))
		assert.ErrorContains(t, CheckPaths(ctx, []string{input}, []string{output}, false), "输入文件重合")
	})

	t.Run("多个输出", func(t *testing.T) {
		root := t.TempDir()
		input := filepath.Join(root, "输入.mp4")
		first := filepath.Join(root, "输出一.mkv")
		second := filepath.Join(root, "输出二.mkv")
		testutil.WriteFile(t, input, "video")
		testutil.WriteFile(t, first, "old")
		require.NoError(t, os.Link(first, second))
		assert.ErrorContains(t, CheckPaths(ctx, []string{input}, []string{first, second}, false), "同一输出文件")
	})
}

func TestCheckPathsRejectsCaseAliasWhenSameFile(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "Movie.mp4")
	alias := filepath.Join(root, "movie.MP4")
	testutil.WriteFile(t, input, "video")
	inputInfo, err := os.Stat(input)
	require.NoError(t, err)
	aliasInfo, err := os.Stat(alias)
	if err != nil || !os.SameFile(inputInfo, aliasInfo) {
		t.Skip("当前文件系统区分大小写，两个名称不是同一文件")
	}
	assert.ErrorContains(t, CheckPaths(context.Background(), []string{input}, []string{alias}, false), "输入文件重合")

	output := filepath.Join(root, "Result.mkv")
	outputAlias := filepath.Join(root, "result.MKV")
	testutil.WriteFile(t, output, "old")
	outputInfo, err := os.Stat(output)
	require.NoError(t, err)
	outputAliasInfo, err := os.Stat(outputAlias)
	require.NoError(t, err)
	require.True(t, os.SameFile(outputInfo, outputAliasInfo))
	assert.ErrorContains(t, CheckPaths(context.Background(), []string{input}, []string{output, outputAlias}, false), "同一输出文件")
}
