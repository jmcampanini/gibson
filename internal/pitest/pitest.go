package pitest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	fakePiOnce   sync.Once
	fakePiBinary []byte
	fakePiErr    error
)

func BuildFakePi(t testing.TB) string {
	t.Helper()

	fakePiOnce.Do(func() {
		fakePiBinary, fakePiErr = compileFakePi()
	})
	if fakePiErr != nil {
		t.Fatalf("BuildFakePi: %v", fakePiErr)
	}

	path := filepath.Join(t.TempDir(), fakePiName())
	if err := os.WriteFile(path, fakePiBinary, 0o755); err != nil {
		t.Fatalf("materialize fake pi: %v", err)
	}
	return path
}

func compileFakePi() (binary []byte, resultErr error) {
	dir, err := os.MkdirTemp("", "gibson-fakepi-build-")
	if err != nil {
		return nil, fmt.Errorf("create fake pi build directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove fake pi build directory: %w", err))
		}
	}()

	path := filepath.Join(dir, fakePiName())
	cmd := exec.Command("go", "build", "-o", path, "github.com/jmcampanini/gibson/internal/fakepi")
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build fake pi: %w: %s", err, output)
	}
	binary, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fake pi binary: %w", err)
	}
	return binary, nil
}

func fakePiName() string {
	if runtime.GOOS == "windows" {
		return "pi.exe"
	}
	return "pi"
}

func RequireRealPi(t testing.TB) string {
	t.Helper()
	if os.Getenv("GIBSON_TEST_REAL_PI") != "1" {
		t.Skip("set GIBSON_TEST_REAL_PI=1 to run real pi checks")
	}

	path, err := exec.LookPath("pi")
	if err != nil {
		t.Fatalf("resolve real pi: %v", err)
	}
	return path
}
