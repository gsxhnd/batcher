package ffmpeg

import (
	"context"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"
)

var addSubCmd = &cli.Command{
	Name:      "add_sub",
	Aliases:   nil,
	Usage:     "视频添加字幕批处理",
	UsageText: "",
	Flags: []cli.Flag{
		inputPath,
		inputFormat,
		outputPath,
		outputFormat,
		advance,
		inputFontsPath,
		&cli.StringFlag{
			Name:  "input_sub_suffix",
			Value: ".ass",
			Usage: "添加的字幕后缀",
		},
		&cli.IntFlag{
			Name:  "input_sub_no",
			Value: 0,
			Usage: "添加的字幕所处流的位置",
		},
		&cli.StringFlag{
			Name:  "input_sub_lang",
			Value: "chi",
			Usage: "添加的字幕语言缩写其他语言请参考ffmpeg",
		},
		&cli.StringFlag{
			Name:  "input_sub_title",
			Value: "Chinese",
			Usage: "添加的字幕标题",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		var opt = VideoBatchOption{
			InputPath:      c.String("input_path"),
			InputFormat:    c.String("input_format"),
			OutputPath:     c.String("output_path"),
			OutputFormat:   c.String("output_format"),
			InputSubSuffix: c.String("input_sub_suffix"),
			InputSubNo:     c.Int("input_sub_no"),
			InputSubTitle:  c.String("input_sub_title"),
			InputSubLang:   c.String("input_sub_lang"),
			FontsPath:      c.String("input_fonts_path"),
		}

		vb, err := NewVideoBatch(&opt)
		if err != nil {
			logger.Panic("Create dest path error", zap.Error(err))
			return err
		}

		cmdList, err := vb.GetAddSubtitleBatch()
		if err != nil {
			return err
		}

		if c.Bool("dry_run") {
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
