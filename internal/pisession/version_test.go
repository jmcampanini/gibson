package pisession

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePiBin(t *testing.T) {
	t.Run("default pi on PATH", func(t *testing.T) {
		dir := t.TempDir()
		pi := writeExecutable(t, dir, "pi", "echo 0.82.0")
		t.Setenv("PATH", dir)

		found, err := ResolvePiBin("")

		require.NoError(t, err)
		assert.Equal(t, pi, found)
	})

	t.Run("configured command on PATH", func(t *testing.T) {
		dir := t.TempDir()
		pi := writeExecutable(t, dir, "project-pi", "echo 0.82.0")
		t.Setenv("PATH", dir)

		found, err := ResolvePiBin("project-pi")

		require.NoError(t, err)
		assert.Equal(t, pi, found)
	})

	t.Run("configured relative path becomes absolute", func(t *testing.T) {
		dir := t.TempDir()
		pi := writeExecutable(t, dir, "project-pi", "echo 0.82.0")
		t.Chdir(dir)

		found, err := ResolvePiBin("./project-pi")

		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(found), "ResolvePiBin() = %q, want an absolute path", found)
		assert.Equal(t, pi, found)
	})

	t.Run("empty PATH", func(t *testing.T) {
		t.Setenv("PATH", "")

		_, err := ResolvePiBin("")

		require.ErrorIs(t, err, ErrPiNotFound)
		for _, want := range []string{"PATH", "install pi", "configure pi_bin"} {
			assert.ErrorContains(t, err, want)
		}
	})

	t.Run("missing configured value", func(t *testing.T) {
		t.Setenv("PATH", "")

		_, err := ResolvePiBin("missing-pi")

		require.ErrorIs(t, err, ErrPiNotFound)
		for _, want := range []string{"configured pi_bin", "missing-pi", "executable path or command"} {
			assert.ErrorContains(t, err, want)
		}
	})

	t.Run("configured tilde is not expanded", func(t *testing.T) {
		home := t.TempDir()
		writeExecutable(t, home, "pi", "echo 0.82.0")
		t.Setenv("HOME", home)
		t.Setenv("PATH", "")

		_, err := ResolvePiBin("~/pi")

		require.ErrorIs(t, err, ErrPiNotFound)
	})
}

func TestCheckPiVersion(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		wantFound    string
		wantVerified bool
		wantError    error
		wantMessage  []string
	}{
		{
			name:         "minimum current version",
			output:       "0.82.0",
			wantFound:    "0.82.0",
			wantVerified: true,
		},
		{
			name:         "decorated current version",
			output:       "pi version v0.82.17 (release)",
			wantFound:    "0.82.17",
			wantVerified: true,
		},
		{
			name:         "newer minor is accepted as unverified",
			output:       "pi 0.83.0",
			wantFound:    "0.83.0",
			wantVerified: false,
		},
		{
			name:         "newer major is accepted as unverified",
			output:       "1.0.0",
			wantFound:    "1.0.0",
			wantVerified: false,
		},
		{
			name:        "too old",
			output:      "0.81.9",
			wantError:   ErrPiVersionTooOld,
			wantMessage: []string{"0.81.9", MinimumPiVersion},
		},
		{
			name:        "minimum prerelease is too old",
			output:      "0.82.0-rc.1",
			wantError:   ErrPiVersionTooOld,
			wantMessage: []string{"0.82.0-rc.1", MinimumPiVersion},
		},
		{
			name:        "unparseable output",
			output:      "pi development build",
			wantError:   ErrPiVersionUnparseable,
			wantMessage: []string{"semantic version", "pi development build"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			pi := writeExecutable(t, dir, "pi", "printf '%s\\n' "+shellQuote(test.output))

			result, err := CheckPiVersion(context.Background(), pi)

			if test.wantError != nil {
				require.ErrorIs(t, err, test.wantError)
				for _, want := range test.wantMessage {
					assert.ErrorContains(t, err, want)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantFound, result.Found)
			assert.Equal(t, test.wantVerified, result.Verified)
		})
	}
}

func TestCheckPiVersionParsesStdoutOnly(t *testing.T) {
	dir := t.TempDir()
	pi := writeExecutable(t, dir, "pi", "echo 'warning: v9.9.9 wrapper is deprecated' >&2\necho 0.82.1")

	result, err := CheckPiVersion(context.Background(), pi)

	require.NoError(t, err)
	assert.Equal(t, "0.82.1", result.Found)
	assert.True(t, result.Verified)
}

func TestCheckPiVersionReportsCommandFailure(t *testing.T) {
	dir := t.TempDir()
	pi := writeExecutable(t, dir, "pi", "echo unavailable >&2\nexit 7")

	_, err := CheckPiVersion(context.Background(), pi)

	require.ErrorIs(t, err, ErrPiVersionExecution)
	assert.ErrorContains(t, err, "unavailable")
}

func TestCheckPiVersionReportsTimeout(t *testing.T) {
	dir := t.TempDir()
	pi := writeExecutable(t, dir, "pi", "exec sleep 10")

	_, err := checkPiVersion(context.Background(), pi, 20*time.Millisecond)

	require.ErrorIs(t, err, ErrPiVersionTimeout)
}

func TestCheckPiVersionReportsMissingExecutableAsExecutionFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-pi")

	_, err := CheckPiVersion(context.Background(), missing)

	require.ErrorIs(t, err, ErrPiVersionExecution)
}

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	contents := "#!/bin/sh\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o755))
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
