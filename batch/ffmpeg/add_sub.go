package ffmpeg

import (
	"context"
	"fmt"

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
		workers,
		&cli.StringFlag{
			Name:  "input_sub_suffix",
			Value: "ass",
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
		opt := &VideoBatchOption{
			InputPath:      c.String("input_path"),
			InputFormat:    c.String("input_format"),
			OutputPath:     c.String("output_path"),
			OutputFormat:   c.String("output_format"),
			InputSubSuffix: c.String("input_sub_suffix"),
			InputSubNo:     c.Int("input_sub_no"),
			InputSubTitle:  c.String("input_sub_title"),
			InputSubLang:   c.String("input_sub_lang"),
			FontsPath:      c.String("input_fonts_path"),
			Workers:        c.Int("workers"),
		}

		vb, err := NewVideoBatch(opt)
		if err != nil {
			logger.Error("创建批处理失败", zap.Error(err))
			return fmt.Errorf("create video batch: %w", err)
		}

		cmdList, err := vb.GetAddSubtitleBatch()
		if err != nil {
			logger.Error("获取字幕命令失败", zap.Error(err))
			return fmt.Errorf("get subtitle batch: %w", err)
		}

		if c.Bool("dry-run") {
			for _, cmd := range cmdList {
				logger.Info("预览命令: " + FormatCommand(cmd))
			}
			return nil
		}

		logger.Info("开始执行字幕添加批处理", zap.Int("count", len(cmdList)))
		if err := vb.ExecuteBatch(ctx, cmdList); err != nil {
			logger.Error("执行字幕添加失败", zap.Error(err))
			return fmt.Errorf("execute batch: %w", err)
		}
		logger.Info("字幕添加批处理完成")

		return nil
	},
}
