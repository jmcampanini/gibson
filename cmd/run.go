package cmd

import (
	"context"

	"github.com/jmcampanini/gibson/internal/app"
	"github.com/spf13/cobra"
)

type runFunc func(context.Context, app.RunOptions) (app.RunOutcome, error)

func newRunCommand(run runFunc, outcome *app.RunOutcome) *cobra.Command {
	return &cobra.Command{
		Use:   "run <type> <message>",
		Short: "Run one pi prompt in the launch checkout",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			result, err := run(command.Context(), app.RunOptions{
				Type:    args[0],
				Message: args[1],
				Stdout:  command.OutOrStdout(),
				Stderr:  command.ErrOrStderr(),
			})
			*outcome = result
			return err
		},
	}
}
