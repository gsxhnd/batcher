package renamefile

import (
	"context"

	"github.com/gsxhnd/batcher/utils"
	"github.com/urfave/cli/v3"
)

var (
	folder = &cli.StringFlag{
		Name:  "input_path",
		Value: "./",
		Usage: "源视频路径",
	}
	md5 = &cli.BoolFlag{
		Name: "md5",
	}
)

var logger = utils.NewLogger()
var RenameFileCmd = &cli.Command{
	Name:  "rename_file",
	Flags: []cli.Flag{},
	Action: func(ctx context.Context, c *cli.Command) error {
		logger.Debug("debug")
		return nil
	},
}
