package testws

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const defaultConfig = `[server]
port = 7311

[sessions.test]
description = "Test session"
`

type WS struct {
	Root     string
	Checkout string
}

func New(t testing.TB) *WS {
	t.Helper()
	isolateGitEnvironment(t)

	root := t.TempDir()
	checkout := filepath.Join(root, "main")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatalf("create test checkout: %v", err)
	}

	ws := &WS{Root: root, Checkout: checkout}
	ws.runGit(t, "init", "-b", "main")
	ws.WriteConfig(t, defaultConfig)
	if err := os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte(".gibson/\n"), 0o644); err != nil {
		t.Fatalf("write test .gitignore: %v", err)
	}
	ws.runGit(t, "add", "gibson.toml", ".gitignore")
	ws.runGit(t, "-c", "user.name=Gibson Tests", "-c", "user.email=gibson@example.invalid", "commit", "-m", "initial test workspace")
	return ws
}

func (ws *WS) WriteConfig(t testing.TB, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws.Checkout, "gibson.toml"), []byte(source), 0o644); err != nil {
		t.Fatalf("write test gibson.toml: %v", err)
	}
}

func (ws *WS) runGit(t testing.TB, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", ws.Checkout}, args...)...)
	cmd.Env = isolatedGitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

func isolateGitEnvironment(t testing.TB) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "GIT_") {
			continue
		}
		value, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if existed {
				if err := os.Setenv(key, value); err != nil {
					t.Errorf("restore %s: %v", key, err)
				}
				return
			}
			if err := os.Unsetenv(key); err != nil {
				t.Errorf("restore %s: %v", key, err)
			}
		})
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}

func isolatedGitEnv() []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
	)
}
