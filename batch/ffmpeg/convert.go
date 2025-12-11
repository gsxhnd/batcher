package ffmpeg

import (
	"context"

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
	},
	Usage:     "视频转换批处理",
	UsageText: "",
	Action: func(ctx context.Context, c *cli.Command) error {
		var opt = VideoBatchOption{
			InputPath:    c.String("input_path"),
			InputFormat:  c.String("input_format"),
			OutputPath:   c.String("output_path"),
			OutputFormat: c.String("output_format"),
			Advance:      c.String("advance"),
		}
		// logger.Infof("Source videos directory: " + opt.InputPath)
		// logger.Infof("Source videos format: " + opt.InputFormat)
		// logger.Infof("Target video's font paths: " + opt.FontsPath)
		// logger.Infof("Dest video directory: " + opt.OutputPath)
		// logger.Infof("Dest video format: " + opt.OutputFormat)

		vb, err := NewVideoBatch(&opt)
		if err != nil {
			logger.Panic("Create dest path error", zap.Error(err))
			return err
		}
		// logger.Debugf("video batcher init")

		cmdList, err := vb.GetConvertBatch()
		if err != nil {
			logger.Panic("Get Convert Batch error", zap.Error(err))
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
