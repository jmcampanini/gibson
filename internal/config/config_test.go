package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFullConfig(t *testing.T) {
	root := writeTestConfig(t, `
[server]
port = 7311
bind = "0.0.0.0"
pi_bin = "/opt/pi/bin/pi"

[sessions.review]
description = "Adversarial code review"
model = "anthropic/claude-opus-5"
thinking = "high"
extra_args = ["-e", "/opt/pi/review.ts"]
`)

	cfg, err := Load(root)
	require.NoError(t, err)

	assert.Equal(t, Server{
		Port:  7311,
		Bind:  "0.0.0.0",
		PiBin: "/opt/pi/bin/pi",
	}, cfg.Server)
	assert.Equal(t, map[string]SessionType{
		"review": {
			Description: "Adversarial code review",
			Model:       "anthropic/claude-opus-5",
			Thinking:    "high",
			ExtraArgs:   []string{"-e", "/opt/pi/review.ts"},
		},
	}, cfg.Sessions)
}

func TestLoadMinimalConfigAppliesDefaults(t *testing.T) {
	root := writeTestConfig(t, "[server]\nport = 7311\n")

	cfg, err := Load(root)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.Server.Bind)
	assert.Empty(t, cfg.Server.PiBin)
	assert.NotNil(t, cfg.Sessions)
	assert.Empty(t, cfg.Sessions)
}

func TestLoadTreatsEmptyBindAsLoopbackDefault(t *testing.T) {
	root := writeTestConfig(t, "[server]\nport = 7311\nbind = \"\"\n")

	cfg, err := Load(root)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.Server.Bind)
}

func TestLoadRequiresConfigAtCheckoutRoot(t *testing.T) {
	workspace := t.TempDir()
	checkout := filepath.Join(workspace, "main")
	require.NoError(t, os.Mkdir(checkout, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "gibson.toml"),
		[]byte("[server]\nport = 7311\n"),
		0o644,
	))

	_, err := Load(checkout)
	require.Error(t, err)
	assert.Equal(t, "gibson.toml not found at "+filepath.Join(checkout, "gibson.toml"), err.Error())
}

func TestLoadReportsMalformedConfig(t *testing.T) {
	root := writeTestConfig(t, "[server]\nport =\n")

	_, err := Load(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gibson.toml:")
	assert.Contains(t, err.Error(), "line")
	assert.NotContains(t, err.Error(), "not found")
}

func TestLoadValidatesServerPort(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		message string
	}{
		{
			name:    "missing",
			config:  "[server]\n",
			message: "gibson.toml: server.port is required",
		},
		{
			name:    "zero",
			config:  "[server]\nport = 0\n",
			message: "gibson.toml: server.port must be 1-65535, got 0",
		},
		{
			name:    "negative",
			config:  "[server]\nport = -1\n",
			message: "gibson.toml: server.port must be 1-65535, got -1",
		},
		{
			name:    "too large",
			config:  "[server]\nport = 65536\n",
			message: "gibson.toml: server.port must be 1-65535, got 65536",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTestConfig(t, tt.config)

			_, err := Load(root)
			require.EqualError(t, err, tt.message)
		})
	}
}

func TestLoadValidatesSessionDescriptionsInKeyOrder(t *testing.T) {
	root := writeTestConfig(t, `
[server]
port = 7311

[sessions.zulu]
model = "some/model"

[sessions.alpha]
thinking = "custom"
`)

	_, err := Load(root)
	require.EqualError(t, err, "gibson.toml: sessions.alpha.description is required")
}

func TestLoadPreservesExtraArgsOpaque(t *testing.T) {
	extraArgs := []string{
		"-e",
		"~/.pi/agent/extensions/review.ts",
		"--provider-option=value with spaces",
		"--",
		"",
	}
	root := writeTestConfig(t, `
[server]
port = 7311

[sessions.opaque]
description = "Opaque arguments"
extra_args = [
  "-e",
  "~/.pi/agent/extensions/review.ts",
  "--provider-option=value with spaces",
  "--",
  "",
]
`)

	cfg, err := Load(root)
	require.NoError(t, err)
	assert.Equal(t, extraArgs, cfg.Sessions["opaque"].ExtraArgs)
}

func writeTestConfig(t *testing.T, contents string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gibson.toml"), []byte(contents), 0o644))
	return root
}
