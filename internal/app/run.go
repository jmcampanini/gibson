package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

const runCleanupTimeout = 5 * time.Second

type RunOutcome uint8

const (
	RunCompleted RunOutcome = iota
	RunInterrupted
)

type RunOptions struct {
	Type    string
	Message string
	Stdout  io.Writer
	Stderr  io.Writer
}

type runSession interface {
	Prompt(context.Context, string, string) error
	GetState(context.Context) (json.RawMessage, error)
	Events() <-chan pisession.Event
	Close(context.Context) error
	ExitErr() error
	PID() int
}

type runDependencies struct {
	getwd          func() (string, error)
	resolvePiBin   func(string) (string, error)
	checkPiVersion func(context.Context, string) (pisession.VersionResult, error)
	spawn          func(context.Context, pisession.Config) (runSession, error)
	now            func() time.Time
}

func Run(ctx context.Context, options RunOptions, logger *log.Logger) (RunOutcome, error) {
	return run(ctx, options, logger, runDependencies{
		getwd:          os.Getwd,
		resolvePiBin:   pisession.ResolvePiBin,
		checkPiVersion: pisession.CheckPiVersion,
		spawn: func(ctx context.Context, cfg pisession.Config) (runSession, error) {
			return pisession.Spawn(ctx, cfg)
		},
		now: time.Now,
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

	workingDirectory, err := dependencies.getwd()
	if err != nil {
		return classifyRunError(ctx, fmt.Errorf("determine working directory: %w", err))
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

	piBin, err := dependencies.resolvePiBin(cfg.Server.PiBin)
	if err != nil {
		return classifyRunError(ctx, err)
	}
	piVersion, err := dependencies.checkPiVersion(ctx, piBin)
	if err != nil {
		return classifyRunError(ctx, err)
	}
	if !piVersion.Verified {
		logger.Warn(
			"pi version has not been verified with Gibson",
			"found", piVersion.Found,
			"verified", fmt.Sprintf("0.%d.x", pisession.VerifiedPiMinor),
			"minimum", pisession.MinimumPiVersion,
		)
	}

	storage := store.Open(ws.LaunchCheckout)
	if err := storage.EnsureLayout(); err != nil {
		return RunCompleted, err
	}
	sessionID, err := storage.NewSessionID()
	if err != nil {
		return RunCompleted, fmt.Errorf("generate session id: %w", err)
	}
	logPath := storage.StderrLogPath(sessionID)
	session, err := dependencies.spawn(ctx, pisession.Config{
		PiBin:      piBin,
		SessionID:  sessionID,
		SessionDir: storage.SessionsDir(),
		Cwd:        ws.LaunchCheckout,
		Model:      sessionType.Model,
		Thinking:   sessionType.Thinking,
		ExtraArgs:  sessionType.ExtraArgs,
		StderrPath: logPath,
		Logger:     logger,
	})
	if err != nil {
		return classifyRunError(ctx, err)
	}

	startedAt := dependencies.now().UTC()
	if err := storage.Put(store.Record{
		ID:             sessionID,
		Type:           options.Type,
		Status:         store.StatusLive,
		CreatedAt:      startedAt.Format(time.RFC3339),
		LastActivityAt: startedAt.Format(time.RFC3339),
		PID:            session.PID(),
	}); err != nil {
		closeErr := closeRunSession(session)
		return RunCompleted, errors.Join(fmt.Errorf("record live session: %w", err), closeErr)
	}
	finisher := runFinisher{
		session:   session,
		storage:   storage,
		stderr:    stderr,
		sessionID: sessionID,
		logPath:   logPath,
	}

	stateRaw, err := session.GetState(ctx)
	if err != nil {
		outcome, runErr := classifyRunError(ctx, fmt.Errorf("read pi session state: %w", err))
		return finisher.finish(outcome, runErr)
	}
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
	finishAfterEvent := func(event pisession.Event) (bool, error) {
		settled, eventErr := processEvent(event)
		if eventErr != nil {
			return false, errors.Join(eventErr, presenter.finishText())
		}
		if settled {
			return true, errors.Join(presenter.finalAssistantErr, presenter.finishText())
		}
		return false, nil
	}

	promptResults := make(chan error, 1)
	go func() {
		promptResults <- session.Prompt(ctx, options.Message, "")
	}()

	agentStarted := false
	settledBeforeAcceptance := false
promptWait:
	for {
		select {
		case <-ctx.Done():
			if err := presenter.finishText(); err != nil {
				return finisher.finish(RunCompleted, err)
			}
			return finisher.finish(RunInterrupted, nil)
		case event, open := <-events:
			if !open {
				return finisher.finishExited(presenter.finishText())
			}
			settled, eventErr := processEvent(event)
			if eventErr != nil {
				runErr := errors.Join(eventErr, presenter.finishText())
				return finisher.finish(RunCompleted, runErr)
			}
			if event.Type == "agent_start" {
				agentStarted = true
			}
			settledBeforeAcceptance = settledBeforeAcceptance || settled
		case promptErr := <-promptResults:
			if promptErr != nil {
				outcome, runErr := classifyRunError(ctx, fmt.Errorf("prompt pi: %w", promptErr))
				runErr = errors.Join(runErr, presenter.finishText())
				return finisher.finish(outcome, runErr)
			}
			if err := storage.Touch(sessionID, dependencies.now().UTC()); err != nil {
				runErr := errors.Join(fmt.Errorf("record accepted prompt: %w", err), presenter.finishText())
				return finisher.finish(RunCompleted, runErr)
			}
			break promptWait
		}
	}
	if settledBeforeAcceptance {
		runErr := errors.Join(presenter.finalAssistantErr, presenter.finishText())
		return finisher.finish(RunCompleted, runErr)
	}

	type stateResult struct {
		raw json.RawMessage
		err error
	}
	// Pi marks a normal run streaming before it can process this follow-up command. A false result means the prompt was handled without a run; ordered stdout ensures any preceding events are already queued for the drain below.
	stateResults := make(chan stateResult, 1)
	go func() {
		raw, err := session.GetState(ctx)
		stateResults <- stateResult{raw: raw, err: err}
	}()

	idleConfirmed := false
	for {
		if idleConfirmed {
			select {
			case event, open := <-events:
				if !open {
					return finisher.finishExited(presenter.finishText())
				}
				settled, eventErr := finishAfterEvent(event)
				if eventErr != nil || settled {
					return finisher.finish(RunCompleted, eventErr)
				}
				if event.Type == "agent_start" {
					agentStarted = true
				}
				continue
			default:
				if agentStarted {
					runErr := errors.Join(errors.New("pi became idle before agent_settled"), presenter.finishText())
					return finisher.finish(RunCompleted, runErr)
				}
				return finisher.finish(RunCompleted, presenter.finishText())
			}
		}

		select {
		case <-ctx.Done():
			if err := presenter.finishText(); err != nil {
				return finisher.finish(RunCompleted, err)
			}
			return finisher.finish(RunInterrupted, nil)
		case event, open := <-events:
			if !open {
				return finisher.finishExited(presenter.finishText())
			}
			settled, eventErr := finishAfterEvent(event)
			if eventErr != nil || settled {
				return finisher.finish(RunCompleted, eventErr)
			}
			if event.Type == "agent_start" {
				agentStarted = true
			}
		case result := <-stateResults:
			stateResults = nil
			if result.err != nil {
				outcome, runErr := classifyRunError(ctx, fmt.Errorf("check pi state after prompt acceptance: %w", result.err))
				runErr = errors.Join(runErr, presenter.finishText())
				return finisher.finish(outcome, runErr)
			}
			streaming, stateErr := runSessionStreaming(result.raw)
			if stateErr != nil {
				runErr := errors.Join(stateErr, presenter.finishText())
				return finisher.finish(RunCompleted, runErr)
			}
			idleConfirmed = !streaming
		}
	}
}

type runFinisher struct {
	session     runSession
	storage     *store.Store
	stderr      io.Writer
	sessionID   string
	sessionFile string
	logPath     string
}

func (f *runFinisher) finishExited(textErr error) (RunOutcome, error) {
	exitErr := f.session.ExitErr()
	if exitErr == nil {
		exitErr = errors.New("event stream closed")
	}
	runErr := errors.Join(fmt.Errorf("pi exited before agent settled: %w", exitErr), textErr)
	return f.finish(RunCompleted, runErr)
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

func classifyRunError(ctx context.Context, err error) (RunOutcome, error) {
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, ctx.Err())) {
		return RunInterrupted, nil
	}
	return RunCompleted, err
}

func (f *runFinisher) finish(outcome RunOutcome, runErr error) (RunOutcome, error) {
	closeErr := closeRunSession(f.session)
	var registryErr error
	if closeErr == nil {
		if err := f.storage.SetStatus(f.sessionID, store.StatusStopped); err != nil {
			registryErr = fmt.Errorf("record stopped session: %w", err)
		}
	}
	if closeErr == nil && registryErr == nil {
		if _, err := fmt.Fprintf(f.stderr, "[session] id=%s status=stopped file=%s log=%s\n", f.sessionID, f.sessionFile, f.logPath); err != nil {
			registryErr = fmt.Errorf("write final session details: %w", err)
		}
	}
	return outcome, errors.Join(runErr, closeErr, registryErr)
}

func closeRunSession(session runSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), runCleanupTimeout)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		return fmt.Errorf("stop pi: %w", err)
	}
	return nil
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
		if payload.AssistantMessageEvent.Type != "text_delta" {
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
