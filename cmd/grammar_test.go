package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/jmcampanini/gibson/internal/app"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type grammarScenario struct {
	name           string
	args           []string
	commandPath    string
	wantError      bool
	wantOutput     bool
	wantServeCalls int
	wantRunCalls   int
}

var commandGrammarMatrix = []grammarScenario{
	{name: "bare root"},
	{name: "unknown command", args: []string{"bogus"}, commandPath: "gibson", wantError: true},
	{name: "unknown root flag", args: []string{"--bogus"}, wantError: true},
	{name: "unknown serve flag", args: []string{"serve", "--bogus"}, wantError: true},
	{name: "unknown run flag", args: []string{"run", "a", "b", "--bogus"}, wantError: true},
	{name: "root help", args: []string{"--help"}},
	{name: "serve help", args: []string{"serve", "--help"}},
	{name: "run help", args: []string{"run", "--help"}},
	{name: "help serve", args: []string{"help", "serve"}},
	{name: "version", args: []string{"--version"}, wantOutput: true},
	{name: "completion fish", args: []string{"completion", "fish"}, wantOutput: true},
	{name: "serve", args: []string{"serve"}, wantServeCalls: 1},
	{name: "run", args: []string{"run", "review", "message"}, wantRunCalls: 1},
	{name: "serve rejected operand", args: []string{"serve", "extra"}, commandPath: "gibson serve", wantError: true},
	{name: "run rejected operand", args: []string{"run", "a", "b", "extra"}, commandPath: "gibson run", wantError: true},
}

func TestApplicationCommandGrammarInventory(t *testing.T) {
	covered := make(map[string]struct{})
	for _, scenario := range commandGrammarMatrix {
		if scenario.commandPath != "" {
			covered[scenario.commandPath] = struct{}{}
		}
	}

	outcome := app.RunCompleted
	command := newRootCommand(
		func(context.Context, app.ServeOptions) error { return nil },
		func(context.Context, app.RunOptions) (app.RunOutcome, error) { return app.RunCompleted, nil },
		&outcome,
	)
	for _, path := range collectApplicationCommands(command) {
		if _, ok := covered[path]; !ok {
			t.Errorf("%s has no grammar row", path)
		}
	}
}

func TestCommandGrammarMatrix(t *testing.T) {
	for _, scenario := range commandGrammarMatrix {
		t.Run(scenario.name, func(t *testing.T) {
			serveCalls := 0
			runCalls := 0
			outcome := app.RunCompleted
			command := newRootCommand(
				func(context.Context, app.ServeOptions) error {
					serveCalls++
					return nil
				},
				func(context.Context, app.RunOptions) (app.RunOutcome, error) {
					runCalls++
					return app.RunCompleted, nil
				},
				&outcome,
			)

			output, err := executeCommand(command, scenario.args...)
			if scenario.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if scenario.wantOutput {
				assert.NotEmpty(t, output)
			}
			assert.Equal(t, scenario.wantServeCalls, serveCalls)
			assert.Equal(t, scenario.wantRunCalls, runCalls)
		})
	}
}

func collectApplicationCommands(command *cobra.Command) []string {
	paths := []string{command.CommandPath()}
	for _, child := range command.Commands() {
		paths = append(paths, collectApplicationCommands(child)...)
	}
	return paths
}

func executeCommand(command *cobra.Command, args ...string) (string, error) {
	if args == nil {
		args = []string{}
	}
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}
