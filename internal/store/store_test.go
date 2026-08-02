package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureLayoutKeepsArtifactsUnderCheckout(t *testing.T) {
	checkout := t.TempDir()
	storage := Open(checkout)

	require.NoError(t, storage.EnsureLayout())

	require.DirExists(t, filepath.Join(checkout, ".gibson", "sessions"))
	require.DirExists(t, filepath.Join(checkout, ".gibson", "logs"))

	assert.Equal(t, filepath.Join(checkout, ".gibson", "sessions"), storage.SessionsDir())
	assert.Equal(t, filepath.Join(checkout, ".gibson", "state.json"), storage.RegistryPath())
	assert.Equal(
		t,
		filepath.Join(checkout, ".gibson", "logs", "s-20260726-abc123.stderr.log"),
		storage.StderrLogPath("s-20260726-abc123"),
	)
}
