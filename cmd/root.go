package cmd

import (
	"context"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/internal/app"
	"github.com/spf13/cobra"
)

func Execute(ctx context.Context, logger *log.Logger) error {
	serve := func(ctx context.Context, options app.ServeOptions) error {
		return app.Serve(ctx, options, logger)
	}
	return newRootCommand(serve).ExecuteContext(ctx)
}

func newRootCommand(serve serveFunc) *cobra.Command {
	command := &cobra.Command{
		Use:           "gibson",
		Short:         "Drive pi coding-agent sessions from a browser",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.AddCommand(newServeCommand(serve))
	return command
}
