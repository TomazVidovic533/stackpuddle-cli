// Command stackpuddle is the StackPuddle command-line interface.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
)

// main only wires the process to run and exits; it is the one place that calls os.Exit. SIGINT, SIGTERM and
// SIGHUP cancel the root context, so a running operation stops at its next step boundary as interrupted.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	err := run(ctx, os.Args[1:], os.LookupEnv, os.Getwd, os.Stdout, os.Stderr)
	stop()
	os.Exit(cli.ExitCode(err))
}
