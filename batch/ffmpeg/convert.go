package ffmpeg

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"
)

var convertCmd = &cli.Command{
	Name:    "convert",
	Aliases: nil,
	Flags: []cli.Flag{
		inputPath,
		inputFormat,
		outputPath,
		outputFormat,
		advance,
		dryRun,
		workers,
	},
	Usage:     "视频转换批处理",
	UsageText: "",
	Action: func(ctx context.Context, c *cli.Command) error {
		opt := &VideoBatchOption{
			InputPath:    c.String("input_path"),
			InputFormat:  c.String("input_format"),
			OutputPath:   c.String("output_path"),
			OutputFormat: c.String("output_format"),
			Advance:      c.String("advance"),
			Workers:      c.Int("workers"),
		}

		vb, err := NewVideoBatch(opt)
		if err != nil {
			logger.Error("创建批处理失败", zap.Error(err))
			return fmt.Errorf("create video batch: %w", err)
		}

		cmdList, err := vb.GetConvertBatch()
		if err != nil {
			logger.Error("获取转换命令失败", zap.Error(err))
			return fmt.Errorf("get convert batch: %w", err)
		}

		if c.Bool("dry-run") {
			for _, cmd := range cmdList {
				logger.Info("预览命令: " + FormatCommand(cmd))
			}
			return nil
		}

		logger.Info("开始执行转换批处理", zap.Int("count", len(cmdList)))
		if err := vb.ExecuteBatch(ctx, cmdList); err != nil {
			logger.Error("执行转换失败", zap.Error(err))
			return fmt.Errorf("execute batch: %w", err)
		}
		logger.Info("转换批处理完成")

		return nil
	},
}
