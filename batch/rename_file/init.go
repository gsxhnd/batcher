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
		Name:  "md5",
		Usage: "使用 MD5 哈希作为文件名",
	}
)

// logger 是包级别的共享日志实例
var logger = utils.NewLogger()

// RenameFileCmd 是文件重命名命令的入口
var RenameFileCmd = &cli.Command{
	Name:  "rename_file",
	Usage: "文件重命名工具",
	Flags: []cli.Flag{folder, md5},
	Action: func(ctx context.Context, c *cli.Command) error {
		logger.Debug("rename file command executed")
		return nil
	},
}
