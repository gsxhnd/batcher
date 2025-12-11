package ffmpeg

import (
	"github.com/gsxhnd/batcher/utils"
	"github.com/urfave/cli/v3"
)

var (
	inputPath = &cli.StringFlag{
		Name:  "input_path",
		Value: "./",
		Usage: "源视频路径",
	}

	inputFormat = &cli.StringFlag{
		Name:  "input_format",
		Value: "mp4",
		Usage: "源视频后缀",
	}

	outputPath = &cli.StringFlag{
		Name:  "output_path",
		Value: "./result/",
		Usage: "转换后文件存储位置",
	}

	outputFormat = &cli.StringFlag{
		Name:  "output_format",
		Value: "mkv",
		Usage: "转换后的视频后缀",
	}

	advance = &cli.StringFlag{
		Name:  "advance",
		Value: "",
		Usage: "高级自定义参数",
	}

	dryRun = &cli.BoolFlag{
		Name:  "dry-run",
		Value: false,
		Usage: "是否执行批处理命令False时仅打印命令",
	}

	inputFontsPath = &cli.StringFlag{
		Name:     "input_fonts_path",
		Usage:    "添加的字体文件夹",
		Required: false,
	}
)

var logger = utils.NewLogger()
var FfmpegBatchCmd = &cli.Command{
	Name:        "ffmpeg",
	Description: "ffmpeg视频批处理工具，支持视频格式转换、字幕添加和字体添加",
	Flags:       []cli.Flag{},
	Commands: []*cli.Command{
		convertCmd,
		addSubCmd,
		addFontCmd,
	},
}
