// Command grind is the CLI entry point. It maps errors to exit codes:
// user errors exit 1, system errors exit 2, unexpected errors exit 99.
package main

import (
	"fmt"
	"os"

	"github.com/leebrandt/grind/internal/cli"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
)

func main() {
	root := cli.NewRootCmd(git.New())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "grind:", err)
		os.Exit(grinderr.ExitCode(err))
	}
}
