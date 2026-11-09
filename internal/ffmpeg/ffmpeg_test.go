package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureData 描述临时占位测试文件。
//
// 这些文件不是真实媒体文件：ffmpeg 包的测试只校验“按文件名/后缀生成的命令”，
// 不会真正执行转码，因此内容为空即可。它们在 t.TempDir() 内生成、测试结束自动清理，
// 无需用 taskfile 或手工命令预先创建 testdata。
type fixtureData struct {
	root string // 输入根目录，含 1/ 2/ 两个子目录
	dir1 string // 子目录 1，也用作字体目录
	dir2 string // 子目录 2
	out  string // 输出目录

	mp4s  []string // 1/1.mp4, 1/2.mp4, 2/1.mp4, 2/2.mp4
	mkvs  []string // 1/1.mkv, 1/2.mkv, 2/1.mkv, 2/2.mkv
	fonts []string // 1/1.ttf, 1/2.ttf, 2/1.ttf, 2/2.ttf
}

// newFixture 在临时目录生成占位测试文件：
//
//	root/1/{1,2}.{mkv,mp4,ass,ttf}
//	root/2/{1,2}.{mkv,mp4,ass,ttf}
func newFixture(t *testing.T) fixtureData {
	t.Helper()
	root := t.TempDir()
	fx := fixtureData{
		root: root,
		dir1: filepath.Join(root, "1"),
		dir2: filepath.Join(root, "2"),
		out:  filepath.Join(root, "output"),
	}
	names := []string{"1.mkv", "1.mp4", "1.ass", "1.ttf", "2.mkv", "2.mp4", "2.ass", "2.ttf"}
	for _, dir := range []string{fx.dir1, fx.dir2} {
		require.NoError(t, os.MkdirAll(dir, 0o755), "create fixture dir")
		for _, n := range names {
			writeFile(t, filepath.Join(dir, n), "")
		}
	}
	fx.mp4s = []string{
		filepath.Join(fx.dir1, "1.mp4"), filepath.Join(fx.dir1, "2.mp4"),
		filepath.Join(fx.dir2, "1.mp4"), filepath.Join(fx.dir2, "2.mp4"),
	}
	fx.mkvs = []string{
		filepath.Join(fx.dir1, "1.mkv"), filepath.Join(fx.dir1, "2.mkv"),
		filepath.Join(fx.dir2, "1.mkv"), filepath.Join(fx.dir2, "2.mkv"),
	}
	fx.fonts = []string{
		filepath.Join(fx.dir1, "1.ttf"), filepath.Join(fx.dir1, "2.ttf"),
		filepath.Join(fx.dir2, "1.ttf"), filepath.Join(fx.dir2, "2.ttf"),
	}
	return fx
}

// attachCmd 生成 fonts 字体列表对应的 -attach 参数段期望值。
func attachCmd(fonts []string) []string {
	cmd := make([]string, 0, len(fonts)*4)
	for i, fontPath := range fonts {
		cmd = append(cmd,
			"-attach", fontPath,
			fmt.Sprintf("-metadata:s:t:%d", i),
			"mimetype=application/x-truetype-font",
		)
	}
	return cmd
}

// outputs 生成输出路径期望值：1.mkv / 2.mkv / 1-1.mkv / 2-1.mkv（重名附加序号）。
func outputs(fx fixtureData, ext string) []string {
	return []string{
		filepath.Join(fx.out, "1."+ext),
		filepath.Join(fx.out, "2."+ext),
		filepath.Join(fx.out, "1-1."+ext),
		filepath.Join(fx.out, "2-1."+ext),
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNewVideoBatch(t *testing.T) {
	tests := []struct {
		name    string
		opt     *VideoBatchOption
		wantErr bool
	}{
		{"test_success", &VideoBatchOption{
			OutputPath: t.TempDir(),
		}, false},
		{"test_nil_option", nil, true},
		{"test_empty_output", &VideoBatchOption{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewVideoBatch(tt.opt)
			if tt.wantErr {
				assert.NotNil(t, err)
				return
			}
			assert.Nil(t, err)
			assert.NotNil(t, got)
		})
	}
}

func Test_videoBatch_GetVideosList(t *testing.T) {
	type args struct {
		InputPath   string
		InputFormat string
	}

	fx := newFixture(t)
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{"test_mkv", args{InputPath: fx.root, InputFormat: "mkv"}, fx.mkvs, false},
		{"test_mp4", args{InputPath: fx.root, InputFormat: "mp4"}, fx.mp4s, false},
		{"test_not_exist_path", args{InputPath: "../111", InputFormat: "mp4"}, []string{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vb := &videoBatch{
				option: &VideoBatchOption{
					InputPath:   tt.args.InputPath,
					InputFormat: tt.args.InputFormat,
				},
			}

			videosList, err := vb.GetVideosList()

			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.want, videosList)
			}
		})
	}
}

func Test_videoBatch_GetFontsList(t *testing.T) {
	type args struct {
		FontsPath string
	}
	fx := newFixture(t)
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{"test_success", args{FontsPath: fx.root}, fx.fonts, false},
		{"test_not_exist", args{FontsPath: "../111"}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vb := &videoBatch{
				option: &VideoBatchOption{
					FontsPath: tt.args.FontsPath,
				},
			}

			got, err := vb.GetFontsList()
			if tt.wantErr {
				assert.NotNil(t, err)
				return
			}
			assert.Nil(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_videoBatch_GetFontsParams(t *testing.T) {
	type args struct {
		FontsPath string
	}
	fx := newFixture(t)
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{"test_success", args{FontsPath: fx.root}, attachCmd(fx.fonts), false},
		{"test_not_exist", args{FontsPath: "../111"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vb := &videoBatch{
				option: &VideoBatchOption{
					FontsPath: tt.args.FontsPath,
				},
			}

			got, err := vb.GetFontsParams()

			assert.Equal(t, tt.want, got)
			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func Test_videoBatch_GetConvertBatch(t *testing.T) {
	type args struct {
		InputPath    string
		InputFormat  string
		OutputPath   string
		OutputFormat string
	}
	fx := newFixture(t)
	want := func(fx fixtureData) [][]string {
		outs := outputs(fx, "mkv")
		return [][]string{
			{"-i", fx.mp4s[0], outs[0]},
			{"-i", fx.mp4s[1], outs[1]},
			{"-i", fx.mp4s[2], outs[2]},
			{"-i", fx.mp4s[3], outs[3]},
		}
	}
	tests := []struct {
		name    string
		args    args
		want    [][]string
		wantErr bool
	}{
		{"test_success", args{InputPath: fx.root, InputFormat: "mp4", OutputPath: fx.out, OutputFormat: "mkv"}, want(fx), false},
		{"test_not_exist", args{InputPath: "../111", InputFormat: "mp4", OutputPath: fx.out, OutputFormat: "mkv"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vb := &videoBatch{
				option: &VideoBatchOption{
					InputPath:    tt.args.InputPath,
					InputFormat:  tt.args.InputFormat,
					OutputPath:   tt.args.OutputPath,
					OutputFormat: tt.args.OutputFormat,
				},
			}

			got, err := vb.GetConvertBatch()

			if tt.wantErr {
				assert.NotNil(t, err)
				return
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_videoBatch_GetAddFontsBatch(t *testing.T) {
	type args struct {
		InputPath    string
		InputFormat  string
		OutputPath   string
		OutputFormat string
		FontsPath    string
	}
	fx := newFixture(t)
	want := func(fx fixtureData) [][]string {
		outs := outputs(fx, "mkv")
		var cmds [][]string
		for i, videoPath := range fx.mkvs {
			cmd := []string{"-i", videoPath, "-c", "copy"}
			cmd = append(cmd, attachCmd(fx.fonts)...)
			cmd = append(cmd, outs[i])
			cmds = append(cmds, cmd)
		}
		return cmds
	}
	tests := []struct {
		name    string
		args    args
		want    [][]string
		wantErr bool
	}{
		{"test_success", args{InputPath: fx.root, InputFormat: "mkv", FontsPath: fx.root, OutputPath: fx.out, OutputFormat: "mkv"}, want(fx), false},
		{"test_not_exist", args{InputPath: "../111", InputFormat: "mkv", OutputPath: fx.out, OutputFormat: "mkv"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vb := &videoBatch{
				option: &VideoBatchOption{
					InputPath:    tt.args.InputPath,
					InputFormat:  tt.args.InputFormat,
					OutputPath:   tt.args.OutputPath,
					OutputFormat: tt.args.OutputFormat,
					FontsPath:    tt.args.FontsPath,
				},
			}

			got, err := vb.GetAddFontsBatch()
			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_videoBatch_filterOutput(t *testing.T) {
	type args struct {
		outPath   string
		outFormat string
	}

	fx := newFixture(t)
	tests := []struct {
		name      string
		inputPath []string
		args      args
		want      map[string]string
	}{
		{"test", fx.mp4s, args{outPath: "../output", outFormat: "mkv"}, map[string]string{
			fx.mp4s[0]: filepath.Join("../output", "1.mkv"),
			fx.mp4s[1]: filepath.Join("../output", "2.mkv"),
			fx.mp4s[2]: filepath.Join("../output", "1-1.mkv"),
			fx.mp4s[3]: filepath.Join("../output", "2-1.mkv"),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vb := &videoBatch{
				option: &VideoBatchOption{
					OutputPath:   tt.args.outPath,
					OutputFormat: tt.args.outFormat,
				},
			}

			got := vb.filterOutput(tt.inputPath)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"simple", []string{"-i", "input.mp4"}, "ffmpeg -i input.mp4"},
		{"empty", []string{}, "ffmpeg "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatCommand(tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_videoBatch_ExecuteBatch(t *testing.T) {
	tests := []struct {
		name     string
		workers  int
		cmdBatch [][]string
		wantErr  bool
	}{
		{"empty_batch", 1, [][]string{}, false},
		{"single_worker", 1, [][]string{{"-version"}}, false},
		{"multi_workers", 2, [][]string{{"-version"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vb := &videoBatch{
				option: &VideoBatchOption{
					Workers: tt.workers,
				},
			}

			err := vb.ExecuteBatch(context.Background(), tt.cmdBatch)
			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func Test_videoBatch_ExecuteBatch_Events(t *testing.T) {
	var events []Event
	vb := &videoBatch{
		option: &VideoBatchOption{
			Workers: 1,
			OnEvent: func(e Event) { events = append(events, e) },
		},
	}

	err := vb.ExecuteBatch(context.Background(), [][]string{{"-version"}})
	assert.Nil(t, err)

	types := make([]EventType, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}
	assert.Equal(t, EventBatchStart, types[0])
	assert.Contains(t, types, EventCommandStart)
	assert.Contains(t, types, EventLog)
	assert.Equal(t, EventCommandDone, types[len(types)-1])
}

func Test_videoBatch_ExecuteBatch_Failed(t *testing.T) {
	var failed bool
	vb := &videoBatch{
		option: &VideoBatchOption{
			Workers: 2,
			OnEvent: func(e Event) {
				if e.Type == EventCommandFailed {
					failed = true
				}
			},
		},
	}

	err := vb.ExecuteBatch(context.Background(), [][]string{{"-i", "not-exist-file.mp4", "-f", "null", "-"}})
	assert.NotNil(t, err)
	assert.True(t, failed)
}

func Test_videoBatch_ExecuteBatch_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	vb := &videoBatch{option: &VideoBatchOption{Workers: 1}}
	err := vb.ExecuteBatch(ctx, [][]string{{"-version"}})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPlan_UnknownTask(t *testing.T) {
	_, _, err := Plan("nope", &VideoBatchOption{OutputPath: t.TempDir()})
	assert.NotNil(t, err)
}
