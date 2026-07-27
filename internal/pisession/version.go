package pisession

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MinimumPiVersion = "0.82.0"
	VerifiedPiMinor  = 82
)

const versionCheckTimeout = 5 * time.Second

var (
	ErrPiNotFound           = errors.New("pi executable not found")
	ErrPiVersionExecution   = errors.New("pi version command failed")
	ErrPiVersionTimeout     = errors.New("pi version command timed out")
	ErrPiVersionUnparseable = errors.New("pi version output is unparseable")
	ErrPiVersionTooOld      = errors.New("pi version is too old")
)

type VersionResult struct {
	Found    string
	Verified bool
}

func ResolvePiBin(configured string) (string, error) {
	name := configured
	if name == "" {
		name = "pi"
	}

	path, err := exec.LookPath(name)
	if err == nil {
		return path, nil
	}
	if configured == "" {
		return "", fmt.Errorf("%w: %q is not on PATH; install pi %s or newer, or configure pi_bin: %v", ErrPiNotFound, name, MinimumPiVersion, err)
	}
	return "", fmt.Errorf("%w: configured pi_bin %q was not found or is not executable; set pi_bin to an executable path or command on PATH: %v", ErrPiNotFound, configured, err)
}

func CheckPiVersion(ctx context.Context, bin string) (VersionResult, error) {
	return checkPiVersion(ctx, bin, versionCheckTimeout)
}

func checkPiVersion(ctx context.Context, bin string, timeout time.Duration) (VersionResult, error) {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, err := exec.CommandContext(checkCtx, bin, "--version").CombinedOutput()
	if err != nil {
		if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
			return VersionResult{}, fmt.Errorf("%w while running %q --version (checks are capped at %s)", ErrPiVersionTimeout, bin, timeout)
		}
		if errors.Is(checkCtx.Err(), context.Canceled) {
			return VersionResult{}, fmt.Errorf("%w: running %q --version was canceled: %w", ErrPiVersionExecution, bin, checkCtx.Err())
		}
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return VersionResult{}, fmt.Errorf("%w: %q --version: %v (output: %q)", ErrPiVersionExecution, bin, err, detail)
		}
		return VersionResult{}, fmt.Errorf("%w: %q --version: %v", ErrPiVersionExecution, bin, err)
	}

	found, version, ok := findSemanticVersion(string(output))
	if !ok {
		return VersionResult{}, fmt.Errorf("%w: %q --version returned %q; expected a semantic version such as %s", ErrPiVersionUnparseable, bin, strings.TrimSpace(string(output)), MinimumPiVersion)
	}

	if !isSupportedPiVersion(version) {
		return VersionResult{}, fmt.Errorf("%w: found %s, minimum supported version is %s", ErrPiVersionTooOld, found, MinimumPiVersion)
	}

	return VersionResult{
		Found:    found,
		Verified: version.major == 0 && version.minor == VerifiedPiMinor,
	}, nil
}

var semanticVersionCandidate = regexp.MustCompile(`(?i)v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9a-z-]+(?:\.[0-9a-z-]+)*)?(?:\+[0-9a-z-]+(?:\.[0-9a-z-]+)*)?`)

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func findSemanticVersion(output string) (string, semanticVersion, bool) {
	for _, location := range semanticVersionCandidate.FindAllStringIndex(output, -1) {
		if !isTokenBoundary(output, location[0], location[1]) {
			continue
		}
		found := output[location[0]:location[1]]
		found = strings.TrimPrefix(strings.TrimPrefix(found, "v"), "V")
		version, ok := parseSemanticVersion(found)
		if ok {
			return found, version, true
		}
	}
	return "", semanticVersion{}, false
}

func isTokenBoundary(value string, start, end int) bool {
	if start > 0 && isASCIIAlphaNumeric(value[start-1]) {
		return false
	}
	return end == len(value) || !isASCIIAlphaNumeric(value[end])
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func parseSemanticVersion(value string) (semanticVersion, bool) {
	withoutBuild, _, _ := strings.Cut(value, "+")
	core, prerelease, _ := strings.Cut(withoutBuild, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semanticVersion{}, false
	}

	numbers := [3]int{}
	for i, part := range parts {
		if len(part) > 1 && part[0] == '0' {
			return semanticVersion{}, false
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return semanticVersion{}, false
		}
		numbers[i] = number
	}
	if !validPrerelease(prerelease) {
		return semanticVersion{}, false
	}
	return semanticVersion{major: numbers[0], minor: numbers[1], patch: numbers[2], prerelease: prerelease}, true
}

func validPrerelease(value string) bool {
	if value == "" {
		return true
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" || len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "" {
			return false
		}
	}
	return true
}

func isSupportedPiVersion(version semanticVersion) bool {
	if version.major != 0 {
		return version.major > 0
	}
	if version.minor != VerifiedPiMinor {
		return version.minor > VerifiedPiMinor
	}
	return version.patch > 0 || version.prerelease == ""
}
