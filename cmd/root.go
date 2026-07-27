package cmd

import (
	"github.com/jmcampanini/gibson/internal/app"
	"github.com/spf13/cobra"
)

func Execute() error {
	return newRootCommand().Execute()
}

func newRootCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "gibson",
		Short:         "Drive pi coding-agent sessions from a browser",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.AddCommand(newServeCommand(app.Serve))
	return command
}
