package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/cmd"
)

func main() {
	logger := log.NewWithOptions(os.Stderr, log.Options{
		Formatter:       log.TextFormatter,
		ReportTimestamp: true,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.Execute(ctx, logger); err != nil {
		fmt.Fprintf(os.Stderr, "gibson: error: %v\n", err)
		os.Exit(1)
	}
}
