package pisession

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildArgv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		model     string
		thinking  string
		extraArgs []string
		want      []string
	}{
		{
			name: "required arguments only",
			want: []string{
				"/opt/pi", "--mode", "rpc", "--session-id", "s-20260728-abc123",
				"--session-dir", "/checkout/.gibson/sessions",
			},
		},
		{
			name:  "model",
			model: "anthropic/claude-opus-5",
			want: []string{
				"/opt/pi", "--mode", "rpc", "--session-id", "s-20260728-abc123",
				"--session-dir", "/checkout/.gibson/sessions",
				"--model", "anthropic/claude-opus-5",
			},
		},
		{
			name:     "thinking",
			thinking: "high",
			want: []string{
				"/opt/pi", "--mode", "rpc", "--session-id", "s-20260728-abc123",
				"--session-dir", "/checkout/.gibson/sessions",
				"--thinking", "high",
			},
		},
		{
			name:     "model then thinking",
			model:    "openai/gpt-5.6",
			thinking: "xhigh",
			want: []string{
				"/opt/pi", "--mode", "rpc", "--session-id", "s-20260728-abc123",
				"--session-dir", "/checkout/.gibson/sessions",
				"--model", "openai/gpt-5.6", "--thinking", "xhigh",
			},
		},
		{
			name:      "opaque extras remain last",
			model:     " ",
			thinking:  "custom value",
			extraArgs: []string{"-e", "~/review.ts", "--provider-option=value with spaces", "--", ""},
			want: []string{
				"/opt/pi", "--mode", "rpc", "--session-id", "s-20260728-abc123",
				"--session-dir", "/checkout/.gibson/sessions",
				"--model", " ", "--thinking", "custom value",
				"-e", "~/review.ts", "--provider-option=value with spaces", "--", "",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := buildArgv(
				"/opt/pi",
				"s-20260728-abc123",
				"/checkout/.gibson/sessions",
				test.model,
				test.thinking,
				test.extraArgs,
			)
			assert.Equal(t, test.want, got)
		})
	}
}
