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
