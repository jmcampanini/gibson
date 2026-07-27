package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmcampanini/gibson/internal/testws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckIgnored(t *testing.T) {
	isolateGitEnvironment(t)

	t.Run("matches trailing slash while directory is absent", func(t *testing.T) {
		ws := testws.New(t)

		ignored, err := CheckIgnored(ws.Checkout)

		require.NoError(t, err)
		assert.True(t, ignored)
		_, err = os.Stat(filepath.Join(ws.Checkout, ".gibson"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("missing ignore is false", func(t *testing.T) {
		ws := testws.New(t)
		require.NoError(t, os.WriteFile(filepath.Join(ws.Checkout, ".gitignore"), nil, 0o644))

		ignored, err := CheckIgnored(ws.Checkout)

		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("global exclude does not replace committed coverage", func(t *testing.T) {
		ws := testws.New(t)
		require.NoError(t, os.WriteFile(filepath.Join(ws.Checkout, ".gitignore"), nil, 0o644))
		excludes := filepath.Join(t.TempDir(), "global-excludes")
		require.NoError(t, os.WriteFile(excludes, []byte(".gibson/\n"), 0o644))
		globalConfig := filepath.Join(t.TempDir(), "gitconfig")
		runGit(t, "", "config", "--file", globalConfig, "core.excludesFile", excludes)
		t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)

		ignored, err := CheckIgnored(ws.Checkout)

		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("globally configured nested gitignore does not count", func(t *testing.T) {
		ws := testws.New(t)
		nestedDir := filepath.Join(ws.Checkout, "config")
		require.NoError(t, os.Mkdir(nestedDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(ws.Checkout, ".gitignore"), nil, 0o644))
		nestedIgnore := filepath.Join(nestedDir, ".gitignore")
		require.NoError(t, os.WriteFile(nestedIgnore, []byte(".gibson/\n"), 0o644))
		runGit(t, ws.Checkout, "add", ".gitignore", "config/.gitignore")
		runGit(t, ws.Checkout, "-c", "user.name=Gibson Tests", "-c", "user.email=gibson@example.invalid", "commit", "-m", "move ignore")
		globalConfig := filepath.Join(t.TempDir(), "gitconfig")
		runGit(t, "", "config", "--file", globalConfig, "core.excludesFile", nestedIgnore)
		t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)

		ignored, err := CheckIgnored(ws.Checkout)

		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("uncommitted ignore rule does not count", func(t *testing.T) {
		ws := testws.New(t)
		require.NoError(t, os.WriteFile(filepath.Join(ws.Checkout, ".gitignore"), nil, 0o644))
		runGit(t, ws.Checkout, "add", ".gitignore")
		runGit(t, ws.Checkout, "-c", "user.name=Gibson Tests", "-c", "user.email=gibson@example.invalid", "commit", "-m", "remove ignore")
		require.NoError(t, os.WriteFile(filepath.Join(ws.Checkout, ".gitignore"), []byte(".gibson/\n"), 0o644))

		ignored, err := CheckIgnored(ws.Checkout)

		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("matches existing directory", func(t *testing.T) {
		ws := testws.New(t)
		require.NoError(t, os.Mkdir(filepath.Join(ws.Checkout, ".gibson"), 0o755))

		ignored, err := CheckIgnored(ws.Checkout)

		require.NoError(t, err)
		assert.True(t, ignored)
	})

	t.Run("non repository is an error", func(t *testing.T) {
		root := t.TempDir()

		ignored, err := CheckIgnored(root)

		assert.False(t, ignored)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "check whether .gibson/ is ignored")
		assert.Contains(t, err.Error(), root)
	})
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	commandArgs := args
	if dir != "" {
		commandArgs = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", commandArgs...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, strings.TrimSpace(string(output)))
}

func isolateGitEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "GIT_") {
			continue
		}
		value := os.Getenv(key)
		require.NoError(t, os.Unsetenv(key))
		t.Cleanup(func() { require.NoError(t, os.Setenv(key, value)) })
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}
