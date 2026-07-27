package cmd

import (
	"context"

	"github.com/jmcampanini/gibson/internal/app"
	"github.com/spf13/cobra"
)

type serveFunc func(context.Context, app.ServeOptions) error

func newServeCommand(serve serveFunc) *cobra.Command {
	var port int
	var dev bool
	command := &cobra.Command{
		Use:   "serve",
		Short: "Serve the Gibson web application",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			options := app.ServeOptions{Dev: dev}
			if command.Flags().Changed("port") {
				options.PortOverride = &port
			}
			return serve(command.Context(), options)
		},
	}
	command.Flags().IntVar(&port, "port", 0, "override the configured server port")
	command.Flags().BoolVar(&dev, "dev", false, "proxy application requests to the Vite development server")
	return command
}
