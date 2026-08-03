package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/internal/config"
	"github.com/jmcampanini/gibson/internal/pisession"
	"github.com/jmcampanini/gibson/internal/store"
	"github.com/jmcampanini/gibson/internal/workspace"
)

const (
	runCleanupTimeout = 5 * time.Second
	runAbortTimeout   = 10 * time.Second
	crashTailSize     = 8 << 10
)

type RunOutcome uint8

const (
	RunCompleted RunOutcome = iota
	RunInterrupted
)

type RunOptions struct {
	Type     string
	Message  string
	Checkout string
	Stdout   io.Writer
	Stderr   io.Writer
}

type runSession interface {
	StartPrompt(context.Context, string, string) (<-chan error, error)
	Abort(context.Context) error
	GetState(context.Context) (json.RawMessage, error)
	Events() <-chan pisession.Event
	Close(context.Context) error
	ExitErr() error
	PID() int
}

type runDependencies struct {
	getwd           func() (string, error)
	resolvePiBin    func(string) (string, error)
	checkPiVersion  func(context.Context, string) (pisession.VersionResult, error)
	spawn           func(readinessCtx, cleanupCtx context.Context, cfg pisession.Config) (runSession, error)
	now             func() time.Time
	interrupts      <-chan os.Signal
	abortTimeout    time.Duration
	onIdleConfirmed func()
}

type runStartupControl struct {
	ctx            context.Context
	cancel         context.CancelFunc
	cleanupCtx     context.Context
	forceCleanup   context.CancelFunc
	interrupts     <-chan os.Signal
	interruptCount int
}

func newRunStartupControl(ctx context.Context, interrupts <-chan os.Signal) *runStartupControl {
	startupCtx, cancel := context.WithCancel(ctx)
	cleanupCtx, forceCleanup := context.WithCancel(context.Background())
	return &runStartupControl{
		ctx:          startupCtx,
		cancel:       cancel,
		cleanupCtx:   cleanupCtx,
		forceCleanup: forceCleanup,
		interrupts:   interrupts,
	}
}

func (c *runStartupControl) close() {
	c.cancel()
	c.forceCleanup()
}

func (c *runStartupControl) interrupted() bool {
	return c.interruptCount > 0
}

func (c *runStartupControl) checkInterrupted() bool {
	c.drainInterrupts()
	return c.interrupted()
}

func (c *runStartupControl) receiveInterrupt() {
	c.interruptCount++
	if c.interruptCount == 1 {
		c.cancel()
		return
	}
	c.forceCleanup()
}

func (c *runStartupControl) drainInterrupts() {
	for {
		select {
		case <-c.interrupts:
			c.receiveInterrupt()
		default:
			return
		}
	}
}

type runAsyncResult[T any] struct {
	value T
	err   error
}

func waitRunStartup[T any](control *runStartupControl, results <-chan runAsyncResult[T], abandonOnSecondInterrupt bool) (runAsyncResult[T], bool) {
	for {
		select {
		case result := <-results:
			control.drainInterrupts()
			return result, false
		case <-control.interrupts:
			control.receiveInterrupt()
			if abandonOnSecondInterrupt && control.interruptCount > 1 {
				return runAsyncResult[T]{}, true
			}
		}
	}
}

func Run(ctx context.Context, options RunOptions, logger *log.Logger) (RunOutcome, error) {
	interrupts := make(chan os.Signal, 2)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	return run(ctx, options, logger, runDependencies{
		getwd:          os.Getwd,
		resolvePiBin:   pisession.ResolvePiBin,
		checkPiVersion: pisession.CheckPiVersion,
		spawn: func(readinessCtx, cleanupCtx context.Context, cfg pisession.Config) (runSession, error) {
			return pisession.SpawnWithCleanupContext(readinessCtx, cleanupCtx, cfg)
		},
		now:          time.Now,
		interrupts:   interrupts,
		abortTimeout: runAbortTimeout,
	})
}

func run(ctx context.Context, options RunOptions, logger *log.Logger, dependencies runDependencies) (RunOutcome, error) {
	stdout := options.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	if logger == nil {
		logger = log.New(io.Discard)
	}

	startup := newRunStartupControl(ctx, dependencies.interrupts)
	defer startup.close()
	if startup.checkInterrupted() {
		return RunInterrupted, nil
	}
	if err := startup.ctx.Err(); err != nil {
		return classifyRunError(ctx, err)
	}

	workingDirectory, err := dependencies.getwd()
	if err != nil {
		return classifyRunError(ctx, fmt.Errorf("determine working directory: %w", err))
	}
	if startup.checkInterrupted() {
		return RunInterrupted, nil
	}
	ws, err := workspace.Locate(workingDirectory)
	if err != nil {
		return classifyRunError(ctx, err)
	}
	cfg, err := config.Load(ws.LaunchCheckout)
	if err != nil {
		return classifyRunError(ctx, err)
	}
	sessionType, ok := cfg.Sessions[options.Type]
	if !ok {
		return RunCompleted, unknownSessionTypeError(options.Type, cfg.Sessions)
	}
	checkout := ws.LaunchCheckout
	if options.Checkout != "" {
		checkout, err = workspace.ResolveCheckout(ws.Root, options.Checkout)
		if err != nil {
			return classifyRunError(ctx, err)
		}
	}
	if startup.checkInterrupted() {
		return RunInterrupted, nil
	}

	piBin, err := dependencies.resolvePiBin(cfg.Server.PiBin)
	if err != nil {
		return classifyRunError(ctx, err)
	}
	if startup.checkInterrupted() {
		return RunInterrupted, nil
	}
	versionResults := make(chan runAsyncResult[pisession.VersionResult], 1)
	go func() {
		version, versionErr := dependencies.checkPiVersion(startup.ctx, piBin)
		versionResults <- runAsyncResult[pisession.VersionResult]{value: version, err: versionErr}
	}()
	versionResult, abandoned := waitRunStartup(startup, versionResults, true)
	if abandoned {
		return RunInterrupted, nil
	}
	if versionResult.err != nil {
		if startup.interrupted() {
			if errors.Is(versionResult.err, startup.ctx.Err()) {
				return RunInterrupted, withoutExpectedErrors(versionResult.err, startup.ctx.Err(), pisession.ErrPiVersionExecution)
			}
			return RunInterrupted, versionResult.err
		}
		return classifyRunError(ctx, versionResult.err)
	}
	if startup.interrupted() {
		return RunInterrupted, nil
	}
	piVersion := versionResult.value
	if !piVersion.Verified {
		logger.Warn(
			"pi version has not been verified with Gibson",
			"found", piVersion.Found,
			"verified", fmt.Sprintf("0.%d.x", pisession.VerifiedPiMinor),
			"minimum", pisession.MinimumPiVersion,
		)
	}

	storage, storageErr := store.Open(checkout)
	if storageErr != nil {
		return RunCompleted, storageErr
	}
	if startup.checkInterrupted() {
		return RunInterrupted, nil
	}
	if err := storage.EnsureLayout(); err != nil {
		return RunCompleted, err
	}
	if startup.checkInterrupted() {
		return RunInterrupted, nil
	}
	var session runSession
	var logPath string
	creationResults := make(chan runAsyncResult[string], 1)
	go func() {
		sessionID, createErr := storage.CreateSession(startup.ctx, func(id string) (store.SessionCreation, error) {
			logPath = storage.StderrLogPath(id)
			spawnConfig := pisession.Config{
				PiBin:      piBin,
				SessionID:  id,
				SessionDir: storage.SessionsDir(),
				Cwd:        checkout,
				Model:      sessionType.Model,
				Thinking:   sessionType.Thinking,
				ExtraArgs:  sessionType.ExtraArgs,
				StderrPath: logPath,
				Logger:     logger,
			}
			spawned, spawnErr := dependencies.spawn(startup.ctx, startup.cleanupCtx, spawnConfig)
			if spawnErr != nil {
				return store.SessionCreation{}, spawnErr
			}
			session = spawned
			startedAt := dependencies.now().UTC().Format(time.RFC3339)
			return store.SessionCreation{
				Record: store.Record{
					ID:             id,
					Type:           options.Type,
					Status:         store.StatusLive,
					CreatedAt:      startedAt,
					LastActivityAt: startedAt,
					PID:            session.PID(),
				},
				Rollback: func() error {
					rollbackErr := closeRunSession(spawned, startup.cleanupCtx.Err() != nil, nil)
					session = nil
					return rollbackErr
				},
			}, nil
		})
		creationResults <- runAsyncResult[string]{value: sessionID, err: createErr}
	}()
	creationResult, _ := waitRunStartup(startup, creationResults, false)
	sessionID := creationResult.value
	if creationResult.err != nil {
		var closeErr error
		var registryErr error
		if session != nil {
			closeErr = closeRunSession(session, startup.cleanupCtx.Err() != nil, nil)
			if _, stopErr := storage.StopIfLivePID(sessionID, session.PID()); stopErr != nil {
				registryErr = fmt.Errorf("record stopped session after creation failure: %w", stopErr)
			}
		}
		runErr := errors.Join(fmt.Errorf("create live session: %w", creationResult.err), closeErr, registryErr)
		if startup.interrupted() {
			return RunInterrupted, withoutContextError(runErr, startup.ctx.Err())
		}
		return classifyRunError(ctx, runErr)
	}
	finisher := runFinisher{
		session:   session,
		storage:   storage,
		stderr:    stderr,
		sessionID: sessionID,
		logPath:   logPath,
		logger:    logger,
	}
	finishStartupInterrupt := func(runErr error) (RunOutcome, error) {
		if startup.interruptCount > 1 {
			return finisher.finishForced(RunInterrupted, runErr)
		}
		return finisher.finishInterrupt(RunInterrupted, runErr, dependencies.interrupts)
	}
	if startup.interrupted() {
		return finishStartupInterrupt(nil)
	}

	initialStateResults := make(chan runAsyncResult[json.RawMessage], 1)
	go func() {
		stateRaw, stateErr := session.GetState(startup.ctx)
		initialStateResults <- runAsyncResult[json.RawMessage]{value: stateRaw, err: stateErr}
	}()
	initialStateResult, _ := waitRunStartup(startup, initialStateResults, false)
	if startup.interrupted() {
		runErr := withoutContextError(initialStateResult.err, startup.ctx.Err())
		return finishStartupInterrupt(runErr)
	}
	if initialStateResult.err != nil {
		outcome, runErr := classifyRunError(ctx, fmt.Errorf("read pi session state: %w", initialStateResult.err))
		return finisher.finish(outcome, runErr)
	}
	stateRaw := initialStateResult.value
	var state struct {
		SessionID   string `json:"sessionId"`
		SessionFile string `json:"sessionFile"`
	}
	if err := json.Unmarshal(stateRaw, &state); err != nil {
		return finisher.finish(RunCompleted, fmt.Errorf("decode pi session state: %w", err))
	}
	finisher.sessionFile = state.SessionFile
	if state.SessionID != sessionID {
		return finisher.finish(RunCompleted, fmt.Errorf("pi reported session id %q, expected %q", state.SessionID, sessionID))
	}
	if state.SessionFile == "" {
		return finisher.finish(RunCompleted, errors.New("pi reported an empty session file path"))
	}
	if filepath.Clean(filepath.Dir(state.SessionFile)) != filepath.Clean(storage.SessionsDir()) {
		return finisher.finish(RunCompleted, fmt.Errorf("pi reported session file outside Gibson's session directory: %s", state.SessionFile))
	}
	if _, err := fmt.Fprintf(stderr, "[session] id=%s file=%s log=%s\n", sessionID, state.SessionFile, logPath); err != nil {
		return finisher.finish(RunCompleted, fmt.Errorf("write session details: %w", err))
	}

	presenter := runPresenter{stdout: stdout, stderr: stderr}
	events := session.Events()
	processEvent := func(event pisession.Event) (bool, error) {
		if err := presenter.present(event); err != nil {
			return false, err
		}
		if event.Type == "message_end" {
			if err := storage.Touch(sessionID, dependencies.now().UTC()); err != nil {
				return false, fmt.Errorf("record durable session activity: %w", err)
			}
		}
		return event.Type == "agent_settled", nil
	}

	interrupt := runInterruptState{}
	interrupts := dependencies.interrupts
	abortTimeout := dependencies.abortTimeout
	if abortTimeout <= 0 {
		abortTimeout = runAbortTimeout
	}
	finishInterrupted := func(force bool, runErr error) (RunOutcome, error) {
		interrupt.stopWatchdog()
		runErr = errors.Join(runErr, interrupt.err, presenter.finalAssistantErr, presenter.finishText())
		if force {
			return finisher.finishForced(RunInterrupted, runErr)
		}
		return finisher.finishInterrupt(RunInterrupted, runErr, dependencies.interrupts)
	}
	finishCancelled := func(racingErr error) (RunOutcome, error) {
		interrupt.stopWatchdog()
		runErr := errors.Join(withoutContextError(racingErr, ctx.Err()), presenter.finalAssistantErr, presenter.finishText())
		return finisher.finish(RunInterrupted, runErr)
	}
	finishExited := func(runErr error) (RunOutcome, error) {
		interrupt.stopWatchdog()
		interrupted := interrupt.started
		if !interrupted {
			select {
			case <-interrupts:
				interrupted = true
				presenter.suppressText = true
			default:
			}
		}
		return finisher.finishExited(interrupted, runErr)
	}
	handleInterrupt := func() (bool, RunOutcome, error) {
		if interrupt.started {
			outcome, err := finishInterrupted(true, nil)
			return true, outcome, err
		}
		interrupt.start(session, &presenter, abortTimeout)
		return false, RunCompleted, nil
	}
	handleAbortResult := func(err error) (bool, RunOutcome, error) {
		interrupt.results = nil
		if err != nil {
			interrupt.err = fmt.Errorf("abort pi: %w", err)
			outcome, finishErr := finishInterrupted(false, nil)
			return true, outcome, finishErr
		}
		if interrupt.settled {
			outcome, finishErr := finishInterrupted(false, nil)
			return true, outcome, finishErr
		}
		return false, RunCompleted, nil
	}
	handleEvent := func(event pisession.Event) (bool, error) {
		settled, err := processEvent(event)
		if event.Type == "agent_start" {
			interrupt.agentStarted = true
		}
		interrupt.settled = interrupt.settled || settled
		return settled, err
	}
	finishEventError := func(eventErr error) (RunOutcome, error) {
		runErr := errors.Join(eventErr, presenter.finishText())
		if interrupt.started {
			return finishInterrupted(false, runErr)
		}
		return finisher.finish(RunCompleted, runErr)
	}
	finishNormalWithInterruptPriority := func(runErr func() error) (bool, RunOutcome, error) {
		select {
		case <-interrupts:
			return handleInterrupt()
		default:
			outcome, err := finisher.finish(RunCompleted, runErr())
			return true, outcome, err
		}
	}
	finishSettled := func() (bool, RunOutcome, error) {
		if interrupt.started {
			outcome, err := finishInterrupted(false, nil)
			return true, outcome, err
		}
		return finishNormalWithInterruptPriority(func() error {
			return errors.Join(presenter.finalAssistantErr, presenter.finishText())
		})
	}
	if startup.checkInterrupted() {
		presenter.suppressText = true
		return finishStartupInterrupt(presenter.finishText())
	}

	type promptStartResult struct {
		acceptance <-chan error
		err        error
	}
	promptCtx, cancelPrompt := context.WithCancel(ctx)
	defer cancelPrompt()
	promptStarts := make(chan promptStartResult, 1)
	go func() {
		acceptance, err := session.StartPrompt(promptCtx, options.Message, "")
		promptStarts <- promptStartResult{acceptance: acceptance, err: err}
	}()
	var promptResults <-chan error
	interruptBeforePrompt := false
	for promptResults == nil {
		select {
		case <-ctx.Done():
			var racingErr error
			select {
			case result := <-promptStarts:
				if result.err != nil {
					racingErr = fmt.Errorf("prompt pi: %w", result.err)
				}
			default:
			}
			return finishCancelled(racingErr)
		case <-interrupts:
			if interruptBeforePrompt {
				return finisher.finishForced(RunInterrupted, presenter.finishText())
			}
			interruptBeforePrompt = true
			presenter.suppressText = true
			cancelPrompt()
		case result := <-promptStarts:
			if result.err != nil {
				if !interruptBeforePrompt {
					select {
					case <-interrupts:
						interruptBeforePrompt = true
						presenter.suppressText = true
					default:
					}
				}
				promptErr := fmt.Errorf("prompt pi: %w", result.err)
				if interruptBeforePrompt {
					runErr := errors.Join(withoutContextError(promptErr, promptCtx.Err()), presenter.finishText())
					return finisher.finishInterrupt(RunInterrupted, runErr, interrupts)
				}
				outcome, classifiedErr := classifyRunError(ctx, promptErr)
				return finisher.finish(outcome, errors.Join(classifiedErr, presenter.finishText()))
			}
			promptResults = result.acceptance
			if interruptBeforePrompt {
				interrupt.start(session, &presenter, abortTimeout)
			}
		}
	}

	settledBeforeAcceptance := false
promptWait:
	for {
		select {
		case <-ctx.Done():
			var racingErr error
			select {
			case promptErr := <-promptResults:
				if promptErr != nil {
					racingErr = fmt.Errorf("prompt pi: %w", promptErr)
				}
			default:
			}
			return finishCancelled(racingErr)
		case <-interrupts:
			if done, outcome, interruptErr := handleInterrupt(); done {
				return outcome, interruptErr
			}
		case abortErr := <-interrupt.results:
			if done, outcome, finishErr := handleAbortResult(abortErr); done {
				return outcome, finishErr
			}
		case <-interrupt.watchdog:
			return finishInterrupted(false, fmt.Errorf("pi did not settle within %s after interrupt", abortTimeout))
		case event, open := <-events:
			if !open {
				return finishExited(errors.Join(interrupt.err, presenter.finishText()))
			}
			settled, eventErr := handleEvent(event)
			if eventErr != nil {
				return finishEventError(eventErr)
			}
			settledBeforeAcceptance = settledBeforeAcceptance || settled
			if interrupt.started && interrupt.settled {
				return finishInterrupted(false, nil)
			}
		case promptErr := <-promptResults:
			if promptErr != nil {
				if interrupt.started {
					promptErr = withoutContextError(promptErr, promptCtx.Err())
					if promptErr == nil {
						promptResults = nil
						continue
					}
					return finishInterrupted(false, fmt.Errorf("prompt pi: %w", promptErr))
				}
				outcome, runErr := classifyRunError(ctx, fmt.Errorf("prompt pi: %w", promptErr))
				runErr = errors.Join(runErr, presenter.finishText())
				return finisher.finish(outcome, runErr)
			}
			if err := storage.Touch(sessionID, dependencies.now().UTC()); err != nil {
				runErr := errors.Join(fmt.Errorf("record accepted prompt: %w", err), presenter.finishText())
				if interrupt.started {
					return finishInterrupted(false, runErr)
				}
				return finisher.finish(RunCompleted, runErr)
			}
			break promptWait
		}
	}
	if settledBeforeAcceptance && !interrupt.started {
		if done, outcome, finishErr := finishSettled(); done {
			return outcome, finishErr
		}
	}

	// Verified pi marks a normal run streaming before it can process this follow-up command. Ordered stdout ensures events emitted before the state response are queued for the drain below.
	stateResults := make(chan runAsyncResult[json.RawMessage], 1)
	go func() {
		raw, err := session.GetState(ctx)
		stateResults <- runAsyncResult[json.RawMessage]{value: raw, err: err}
	}()

	idleConfirmed := false
	for {
		if idleConfirmed {
			select {
			case <-interrupts:
				if done, outcome, interruptErr := handleInterrupt(); done {
					return outcome, interruptErr
				}
				continue
			default:
			}
			select {
			case abortErr := <-interrupt.results:
				if done, outcome, finishErr := handleAbortResult(abortErr); done {
					return outcome, finishErr
				}
				continue
			case <-interrupt.watchdog:
				return finishInterrupted(false, fmt.Errorf("pi did not settle within %s after interrupt", abortTimeout))
			default:
			}
			select {
			case event, open := <-events:
				if !open {
					return finishExited(errors.Join(interrupt.err, presenter.finishText()))
				}
				settled, eventErr := handleEvent(event)
				if eventErr != nil {
					return finishEventError(eventErr)
				}
				if settled {
					if done, outcome, finishErr := finishSettled(); done {
						return outcome, finishErr
					}
				}
				continue
			default:
				if !interrupt.agentStarted {
					var idleErr error
					if !piVersion.Verified {
						idleErr = fmt.Errorf("cannot safely determine prompt completion with unverified pi %s: pi reported idle before agent_start", piVersion.Found)
					}
					if interrupt.started {
						return finishInterrupted(false, idleErr)
					}
					if done, outcome, finishErr := finishNormalWithInterruptPriority(func() error {
						if idleErr != nil {
							return idleErr
						}
						return presenter.finishText()
					}); done {
						return outcome, finishErr
					}
					continue
				}
			}
		}

		select {
		case <-ctx.Done():
			var racingErr error
			select {
			case result := <-stateResults:
				if result.err != nil {
					racingErr = fmt.Errorf("check pi state after prompt acceptance: %w", result.err)
				}
			default:
			}
			return finishCancelled(racingErr)
		case <-interrupts:
			if done, outcome, interruptErr := handleInterrupt(); done {
				return outcome, interruptErr
			}
		case abortErr := <-interrupt.results:
			if done, outcome, finishErr := handleAbortResult(abortErr); done {
				return outcome, finishErr
			}
		case <-interrupt.watchdog:
			return finishInterrupted(false, fmt.Errorf("pi did not settle within %s after interrupt", abortTimeout))
		case event, open := <-events:
			if !open {
				return finishExited(errors.Join(interrupt.err, presenter.finishText()))
			}
			settled, eventErr := handleEvent(event)
			if eventErr != nil {
				return finishEventError(eventErr)
			}
			if settled {
				if done, outcome, finishErr := finishSettled(); done {
					return outcome, finishErr
				}
			}
		case result := <-stateResults:
			stateResults = nil
			if result.err != nil {
				if interrupt.started {
					return finishInterrupted(false, fmt.Errorf("check pi state after prompt acceptance: %w", result.err))
				}
				outcome, runErr := classifyRunError(ctx, fmt.Errorf("check pi state after prompt acceptance: %w", result.err))
				runErr = errors.Join(runErr, presenter.finishText())
				return finisher.finish(outcome, runErr)
			}
			streaming, stateErr := runSessionStreaming(result.value)
			if stateErr != nil {
				runErr := errors.Join(stateErr, presenter.finishText())
				if interrupt.started {
					return finishInterrupted(false, runErr)
				}
				return finisher.finish(RunCompleted, runErr)
			}
			idleConfirmed = !streaming
			if idleConfirmed && dependencies.onIdleConfirmed != nil {
				dependencies.onIdleConfirmed()
			}
		}
	}
}

type runInterruptState struct {
	started      bool
	settled      bool
	agentStarted bool
	results      <-chan error
	watchdog     <-chan time.Time
	timer        *time.Timer
	err          error
}

func (s *runInterruptState) start(session runSession, presenter *runPresenter, timeout time.Duration) {
	s.started = true
	presenter.suppressText = true
	s.timer = time.NewTimer(timeout)
	s.watchdog = s.timer.C
	results := make(chan error, 1)
	s.results = results
	go func() {
		results <- session.Abort(context.Background())
	}()
}

func (s *runInterruptState) stopWatchdog() {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.watchdog = nil
}

type runFinisher struct {
	session     runSession
	storage     *store.Store
	stderr      io.Writer
	sessionID   string
	sessionFile string
	logPath     string
	logger      *log.Logger
	crashLogged bool
}

func (f *runFinisher) finishExited(interrupted bool, textErr error) (RunOutcome, error) {
	exitErr := f.session.ExitErr()
	if exitErr == nil {
		exitErr = errors.New("event stream closed")
	}
	f.logCrashTail()
	runErr := errors.Join(fmt.Errorf("pi exited before agent settled: %w", exitErr), textErr)
	outcome := RunCompleted
	if interrupted {
		outcome = RunInterrupted
	}
	return f.finish(outcome, runErr)
}

func (f *runFinisher) logCrashTail() {
	if f.crashLogged {
		return
	}
	f.crashLogged = true
	tail, truncated, err := readFileTail(f.logPath, crashTailSize)
	if err != nil {
		f.logger.Error("read pi stderr after unexpected exit", "stderr_log", f.logPath, "error", err)
		return
	}
	f.logger.Error(
		"pi exited unexpectedly",
		"stderr_log", f.logPath,
		"stderr_tail", tail,
		"stderr_tail_truncated", truncated,
	)
}

func readFileTail(path string, size int64) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	start := max(info.Size()-size, 0)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", false, err
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(contents), "")), start > 0, nil
}

func runSessionStreaming(stateRaw json.RawMessage) (bool, error) {
	var state struct {
		IsStreaming bool `json:"isStreaming"`
	}
	if err := json.Unmarshal(stateRaw, &state); err != nil {
		return false, fmt.Errorf("decode pi streaming state: %w", err)
	}
	return state.IsStreaming, nil
}

// Callers classify the primary error before joining secondary diagnostics.
func classifyRunError(ctx context.Context, err error) (RunOutcome, error) {
	if ctx.Err() != nil {
		return RunInterrupted, withoutContextError(err, ctx.Err())
	}
	return RunCompleted, err
}

func withoutContextError(err, contextErr error) error {
	return withoutExpectedErrors(err, contextErr)
}

func withoutExpectedErrors(err error, expected ...error) error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		kept := make([]error, 0, len(parts))
		for _, part := range parts {
			if filtered := withoutExpectedErrors(part, expected...); filtered != nil {
				kept = append(kept, filtered)
			}
		}
		return errors.Join(kept...)
	}
	for _, target := range expected {
		if target != nil && errors.Is(err, target) {
			return withoutExpectedErrors(errors.Unwrap(err), expected...)
		}
	}
	return err
}

func (f *runFinisher) finish(outcome RunOutcome, runErr error) (RunOutcome, error) {
	return f.finishWithClose(outcome, runErr, false, nil)
}

func (f *runFinisher) finishForced(outcome RunOutcome, runErr error) (RunOutcome, error) {
	return f.finishWithClose(outcome, runErr, true, nil)
}

func (f *runFinisher) finishInterrupt(outcome RunOutcome, runErr error, interrupts <-chan os.Signal) (RunOutcome, error) {
	return f.finishWithClose(outcome, runErr, false, interrupts)
}

func (f *runFinisher) finishWithClose(outcome RunOutcome, runErr error, force bool, interrupts <-chan os.Signal) (RunOutcome, error) {
	closeErr := closeRunSession(f.session, force, interrupts)
	if errors.Is(runErr, pisession.ErrProcessExited) || errors.Is(runErr, pisession.ErrTransportClosed) {
		f.logCrashTail()
	}
	var registryErr error
	if err := f.storage.SetStatus(f.sessionID, store.StatusStopped); err != nil {
		registryErr = fmt.Errorf("record stopped session: %w", err)
	}
	var presentErr error
	{
		var err error
		if f.sessionFile == "" {
			_, err = fmt.Fprintf(f.stderr, "[session] id=%s status=stopped log=%s\n", f.sessionID, f.logPath)
		} else {
			_, err = fmt.Fprintf(f.stderr, "[session] id=%s status=stopped file=%s log=%s\n", f.sessionID, f.sessionFile, f.logPath)
		}
		if err != nil {
			presentErr = fmt.Errorf("write final session details: %w", err)
		}
	}
	return outcome, errors.Join(runErr, closeErr, registryErr, presentErr)
}

func closeRunSession(session runSession, force bool, interrupts <-chan os.Signal) error {
	ctx, cancel := context.WithTimeout(context.Background(), runCleanupTimeout)
	defer cancel()
	if force {
		cancel()
	}
	if interrupts == nil {
		if err := session.Close(ctx); err != nil {
			return fmt.Errorf("stop pi: %w", err)
		}
		return nil
	}

	result := make(chan error, 1)
	go func() {
		result <- session.Close(ctx)
	}()
	select {
	case err := <-result:
		if err != nil {
			return fmt.Errorf("stop pi: %w", err)
		}
		return nil
	case <-interrupts:
		cancel()
		if err := <-result; err != nil {
			return fmt.Errorf("force pi shutdown: %w", err)
		}
		return nil
	}
}

func unknownSessionTypeError(name string, configured map[string]config.SessionType) error {
	names := make([]string, 0, len(configured))
	for configuredName := range configured {
		names = append(names, configuredName)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Errorf("unknown session type %q; no session types are configured", name)
	}
	return fmt.Errorf("unknown session type %q; configured types: %s", name, strings.Join(names, ", "))
}

type runPresenter struct {
	stdout            io.Writer
	stderr            io.Writer
	suppressText      bool
	wroteText         bool
	endsNewline       bool
	finalAssistantErr error
}

func (p *runPresenter) present(event pisession.Event) error {
	switch event.Type {
	case "message_update":
		var payload struct {
			AssistantMessageEvent struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
			} `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(event.Raw, &payload); err != nil {
			return fmt.Errorf("decode pi message update: %w", err)
		}
		if payload.AssistantMessageEvent.Type != "text_delta" || p.suppressText {
			return nil
		}
		if _, err := io.WriteString(p.stdout, payload.AssistantMessageEvent.Delta); err != nil {
			return fmt.Errorf("write assistant text: %w", err)
		}
		if payload.AssistantMessageEvent.Delta != "" {
			p.wroteText = true
			p.endsNewline = strings.HasSuffix(payload.AssistantMessageEvent.Delta, "\n")
		}
		return nil
	case "message_end":
		var payload struct {
			Message struct {
				Role         string `json:"role"`
				StopReason   string `json:"stopReason"`
				ErrorMessage string `json:"errorMessage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(event.Raw, &payload); err != nil {
			return fmt.Errorf("decode pi completed message: %w", err)
		}
		if payload.Message.Role != "assistant" {
			return nil
		}
		p.finalAssistantErr = nil
		if payload.Message.StopReason == "error" {
			p.finalAssistantErr = errors.New("assistant response failed")
			if payload.Message.ErrorMessage != "" {
				p.finalAssistantErr = fmt.Errorf("assistant response failed: %s", payload.Message.ErrorMessage)
			}
		}
	case "tool_execution_start":
		var payload struct {
			ToolName string `json:"toolName"`
		}
		if err := json.Unmarshal(event.Raw, &payload); err != nil {
			return fmt.Errorf("decode pi tool start: %w", err)
		}
		if _, err := fmt.Fprintf(p.stderr, "[tool %s] running\n", payload.ToolName); err != nil {
			return fmt.Errorf("write tool activity: %w", err)
		}
	case "tool_execution_end":
		var payload struct {
			ToolName string `json:"toolName"`
			IsError  bool   `json:"isError"`
		}
		if err := json.Unmarshal(event.Raw, &payload); err != nil {
			return fmt.Errorf("decode pi tool end: %w", err)
		}
		status := "done"
		if payload.IsError {
			status = "error"
		}
		if _, err := fmt.Fprintf(p.stderr, "[tool %s] %s\n", payload.ToolName, status); err != nil {
			return fmt.Errorf("write tool activity: %w", err)
		}
	case "extension_ui_request":
		var payload struct {
			Method  string `json:"method"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(event.Raw, &payload); err != nil {
			return fmt.Errorf("decode pi extension UI request: %w", err)
		}
		switch payload.Method {
		case "notify":
			if _, err := fmt.Fprintf(p.stderr, "[notify] %s\n", payload.Message); err != nil {
				return fmt.Errorf("write notification: %w", err)
			}
		case "select", "confirm", "input", "editor":
			if _, err := fmt.Fprintf(p.stderr, "[warning] pi is waiting for a %s dialog; gibson run cannot answer dialogs; press Ctrl+C to stop\n", payload.Method); err != nil {
				return fmt.Errorf("write dialog warning: %w", err)
			}
		}
	case "extension_error":
		var payload struct {
			ExtensionPath string `json:"extensionPath"`
			Error         string `json:"error"`
		}
		if err := json.Unmarshal(event.Raw, &payload); err != nil {
			return fmt.Errorf("decode pi extension error: %w", err)
		}
		if _, err := fmt.Fprintf(p.stderr, "[error] extension %s: %s\n", payload.ExtensionPath, payload.Error); err != nil {
			return fmt.Errorf("write extension error: %w", err)
		}
	}
	return nil
}

func (p *runPresenter) finishText() error {
	if !p.wroteText || p.endsNewline {
		return nil
	}
	if _, err := io.WriteString(p.stdout, "\n"); err != nil {
		return fmt.Errorf("finish assistant text: %w", err)
	}
	p.endsNewline = true
	return nil
}
