package cmd

import (
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
