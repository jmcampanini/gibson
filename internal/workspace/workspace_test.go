package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocateFromCheckout(t *testing.T) {
	isolateGitEnvironment(t)
	workspaceRoot := t.TempDir()
	checkout := initRepository(t, filepath.Join(workspaceRoot, "checkout"))
	nested := filepath.Join(checkout, "one", "two")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		startDir string
	}{
		{name: "root", startDir: checkout},
		{name: "nested directory", startDir: nested},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Locate(test.startDir)
			if err != nil {
				t.Fatalf("Locate() error = %v", err)
			}
			if got.LaunchCheckout != checkout {
				t.Errorf("LaunchCheckout = %q, want %q", got.LaunchCheckout, checkout)
			}
			if got.Root != workspaceRoot {
				t.Errorf("Root = %q, want %q", got.Root, workspaceRoot)
			}
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
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedCheckout := filepath.Join(workspaceRoot, "linked")
	runGit(t, primary, "worktree", "add", "--quiet", "--detach", linkedCheckout, "HEAD")

	nested := filepath.Join(linkedCheckout, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, startDir := range []string{linkedCheckout, nested} {
		got, err := Locate(startDir)
		if err != nil {
			t.Fatalf("Locate(%q) error = %v", startDir, err)
		}
		if got.LaunchCheckout != linkedCheckout {
			t.Errorf("Locate(%q).LaunchCheckout = %q, want %q", startDir, got.LaunchCheckout, linkedCheckout)
		}
		if got.Root != workspaceRoot {
			t.Errorf("Locate(%q).Root = %q, want %q", startDir, got.Root, workspaceRoot)
		}
	}
}

func TestLocateOutsideCheckout(t *testing.T) {
	isolateGitEnvironment(t)
	outside := t.TempDir()

	got, err := Locate(outside)
	if err == nil {
		t.Fatalf("Locate() = %#v, want error", got)
	}
	if !strings.Contains(err.Error(), "no Git checkout found") {
		t.Fatalf("Locate() error = %q, want a clear non-checkout error", err)
	}
}

func isolateGitEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "GIT_") {
			continue
		}
		value := os.Getenv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Setenv(key, value); err != nil {
				t.Errorf("restore %s: %v", key, err)
			}
		})
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}

func initRepository(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "init", "--quiet")
	return path
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
