package store

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const gibsonIgnoreTarget = ".gibson/"

func CheckIgnored(checkoutRoot string) (bool, error) {
	source, line, matched, err := findIgnoreMatch(checkoutRoot)
	if err != nil || !matched {
		return false, err
	}

	source, ok := repositoryIgnorePath(checkoutRoot, source)
	if !ok {
		return false, nil
	}
	tracked, err := isTracked(checkoutRoot, source)
	if err != nil || !tracked {
		return false, err
	}
	return isCommittedLine(checkoutRoot, source, line)
}

func findIgnoreMatch(checkoutRoot string) (string, int, bool, error) {
	cmd := exec.Command("git", "-C", checkoutRoot, "check-ignore", "-z", "-v", "--stdin")
	cmd.Stdin = strings.NewReader(gibsonIgnoreTarget + "\x00")
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", 0, false, nil
		}
		return "", 0, false, gitError("check whether .gibson/ is ignored", checkoutRoot, output, err)
	}

	fields := bytes.Split(output, []byte{0})
	if len(fields) < 4 {
		return "", 0, false, fmt.Errorf("check whether .gibson/ is ignored in %q: unexpected git output", checkoutRoot)
	}
	line, err := strconv.Atoi(string(fields[1]))
	if err != nil {
		return "", 0, false, fmt.Errorf("check whether .gibson/ is ignored in %q: invalid source line %q", checkoutRoot, fields[1])
	}
	return string(fields[0]), line, true, nil
}

func repositoryIgnorePath(checkoutRoot, source string) (string, bool) {
	if filepath.IsAbs(source) {
		relative, err := filepath.Rel(checkoutRoot, source)
		if err != nil {
			return "", false
		}
		source = relative
	}
	source = filepath.Clean(source)
	if source == ".." || strings.HasPrefix(source, ".."+string(filepath.Separator)) {
		return "", false
	}
	if source != ".gitignore" {
		return "", false
	}
	return source, true
}

func isTracked(checkoutRoot, source string) (bool, error) {
	cmd := exec.Command("git", "-C", checkoutRoot, "ls-files", "--error-unmatch", "--", source)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, gitError("check whether ignore source is tracked", checkoutRoot, output, err)
}

func isCommittedLine(checkoutRoot, source string, line int) (bool, error) {
	head := exec.Command("git", "-C", checkoutRoot, "rev-parse", "--verify", "HEAD")
	if err := head.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("check repository HEAD in %q: %w", checkoutRoot, err)
	}

	location := fmt.Sprintf("%d,%d", line, line)
	cmd := exec.Command("git", "-C", checkoutRoot, "blame", "--porcelain", "-L", location, "--", source)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, gitError("check whether ignore rule is committed", checkoutRoot, output, err)
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return false, fmt.Errorf("check whether ignore rule is committed in %q: unexpected git output", checkoutRoot)
	}
	return strings.Trim(fields[0], "0") != "", nil
}

func gitError(action, checkoutRoot string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("%s in %q: %s", action, checkoutRoot, detail)
}
