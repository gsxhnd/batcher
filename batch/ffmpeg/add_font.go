package ffmpeg

import (
	"context"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"
)

var addFontCmd = &cli.Command{
	Name:    "add_fonts",
	Aliases: nil,
	Usage:   "视频添加字体批处理",
	Flags: []cli.Flag{
		inputPath,
		inputFormat,
		outputPath,
		outputFormat,
		&cli.StringFlag{
			Name:     "input_fonts_path",
			Usage:    "添加的字体文件夹",
			Value:    "fonts",
			Required: true,
		},
	},
	UsageText: "",
	Action: func(ctx context.Context, c *cli.Command) error {
		var opt = VideoBatchOption{
			InputPath:    c.String("input_path"),
			InputFormat:  c.String("input_format"),
			OutputPath:   c.String("output_path"),
			OutputFormat: c.String("output_format"),
			FontsPath:    c.String("input_fonts_path"),
		}

		vb, err := NewVideoBatch(&opt)
		if err != nil {
			logger.Panic("Create dest path error", zap.Error(err))
			return err
		}

		cmdList, err := vb.GetAddFontsBatch()
		if err != nil {
			return err
		}

		if c.Bool("exec") {
			for _, cmd := range cmdList {
				var cmdStr = "ffmpeg "
				for _, c := range cmd {
					cmdStr += c + " "
				}
				logger.Info("Cmd batch not execute,cmd: " + cmdStr)
			}
			return nil
		} else {
			return vb.ExecuteBatch(cmdList)
		}
	},
}
