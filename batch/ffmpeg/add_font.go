package ffmpeg

import (
	"context"
	"fmt"

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
		dryRun,
		workers,
		&cli.StringFlag{
			Name:     "input_fonts_path",
			Usage:    "添加的字体文件夹",
			Value:    "fonts",
			Required: true,
		},
	},
	UsageText: "",
	Action: func(ctx context.Context, c *cli.Command) error {
		opt := &VideoBatchOption{
			InputPath:    c.String("input_path"),
			InputFormat:  c.String("input_format"),
			OutputPath:   c.String("output_path"),
			OutputFormat: c.String("output_format"),
			FontsPath:    c.String("input_fonts_path"),
			Workers:      c.Int("workers"),
		}

		vb, err := NewVideoBatch(opt)
		if err != nil {
			logger.Error("创建批处理失败", zap.Error(err))
			return fmt.Errorf("create video batch: %w", err)
		}

		cmdList, err := vb.GetAddFontsBatch()
		if err != nil {
			logger.Error("获取字体命令失败", zap.Error(err))
			return fmt.Errorf("get fonts batch: %w", err)
		}

		if c.Bool("dry-run") {
			for _, cmd := range cmdList {
				logger.Info("预览命令: " + FormatCommand(cmd))
			}
			return nil
		}

		logger.Info("开始执行字体添加批处理", zap.Int("count", len(cmdList)))
		if err := vb.ExecuteBatch(ctx, cmdList); err != nil {
			logger.Error("执行字体添加失败", zap.Error(err))
			return fmt.Errorf("execute batch: %w", err)
		}
		logger.Info("字体添加批处理完成")

		return nil
	},
}
