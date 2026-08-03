package workspace

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "linked worktree",
			makeMarker: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("gitdir: elsewhere"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			checkout := filepath.Join(root, test.name)
			if err := os.Mkdir(checkout, 0o755); err != nil {
				t.Fatal(err)
			}
			test.makeMarker(t, filepath.Join(checkout, ".git"))

			got, err := ResolveCheckout(root, test.name)
			if err != nil {
				t.Fatalf("ResolveCheckout() error = %v", err)
			}
			if got != checkout {
				t.Fatalf("ResolveCheckout() = %q, want %q", got, checkout)
			}
		})
	}
}

func TestResolveCheckoutRejectsInvalidName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"", ".", "..", "../outside", "nested/checkout", `nested\checkout`, filepath.Join(root, "absolute")} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if got, err := ResolveCheckout(root, name); err == nil {
				t.Fatalf("ResolveCheckout(%q) = %q, want error", name, got)
			}
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
				if err := os.WriteFile(checkout, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "checkout is symlink",
			wantError: "must not be a symlink",
			setup: func(t *testing.T, root, checkout string) {
				t.Helper()
				destination := filepath.Join(root, "destination")
				makeCheckoutDirectory(t, destination)
				if err := os.Symlink(destination, checkout); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "missing git marker",
			wantError: "has no .git marker",
			setup: func(t *testing.T, _ string, checkout string) {
				t.Helper()
				if err := os.Mkdir(checkout, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "git marker is symlink",
			wantError: "must not be a symlink",
			setup: func(t *testing.T, root, checkout string) {
				t.Helper()
				if err := os.Mkdir(checkout, 0o755); err != nil {
					t.Fatal(err)
				}
				destination := filepath.Join(root, "git-data")
				if err := os.Mkdir(destination, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, filepath.Join(checkout, ".git")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "git marker has unsupported kind",
			wantError: "must be a directory or regular file",
			setup: func(t *testing.T, _ string, checkout string) {
				t.Helper()
				if err := os.Mkdir(checkout, 0o755); err != nil {
					t.Fatal(err)
				}
				listener, err := net.Listen("unix", filepath.Join(checkout, ".git"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			checkout := filepath.Join(root, "chosen")
			test.setup(t, root, checkout)

			got, err := ResolveCheckout(root, "chosen")
			if err == nil {
				t.Fatalf("ResolveCheckout() = %q, want error", got)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ResolveCheckout() error = %q, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func makeCheckoutDirectory(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}
