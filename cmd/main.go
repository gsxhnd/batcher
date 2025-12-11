package main

import (
	"context"
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
	err := cmd.Run(context.Background(), os.Args)
	if err != nil {
		panic(err)
	}
}
