package main

import (
	"context"
	"fmt"
	"os"

	"github.com/gsxhnd/batcher/batch/ffmpeg"
	renamefile "github.com/gsxhnd/batcher/batch/rename_file"
	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		HideVersion: true,
		Usage:       "命令行工具",
		Commands: []*cli.Command{
			ffmpeg.FfmpegBatchCmd,
			renamefile.RenameFileCmd,
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		// 使用 fmt.Fprintf 输出错误信息，而不是 panic
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}
