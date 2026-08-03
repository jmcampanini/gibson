package pisession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"charm.land/log/v2"
)

const (
	spawnCleanupTimeout  = 5 * time.Second
	shutdownGraceTimeout = 5 * time.Second
	stdoutDrainTimeout   = 500 * time.Millisecond
	processScanInterval  = 50 * time.Millisecond
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
	processes        *ownedProcessTracker
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
	stderrDir := filepath.Dir(cfg.StderrPath)
	if err := os.MkdirAll(stderrDir, 0o755); err != nil {
		return nil, fmt.Errorf("create pi stderr directory: %w", err)
	}
	if err := os.Chmod(stderrDir, 0o755); err != nil {
		return nil, fmt.Errorf("set pi stderr directory permissions: %w", err)
	}
	stderr, err := os.OpenFile(cfg.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open pi stderr log: %w", err)
	}
	if err := stderr.Chmod(0o644); err != nil {
		_ = stderr.Close()
		return nil, fmt.Errorf("set pi stderr log permissions: %w", err)
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
	processes, err := newOwnedProcessTracker(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Wait()
		_ = stderr.Close()
		return nil, fmt.Errorf("identify pi process: %w", err)
	}
	session := &Session{
		id:               cfg.SessionID,
		pid:              cmd.Process.Pid,
		cmd:              cmd,
		rpc:              newRPCClient(stdout, stdin, logger),
		stdout:           stdout,
		stderr:           stderr,
		logger:           logger,
		processes:        processes,
		done:             make(chan struct{}),
		complete:         make(chan struct{}),
		shutdownDone:     make(chan struct{}),
		shutdownEscalate: make(chan struct{}),
		shutdownGrace:    shutdownGraceTimeout,
	}
	go processes.run()
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
	_, err := s.rpc.command(ctx, "abort", nil)
	return err
}

func (s *Session) GetState(ctx context.Context) (json.RawMessage, error) {
	return s.rpc.command(ctx, "get_state", nil)
}

func (s *Session) GetEntries(ctx context.Context, since string) ([]json.RawMessage, string, error) {
	var fields map[string]any
	if since != "" {
		fields = map[string]any{"since": since}
	}
	data, err := s.rpc.command(ctx, "get_entries", fields)
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
	return s.rpc.command(ctx, "get_session_stats", nil)
}

func (s *Session) SetSessionName(ctx context.Context, name string) error {
	_, err := s.rpc.command(ctx, "set_session_name", map[string]any{"name": name})
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

	if err := s.signalRootProcessGroup(syscall.SIGTERM); err != nil {
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
	if s.processes == nil {
		if err := signalProcessGroup(s.pid, syscall.SIGKILL); err != nil {
			s.setShutdownError(fmt.Errorf("kill pi process group with SIGKILL: %w", err))
		}
		return
	}
	if err := s.processes.killTree(); err != nil {
		s.setShutdownError(fmt.Errorf("kill pi process tree with SIGKILL: %w", err))
	}
}

func (s *Session) signalRootProcessGroup(signal syscall.Signal) error {
	if s.processes == nil {
		return signalProcessGroup(s.pid, signal)
	}
	return s.processes.signalRootProcessGroup(signal)
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
	if s.processes != nil {
		s.processes.stop()
		if err := s.processes.killTracked(); err != nil {
			s.logger.Error("kill tracked pi descendants after exit", "error", err)
		}
	}
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
	pid     int
	ppid    int
	pgid    int
	started string
}

type processListFunc func() ([]processRecord, error)
type processReadFunc func(int) (processRecord, bool, error)

type ownedProcessTracker struct {
	root     processRecord
	rootGone bool
	list     processListFunc
	read     processReadFunc
	mu       sync.Mutex
	owned    map[int]processRecord
	stopOnce sync.Once
	stopCh   chan struct{}
	done     chan struct{}
}

func newOwnedProcessTracker(rootPID int) (*ownedProcessTracker, error) {
	return newOwnedProcessTrackerWith(rootPID, listProcesses, readProcessRecord)
}

func newOwnedProcessTrackerWith(rootPID int, list processListFunc, read processReadFunc) (*ownedProcessTracker, error) {
	root, exists, err := read(rootPID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("process %d exited before ownership was recorded", rootPID)
	}
	return &ownedProcessTracker{
		root:   root,
		list:   list,
		read:   read,
		owned:  make(map[int]processRecord),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}, nil
}

func (t *ownedProcessTracker) run() {
	defer close(t.done)
	ticker := time.NewTicker(processScanInterval)
	defer ticker.Stop()
	for {
		_ = t.capture()
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
		}
	}
}

func (t *ownedProcessTracker) stop() {
	t.stopOnce.Do(func() { close(t.stopCh) })
	<-t.done
}

func (t *ownedProcessTracker) capture() error {
	records, err := t.list()
	if err != nil {
		return err
	}
	current := make(map[int]processRecord, len(records))
	for _, record := range records {
		current[record.pid] = record
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	var identityErr error
	discover := false
	if !t.rootGone {
		snapshotRoot, inSnapshot := current[t.root.pid]
		liveRoot, exists, err := t.read(t.root.pid)
		if err != nil {
			identityErr = err
		}
		if err != nil || !inSnapshot || !exists || snapshotRoot.started != t.root.started || liveRoot.started != t.root.started {
			t.rootGone = true
		} else {
			discover = true
		}
	}
	for pid, tracked := range t.owned {
		record, ok := current[pid]
		if !ok || record.started != tracked.started {
			delete(t.owned, pid)
			continue
		}
		t.owned[pid] = record
	}
	if discover {
		for _, record := range descendantsOf(t.root.pid, records) {
			validated, valid, err := t.validateDescendant(record, current)
			if err != nil {
				identityErr = errors.Join(identityErr, err)
			}
			if valid {
				t.owned[validated.pid] = validated
			}
		}
	}
	return identityErr
}

func (t *ownedProcessTracker) validateDescendant(candidate processRecord, snapshot map[int]processRecord) (processRecord, bool, error) {
	chain := []processRecord{candidate}
	seen := map[int]bool{candidate.pid: true}
	for parentPID := candidate.ppid; parentPID != t.root.pid; {
		if seen[parentPID] {
			return processRecord{}, false, nil
		}
		parent, exists := snapshot[parentPID]
		if !exists {
			return processRecord{}, false, nil
		}
		seen[parentPID] = true
		chain = append(chain, parent)
		parentPID = parent.ppid
	}

	liveRoot, exists, err := t.read(t.root.pid)
	if err != nil {
		return processRecord{}, false, err
	}
	if !exists || liveRoot.started != t.root.started {
		return processRecord{}, false, nil
	}
	parentPID := t.root.pid
	for index := len(chain) - 1; index >= 0; index-- {
		expected := chain[index]
		live, exists, err := t.read(expected.pid)
		if err != nil {
			return processRecord{}, false, err
		}
		if !exists || live.started != expected.started || live.ppid != parentPID {
			return processRecord{}, false, nil
		}
		parentPID = live.pid
		if index == 0 {
			return live, true, nil
		}
	}
	return processRecord{}, false, nil
}

func (t *ownedProcessTracker) signalRootProcessGroup(signal syscall.Signal) error {
	current, exists, err := t.read(t.root.pid)
	if err != nil || !exists || current.started != t.root.started {
		return err
	}
	if current.pgid != t.root.pid {
		if err := signalProcessIfOwned(t.root, signal); err != nil {
			return fmt.Errorf("signal root process %d in group %d: %w", t.root.pid, current.pgid, err)
		}
		return nil
	}
	if err := signalProcessGroup(t.root.pid, signal); err != nil {
		return fmt.Errorf("signal root group %d: %w", t.root.pid, err)
	}
	return nil
}

func (t *ownedProcessTracker) killTree() error {
	var result error
	if err := t.signalRootProcessGroup(syscall.SIGSTOP); err != nil {
		result = errors.Join(result, fmt.Errorf("stop root process group: %w", err))
	}
	if err := t.capture(); err != nil {
		result = errors.Join(result, err)
	}
	result = errors.Join(result, t.killTracked())
	if err := signalProcessIfOwned(t.root, syscall.SIGKILL); err != nil {
		result = errors.Join(result, fmt.Errorf("kill root process: %w", err))
	}
	return result
}

func (t *ownedProcessTracker) killTracked() error {
	t.mu.Lock()
	owned := make(map[int]processRecord, len(t.owned))
	for pid, record := range t.owned {
		owned[pid] = record
	}
	t.mu.Unlock()
	if len(owned) == 0 {
		return nil
	}

	records, err := t.list()
	if err != nil {
		return err
	}
	current := make(map[int]processRecord, len(records))
	for _, record := range records {
		current[record.pid] = record
	}
	matched := make(map[int]processRecord)
	for pid, tracked := range owned {
		if record, ok := current[pid]; ok && record.started == tracked.started {
			matched[pid] = record
		}
	}

	groups := make(map[int]processRecord)
	for _, record := range matched {
		if record.pgid == t.root.pid {
			groups[record.pgid] = record
			continue
		}
		leader, leaderOwned := matched[record.pgid]
		if leaderOwned && leader.pid == leader.pgid {
			groups[record.pgid] = leader
		}
	}
	groupIDs := make([]int, 0, len(groups))
	for pgid := range groups {
		groupIDs = append(groupIDs, pgid)
	}
	sort.Ints(groupIDs)
	var result error
	for _, pgid := range groupIDs {
		witness := groups[pgid]
		currentWitness, exists, err := t.read(witness.pid)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if !exists || currentWitness.started != witness.started || currentWitness.pgid != pgid {
			continue
		}
		if err := signalProcessGroup(pgid, syscall.SIGKILL); err != nil {
			result = errors.Join(result, fmt.Errorf("kill descendant process group %d: %w", pgid, err))
		}
	}
	for pid, record := range matched {
		if err := signalProcessIfOwned(record, syscall.SIGKILL); err != nil {
			result = errors.Join(result, fmt.Errorf("kill descendant process %d: %w", pid, err))
		}
	}
	return result
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
