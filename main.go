package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/cmd"
	"github.com/jmcampanini/gibson/internal/app"
)

func main() {
	logger := log.NewWithOptions(os.Stderr, log.Options{
		Formatter:       log.TextFormatter,
		ReportTimestamp: true,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	outcome, err := cmd.Execute(ctx, logger)
	if code := processExitCode(outcome, err, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func processExitCode(outcome app.RunOutcome, err error, stderr io.Writer) int {
	if err != nil {
		if _, writeErr := fmt.Fprintf(stderr, "gibson: error: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}
	if outcome == app.RunInterrupted {
		return 130
	}
	return 0
}
