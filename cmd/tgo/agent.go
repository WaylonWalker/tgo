package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const agentRegistryVersion = 1

// agentRegistry is the on-disk record of harness lifecycle events. It is
// intentionally harness-neutral so hook integrations do not need a database.
type agentRegistry struct {
	Version   int                     `json:"version"`
	Harnesses map[string]agentHarness `json:"harnesses"`
}

type agentHarness struct {
	Sessions map[string]agentSession `json:"sessions"`
}

type agentSession struct {
	ID        string          `json:"id"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Runs      map[string]agentRun `json:"runs"`
}

type agentRun struct {
	ID        string          `json:"id"`
	Pane      string          `json:"pane,omitempty"`
	PID       int             `json:"pid,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Status    string          `json:"status"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Events    []agentEvent    `json:"events"`
}

type agentEvent struct {
	Kind string          `json:"kind"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data,omitempty"`
}

// agentEventInput is accepted from a hook as JSON or as scalar command flags.
// Metadata fields must be JSON objects; data may be any valid JSON value.
type agentEventInput struct {
	Harness         string          `json:"harness"`
	Kind            string          `json:"kind"`
	SessionID       string          `json:"sessionId"`
	RunID           string          `json:"runId,omitempty"`
	Pane            string          `json:"pane,omitempty"`
	PID             int             `json:"pid,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	At              time.Time       `json:"at,omitempty"`
	SessionMetadata json.RawMessage `json:"session_metadata,omitempty"`
	RunMetadata     json.RawMessage `json:"run_metadata,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
}

type agentRegistryStore struct {
	path string
}

func openAgentRegistryStore() (*agentRegistryStore, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("home dir: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return &agentRegistryStore{path: filepath.Join(stateHome, "tgo", "agents.json")}, nil
}

func (s *agentRegistryStore) Load() (agentRegistry, error) {
	return s.load()
}

func (s *agentRegistryStore) load() (agentRegistry, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newAgentRegistry(), nil
		}
		return agentRegistry{}, fmt.Errorf("read agent registry: %w", err)
	}

	var registry agentRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return agentRegistry{}, fmt.Errorf("decode agent registry: %w", err)
	}
	if registry.Version != agentRegistryVersion {
		return agentRegistry{}, fmt.Errorf("unsupported agent registry version %d", registry.Version)
	}
	normalizeAgentRegistry(&registry)
	return registry, nil
}

func (s *agentRegistryStore) Apply(input agentEventInput) error {
	normalizeAgentEventInput(&input)
	if err := validateAgentEvent(input); err != nil {
		return err
	}
	return s.withLock(func() error {
		registry, err := s.load()
		if err != nil {
			return err
		}
		registry.apply(input)
		return s.save(registry)
	})
}

func (s *agentRegistryStore) withLock(fn func() error) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create agent state dir: %w", err)
	}

	lockPath := s.path + ".lock"
	deadline := time.Now().Add(5 * time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			defer func() {
				_ = lock.Close()
				_ = os.Remove(lockPath)
			}()
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("lock agent registry: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lock agent registry: timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *agentRegistryStore) save(registry agentRegistry) error {
	normalizeAgentRegistry(&registry)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent registry: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	temp, err := os.CreateTemp(dir, ".agents-*.tmp")
	if err != nil {
		return fmt.Errorf("create agent registry temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set agent registry permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write agent registry: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync agent registry: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close agent registry: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace agent registry: %w", err)
	}
	return nil
}

func newAgentRegistry() agentRegistry {
	return agentRegistry{
		Version:   agentRegistryVersion,
		Harnesses: make(map[string]agentHarness),
	}
}

func normalizeAgentRegistry(registry *agentRegistry) {
	if registry.Version == 0 {
		registry.Version = agentRegistryVersion
	}
	if registry.Harnesses == nil {
		registry.Harnesses = make(map[string]agentHarness)
	}
	for name, harness := range registry.Harnesses {
		if harness.Sessions == nil {
			harness.Sessions = make(map[string]agentSession)
		}
		for id, session := range harness.Sessions {
			if session.ID == "" {
				session.ID = id
			}
			if session.Runs == nil {
				session.Runs = make(map[string]agentRun)
			}
			harness.Sessions[id] = session
		}
		registry.Harnesses[name] = harness
	}
}

func (registry *agentRegistry) apply(input agentEventInput) {
	normalizeAgentRegistry(registry)
	if input.At.IsZero() {
		input.At = time.Now().UTC()
	}

	harness := registry.Harnesses[input.Harness]
	if harness.Sessions == nil {
		harness.Sessions = make(map[string]agentSession)
	}
	session := harness.Sessions[input.SessionID]
	if session.ID == "" {
		session.ID = input.SessionID
		session.CreatedAt = input.At
	}
	session.UpdatedAt = input.At
	if input.SessionMetadata != nil {
		session.Metadata = cloneJSON(input.SessionMetadata)
	}
	if session.Runs == nil {
		session.Runs = make(map[string]agentRun)
	}

	run := session.Runs[input.RunID]
	if run.ID == "" {
		run.ID = input.RunID
		run.StartedAt = input.At
	}
	run.UpdatedAt = input.At
	if input.Kind != "agent-stop" || run.Status != "question" {
		run.Status = input.Kind
	}
	if input.Pane != "" {
		run.Pane = input.Pane
	}
	if input.PID != 0 {
		run.PID = input.PID
	}
	if input.Summary != "" {
		run.Summary = input.Summary
	} else if run.Summary == "" {
		run.Summary = agentEventDescription(input.Data)
	}
	if input.RunMetadata != nil {
		run.Metadata = cloneJSON(input.RunMetadata)
	}
	if isTerminalAgentEvent(input.Kind) {
		endedAt := input.At
		run.EndedAt = &endedAt
	}
	run.Events = append(run.Events, agentEvent{
		Kind: input.Kind,
		At:   input.At,
		Data: cloneJSON(input.Data),
	})
	session.Runs[input.RunID] = run
	harness.Sessions[input.SessionID] = session
	registry.Harnesses[input.Harness] = harness
}

func isTerminalAgentEvent(kind string) bool {
	switch kind {
	case "completed", "failed", "cancelled", "stopped", "session-end":
		return true
	default:
		return false
	}
}

func validateAgentEvent(input agentEventInput) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"harness", input.Harness},
		{"kind", input.Kind},
		{"sessionId", input.SessionID},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if err := validateJSONObject("session_metadata", input.SessionMetadata); err != nil {
		return err
	}
	if err := validateJSONObject("run_metadata", input.RunMetadata); err != nil {
		return err
	}
	if err := validateJSON("data", input.Data); err != nil {
		return err
	}
	return nil
}

func normalizeAgentEventInput(input *agentEventInput) {
	input.Harness = strings.TrimSpace(input.Harness)
	input.Kind = strings.TrimSpace(input.Kind)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.RunID = strings.TrimSpace(input.RunID)
	input.Pane = strings.TrimSpace(input.Pane)
	if input.RunID == "" {
		switch {
		case input.Pane != "":
			input.RunID = input.Pane
		case input.PID != 0:
			input.RunID = fmt.Sprintf("pid:%d", input.PID)
		default:
			input.RunID = input.SessionID
		}
	}
}

func validateJSONObject(name string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	return nil
}

func validateJSON(name string, raw json.RawMessage) error {
	if len(raw) == 0 || json.Valid(raw) {
		return nil
	}
	return fmt.Errorf("%s must be valid JSON", name)
}

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func agentEventDescription(data json.RawMessage) string {
	var payload struct {
		InitialPrompt string `json:"initialPrompt"`
		Prompt        string `json:"prompt"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	description := payload.InitialPrompt
	if description == "" {
		description = payload.Prompt
	}
	description = strings.Join(strings.Fields(description), " ")
	const maxRunes = 72
	runes := []rune(description)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return description
}

func runAgentCommand(args []string, stdin io.Reader) error {
	if len(args) == 0 {
		return fmt.Errorf("agent command required (try: tgo agent event --help)")
	}
	if args[0] != "event" {
		return fmt.Errorf("unknown agent command %q", args[0])
	}
	input, err := parseAgentEventArgs(args[1:], stdin)
	if err != nil {
		return err
	}
	store, err := openAgentRegistryStore()
	if err != nil {
		return err
	}
	return store.Apply(input)
}

func parseAgentEventArgs(args []string, stdin io.Reader) (agentEventInput, error) {
	flags := flag.NewFlagSet("tgo agent event", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var fromFlags agentEventInput
	var jsonInput string
	flags.StringVar(&fromFlags.Harness, "harness", "", "harness name")
	flags.StringVar(&fromFlags.Kind, "kind", "", "event kind")
	flags.StringVar(&fromFlags.SessionID, "session", "", "session ID")
	flags.StringVar(&fromFlags.RunID, "run", "", "run ID")
	flags.StringVar(&fromFlags.Pane, "pane", "", "tmux pane target")
	flags.IntVar(&fromFlags.PID, "pid", 0, "agent process ID")
	flags.StringVar(&fromFlags.Summary, "summary", "", "human-readable summary")
	flags.StringVar(&jsonInput, "json", "", "event JSON")
	if err := flags.Parse(args); err != nil {
		return agentEventInput{}, err
	}
	if flags.NArg() != 0 {
		return agentEventInput{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	visited := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	var input agentEventInput
	if visited["json"] {
		if strings.TrimSpace(jsonInput) == "" {
			return agentEventInput{}, fmt.Errorf("--json must not be empty")
		}
		if err := decodeAgentEventJSON([]byte(jsonInput), &input); err != nil {
			return agentEventInput{}, err
		}
	} else if shouldReadAgentEventStdin(stdin) {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return agentEventInput{}, fmt.Errorf("read event JSON: %w", err)
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := decodeAgentEventJSON(data, &input); err != nil {
				return agentEventInput{}, err
			}
		}
	}
	for _, field := range []struct {
		name string
		set  bool
		dst  *string
		src  string
	}{
		{"harness", visited["harness"], &input.Harness, fromFlags.Harness},
		{"kind", visited["kind"], &input.Kind, fromFlags.Kind},
		{"session", visited["session"], &input.SessionID, fromFlags.SessionID},
		{"run", visited["run"], &input.RunID, fromFlags.RunID},
		{"pane", visited["pane"], &input.Pane, fromFlags.Pane},
		{"summary", visited["summary"], &input.Summary, fromFlags.Summary},
	} {
		if !field.set {
			continue
		}
		if *field.dst != "" && *field.dst != field.src {
			return agentEventInput{}, fmt.Errorf("--%s conflicts with event JSON", field.name)
		}
		*field.dst = field.src
	}
	if visited["pid"] {
		if input.PID != 0 && input.PID != fromFlags.PID {
			return agentEventInput{}, fmt.Errorf("--pid conflicts with event JSON")
		}
		input.PID = fromFlags.PID
	}
	normalizeAgentEventInput(&input)
	if err := validateAgentEvent(input); err != nil {
		return agentEventInput{}, err
	}
	return input, nil
}

func shouldReadAgentEventStdin(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	info, err := file.Stat()
	return err != nil || info.Mode()&os.ModeCharDevice == 0
}

func decodeAgentEventJSON(data []byte, input *agentEventInput) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(input); err != nil {
		return fmt.Errorf("decode event JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode event JSON: expected one JSON object")
	}
	var legacy struct {
		SessionID string `json:"session_id"`
		RunID     string `json:"run_id"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("decode event JSON aliases: %w", err)
	}
	if input.SessionID == "" {
		input.SessionID = legacy.SessionID
	}
	if input.RunID == "" {
		input.RunID = legacy.RunID
	}
	if len(input.Data) == 0 {
		input.Data = append(json.RawMessage(nil), bytes.TrimSpace(data)...)
	}
	return nil
}

type agentRunReference struct {
	SessionID string
	Run       agentRun
}

func agentRunsForHarness(registry agentRegistry, name string) []agentRunReference {
	harness, ok := registry.Harnesses[name]
	if !ok {
		return nil
	}
	runs := make([]agentRunReference, 0)
	for sessionID, session := range harness.Sessions {
		for _, run := range session.Runs {
			runs = append(runs, agentRunReference{SessionID: sessionID, Run: run})
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].Run.UpdatedAt.Equal(runs[j].Run.UpdatedAt) {
			return runs[i].Run.UpdatedAt.After(runs[j].Run.UpdatedAt)
		}
		if runs[i].SessionID != runs[j].SessionID {
			return runs[i].SessionID < runs[j].SessionID
		}
		return runs[i].Run.ID < runs[j].Run.ID
	})
	return runs
}
