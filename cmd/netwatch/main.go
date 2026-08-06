package main

import (
	"fmt"
	"os"

	"github.com/chewcw/netwatch/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "netwatch:", err)
		os.Exit(cli.ExitCode(err))
	}
}
