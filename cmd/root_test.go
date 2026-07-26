package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommand(t *testing.T) {
	first := newRootCommand()
	second := newRootCommand()

	assert.NotSame(t, first, second)
	assert.Equal(t, "gibson", first.Use)
	assert.True(t, first.SilenceErrors)
	assert.True(t, first.SilenceUsage)
	assert.Equal(t, Version, first.Version)

	serve, _, err := first.Find([]string{"serve"})
	require.NoError(t, err)
	assert.Equal(t, "serve", serve.Name())
}

func TestRootVersion(t *testing.T) {
	originalVersion := Version
	Version = "test-version"
	t.Cleanup(func() { Version = originalVersion })

	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--version"})

	require.NoError(t, command.Execute())
	assert.Equal(t, "gibson version test-version", strings.TrimSpace(output.String()))
}

func TestRootHelp(t *testing.T) {
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})

	require.NoError(t, command.Execute())
	for _, want := range []string{
		"Drive pi coding-agent sessions from a browser",
		"serve",
		"Serve the Gibson web application",
	} {
		assert.Contains(t, output.String(), want)
	}
}
