package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// buildSelection 构造指定组合的 Selection。
func buildSelection(convert, sub, fonts bool) Selection {
	return Selection{Convert: convert, AddSubtitle: sub, AddFonts: fonts}
}

// cleanupOut 测试结束移除 NewVideoBatch 创建的输出目录。
func cleanupOut(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = os.RemoveAll("../../testdata/out")
	})
}

func TestSelection_String(t *testing.T) {
	assert.Equal(t, "无", Selection{}.String())
	assert.Equal(t, "convert", buildSelection(true, false, false).String())
	assert.Equal(t, "convert+add_sub", buildSelection(true, true, false).String())
	assert.Equal(t, "convert+add_sub+add_fonts", buildSelection(true, true, true).String())
}

// Test_videoBatch_GetCombinedBatch_Single 验证单一操作委托给既有批处理，命令保持不变。
func Test_videoBatch_GetCombinedBatch_Single(t *testing.T) {
	cases := []struct {
		name string
		sel  Selection
	}{
		{"convert only", buildSelection(true, false, false)},
		{"add_sub only", buildSelection(false, true, false)},
		{"add_fonts only", buildSelection(false, false, true)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cleanupOut(t)
			opt := &VideoBatchOption{
				InputPath:    "../../testdata",
				InputFormat:  "mp4",
				OutputPath:   "../../testdata/out/",
				OutputFormat: "mkv",
			}
			vb, err := NewVideoBatch(opt)
			assert.NoError(t, err)
			expected, err := planSingle(t, opt, c.sel)
			assert.NoError(t, err)

			b := vb.(*videoBatch)
			got, err := b.GetCombinedBatch(c.sel)
			assert.NoError(t, err)
			assert.Equal(t, expected, got)
		})
	}
}

// planSingle 用既有 Plan 流程生成基准命令。
func planSingle(t *testing.T, opt *VideoBatchOption, sel Selection) ([][]string, error) {
	t.Helper()
	var task Task
	switch {
	case sel.Convert:
		task = TaskConvert
	case sel.AddSubtitle:
		task = TaskAddSubtitle
	case sel.AddFonts:
		task = TaskAddFonts
	}
	_, cmds, err := Plan(task, opt)
	return cmds, err
}

func Test_videoBatch_GetCombinedBatch(t *testing.T) {
	cleanupOut(t)
	baseOpt := func() *VideoBatchOption {
		return &VideoBatchOption{
			InputPath:      "../../testdata",
			InputFormat:    "mp4",
			OutputPath:     "../../testdata/out/",
			OutputFormat:   "mkv",
			FontsPath:      "../../testdata/1",
			InputSubSuffix: "ass",
			InputSubNo:     0,
			InputSubLang:   "chi",
			InputSubTitle:  "Chinese",
			Advance:        "-c:v h265 -crf 20",
		}
	}

	// add_sub + add_fonts（未转码）：一条命令同时带字幕参数、字体参数和 -c copy
	t.Run("sub+fonts without convert", func(t *testing.T) {
		opt := baseOpt()
		viewer, err := NewVideoBatch(opt)
		assert.NoError(t, err)
		b := viewer.(*videoBatch)

		cmds, err := b.GetCombinedBatch(buildSelection(false, true, true))
		assert.NoError(t, err)
		assert.Len(t, cmds, len(correctMp4Videos))

		for _, cmd := range cmds {
			assert.True(t, contains(cmd, []string{"-map", "0", "-map", "1"}))
			assert.True(t, contains(cmd, []string{"-c", "copy"}))
			assert.True(t, contains(cmd, []string{"-attach"}))
			assert.True(t, strings.HasPrefix(cmd[len(cmd)-1], "../../testdata/out/"))
		}
	})

	// convert + sub + fonts：开启转码时高级参数参与编码，不再追加 -c copy
	t.Run("convert+sub+fonts with convert", func(t *testing.T) {
		opt := baseOpt()
		viewer, err := NewVideoBatch(opt)
		assert.NoError(t, err)
		b := viewer.(*videoBatch)

		cmds, err := b.GetCombinedBatch(buildSelection(true, true, true))
		assert.NoError(t, err)
		assert.Len(t, cmds, len(correctMp4Videos))

		for _, cmd := range cmds {
			assert.True(t, contains(cmd, []string{"-c:v", "h265", "-crf", "20"}))
			assert.True(t, contains(cmd, []string{"-map", "0", "-map", "1"}))
			// 未开启转码分支：不允许出现 -c copy
			assert.False(t, contains(cmd, []string{"-c", "copy"}))
			assert.True(t, contains(cmd, []string{"-attach"}))
		}
	})

	// 没有字体文件夹时，字体参数不应出现
	t.Run("sub only via combined path with empty fonts", func(t *testing.T) {
		opt := baseOpt()
		opt.FontsPath = ""
		viewer, err := NewVideoBatch(opt)
		assert.NoError(t, err)
		b := viewer.(*videoBatch)

		cmds, err := b.GetCombinedBatch(buildSelection(true, true, false))
		assert.NoError(t, err)
		assert.Len(t, cmds, len(correctMp4Videos))
		for _, cmd := range cmds {
			assert.False(t, contains(cmd, []string{"-attach"}))
		}
	})
}

func TestPlanCombined_Errors(t *testing.T) {
	cleanupOut(t)
	opt := &VideoBatchOption{
		InputPath:    "../../testdata",
		InputFormat:  "mp4",
		OutputPath:   "../../testdata/out/",
		OutputFormat: "mkv",
	}
	_, _, err := PlanCombined(opt, Selection{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "至少需要选择一个操作")

	_, _, err = PlanCombined(nil, buildSelection(true, false, false))
	assert.Error(t, err)
}

func TestPreviewCombined(t *testing.T) {
	cleanupOut(t)
	opt := &VideoBatchOption{
		InputPath:    "../../testdata",
		InputFormat:  "mp4",
		OutputPath:   "../../testdata/out/",
		OutputFormat: "mkv",
		Advance:      "-c:v h265",
	}
	lines, err := PreviewCombined(opt, buildSelection(true, false, false))
	assert.NoError(t, err)
	assert.Len(t, lines, len(correctMp4Videos))
	for _, l := range lines {
		assert.True(t, strings.HasPrefix(l, "ffmpeg -i "), fmt.Sprintf("bad line: %s", l))
		assert.Contains(t, l, "-c:v")
	}
}

func Test_videoBatch_GetCombinedBatch_MultiSubtitle(t *testing.T) {
	cleanupOut(t)
	// 布置一个临时目录：1.mp4 + 两条字幕（带语言标记命名）
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "1.mp4"), "video")
	writeFile(t, filepath.Join(tmp, "1.chi.ass"), "sub1")
	writeFile(t, filepath.Join(tmp, "1.eng.ass"), "sub2")

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
	viewer, err := NewVideoBatch(opt)
	assert.NoError(t, err)
	b := viewer.(*videoBatch)

	cmds, err := b.GetCombinedBatch(Selection{AddSubtitle: true})
	assert.NoError(t, err)
	assert.Len(t, cmds, 1)

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

func Test_videoBatch_matchSubtitle(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "1.mp4"), "video")
	writeFile(t, filepath.Join(tmp, "1.chi.ass"), "sub1")
	writeFile(t, filepath.Join(tmp, "1.eng.ass"), "sub2")
	writeFile(t, filepath.Join(tmp, "2.srt"), "sub3")

	opt := &VideoBatchOption{InputPath: tmp, OutputPath: filepath.Join(tmp, "out/")}
	b, err := NewVideoBatch(opt)
	assert.NoError(t, err)
	vb := b.(*videoBatch)

	names, err := vb.listInputFiles()
	assert.NoError(t, err)

	// 精确匹配优先
	assert.Equal(t, filepath.Join(tmp, "2.srt"), vb.matchSubtitle(names, "2", "srt"))
	// 带标记的字幕取排序后的第一个（1.chi.ass < 1.eng.ass）
	assert.Equal(t, filepath.Join(tmp, "1.chi.ass"), vb.matchSubtitle(names, "1", "ass"))
	// 未命中回退旧版约定路径
	assert.Equal(t, filepath.Join(tmp, "3.ass"), vb.matchSubtitle(names, "3", "ass"))
}

func TestAdvancePresets(t *testing.T) {
	home := t.TempDir()

	presets, err := loadAdvancePresets(home)
	assert.NoError(t, err)
	assert.Empty(t, presets)

	// 保存并回读
	want := []string{"-c:v h265 -crf 20 -c:a copy", "-c:v libx264 -preset fast"}
	assert.NoError(t, saveAdvancePresets(home, want))
	got, err := loadAdvancePresets(home)
	assert.NoError(t, err)
	assert.Equal(t, want, got)

	// 追加去重 + 空白规范化
	upserted := UpsertPreset(got, "  -c:v   h265  -crf 20  -c:a copy ")
	assert.Equal(t, got, upserted)
	upserted = UpsertPreset(got, "-c:v nvenc -cq 23")
	assert.Len(t, upserted, 3)
	assert.NoError(t, saveAdvancePresets(home, upserted))
	assert.NoError(t, saveAdvancePresets(home, append(upserted, "", "  ")))

	final, err := loadAdvancePresets(home)
	assert.NoError(t, err)
	assert.Equal(t, upserted, final)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func contains(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
