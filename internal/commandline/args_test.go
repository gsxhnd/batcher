package commandline

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"空参数", " \t\n", nil},
		{"连续空白", " -c:v   libx264\t-crf  20 ", []string{"-c:v", "libx264", "-crf", "20"}},
		{"引号与内部空白", `-metadata title="hello  world" -vf "drawtext=text='你好 世界'"`, []string{"-metadata", "title=hello  world", "-vf", "drawtext=text='你好 世界'"}},
		{"空字符串", `-metadata title= '' ""`, []string{"-metadata", "title=", "", ""}},
		{"转义", `one\ two 'it'\''s' "a\"b"`, []string{"one two", "it's", `a"b`}},
		{"路径与滤镜", `-passlogfile 'C:\中文 文件\日志' -vf drawtext=text=a\,b`, []string{"-passlogfile", `C:\中文 文件\日志`, "-vf", `drawtext=text=a\,b`}},
		{"无 shell 展开", `'$HOME' '$(whoami)' '*.mkv'`, []string{"$HOME", "$(whoami)", "*.mkv"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.text)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			roundtrip, err := Parse(Join(got))
			require.NoError(t, err)
			assert.Equal(t, got, roundtrip)
		})
	}
	for _, text := range []string{`'未闭合`, `"未闭合`} {
		_, err := Parse(text)
		assert.Error(t, err)
	}
}

func TestNormalizeAndPowerShell(t *testing.T) {
	assert.Equal(t, `-c:v libx264 -metadata 'title=hello  world'`, Normalize(`  -c:v   libx264 -metadata title="hello  world"  `))
	assert.Equal(t, `-vf "未闭合`, Normalize(`  -vf "未闭合  `))
	assert.Equal(t, `-i 'C:\视频 文件\a''b.mkv'`, JoinPowerShell([]string{"-i", `C:\视频 文件\a'b.mkv`}))
	assert.Equal(t, `'@args' 'a,b'`, JoinPowerShell([]string{"@args", "a,b"}))
	assert.Equal(t, `'-metadata:s:t:m:filename:1.ttf'`, JoinPowerShell([]string{"-metadata:s:t:m:filename:1.ttf"}))
}

func FuzzArgumentRoundTrip(f *testing.F) {
	for _, text := range []string{"", "-c copy", `-vf "drawtext=text='hello  world'"`, `-i 'C:\a b\file.mkv'`} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if !utf8.ValidString(text) {
			t.Skip("仅处理有效 UTF-8 文本")
		}
		args, err := Parse(text)
		if err != nil {
			return
		}
		got, err := Parse(Join(args))
		require.NoError(t, err)
		assert.Equal(t, args, got)
	})
}
