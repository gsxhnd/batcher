package main

import (
	"github.com/gsxhnd/batcher/batch_ffmpeg"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
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

	exec = &cli.BoolFlag{
		Name:  "exec",
		Value: false,
		Usage: "是否执行批处理命令False时仅打印命令",
	}

	inputFontsPath = &cli.StringFlag{
		Name:     "input_fonts_path",
		Usage:    "添加的字体文件夹",
		Required: false,
	}
)

var ffmpegBatchCmd = &cli.Command{
	Name:        "ffmpeg_batch",
	Description: "ffmpeg视频批处理工具，支持视频格式转换、字幕添加和字体添加",
	Flags:       []cli.Flag{},
	Subcommands: []*cli.Command{
		ffmpegBatchConvertCmd,
		ffmpegBatchAddSubCmd,
		ffmpegBatchAddFontCmd,
	},
}

var ffmpegBatchConvertCmd = &cli.Command{
	Name:    "convert",
	Aliases: nil,
	Flags: []cli.Flag{
		inputPath,
		inputFormat,
		outputPath,
		outputFormat,
		advance,
		exec,
	},
	Usage:     "视频转换批处理",
	UsageText: "",
	Action: func(ctx *cli.Context) error {
		var opt = batch_ffmpeg.VideoBatchOption{
			InputPath:    ctx.String("input_path"),
			InputFormat:  ctx.String("input_format"),
			OutputPath:   ctx.String("output_path"),
			OutputFormat: ctx.String("output_format"),
			Advance:      ctx.String("advance"),
			Exec:         ctx.Bool("exec"),
		}
		// logger.Infof("Source videos directory: " + opt.InputPath)
		// logger.Infof("Source videos format: " + opt.InputFormat)
		// logger.Infof("Target video's font paths: " + opt.FontsPath)
		// logger.Infof("Dest video directory: " + opt.OutputPath)
		// logger.Infof("Dest video format: " + opt.OutputFormat)

		vb, err := batch_ffmpeg.NewVideoBatch(&opt)
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

		if !opt.Exec {
			for _, cmd := range cmdList {
				var cmdStr = "ffmpeg "
				for _, c := range cmd {
					cmdStr += c + " "
				}
				logger.Info("Cmd batch not execute,cmd: " + cmdStr)
			}
			return nil
		}

		return vb.ExecuteBatch(cmdList)
	},
}

var ffmpegBatchAddSubCmd = &cli.Command{
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
		exec,
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
	Action: func(ctx *cli.Context) error {
		var opt = batch_ffmpeg.VideoBatchOption{
			InputPath:      ctx.String("input_path"),
			InputFormat:    ctx.String("input_format"),
			OutputPath:     ctx.String("output_path"),
			OutputFormat:   ctx.String("output_format"),
			InputSubSuffix: ctx.String("input_sub_suffix"),
			InputSubNo:     ctx.Int("input_sub_no"),
			InputSubTitle:  ctx.String("input_sub_title"),
			InputSubLang:   ctx.String("input_sub_lang"),
			FontsPath:      ctx.String("input_fonts_path"),
			Exec:           ctx.Bool("exec"),
		}

		vb, err := batch_ffmpeg.NewVideoBatch(&opt)
		if err != nil {
			logger.Panic("Create dest path error", zap.Error(err))
			return err
		}

		cmdList, err := vb.GetAddSubtitleBatch()
		if err != nil {
			return err
		}

		if !opt.Exec {
			for _, cmd := range cmdList {
				var cmdStr = "ffmpeg "
				for _, c := range cmd {
					cmdStr += c + " "
				}
				logger.Info("Cmd batch not execute,cmd: " + cmdStr)
			}
			return nil
		}

		return vb.ExecuteBatch(cmdList)
	},
}

var ffmpegBatchAddFontCmd = &cli.Command{
	Name:    "add_fonts",
	Aliases: nil,
	Usage:   "视频添加字体批处理",
	Flags: []cli.Flag{
		inputPath,
		inputFormat,
		outputPath,
		outputFormat,
		exec,
		&cli.StringFlag{
			Name:     "input_fonts_path",
			Usage:    "添加的字体文件夹",
			Value:    "fonts",
			Required: true,
		},
	},
	UsageText: "",
	Action: func(ctx *cli.Context) error {
		var opt = batch_ffmpeg.VideoBatchOption{
			InputPath:    ctx.String("input_path"),
			InputFormat:  ctx.String("input_format"),
			OutputPath:   ctx.String("output_path"),
			OutputFormat: ctx.String("output_format"),
			Exec:         ctx.Bool("exec"),
			FontsPath:    ctx.String("input_fonts_path"),
		}

		vb, err := batch_ffmpeg.NewVideoBatch(&opt)
		if err != nil {
			logger.Panic("Create dest path error", zap.Error(err))
			return err
		}

		cmdList, err := vb.GetAddFontsBatch()
		if err != nil {
			return err
		}

		if !opt.Exec {
			for _, cmd := range cmdList {
				var cmdStr = "ffmpeg "
				for _, c := range cmd {
					cmdStr += c + " "
				}
				logger.Info("Cmd batch not execute,cmd: " + cmdStr)
			}
			return nil
		}

		return vb.ExecuteBatch(cmdList)
	},
}
