package main

import (
	"fmt"
	"os"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
)

func main() {
	command := cli.NewRootCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintln(command.ErrOrStderr(), err)
		os.Exit(1)
	}
}
