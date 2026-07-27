package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jmcampanini/gibson/internal/fakepi/scenarios"
)

const version = "0.82.1"

type config struct {
	sessionID  string
	sessionDir string
}

type response struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    any             `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type sessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type entry struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	ParentID  *string  `json:"parentId"`
	Timestamp string   `json:"timestamp"`
	Message   *message `json:"message,omitempty"`
	Name      string   `json:"name,omitempty"`
}

type message struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	API        string `json:"api,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Usage      *usage `json:"usage,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	Timestamp  int64  `json:"timestamp"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usage struct {
	Input       int       `json:"input"`
	Output      int       `json:"output"`
	CacheRead   int       `json:"cacheRead"`
	CacheWrite  int       `json:"cacheWrite"`
	TotalTokens int       `json:"totalTokens"`
	Cost        usageCost `json:"cost"`
}

type usageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type fakePi struct {
	cfg         config
	sessionFile string
	file        *os.File
	out         io.Writer
	scenario    scenarios.Scenario
	entries     []entry
	leafID      *string
	nextID      uint32
	baseTime    time.Time
	timeOffset  int64
	isStreaming bool
	sessionName string
}

func main() {
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)
	go func() {
		<-term
		os.Exit(143)
	}()

	args := os.Args[1:]
	if hasVersionArg(args) {
		if _, err := fmt.Fprintln(os.Stdout, version); err != nil {
			fmt.Fprintf(os.Stderr, "fakepi: write version: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakepi: %v\n", err)
		os.Exit(2)
	}

	scenarioName := os.Getenv("FAKEPI_SCENARIO")
	if scenarioName == "" {
		scenarioName = "basic"
	}
	scenario, ok := scenarios.Scenarios[scenarioName]
	if !ok {
		fmt.Fprintf(os.Stderr, "fakepi: unsupported FAKEPI_SCENARIO %q\n", scenarioName)
		os.Exit(2)
	}

	if err := run(cfg, scenario, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "fakepi: %v\n", err)
		os.Exit(1)
	}
}

func hasVersionArg(args []string) bool {
	for _, arg := range args {
		if arg == "--version" {
			return true
		}
	}
	return false
}

func parseArgs(args []string) (config, error) {
	var cfg config
	var mode string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--mode":
			value, next, err := optionValue(args, i, "--mode")
			if err != nil {
				return config{}, err
			}
			mode = value
			i = next
		case strings.HasPrefix(arg, "--mode="):
			mode = strings.TrimPrefix(arg, "--mode=")
		case arg == "--session-id":
			value, next, err := optionValue(args, i, "--session-id")
			if err != nil {
				return config{}, err
			}
			cfg.sessionID = value
			i = next
		case strings.HasPrefix(arg, "--session-id="):
			cfg.sessionID = strings.TrimPrefix(arg, "--session-id=")
		case arg == "--session-dir":
			value, next, err := optionValue(args, i, "--session-dir")
			if err != nil {
				return config{}, err
			}
			cfg.sessionDir = value
			i = next
		case strings.HasPrefix(arg, "--session-dir="):
			cfg.sessionDir = strings.TrimPrefix(arg, "--session-dir=")
		}
	}

	if mode == "" {
		return config{}, errors.New("missing required --mode rpc")
	}
	if mode != "rpc" {
		return config{}, fmt.Errorf("--mode must be rpc, got %q", mode)
	}
	if cfg.sessionID == "" {
		return config{}, errors.New("missing required --session-id")
	}
	if cfg.sessionDir == "" {
		return config{}, errors.New("missing required --session-dir")
	}
	return cfg, nil
}

func optionValue(args []string, index int, name string) (string, int, error) {
	if index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "--") {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	return args[index+1], index + 1, nil
}

func run(cfg config, scenario scenarios.Scenario, in io.Reader, out io.Writer) (runErr error) {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if err := os.MkdirAll(cfg.sessionDir, 0o755); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	digest := sha256.Sum256([]byte(cfg.sessionID + "\x00" + cwd))
	sessionFile := filepath.Join(cfg.sessionDir, fmt.Sprintf("%x.jsonl", digest[:12]))
	file, err := os.OpenFile(sessionFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create session file: %w", err)
	}

	pi := &fakePi{
		cfg:         cfg,
		sessionFile: sessionFile,
		file:        file,
		out:         out,
		scenario:    scenario,
		entries:     make([]entry, 0),
		nextID:      1,
		baseTime:    time.Now().UTC().Truncate(time.Millisecond),
	}
	defer func() {
		if err := file.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close session file: %w", err))
		}
	}()

	header := sessionHeader{
		Type:      "session",
		Version:   3,
		ID:        cfg.sessionID,
		Timestamp: pi.baseTime.Format(time.RFC3339Nano),
		CWD:       cwd,
	}
	if err := pi.writeFileRecord(header); err != nil {
		return err
	}
	return pi.readCommands(in)
}

func (p *fakePi) readCommands(in io.Reader) error {
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if handleErr := p.handleLine(line); handleErr != nil {
				return handleErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read command: %w", err)
		}
	}
}

func (p *fakePi) handleLine(line []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil || fields == nil {
		message := "command must be a JSON object"
		if err != nil {
			message = fmt.Sprintf("failed to parse command: %v", err)
		}
		return p.writeResponse(response{Type: "response", Command: "parse", Success: false, Error: message})
	}

	id := cloneRaw(fields["id"])
	var command string
	if rawType, ok := fields["type"]; !ok || json.Unmarshal(rawType, &command) != nil || command == "" {
		return p.writeResponse(response{ID: id, Type: "response", Command: "parse", Success: false, Error: "command type must be a non-empty string"})
	}

	switch command {
	case "get_state":
		return p.writeResponse(p.success(id, command, p.stateData()))
	case "get_entries":
		return p.getEntries(id, fields)
	case "get_session_stats":
		return p.writeResponse(p.success(id, command, p.statsData()))
	case "set_session_name":
		return p.setSessionName(id, fields)
	case "prompt", "steer", "follow_up":
		return p.acceptPrompt(id, command, fields)
	case "abort":
		return p.writeResponse(p.success(id, command, nil))
	default:
		return p.writeResponse(p.failure(id, command, fmt.Sprintf("unsupported command: %s", command)))
	}
}

func (p *fakePi) stateData() map[string]any {
	data := map[string]any{
		"model":                 fakeModel(),
		"thinkingLevel":         "off",
		"isStreaming":           p.isStreaming,
		"isCompacting":          false,
		"steeringMode":          "one-at-a-time",
		"followUpMode":          "one-at-a-time",
		"sessionFile":           p.sessionFile,
		"sessionId":             p.cfg.sessionID,
		"autoCompactionEnabled": true,
		"messageCount":          p.messageCount(),
		"pendingMessageCount":   0,
	}
	if p.sessionName != "" {
		data["sessionName"] = p.sessionName
	}
	return data
}

func (p *fakePi) getEntries(id json.RawMessage, fields map[string]json.RawMessage) error {
	entries := p.entries
	if rawSince, ok := fields["since"]; ok {
		var since string
		if err := json.Unmarshal(rawSince, &since); err != nil || since == "" {
			return p.writeResponse(p.failure(id, "get_entries", "since must be a non-empty string"))
		}
		index := -1
		for i := range p.entries {
			if p.entries[i].ID == since {
				index = i
				break
			}
		}
		if index == -1 {
			return p.writeResponse(p.failure(id, "get_entries", fmt.Sprintf("entry not found: %s", since)))
		}
		entries = p.entries[index+1:]
	}

	if entries == nil {
		entries = make([]entry, 0)
	}
	return p.writeResponse(p.success(id, "get_entries", map[string]any{
		"entries": entries,
		"leafId":  p.leafID,
	}))
}

func (p *fakePi) setSessionName(id json.RawMessage, fields map[string]json.RawMessage) error {
	var name string
	if rawName, ok := fields["name"]; !ok || json.Unmarshal(rawName, &name) != nil {
		return p.writeResponse(p.failure(id, "set_session_name", "name must be a string"))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return p.writeResponse(p.failure(id, "set_session_name", "session name cannot be empty"))
	}

	timestamp := p.nextTimestamp()
	if err := p.appendEntry(entry{
		Type:      "session_info",
		ID:        p.newEntryID(),
		ParentID:  cloneStringPointer(p.leafID),
		Timestamp: timestamp.Format(time.RFC3339Nano),
		Name:      name,
	}); err != nil {
		return err
	}
	p.sessionName = name
	if err := p.writeOutput(map[string]any{"type": "session_info_changed", "name": name}); err != nil {
		return err
	}
	return p.writeResponse(p.success(id, "set_session_name", nil))
}

func (p *fakePi) acceptPrompt(id json.RawMessage, command string, fields map[string]json.RawMessage) error {
	var prompt string
	if rawMessage, ok := fields["message"]; !ok || json.Unmarshal(rawMessage, &prompt) != nil {
		return p.writeResponse(p.failure(id, command, "message must be a string"))
	}

	timestamp := p.nextTimestamp()
	userMessage := message{Role: "user", Content: prompt, Timestamp: timestamp.UnixMilli()}
	if err := p.appendEntry(entry{
		Type:      "message",
		ID:        p.newEntryID(),
		ParentID:  cloneStringPointer(p.leafID),
		Timestamp: timestamp.Format(time.RFC3339Nano),
		Message:   &userMessage,
	}); err != nil {
		return err
	}
	if err := p.writeResponse(p.success(id, command, nil)); err != nil {
		return err
	}
	return p.playScenario(userMessage)
}

func (p *fakePi) playScenario(userMessage message) error {
	var text string
	var finalMessage message

	for _, step := range p.scenario.Steps {
		if step.Delay > 0 {
			time.Sleep(step.Delay)
		}
		switch step.Type {
		case scenarios.AgentStart:
			p.isStreaming = true
			if err := p.writeOutput(map[string]any{"type": "agent_start"}); err != nil {
				return err
			}
		case scenarios.MessageStart:
			started := p.assistantMessage("")
			if err := p.writeOutput(map[string]any{"type": "message_start", "message": started}); err != nil {
				return err
			}
		case scenarios.TextDelta:
			text += step.Text
			partial := p.assistantMessage(text)
			if err := p.writeOutput(map[string]any{
				"type":    "message_update",
				"message": partial,
				"assistantMessageEvent": map[string]any{
					"type":         "text_delta",
					"contentIndex": 0,
					"delta":        step.Text,
					"partial":      partial,
				},
			}); err != nil {
				return err
			}
		case scenarios.MessageEnd:
			finalMessage = p.assistantMessage(text)
			finalMessage.StopReason = "stop"
			timestamp := p.nextTimestamp()
			finalMessage.Timestamp = timestamp.UnixMilli()
			if err := p.appendEntry(entry{
				Type:      "message",
				ID:        p.newEntryID(),
				ParentID:  cloneStringPointer(p.leafID),
				Timestamp: timestamp.Format(time.RFC3339Nano),
				Message:   &finalMessage,
			}); err != nil {
				return err
			}
			if err := p.writeOutput(map[string]any{"type": "message_end", "message": finalMessage}); err != nil {
				return err
			}
		case scenarios.AgentEnd:
			if err := p.writeOutput(map[string]any{
				"type":      "agent_end",
				"messages":  []message{userMessage, finalMessage},
				"willRetry": false,
			}); err != nil {
				return err
			}
		case scenarios.AgentSettled:
			p.isStreaming = false
			if err := p.writeOutput(map[string]any{"type": "agent_settled"}); err != nil {
				return err
			}
		default:
			return fmt.Errorf("scenario %q has unsupported step %q", p.scenario.Name, step.Type)
		}
	}
	return nil
}

func (p *fakePi) assistantMessage(text string) message {
	return message{
		Role:     "assistant",
		Content:  []textContent{{Type: "text", Text: text}},
		API:      "fake-messages",
		Provider: "fakepi",
		Model:    "fakepi-basic",
		Usage: &usage{
			Input:       24,
			Output:      8,
			CacheRead:   0,
			CacheWrite:  0,
			TotalTokens: 32,
			Cost: usageCost{
				Input:  0.000024,
				Output: 0.000032,
				Total:  0.000056,
			},
		},
		Timestamp: p.baseTime.Add(time.Duration(p.timeOffset+1) * time.Millisecond).UnixMilli(),
	}
}

func (p *fakePi) statsData() map[string]any {
	userMessages := 0
	assistantMessages := 0
	for _, entry := range p.entries {
		if entry.Message == nil {
			continue
		}
		switch entry.Message.Role {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}

	input := assistantMessages * 24
	output := assistantMessages * 8
	total := input + output
	return map[string]any{
		"sessionFile":       p.sessionFile,
		"sessionId":         p.cfg.sessionID,
		"userMessages":      userMessages,
		"assistantMessages": assistantMessages,
		"toolCalls":         0,
		"toolResults":       0,
		"totalMessages":     userMessages + assistantMessages,
		"tokens": map[string]any{
			"input":      input,
			"output":     output,
			"cacheRead":  0,
			"cacheWrite": 0,
			"total":      total,
		},
		"cost": float64(assistantMessages) * 0.000056,
		"contextUsage": map[string]any{
			"tokens":        total,
			"contextWindow": 128000,
			"percent":       float64(total) / 128000 * 100,
		},
	}
}

func fakeModel() map[string]any {
	return map[string]any{
		"id":            "fakepi-basic",
		"name":          "Fake Pi Basic",
		"api":           "fake-messages",
		"provider":      "fakepi",
		"baseUrl":       "http://127.0.0.1",
		"reasoning":     false,
		"input":         []string{"text"},
		"contextWindow": 128000,
		"maxTokens":     8192,
		"cost": map[string]float64{
			"input":      1,
			"output":     4,
			"cacheRead":  0,
			"cacheWrite": 0,
		},
	}
}

func (p *fakePi) messageCount() int {
	count := 0
	for _, entry := range p.entries {
		if entry.Message != nil {
			count++
		}
	}
	return count
}

func (p *fakePi) newEntryID() string {
	id := fmt.Sprintf("%08x", p.nextID)
	p.nextID++
	return id
}

func (p *fakePi) nextTimestamp() time.Time {
	p.timeOffset++
	return p.baseTime.Add(time.Duration(p.timeOffset) * time.Millisecond)
}

func (p *fakePi) appendEntry(value entry) error {
	if err := p.writeFileRecord(value); err != nil {
		return err
	}
	p.entries = append(p.entries, value)
	leaf := value.ID
	p.leafID = &leaf
	return nil
}

func (p *fakePi) writeFileRecord(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode session record: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := p.file.Write(encoded); err != nil {
		return fmt.Errorf("write session record: %w", err)
	}
	return nil
}

func (p *fakePi) success(id json.RawMessage, command string, data any) response {
	return response{ID: id, Type: "response", Command: command, Success: true, Data: data}
}

func (p *fakePi) failure(id json.RawMessage, command, message string) response {
	return response{ID: id, Type: "response", Command: command, Success: false, Error: message}
}

func (p *fakePi) writeResponse(value response) error {
	return p.writeOutput(value)
}

func (p *fakePi) writeOutput(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode protocol output: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := p.out.Write(encoded); err != nil {
		return fmt.Errorf("write protocol output: %w", err)
	}
	return nil
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
