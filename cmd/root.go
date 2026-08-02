package cmd

import (
	"context"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/internal/app"
	"github.com/spf13/cobra"
)

func Execute(ctx context.Context, logger *log.Logger) (app.RunOutcome, error) {
	serve := func(ctx context.Context, options app.ServeOptions) error {
		return app.Serve(ctx, options, logger)
	}
	run := func(ctx context.Context, options app.RunOptions) (app.RunOutcome, error) {
		return app.Run(ctx, options, logger)
	}
	outcome := app.RunCompleted
	err := newRootCommand(serve, run, &outcome).ExecuteContext(ctx)
	return outcome, err
}

func newRootCommand(serve serveFunc, run runFunc, outcome *app.RunOutcome) *cobra.Command {
	command := &cobra.Command{
		Use:           "gibson",
		Short:         "Drive pi coding-agent sessions from a browser",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.AddCommand(newServeCommand(serve), newRunCommand(run, outcome))
	return command
}
