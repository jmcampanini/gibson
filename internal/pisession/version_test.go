package pisession

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolvePiBin(t *testing.T) {
	t.Run("default pi on PATH", func(t *testing.T) {
		dir := t.TempDir()
		pi := writeExecutable(t, dir, "pi", "echo 0.82.0")
		t.Setenv("PATH", dir)

		found, err := ResolvePiBin("")
		if err != nil {
			t.Fatalf("ResolvePiBin() error = %v", err)
		}
		if found != pi {
			t.Fatalf("ResolvePiBin() = %q, want %q", found, pi)
		}
	})

	t.Run("configured command on PATH", func(t *testing.T) {
		dir := t.TempDir()
		pi := writeExecutable(t, dir, "project-pi", "echo 0.82.0")
		t.Setenv("PATH", dir)

		found, err := ResolvePiBin("project-pi")
		if err != nil {
			t.Fatalf("ResolvePiBin() error = %v", err)
		}
		if found != pi {
			t.Fatalf("ResolvePiBin() = %q, want %q", found, pi)
		}
	})

	t.Run("empty PATH", func(t *testing.T) {
		t.Setenv("PATH", "")

		_, err := ResolvePiBin("")
		if !errors.Is(err, ErrPiNotFound) {
			t.Fatalf("ResolvePiBin() error = %v, want ErrPiNotFound", err)
		}
		for _, want := range []string{"PATH", "install pi", "configure pi_bin"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("ResolvePiBin() error = %q, want it to contain %q", err, want)
			}
		}
	})

	t.Run("missing configured value", func(t *testing.T) {
		t.Setenv("PATH", "")

		_, err := ResolvePiBin("missing-pi")
		if !errors.Is(err, ErrPiNotFound) {
			t.Fatalf("ResolvePiBin() error = %v, want ErrPiNotFound", err)
		}
		for _, want := range []string{"configured pi_bin", "missing-pi", "executable path or command"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("ResolvePiBin() error = %q, want it to contain %q", err, want)
			}
		}
	})

	t.Run("configured tilde is not expanded", func(t *testing.T) {
		home := t.TempDir()
		writeExecutable(t, home, "pi", "echo 0.82.0")
		t.Setenv("HOME", home)
		t.Setenv("PATH", "")

		_, err := ResolvePiBin("~/pi")
		if !errors.Is(err, ErrPiNotFound) {
			t.Fatalf("ResolvePiBin() error = %v, want ErrPiNotFound", err)
		}
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
				if !errors.Is(err, test.wantError) {
					t.Fatalf("CheckPiVersion() error = %v, want %v", err, test.wantError)
				}
				for _, want := range test.wantMessage {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("CheckPiVersion() error = %q, want it to contain %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckPiVersion() error = %v", err)
			}
			if result.Found != test.wantFound || result.Verified != test.wantVerified {
				t.Errorf("CheckPiVersion() = %+v, want Found %q, Verified %t", result, test.wantFound, test.wantVerified)
			}
		})
	}
}

func TestCheckPiVersionReportsCommandFailure(t *testing.T) {
	dir := t.TempDir()
	pi := writeExecutable(t, dir, "pi", "echo unavailable >&2\nexit 7")

	_, err := CheckPiVersion(context.Background(), pi)
	if !errors.Is(err, ErrPiVersionExecution) {
		t.Fatalf("CheckPiVersion() error = %v, want ErrPiVersionExecution", err)
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("CheckPiVersion() error = %q, want command output", err)
	}
}

func TestCheckPiVersionReportsTimeout(t *testing.T) {
	dir := t.TempDir()
	pi := writeExecutable(t, dir, "pi", "exec sleep 10")

	_, err := checkPiVersion(context.Background(), pi, 20*time.Millisecond)
	if !errors.Is(err, ErrPiVersionTimeout) {
		t.Fatalf("CheckPiVersion() error = %v, want ErrPiVersionTimeout", err)
	}
}

func TestCheckPiVersionReportsMissingExecutableAsExecutionFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-pi")

	_, err := CheckPiVersion(context.Background(), missing)
	if !errors.Is(err, ErrPiVersionExecution) {
		t.Fatalf("CheckPiVersion() error = %v, want ErrPiVersionExecution", err)
	}
}

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	contents := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
