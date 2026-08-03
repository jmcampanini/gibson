package testws

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/jmcampanini/gibson/internal/config"
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

type Option func(*options)

type options struct {
	piBin            string
	configCustomized bool
	sessionTypes     map[string]config.SessionType
	siblingCheckouts map[string]struct{}
}

type encodedConfig struct {
	Server   encodedServer                 `toml:"server"`
	Sessions map[string]encodedSessionType `toml:"sessions"`
}

type encodedServer struct {
	Port  int    `toml:"port"`
	PiBin string `toml:"pi_bin,omitempty"`
}

type encodedSessionType struct {
	Description string   `toml:"description"`
	Model       string   `toml:"model,omitempty"`
	Thinking    string   `toml:"thinking,omitempty"`
	ExtraArgs   []string `toml:"extra_args,omitempty"`
}

func WithPiBin(path string) Option {
	return func(opts *options) {
		opts.piBin = path
		opts.configCustomized = true
	}
}

func WithSessionType(name string, cfg config.SessionType) Option {
	return func(opts *options) {
		opts.sessionTypes[name] = cfg
		opts.configCustomized = true
	}
}

func WithSiblingCheckout(name string) Option {
	return func(opts *options) {
		opts.siblingCheckouts[name] = struct{}{}
	}
}

func New(t testing.TB, newOptions ...Option) *WS {
	t.Helper()
	isolateGitEnvironment(t)

	opts := options{
		sessionTypes: map[string]config.SessionType{
			"test": {Description: "Test session"},
		},
		siblingCheckouts: make(map[string]struct{}),
	}
	for _, apply := range newOptions {
		apply(&opts)
	}

	root := t.TempDir()
	checkout := filepath.Join(root, "main")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatalf("create test checkout: %v", err)
	}

	ws := &WS{Root: root, Checkout: checkout}
	ws.runGit(t, "init", "-b", "main")
	configSource := defaultConfig
	if opts.configCustomized {
		configSource = encodeConfig(t, opts)
	}
	ws.WriteConfig(t, configSource)
	if err := os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte(".gibson/\n"), 0o644); err != nil {
		t.Fatalf("write test .gitignore: %v", err)
	}
	ws.runGit(t, "add", "gibson.toml", ".gitignore")
	ws.runGit(t, "-c", "user.name=Gibson Tests", "-c", "user.email=gibson@example.invalid", "commit", "-m", "initial test workspace")

	siblings := make([]string, 0, len(opts.siblingCheckouts))
	for name := range opts.siblingCheckouts {
		siblings = append(siblings, name)
	}
	sort.Strings(siblings)
	for _, name := range siblings {
		ws.runGit(t, "worktree", "add", filepath.Join(root, name))
	}
	return ws
}

func encodeConfig(t testing.TB, opts options) string {
	t.Helper()
	sessions := make(map[string]encodedSessionType, len(opts.sessionTypes))
	for name, sessionType := range opts.sessionTypes {
		sessions[name] = encodedSessionType{
			Description: sessionType.Description,
			Model:       sessionType.Model,
			Thinking:    sessionType.Thinking,
			ExtraArgs:   sessionType.ExtraArgs,
		}
	}
	cfg := encodedConfig{
		Server:   encodedServer{Port: 7311, PiBin: opts.piBin},
		Sessions: sessions,
	}
	var source bytes.Buffer
	if err := toml.NewEncoder(&source).Encode(cfg); err != nil {
		t.Fatalf("encode test gibson.toml: %v", err)
	}
	return source.String()
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
