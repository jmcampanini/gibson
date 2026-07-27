package testws

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCreatesCommittedGroveWorkspace(t *testing.T) {
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "ambient-git-dir"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "ambient-gitconfig"))

	ws := New(t)

	assert.Equal(t, ws.Root, filepath.Dir(ws.Checkout))
	assert.Equal(t, "main", filepath.Base(ws.Checkout))
	assert.Equal(t, ".gibson/\n", readFile(t, filepath.Join(ws.Checkout, ".gitignore")))
	assert.Contains(t, readFile(t, filepath.Join(ws.Checkout, "gibson.toml")), "[sessions.test]")

	cmd := exec.Command("git", "-C", ws.Checkout, "status", "--porcelain")
	cmd.Env = isolatedGitEnv()
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, strings.TrimSpace(string(output)))
	assert.Empty(t, output)
}

func TestWriteConfig(t *testing.T) {
	ws := New(t)
	const source = "[server]\nport = 8123\n"

	ws.WriteConfig(t, source)

	assert.Equal(t, source, readFile(t, filepath.Join(ws.Checkout, "gibson.toml")))
}

func readFile(t testing.TB, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(contents)
}
