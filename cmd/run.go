package cmd

import (
	"context"

	"github.com/jmcampanini/gibson/internal/app"
	"github.com/spf13/cobra"
)

type runFunc func(context.Context, app.RunOptions) (app.RunOutcome, error)

func newRunCommand(run runFunc, outcome *app.RunOutcome) *cobra.Command {
	var checkout string
	command := &cobra.Command{
		Use:   "run <type> <message> [--checkout <name>]",
		Short: "Run one pi prompt in a checkout",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			result, err := run(command.Context(), app.RunOptions{
				Type:     args[0],
				Message:  args[1],
				Checkout: checkout,
				Stdout:   command.OutOrStdout(),
				Stderr:   command.ErrOrStderr(),
			})
			*outcome = result
			return err
		},
	}
	command.Flags().StringVar(&checkout, "checkout", "", "run in the named checkout")
	return command
}
