package main

import (
	"os"

	renamefile "github.com/gsxhnd/batcher/batch/rename_file"
	"github.com/gsxhnd/batcher/utils"
	"github.com/urfave/cli/v2"
)

var (
	RootCmd = cli.NewApp()
	logger  = utils.NewLogger()
)

func init() {
	RootCmd.HideVersion = true
	RootCmd.Usage = "命令行工具"
	RootCmd.Flags = []cli.Flag{}
	RootCmd.Commands = []*cli.Command{
		ffmpegBatchCmd,
		renamefile.RenameFileCmd,
	}
	// RootCmd.CommandNotFound = func(ctx *cli.Context, s string) {
	// 	fmt.Println(s)
	// }
}

func main() {
	err := RootCmd.Run(os.Args)
	if err != nil {
		panic(err)
	}
}
