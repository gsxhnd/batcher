package renamefile

import "github.com/urfave/cli/v2"

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

var RenameFileCmd = &cli.Command{
	Name:  "rename_file",
	Flags: []cli.Flag{},
}
