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
		name    string
		input   string
		format  string
		want    []string
		wantErr bool
	}{
		{"test_mkv", fx.Root, "mkv", fx.MKVs, false},
		{"test_mp4", fx.Root, "mp4", fx.MP4s, false},
		{"test_not_exist_path", "../111", "mp4", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Videos(context.Background(), tt.input, "", tt.format)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFonts(t *testing.T) {
	fx := testutil.NewFixture(t)
	got, err := Fonts(context.Background(), fx.Root)
	require.NoError(t, err)
	assert.Equal(t, fx.Fonts, got)

	_, err = Fonts(context.Background(), "../111")
	assert.Error(t, err)
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
	got, err := Videos(context.Background(), fx.Root, fx.Out+"/", "mp4")
	require.NoError(t, err)
	assert.ElementsMatch(t, append(slices.Clone(fx.MP4s), uppercase), got)
}

func TestListFiles(t *testing.T) {
	tmp := t.TempDir()
	testutil.WriteFile(t, filepath.Join(tmp, "1.mp4"), "video")
	testutil.WriteFile(t, filepath.Join(tmp, "1.chi.ass"), "sub1")
	testutil.WriteFile(t, filepath.Join(tmp, "1.eng.ass"), "sub2")
	testutil.WriteFile(t, filepath.Join(tmp, "2.srt"), "sub3")
	require.NoError(t, os.Mkdir(filepath.Join(tmp, "子目录"), 0o755))

	names, err := ListFiles(tmp)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1.mp4", "1.chi.ass", "1.eng.ass", "2.srt"}, names)
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
