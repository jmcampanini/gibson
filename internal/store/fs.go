package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

func makeDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("inspect directory %s: symbolic links are not allowed", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("inspect directory %s: not a directory", path)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("set directory permissions %s: %w", path, err)
	}
	return nil
}
