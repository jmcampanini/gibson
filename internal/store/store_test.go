package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureLayoutKeepsArtifactsUnderCheckout(t *testing.T) {
	checkout := t.TempDir()
	storage := mustOpen(t, checkout)

	require.NoError(t, storage.EnsureLayout())

	require.DirExists(t, filepath.Join(checkout, ".gibson", "sessions"))
	require.DirExists(t, filepath.Join(checkout, ".gibson", "logs"))
	for _, path := range []string{
		filepath.Join(checkout, ".gibson"),
		filepath.Join(checkout, ".gibson", "sessions"),
		filepath.Join(checkout, ".gibson", "logs"),
	} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	}

	assert.Equal(t, filepath.Join(checkout, ".gibson", "sessions"), storage.SessionsDir())
	assert.Equal(t, filepath.Join(checkout, ".gibson", "state.json"), storage.registryPath())
	assert.Equal(
		t,
		filepath.Join(checkout, ".gibson", "logs", "s-20260726-abc123.stderr.log"),
		storage.StderrLogPath("s-20260726-abc123"),
	)
}

func TestEnsureLayoutNormalizesExistingDirectoryModes(t *testing.T) {
	checkout := t.TempDir()
	storage := mustOpen(t, checkout)
	require.NoError(t, storage.EnsureLayout())
	for _, path := range []string{storage.gibsonDir(), storage.SessionsDir(), storage.logsDir()} {
		require.NoError(t, os.Chmod(path, 0o700))
	}

	require.NoError(t, storage.EnsureLayout())

	for _, path := range []string{storage.gibsonDir(), storage.SessionsDir(), storage.logsDir()} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	}
}

func TestEnsureLayoutRejectsSymlinkedStorage(t *testing.T) {
	checkout := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(checkout, ".gibson")))

	err := mustOpen(t, checkout).EnsureLayout()

	require.Error(t, err)
	assert.ErrorContains(t, err, "symbolic links are not allowed")
}
