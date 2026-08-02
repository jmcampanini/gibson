package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolveCheckout(workspaceRoot, name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("resolve checkout: %q is not a valid checkout name; expected a nonempty directory basename", name)
	}

	checkout := filepath.Join(workspaceRoot, name)
	info, err := os.Lstat(checkout)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("resolve checkout %q: checkout directory %q does not exist", name, checkout)
		}
		return "", fmt.Errorf("resolve checkout %q: inspect checkout directory %q: %w", name, checkout, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("resolve checkout %q: checkout directory %q must not be a symlink", name, checkout)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("resolve checkout %q: checkout path %q is not a directory", name, checkout)
	}

	gitMarker := filepath.Join(checkout, ".git")
	info, err = os.Lstat(gitMarker)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("resolve checkout %q: %q has no .git marker", name, checkout)
		}
		return "", fmt.Errorf("resolve checkout %q: inspect .git marker %q: %w", name, gitMarker, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("resolve checkout %q: .git marker %q must not be a symlink", name, gitMarker)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", fmt.Errorf("resolve checkout %q: .git marker %q must be a directory or regular file", name, gitMarker)
	}

	return checkout, nil
}
