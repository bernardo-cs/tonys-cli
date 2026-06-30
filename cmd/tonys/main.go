// Command tonys is an agent-friendly CLI for the TonieCloud API: it sends audio
// (files, stdin, or YouTube links) to creative tonies, with automatic format
// conversion and loudness normalization.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bernardo-cs/tonys-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := cli.NewApp()
	app.SetContext(ctx)

	err := app.Execute(os.Args[1:])
	if err == nil {
		return
	}

	// Render the error: JSON to stdout when machine output is selected, else a
	// human message to stderr. Exit code reflects the error class.
	app.PrintError(err)
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "interrupted")
	}
	os.Exit(cli.ExitCode(err))
}
