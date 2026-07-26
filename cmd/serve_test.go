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

func TestServeCommandPassesOptionsToApplication(t *testing.T) {
	wantErr := errors.New("serve failed")
	var got app.ServeOptions
	command := newServeCommand(func(_ context.Context, options app.ServeOptions) error {
		got = options
		return wantErr
	})
	command.SetArgs([]string{"--port", "8123", "--dev"})

	require.ErrorIs(t, command.Execute(), wantErr)
	require.NotNil(t, got.PortOverride)
	assert.Equal(t, 8123, *got.PortOverride)
	assert.True(t, got.Dev)
}

func TestServeCommandLeavesPortOverrideUnset(t *testing.T) {
	var got app.ServeOptions
	command := newServeCommand(func(_ context.Context, options app.ServeOptions) error {
		got = options
		return nil
	})

	require.NoError(t, command.Execute())
	assert.Nil(t, got.PortOverride)
}

func TestServeCommandPreservesZeroPortOverride(t *testing.T) {
	var got app.ServeOptions
	command := newServeCommand(func(_ context.Context, options app.ServeOptions) error {
		got = options
		return nil
	})
	command.SetArgs([]string{"--port", "0"})

	require.NoError(t, command.Execute())
	require.NotNil(t, got.PortOverride)
	assert.Zero(t, *got.PortOverride)
}

func TestServeHelp(t *testing.T) {
	command := newServeCommand(func(context.Context, app.ServeOptions) error {
		t.Fatal("serve called while rendering help")
		return nil
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})

	require.NoError(t, command.Execute())
	assert.Contains(t, output.String(), "--port")
	assert.Contains(t, output.String(), "--dev")
}
