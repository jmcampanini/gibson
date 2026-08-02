package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jmcampanini/gibson/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCommandPassesArgumentsAndWriters(t *testing.T) {
	wantErr := errors.New("run failed")
	var got app.RunOptions
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	outcome := app.RunCompleted
	command := newRunCommand(func(_ context.Context, options app.RunOptions) (app.RunOutcome, error) {
		got = options
		return app.RunInterrupted, wantErr
	}, &outcome)
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"review", "Inspect this change"})

	require.ErrorIs(t, command.Execute(), wantErr)
	assert.Equal(t, app.RunInterrupted, outcome)
	assert.Equal(t, "review", got.Type)
	assert.Equal(t, "Inspect this change", got.Message)
	assert.Empty(t, got.Checkout)
	assert.Same(t, &stdout, got.Stdout)
	assert.Same(t, &stderr, got.Stderr)
}

func TestRunCommandPassesCheckout(t *testing.T) {
	var got app.RunOptions
	outcome := app.RunCompleted
	command := newRunCommand(func(_ context.Context, options app.RunOptions) (app.RunOutcome, error) {
		got = options
		return app.RunCompleted, nil
	}, &outcome)
	command.SetArgs([]string{"review", "Inspect this change", "--checkout", "feature"})

	require.NoError(t, command.Execute())
	assert.Equal(t, "feature", got.Checkout)
}

func TestRunCommandRequiresTypeAndMessage(t *testing.T) {
	tests := map[string][]string{
		"no arguments":    nil,
		"missing message": {"quick"},
		"extra argument":  {"quick", "hello", "extra"},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			called := false
			outcome := app.RunCompleted
			command := newRunCommand(func(context.Context, app.RunOptions) (app.RunOutcome, error) {
				called = true
				return app.RunCompleted, nil
			}, &outcome)
			command.SetArgs(args)

			require.Error(t, command.Execute())
			assert.False(t, called)
		})
	}
}
