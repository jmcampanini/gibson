package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocateFromCheckout(t *testing.T) {
	isolateGitEnvironment(t)
	workspaceRoot := t.TempDir()
	checkout := initRepository(t, filepath.Join(workspaceRoot, "checkout"))
	nested := filepath.Join(checkout, "one", "two")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	for _, test := range []struct {
		name     string
		startDir string
	}{
		{name: "root", startDir: checkout},
		{name: "nested directory", startDir: nested},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Locate(test.startDir)

			require.NoError(t, err)
			assert.Equal(t, checkout, got.LaunchCheckout)
			assert.Equal(t, workspaceRoot, got.Root)
		})
	}
}

func TestLocateFromLinkedWorktree(t *testing.T) {
	isolateGitEnvironment(t)
	tempRoot := t.TempDir()
	primary := initRepository(t, filepath.Join(tempRoot, "primary"))
	runGit(t, primary, "config", "user.name", "Gibson Tests")
	runGit(t, primary, "config", "user.email", "gibson-tests@example.invalid")
	runGit(t, primary, "commit", "--quiet", "--allow-empty", "-m", "initial")

	workspaceRoot := filepath.Join(tempRoot, "workspace")
	require.NoError(t, os.MkdirAll(workspaceRoot, 0o755))
	linkedCheckout := filepath.Join(workspaceRoot, "linked")
	runGit(t, primary, "worktree", "add", "--quiet", "--detach", linkedCheckout, "HEAD")

	nested := filepath.Join(linkedCheckout, "nested")
	require.NoError(t, os.Mkdir(nested, 0o755))

	for _, startDir := range []string{linkedCheckout, nested} {
		got, err := Locate(startDir)

		require.NoError(t, err, "Locate(%q)", startDir)
		assert.Equal(t, linkedCheckout, got.LaunchCheckout, "Locate(%q).LaunchCheckout", startDir)
		assert.Equal(t, workspaceRoot, got.Root, "Locate(%q).Root", startDir)
	}
}

func TestLocateOutsideCheckout(t *testing.T) {
	isolateGitEnvironment(t)
	outside := t.TempDir()

	got, err := Locate(outside)

	require.Error(t, err, "Locate() = %#v, want error", got)
	assert.ErrorContains(t, err, "no Git checkout found")
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
		t.Cleanup(func() {
			assert.NoError(t, os.Setenv(key, value), "restore %s", key)
		})
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}

func initRepository(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755))
	runGit(t, path, "init", "--quiet")
	return path
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s:\n%s", strings.Join(args, " "), output)
}
