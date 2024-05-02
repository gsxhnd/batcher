package main

import (
	"os"

	"github.com/urfave/cli/v2"
)

var (
	RootCmd = cli.NewApp()
	// logger  = utils.NewLogger(&utils.Config{
	// 	Dev: true,
	// 	LogConfig: utils.LogConfig{
	// 		Level: "debug",
	// 	},
	// })
)

func init() {
	RootCmd.HideVersion = true
	RootCmd.Usage = "命令行工具"
	RootCmd.Flags = []cli.Flag{}
	RootCmd.Commands = []*cli.Command{
		ffmpegBatchCmd,
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
