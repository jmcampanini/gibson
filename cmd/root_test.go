package cmd

import (
	"context"
	"testing"

	"github.com/jmcampanini/gibson/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommand(t *testing.T) {
	serveStub := func(context.Context, app.ServeOptions) error { return nil }
	runStub := func(context.Context, app.RunOptions) (app.RunOutcome, error) {
		return app.RunCompleted, nil
	}
	firstOutcome := app.RunCompleted
	secondOutcome := app.RunCompleted
	first := newRootCommand(serveStub, runStub, &firstOutcome)
	second := newRootCommand(serveStub, runStub, &secondOutcome)

	assert.NotSame(t, first, second)
	assert.Equal(t, "gibson", first.Use)
	assert.True(t, first.SilenceErrors)
	assert.True(t, first.SilenceUsage)
	assert.Equal(t, Version, first.Version)

	serve, _, err := first.Find([]string{"serve"})
	require.NoError(t, err)
	assert.Equal(t, "serve", serve.Name())
	run, _, err := first.Find([]string{"run"})
	require.NoError(t, err)
	assert.Equal(t, "run", run.Name())
}
