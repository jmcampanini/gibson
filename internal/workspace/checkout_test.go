package workspace

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCheckoutAcceptsRepositoryAndLinkedWorktree(t *testing.T) {
	for _, test := range []struct {
		name       string
		makeMarker func(*testing.T, string)
	}{
		{
			name: "repository",
			makeMarker: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.Mkdir(path, 0o755))
			},
		},
		{
			name: "linked worktree",
			makeMarker: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.WriteFile(path, []byte("gitdir: elsewhere"), 0o644))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			checkout := filepath.Join(root, test.name)
			require.NoError(t, os.Mkdir(checkout, 0o755))
			test.makeMarker(t, filepath.Join(checkout, ".git"))

			got, err := ResolveCheckout(root, test.name)

			require.NoError(t, err)
			assert.Equal(t, checkout, got)
		})
	}
}

func TestResolveCheckoutRejectsInvalidName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"", ".", "..", "../outside", "nested/checkout", `nested\checkout`, filepath.Join(root, "absolute")} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			got, err := ResolveCheckout(root, name)

			require.Error(t, err, "ResolveCheckout(%q) = %q, want error", name, got)
		})
	}
}

func TestResolveCheckoutRejectsInvalidFilesystemEntries(t *testing.T) {
	for _, test := range []struct {
		name      string
		wantError string
		setup     func(*testing.T, string, string)
	}{
		{
			name:      "missing checkout",
			wantError: "does not exist",
			setup:     func(*testing.T, string, string) {},
		},
		{
			name:      "checkout is file",
			wantError: "not a directory",
			setup: func(t *testing.T, _ string, checkout string) {
				t.Helper()
				require.NoError(t, os.WriteFile(checkout, nil, 0o644))
			},
		},
		{
			name:      "checkout is symlink",
			wantError: "must not be a symlink",
			setup: func(t *testing.T, root, checkout string) {
				t.Helper()
				destination := filepath.Join(root, "destination")
				makeCheckoutDirectory(t, destination)
				require.NoError(t, os.Symlink(destination, checkout))
			},
		},
		{
			name:      "missing git marker",
			wantError: "has no .git marker",
			setup: func(t *testing.T, _ string, checkout string) {
				t.Helper()
				require.NoError(t, os.Mkdir(checkout, 0o755))
			},
		},
		{
			name:      "git marker is symlink",
			wantError: "must not be a symlink",
			setup: func(t *testing.T, root, checkout string) {
				t.Helper()
				require.NoError(t, os.Mkdir(checkout, 0o755))
				destination := filepath.Join(root, "git-data")
				require.NoError(t, os.Mkdir(destination, 0o755))
				require.NoError(t, os.Symlink(destination, filepath.Join(checkout, ".git")))
			},
		},
		{
			name:      "git marker has unsupported kind",
			wantError: "must be a directory or regular file",
			setup: func(t *testing.T, _ string, checkout string) {
				t.Helper()
				listener, err := func() (net.Listener, error) {
					require.NoError(t, os.Mkdir(checkout, 0o755))
					return net.Listen("unix", filepath.Join(checkout, ".git"))
				}()
				require.NoError(t, err)
				t.Cleanup(func() { _ = listener.Close() })
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			checkout := filepath.Join(root, "chosen")
			test.setup(t, root, checkout)

			got, err := ResolveCheckout(root, "chosen")

			require.Error(t, err, "ResolveCheckout() = %q, want error", got)
			assert.ErrorContains(t, err, test.wantError)
		})
	}
}

func makeCheckoutDirectory(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.Mkdir(path, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(path, ".git"), 0o755))
}
