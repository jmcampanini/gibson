package testws

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmcampanini/gibson/internal/config"
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
	assert.Equal(t, defaultConfig, readFile(t, filepath.Join(ws.Checkout, "gibson.toml")))

	cmd := exec.Command("git", "-C", ws.Checkout, "status", "--porcelain")
	cmd.Env = isolatedGitEnv()
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, strings.TrimSpace(string(output)))
	assert.Empty(t, output)
}

func TestNewOptionsComposeIndependentOfOrder(t *testing.T) {
	sessionType := config.SessionType{
		Description: "Adversarial code review",
		Model:       "anthropic/claude-opus-5",
		Thinking:    "high",
		ExtraArgs:   []string{"-e", "/opt/pi/review.ts", "--flag=value with spaces"},
	}
	const piBin = "/opt/pi/bin/pi"

	tests := []struct {
		name    string
		options []Option
	}{
		{
			name: "sibling first",
			options: []Option{
				WithSiblingCheckout("review"),
				WithPiBin(piBin),
				WithSessionType("review", sessionType),
			},
		},
		{
			name: "sibling last",
			options: []Option{
				WithSessionType("review", sessionType),
				WithPiBin(piBin),
				WithSiblingCheckout("review"),
			},
		},
	}

	var sources []string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := New(t, tt.options...)
			sibling := filepath.Join(ws.Root, "review")
			source := readFile(t, filepath.Join(ws.Checkout, "gibson.toml"))
			sources = append(sources, source)

			want := config.Config{
				Server: config.Server{Port: 7311, Bind: "127.0.0.1", PiBin: piBin},
				Sessions: map[string]config.SessionType{
					"test":   {Description: "Test session"},
					"review": sessionType,
				},
			}
			for _, checkout := range []string{ws.Checkout, sibling} {
				got, err := config.Load(checkout)
				require.NoError(t, err)
				assert.Equal(t, want, got)
				assert.Equal(t, ".gibson/\n", readFile(t, filepath.Join(checkout, ".gitignore")))
			}

			gitFile, err := os.Stat(filepath.Join(sibling, ".git"))
			require.NoError(t, err)
			assert.True(t, gitFile.Mode().IsRegular())
			assert.Equal(t, source, gitOutput(t, sibling, "show", "HEAD:gibson.toml"))
			assert.Empty(t, gitOutput(t, ws.Checkout, "status", "--porcelain"))
		})
	}
	require.Len(t, sources, 2)
	assert.Equal(t, sources[0], sources[1])
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

func gitOutput(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = isolatedGitEnv()
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, strings.TrimSpace(string(output)))
	return string(output)
}
