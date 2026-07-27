package workspace

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Workspace struct {
	Root           string
	LaunchCheckout string
}

func Locate(startDir string) (*Workspace, error) {
	cmd := exec.Command("git", "-C", startDir, "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			detail := strings.TrimSpace(string(exitErr.Stderr))
			if detail == "" {
				detail = exitErr.Error()
			}
			return nil, fmt.Errorf("no Git checkout found from %q: %s", startDir, detail)
		}
		return nil, fmt.Errorf("locate Git checkout from %q: run git rev-parse --show-toplevel: %w", startDir, err)
	}

	launchCheckout := filepath.Clean(strings.TrimSpace(string(output)))
	if launchCheckout == "." {
		return nil, fmt.Errorf("locate Git checkout from %q: git rev-parse --show-toplevel returned an empty path", startDir)
	}

	return &Workspace{
		Root:           filepath.Dir(launchCheckout),
		LaunchCheckout: launchCheckout,
	}, nil
}
