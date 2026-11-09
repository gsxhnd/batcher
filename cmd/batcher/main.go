package main

import (
	"fmt"
	"os"

	"github.com/gsxhnd/batcher/internal/tui"
)

func main() {
	if err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}
