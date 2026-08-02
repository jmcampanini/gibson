package pisession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"charm.land/log/v2"
)

const (
	spawnCleanupTimeout  = 5 * time.Second
	shutdownGraceTimeout = 5 * time.Second
	stdoutDrainTimeout   = 500 * time.Millisecond
)

type Config struct {
	PiBin      string
	SessionID  string
	SessionDir string
	Cwd        string
	Model      string
	Thinking   string
	ExtraArgs  []string
	StderrPath string
	Logger     *log.Logger
}

type UIResolution struct {
	Value     *string `json:"value,omitempty"`
	Confirmed *bool   `json:"confirmed,omitempty"`
	Cancelled *bool   `json:"cancelled,omitempty"`
}

type Session struct {
	id               string
	pid              int
	cmd              *exec.Cmd
	rpc              *rpcClient
	stdout           io.Closer
	stderr           *os.File
	logger           *log.Logger
	done             chan struct{}
	complete         chan struct{}
	exitMu           sync.RWMutex
	exitErr          error
	shutdownOnce     sync.Once
	shutdownDone     chan struct{}
	shutdownEscalate chan struct{}
	escalateOnce     sync.Once
	shutdownMu       sync.RWMutex
	shutdownErr      error
	shutdownGrace    time.Duration
}

func Spawn(ctx context.Context, cfg Config) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StderrPath), 0o755); err != nil {
		return nil, fmt.Errorf("create pi stderr directory: %w", err)
	}
	stderr, err := os.OpenFile(cfg.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open pi stderr log: %w", err)
	}

	argv := buildArgv(cfg.PiBin, cfg.SessionID, cfg.SessionDir, cfg.Model, cfg.Thinking, cfg.ExtraArgs)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cfg.Cwd
	cmd.Env = os.Environ()
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stderr.Close()
		return nil, fmt.Errorf("open pi stdin: %w", err)
	}
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("open pi stdout: %w", err)
	}
	cmd.Stdout = childStdout
	if err := cmd.Start(); err != nil {
		_ = childStdout.Close()
		_ = stdout.Close()
		_ = stdin.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start pi: %w", err)
	}
	_ = childStdout.Close()

	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard)
	}
	session := &Session{
		id:               cfg.SessionID,
		pid:              cmd.Process.Pid,
		cmd:              cmd,
		rpc:              newRPCClient(stdout, stdin, logger),
		stdout:           stdout,
		stderr:           stderr,
		logger:           logger,
		done:             make(chan struct{}),
		complete:         make(chan struct{}),
		shutdownDone:     make(chan struct{}),
		shutdownEscalate: make(chan struct{}),
		shutdownGrace:    shutdownGraceTimeout,
	}
	go session.reap()

	if _, err := session.GetState(ctx); err != nil {
		cleanupErr := session.cleanupFailedSpawn()
		return nil, errors.Join(fmt.Errorf("probe pi readiness: %w", err), cleanupErr)
	}
	return session, nil
}

func validateConfig(cfg Config) error {
	required := []struct {
		name  string
		value string
	}{
		{name: "PiBin", value: cfg.PiBin},
		{name: "SessionID", value: cfg.SessionID},
		{name: "SessionDir", value: cfg.SessionDir},
		{name: "Cwd", value: cfg.Cwd},
		{name: "StderrPath", value: cfg.StderrPath},
	}
	for _, field := range required {
		if field.value == "" {
			return fmt.Errorf("pisession config %s is required", field.name)
		}
	}
	return nil
}

func (s *Session) Prompt(ctx context.Context, message, behavior string) error {
	result, err := s.StartPrompt(ctx, message, behavior)
	if err != nil {
		return err
	}
	return <-result
}

func (s *Session) StartPrompt(ctx context.Context, message, behavior string) (<-chan error, error) {
	fields := map[string]any{"message": message}
	switch behavior {
	case "":
	case "steer", "followUp":
		fields["streamingBehavior"] = behavior
	default:
		return nil, fmt.Errorf("invalid streaming behavior %q", behavior)
	}

	written := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(result)
		_, err := s.rpc.commandWithWritePolicy(ctx, "prompt", fields, unboundedResponseWait, written)
		result <- err
	}()
	select {
	case <-written:
		return result, nil
	case err := <-result:
		select {
		case <-written:
			completed := make(chan error, 1)
			completed <- err
			close(completed)
			return completed, nil
		default:
			return nil, err
		}
	}
}

func (s *Session) Abort(ctx context.Context) error {
	_, err := s.rpc.commandWithPolicy(ctx, "abort", nil, boundedResponseWait)
	return err
}

func (s *Session) GetState(ctx context.Context) (json.RawMessage, error) {
	return s.rpc.commandWithPolicy(ctx, "get_state", nil, boundedResponseWait)
}

func (s *Session) GetEntries(ctx context.Context, since string) ([]json.RawMessage, string, error) {
	var fields map[string]any
	if since != "" {
		fields = map[string]any{"since": since}
	}
	data, err := s.rpc.commandWithPolicy(ctx, "get_entries", fields, boundedResponseWait)
	if err != nil {
		var commandErr *commandError
		if since != "" && errors.As(err, &commandErr) {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		return nil, "", err
	}

	var result struct {
		Entries []json.RawMessage `json:"entries"`
		LeafID  *string           `json:"leafId"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, "", fmt.Errorf("decode pi get_entries data: %w", err)
	}
	leafID := ""
	if result.LeafID != nil {
		leafID = *result.LeafID
	}
	return result.Entries, leafID, nil
}

func (s *Session) GetSessionStats(ctx context.Context) (json.RawMessage, error) {
	return s.rpc.commandWithPolicy(ctx, "get_session_stats", nil, boundedResponseWait)
}

func (s *Session) SetSessionName(ctx context.Context, name string) error {
	_, err := s.rpc.commandWithPolicy(ctx, "set_session_name", map[string]any{"name": name}, boundedResponseWait)
	return err
}

func (s *Session) RespondUI(id string, resolution UIResolution) error {
	return s.rpc.write(struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		UIResolution
	}{
		Type:         "extension_ui_response",
		ID:           id,
		UIResolution: resolution,
	})
}

func (s *Session) Events() <-chan Event {
	return s.rpc.events
}

func (s *Session) Close(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		go s.shutdown()
	})

	select {
	case <-s.shutdownDone:
		return s.shutdownError()
	case <-ctx.Done():
		s.escalateOnce.Do(func() {
			close(s.shutdownEscalate)
		})
		<-s.shutdownDone
		return s.shutdownError()
	}
}

func (s *Session) shutdown() {
	defer close(s.shutdownDone)
	s.rpc.close()

	select {
	case <-s.complete:
		return
	default:
	}

	if err := signalProcessGroup(s.pid, syscall.SIGTERM); err != nil {
		s.setShutdownError(fmt.Errorf("signal pi process group with SIGTERM: %w", err))
		s.kill()
		<-s.complete
		return
	}

	grace := s.shutdownGrace
	if grace <= 0 {
		grace = shutdownGraceTimeout
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-s.complete:
		return
	case <-s.shutdownEscalate:
		s.logger.Warn("forcing immediate pi shutdown", "pid", s.pid)
	case <-timer.C:
		s.logger.Warn("pi did not exit after SIGTERM; forcing shutdown", "pid", s.pid, "grace", grace)
	}

	s.kill()
	<-s.complete
}

func (s *Session) kill() {
	if err := killOwnedProcessTree(s.pid); err != nil {
		s.setShutdownError(fmt.Errorf("kill pi process tree with SIGKILL: %w", err))
	}
}

func (s *Session) setShutdownError(err error) {
	s.shutdownMu.Lock()
	s.shutdownErr = errors.Join(s.shutdownErr, err)
	s.shutdownMu.Unlock()
}

func (s *Session) shutdownError() error {
	s.shutdownMu.RLock()
	defer s.shutdownMu.RUnlock()
	return s.shutdownErr
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) ExitErr() error {
	s.exitMu.RLock()
	defer s.exitMu.RUnlock()
	return s.exitErr
}

func (s *Session) PID() int {
	return s.pid
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) reap() {
	waitErr := s.cmd.Wait()

	s.exitMu.Lock()
	s.exitErr = waitErr
	s.exitMu.Unlock()

	_ = signalProcessGroup(s.pid, syscall.SIGKILL)
	timer := time.NewTimer(stdoutDrainTimeout)
	select {
	case <-s.rpc.pumpDone:
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
	}

	processErr := error(ErrProcessExited)
	if waitErr != nil {
		processErr = fmt.Errorf("%w: %v", ErrProcessExited, waitErr)
	}
	s.rpc.stop(processErr)
	s.rpc.closeWriter()
	if s.stdout != nil {
		_ = s.stdout.Close()
	}
	<-s.rpc.pumpDone
	<-s.rpc.writerDone
	if err := s.stderr.Close(); err != nil {
		s.logger.Error("close pi stderr log", "error", err)
	}
	close(s.done)
	close(s.rpc.events)
	close(s.complete)
}

func signalProcessGroup(pid int, signal syscall.Signal) error {
	if err := syscall.Kill(-pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

type processRecord struct {
	pid  int
	ppid int
	pgid int
}

func killOwnedProcessTree(rootPID int) error {
	var result error
	if err := signalProcessGroup(rootPID, syscall.SIGSTOP); err != nil {
		result = errors.Join(result, fmt.Errorf("stop root process group: %w", err))
	}

	records, err := listProcesses()
	if err != nil {
		result = errors.Join(result, err)
	} else {
		descendants := descendantsOf(rootPID, records)
		ownedPIDs := map[int]bool{rootPID: true}
		for _, record := range descendants {
			ownedPIDs[record.pid] = true
		}

		groups := make(map[int]bool)
		for _, record := range descendants {
			if ownedPIDs[record.pgid] && record.pgid != rootPID {
				groups[record.pgid] = true
			}
		}
		groupIDs := make([]int, 0, len(groups))
		for pgid := range groups {
			groupIDs = append(groupIDs, pgid)
		}
		sort.Ints(groupIDs)
		for _, pgid := range groupIDs {
			if err := signalProcessGroup(pgid, syscall.SIGKILL); err != nil {
				result = errors.Join(result, fmt.Errorf("kill descendant process group %d: %w", pgid, err))
			}
		}
		for _, record := range descendants {
			if err := syscall.Kill(record.pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				result = errors.Join(result, fmt.Errorf("kill descendant process %d: %w", record.pid, err))
			}
		}
	}
	if err := signalProcessGroup(rootPID, syscall.SIGKILL); err != nil {
		result = errors.Join(result, fmt.Errorf("kill root process group: %w", err))
	}
	return result
}

func listProcesses() ([]processRecord, error) {
	output, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=,pgid=").Output()
	if err != nil {
		return nil, fmt.Errorf("list process tree: %w", err)
	}
	lines := bytes.Split(output, []byte{'\n'})
	records := make([]processRecord, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(string(line))
		if len(fields) != 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		pgid, pgidErr := strconv.Atoi(fields[2])
		if pidErr != nil || ppidErr != nil || pgidErr != nil {
			continue
		}
		records = append(records, processRecord{pid: pid, ppid: ppid, pgid: pgid})
	}
	return records, nil
}

func descendantsOf(rootPID int, records []processRecord) []processRecord {
	children := make(map[int][]processRecord)
	for _, record := range records {
		children[record.ppid] = append(children[record.ppid], record)
	}
	var descendants []processRecord
	queue := []int{rootPID}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			descendants = append(descendants, child)
			queue = append(queue, child.pid)
		}
	}
	return descendants
}

func (s *Session) cleanupFailedSpawn() error {
	ctx, cancel := context.WithTimeout(context.Background(), spawnCleanupTimeout)
	defer cancel()
	return s.Close(ctx)
}
