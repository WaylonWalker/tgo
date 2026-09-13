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

const (
	agentRegistryVersion  = 2
	maxAgentEvents        = 128
	maxRawEventBytes      = 16 * 1024
	maxAgentRegistryBytes = 8 * 1024 * 1024
)

type agentState string

const (
	agentStateWorking agentState = "working"
	agentStateWaiting agentState = "waiting"
	agentStateIdle    agentState = "idle"
	agentStateStopped agentState = "stopped"
	agentStateUnknown agentState = "unknown"
)

type stateAuthority string

const (
	authorityLifecycle stateAuthority = "lifecycle"
	authorityScreen    stateAuthority = "screen"
	authorityLiveness  stateAuthority = "liveness"
	authorityUnknown   stateAuthority = "unknown"
)

// agentIdentity is the generation identity of a harness process. A pane is
// only a location. It is deliberately not sufficient to identify a lifecycle
// stream because tmux panes and PIDs are both reusable.
type agentIdentity struct {
	Harness      string `json:"harness,omitempty"`
	TmuxServer   string `json:"tmux_server,omitempty"`
	TmuxSession  string `json:"tmux_session,omitempty"`
	Pane         string `json:"pane,omitempty"`
	PID          int    `json:"pid,omitempty"`
	ProcessStart string `json:"process_start,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	TurnID       string `json:"turn_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
}

func (identity agentIdentity) generationKey() string {
	parts := []string{
		identity.Harness,
		identity.TmuxServer,
		identity.TmuxSession,
		identity.Pane,
		fmt.Sprintf("pid:%d", identity.PID),
		identity.ProcessStart,
		identity.SessionID,
		identity.TurnID,
		identity.RunID,
	}
	return strings.Join(parts, "|")
}

// identityConflict reports evidence that two events cannot belong to the
// same process/session generation. Empty fields are unknown, not a mismatch.
func identityConflict(left, right agentIdentity) bool {
	if left.Harness != "" && right.Harness != "" && left.Harness != right.Harness {
		return true
	}
	for _, values := range [][2]string{
		{left.TmuxServer, right.TmuxServer},
		{left.TmuxSession, right.TmuxSession},
		{left.Pane, right.Pane},
		{left.ProcessStart, right.ProcessStart},
		{left.SessionID, right.SessionID},
		{left.TurnID, right.TurnID},
		{left.RunID, right.RunID},
	} {
		if values[0] != "" && values[1] != "" && values[0] != values[1] {
			return true
		}
	}
	return left.PID != 0 && right.PID != 0 && left.PID != right.PID
}

func identityMatches(left, right agentIdentity) bool {
	return !identityConflict(left, right)
}

// agentRegistry is the on-disk record of harness lifecycle events. It is
// intentionally harness-neutral so hook integrations do not need a database.
type agentRegistry struct {
	Version   int                     `json:"version"`
	Harnesses map[string]agentHarness `json:"harnesses"`
	Active    map[string]agentActive  `json:"active,omitempty"`
}

type agentActive struct {
	Harness     string    `json:"harness"`
	TmuxServer  string    `json:"tmux_server,omitempty"`
	TmuxSession string    `json:"tmux_session,omitempty"`
	Pane        string    `json:"pane,omitempty"`
	PID         int       `json:"pid,omitempty"`
	Generation  string    `json:"generation,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	RunKey      string    `json:"run_key,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type agentHarness struct {
	Sessions map[string]agentSession `json:"sessions"`
}

type agentSession struct {
	ID        string              `json:"id"`
	Metadata  json.RawMessage     `json:"metadata,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
	Runs      map[string]agentRun `json:"runs"`
}

type agentRun struct {
	ID             string          `json:"id"`
	Pane           string          `json:"pane,omitempty"`
	PID            int             `json:"pid,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	Status         string          `json:"status"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	StartedAt      time.Time       `json:"started_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	EndedAt        *time.Time      `json:"ended_at,omitempty"`
	Events         []agentEvent    `json:"events"`
	Identity       agentIdentity   `json:"identity"`
	State          agentState      `json:"state,omitempty"`
	LifecycleState agentState      `json:"lifecycle_state,omitempty"`
	LifecycleAt    time.Time       `json:"lifecycle_at,omitempty"`
	NativeKind     string          `json:"native_kind,omitempty"`
	Legacy         bool            `json:"legacy,omitempty"`
	CWD            string          `json:"cwd,omitempty"`
	TranscriptID   string          `json:"transcript_id,omitempty"`
	PermissionID   string          `json:"permission_id,omitempty"`
	QuestionID     string          `json:"question_id,omitempty"`
}

type agentEvent struct {
	Kind         string          `json:"kind"`
	NativeKind   string          `json:"native_kind,omitempty"`
	At           time.Time       `json:"at"`
	Accepted     bool            `json:"accepted"`
	Identity     agentIdentity   `json:"identity,omitempty"`
	CWD          string          `json:"cwd,omitempty"`
	TranscriptID string          `json:"transcript_id,omitempty"`
	PermissionID string          `json:"permission_id,omitempty"`
	QuestionID   string          `json:"question_id,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
}

// agentEventInput is accepted from a hook as JSON or as scalar command flags.
// Metadata fields must be JSON objects; data may be any valid JSON value.
type agentEventInput struct {
	Harness         string          `json:"harness"`
	Kind            string          `json:"kind"`
	NativeKind      string          `json:"native_kind,omitempty"`
	SessionID       string          `json:"sessionId"`
	RunID           string          `json:"runId,omitempty"`
	TurnID          string          `json:"turnId,omitempty"`
	Pane            string          `json:"pane,omitempty"`
	PID             int             `json:"pid,omitempty"`
	ProcessStart    string          `json:"processStart,omitempty"`
	TmuxServer      string          `json:"tmuxServer,omitempty"`
	TmuxSession     string          `json:"tmuxSession,omitempty"`
	CWD             string          `json:"cwd,omitempty"`
	TranscriptID    string          `json:"transcriptId,omitempty"`
	PermissionID    string          `json:"permissionId,omitempty"`
	QuestionID      string          `json:"questionId,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	At              time.Time       `json:"at,omitempty"`
	SessionMetadata json.RawMessage `json:"session_metadata,omitempty"`
	RunMetadata     json.RawMessage `json:"run_metadata,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
	RawPayload      json.RawMessage `json:"raw_payload,omitempty"`
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
	data, err := readBoundedAgentFile(s.path, maxAgentRegistryBytes)
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
	if registry.Version == 1 {
		migrateAgentRegistry(&registry)
	} else if registry.Version != agentRegistryVersion {
		return agentRegistry{}, fmt.Errorf("unsupported agent registry version %d", registry.Version)
	}
	normalizeAgentRegistry(&registry)
	return registry, nil
}

func readBoundedAgentFile(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("file %s exceeds %d-byte safety limit", path, limit)
	}
	return data, nil
}

func (s *agentRegistryStore) Apply(input agentEventInput) error {
	normalizeAgentEventInput(&input)
	if err := validateAgentEvent(input); err != nil {
		return err
	}
	return s.withLock(func() error {
		if err := s.backupLegacyRegistry(); err != nil {
			return err
		}
		registry, err := s.load()
		if err != nil {
			return err
		}
		registry.apply(input)
		return s.save(registry)
	})
}

func (s *agentRegistryStore) backupLegacyRegistry() error {
	data, err := readBoundedAgentFile(s.path, maxAgentRegistryBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read legacy agent registry: %w", err)
	}
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil || header.Version != 1 {
		return nil
	}
	backup := s.path + ".v1.bak"
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect legacy agent registry backup: %w", err)
	}
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return fmt.Errorf("write legacy agent registry backup: %w", err)
	}
	return nil
}

func (s *agentRegistryStore) withLock(fn func() error) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create agent state dir: %w", err)
	}

	lockPath := s.path + ".lock"
	lock, err := acquireFileLock(lockPath, 5*time.Second, "agent registry")
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()
	return fn()
}

func (s *agentRegistryStore) save(registry agentRegistry) error {
	normalizeAgentRegistry(&registry)
	data, err := marshalAgentRegistry(registry)
	if err != nil {
		return err
	}
	for len(data) > maxAgentRegistryBytes {
		if !pruneOldestAgentHistory(&registry) {
			return fmt.Errorf("agent registry exceeds %d-byte safety limit", maxAgentRegistryBytes)
		}
		data, err = marshalAgentRegistry(registry)
		if err != nil {
			return err
		}
	}

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

func marshalAgentRegistry(registry agentRegistry) ([]byte, error) {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal agent registry: %w", err)
	}
	return append(data, '\n'), nil
}

func pruneOldestAgentHistory(registry *agentRegistry) bool {
	protected := make(map[string]bool, len(registry.Active))
	for _, active := range registry.Active {
		if active.Harness != "" && active.SessionID != "" && active.RunKey != "" {
			protected[agentRunReferenceKey(active.Harness, active.SessionID, active.RunKey)] = true
		}
	}

	var oldestHarness, oldestSession, oldestRun string
	var oldestAt time.Time
	foundRun := false
	for harnessName, harness := range registry.Harnesses {
		for sessionID, session := range harness.Sessions {
			for runID, run := range session.Runs {
				if protected[agentRunReferenceKey(harnessName, sessionID, runID)] {
					continue
				}
				at := run.UpdatedAt
				if at.IsZero() {
					at = run.StartedAt
				}
				if !foundRun || at.Before(oldestAt) {
					foundRun = true
					oldestHarness = harnessName
					oldestSession = sessionID
					oldestRun = runID
					oldestAt = at
				}
			}
		}
	}
	if foundRun {
		harness := registry.Harnesses[oldestHarness]
		session := harness.Sessions[oldestSession]
		delete(session.Runs, oldestRun)
		if len(session.Runs) == 0 {
			delete(harness.Sessions, oldestSession)
		} else {
			harness.Sessions[oldestSession] = session
		}
		if len(harness.Sessions) == 0 {
			delete(registry.Harnesses, oldestHarness)
		} else {
			registry.Harnesses[oldestHarness] = harness
		}
		pruneDanglingAgentActives(registry)
		return true
	}

	var eventHarness, eventSession, eventRun string
	oldestEvent := -1
	var oldestEventAt time.Time
	for harnessName, harness := range registry.Harnesses {
		for sessionID, session := range harness.Sessions {
			for runID, run := range session.Runs {
				if len(run.Events) == 0 {
					continue
				}
				for index, event := range run.Events {
					at := event.At
					if at.IsZero() {
						at = run.UpdatedAt
					}
					if oldestEvent == -1 || at.Before(oldestEventAt) {
						eventHarness = harnessName
						eventSession = sessionID
						eventRun = runID
						oldestEvent = index
						oldestEventAt = at
					}
				}
			}
		}
	}
	if oldestEvent >= 0 {
		harness := registry.Harnesses[eventHarness]
		session := harness.Sessions[eventSession]
		run := session.Runs[eventRun]
		run.Events = append(run.Events[:oldestEvent], run.Events[oldestEvent+1:]...)
		session.Runs[eventRun] = run
		harness.Sessions[eventSession] = session
		registry.Harnesses[eventHarness] = harness
		return true
	}

	var oldestActive string
	var oldestActiveAt time.Time
	foundActive := false
	for key, active := range registry.Active {
		if !foundActive || active.UpdatedAt.Before(oldestActiveAt) {
			oldestActive = key
			oldestActiveAt = active.UpdatedAt
			foundActive = true
		}
	}
	if foundActive {
		delete(registry.Active, oldestActive)
		return true
	}
	return false
}

func agentRunReferenceKey(harness, session, run string) string {
	return strings.Join([]string{harness, session, run}, "\x00")
}

func pruneDanglingAgentActives(registry *agentRegistry) {
	for key, active := range registry.Active {
		harness, ok := registry.Harnesses[active.Harness]
		if !ok {
			delete(registry.Active, key)
			continue
		}
		session, ok := harness.Sessions[active.SessionID]
		if !ok {
			delete(registry.Active, key)
			continue
		}
		if _, ok := session.Runs[active.RunKey]; !ok {
			delete(registry.Active, key)
		}
	}
}

func newAgentRegistry() agentRegistry {
	return agentRegistry{
		Version:   agentRegistryVersion,
		Harnesses: make(map[string]agentHarness),
		Active:    make(map[string]agentActive),
	}
}

func normalizeAgentRegistry(registry *agentRegistry) {
	if registry.Version == 0 {
		registry.Version = agentRegistryVersion
	}
	if registry.Harnesses == nil {
		registry.Harnesses = make(map[string]agentHarness)
	}
	if registry.Active == nil {
		registry.Active = make(map[string]agentActive)
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
			for runID, run := range session.Runs {
				if run.ID == "" {
					run.ID = runID
				}
				if run.Identity.Harness == "" {
					run.Identity = agentIdentity{
						Harness:   name,
						Pane:      run.Pane,
						PID:       run.PID,
						SessionID: session.ID,
						RunID:     run.ID,
					}
				}
				if run.State == "" {
					run.State = legacyAgentState(run.Status)
				}
				if run.LifecycleState == "" {
					run.LifecycleState = run.State
				}
				if run.Events == nil {
					run.Events = make([]agentEvent, 0)
				}
				session.Runs[runID] = run
			}
			harness.Sessions[id] = session
		}
		registry.Harnesses[name] = harness
	}
}

func migrateAgentRegistry(registry *agentRegistry) {
	registry.Version = agentRegistryVersion
	if registry.Active == nil {
		registry.Active = make(map[string]agentActive)
	}
	for harnessName, harness := range registry.Harnesses {
		for sessionID, session := range harness.Sessions {
			for runID, run := range session.Runs {
				run.Legacy = true
				run.Identity = agentIdentity{
					Harness:   harnessName,
					Pane:      run.Pane,
					PID:       run.PID,
					SessionID: sessionID,
					RunID:     runID,
				}
				run.State = legacyAgentState(run.Status)
				run.LifecycleState = run.State
				run.LifecycleAt = run.UpdatedAt
				for index := range run.Events {
					if run.Events[index].At.IsZero() {
						run.Events[index].At = run.UpdatedAt
					}
					run.Events[index].Accepted = true
				}
				session.Runs[runID] = run
			}
			harness.Sessions[sessionID] = session
		}
		registry.Harnesses[harnessName] = harness
	}
}

func legacyAgentState(kind string) agentState {
	switch kind {
	case "session-start", "user-prompt", "working", "running":
		return agentStateWorking
	case "question", "waiting", "permission-prompt":
		return agentStateWaiting
	case "agent-stop", "completed", "idle", "sleeping":
		return agentStateIdle
	case "session-end", "failed", "cancelled", "stopped", "zombie":
		return agentStateStopped
	default:
		return agentStateUnknown
	}
}

func (registry *agentRegistry) apply(input agentEventInput) {
	normalizeAgentEventInput(&input)
	normalizeAgentRegistry(registry)
	if input.At.IsZero() {
		input.At = time.Now().UTC()
	}
	transition := normalizeLifecycleEvent(input)
	identity := agentIdentityFromInput(input)

	harness := registry.Harnesses[input.Harness]
	if harness.Sessions == nil {
		harness.Sessions = make(map[string]agentSession)
	}
	session := harness.Sessions[input.SessionID]
	if session.ID == "" {
		session.ID = input.SessionID
		session.CreatedAt = input.At
	}
	if session.UpdatedAt.IsZero() || input.At.After(session.UpdatedAt) {
		session.UpdatedAt = input.At
	}
	if input.SessionMetadata != nil {
		session.Metadata = cloneJSON(input.SessionMetadata)
	}
	if session.Runs == nil {
		session.Runs = make(map[string]agentRun)
	}

	runKey := agentRunKey(session.Runs, input.RunID, identity)
	run := session.Runs[runKey]
	if run.ID == "" {
		run.ID = input.RunID
		run.StartedAt = input.At
	}
	if run.Identity.Harness == "" {
		run.Identity = identity
	}
	run.Identity = mergeAgentIdentity(run.Identity, identity)
	accepted, rejectReason := registry.acceptLifecycleEvent(input, transition, identity)
	if accepted && (run.UpdatedAt.IsZero() || !input.At.Before(run.UpdatedAt)) {
		run.UpdatedAt = input.At
		if input.Kind != "agent-stop" || run.Status != "question" {
			run.Status = input.Kind
		}
		run.NativeKind = transition.NativeKind
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
	if input.CWD != "" {
		run.CWD = input.CWD
	}
	if input.TranscriptID != "" {
		run.TranscriptID = input.TranscriptID
	}
	if input.PermissionID != "" {
		run.PermissionID = input.PermissionID
	}
	if input.QuestionID != "" {
		run.QuestionID = input.QuestionID
	}
	if accepted && transition.State != agentStateUnknown && (run.LifecycleAt.IsZero() || !input.At.Before(run.LifecycleAt)) {
		run.State = transition.State
		run.LifecycleState = transition.State
		run.LifecycleAt = input.At
	}
	if accepted && isTerminalAgentEvent(input.Kind) {
		endedAt := input.At
		run.EndedAt = &endedAt
	}
	data := input.RawPayload
	if len(data) == 0 {
		data = input.Data
	}
	data = limitRawEvent(data)
	run.Events = append(run.Events, agentEvent{
		Kind:         input.Kind,
		NativeKind:   transition.NativeKind,
		At:           input.At,
		Accepted:     accepted,
		Identity:     identity,
		CWD:          input.CWD,
		TranscriptID: input.TranscriptID,
		PermissionID: input.PermissionID,
		QuestionID:   input.QuestionID,
		Data:         cloneJSON(data),
	})
	if len(run.Events) > maxAgentEvents {
		run.Events = append([]agentEvent(nil), run.Events[len(run.Events)-maxAgentEvents:]...)
	}
	if !accepted && rejectReason != "" {
		run.Summary = joinAgentReason(run.Summary, "ignored: "+rejectReason)
	}
	session.Runs[runKey] = run
	harness.Sessions[input.SessionID] = session
	registry.Harnesses[input.Harness] = harness
	if accepted && input.Pane != "" {
		key := activeAgentKey(identity)
		if key != "" {
			active := registry.Active[key]
			active.Harness = input.Harness
			active.TmuxServer = input.TmuxServer
			active.TmuxSession = input.TmuxSession
			active.Pane = input.Pane
			active.PID = input.PID
			active.Generation = identity.generationKey()
			active.SessionID = input.SessionID
			active.RunKey = runKey
			if active.UpdatedAt.IsZero() || !input.At.Before(active.UpdatedAt) {
				active.UpdatedAt = input.At
			}
			registry.Active[key] = active
		}
	}
}

func agentIdentityFromInput(input agentEventInput) agentIdentity {
	return agentIdentity{
		Harness:      input.Harness,
		TmuxServer:   input.TmuxServer,
		TmuxSession:  input.TmuxSession,
		Pane:         input.Pane,
		PID:          input.PID,
		ProcessStart: input.ProcessStart,
		SessionID:    input.SessionID,
		TurnID:       input.TurnID,
		RunID:        input.RunID,
	}
}

func mergeAgentIdentity(existing, incoming agentIdentity) agentIdentity {
	if existing.Harness == "" {
		existing.Harness = incoming.Harness
	}
	if existing.TmuxServer == "" {
		existing.TmuxServer = incoming.TmuxServer
	}
	if existing.TmuxSession == "" {
		existing.TmuxSession = incoming.TmuxSession
	}
	if existing.Pane == "" {
		existing.Pane = incoming.Pane
	}
	if existing.PID == 0 {
		existing.PID = incoming.PID
	}
	if existing.ProcessStart == "" {
		existing.ProcessStart = incoming.ProcessStart
	}
	if existing.SessionID == "" {
		existing.SessionID = incoming.SessionID
	}
	if existing.TurnID == "" {
		existing.TurnID = incoming.TurnID
	}
	if existing.RunID == "" {
		existing.RunID = incoming.RunID
	}
	return existing
}

func agentRunKey(runs map[string]agentRun, requested string, identity agentIdentity) string {
	if requested == "" {
		requested = identity.RunID
	}
	if current, ok := runs[requested]; !ok || identityMatches(current.Identity, identity) {
		return requested
	}
	key := requested + "#" + compactIdentityKey(identity)
	if _, ok := runs[key]; ok {
		return key
	}
	return key
}

func compactIdentityKey(identity agentIdentity) string {
	value := identity.generationKey()
	value = strings.NewReplacer("|", "_", "/", "_", " ", "_").Replace(value)
	if len(value) > 96 {
		value = value[len(value)-96:]
	}
	return value
}

func activeAgentKey(identity agentIdentity) string {
	if identity.Pane == "" {
		return ""
	}
	return strings.Join([]string{
		identity.Harness,
		identity.TmuxServer,
		identity.TmuxSession,
		identity.Pane,
		fmt.Sprintf("pid:%d", identity.PID),
		identity.ProcessStart,
	}, "|")
}

func (registry *agentRegistry) acceptLifecycleEvent(input agentEventInput, transition lifecycleTransition, identity agentIdentity) (bool, string) {
	if transition.State == agentStateUnknown {
		return true, ""
	}
	key := activeAgentKey(identity)
	if key == "" {
		return true, ""
	}
	active, ok := registry.Active[key]
	if !ok {
		return true, ""
	}
	if active.SessionID == input.SessionID {
		if !active.UpdatedAt.IsZero() && input.At.Before(active.UpdatedAt) {
			return false, "older than active lifecycle event"
		}
		return true, ""
	}
	if transition.SessionStart {
		if active.UpdatedAt.IsZero() || !input.At.Before(active.UpdatedAt) {
			return true, ""
		}
		return false, "older session start"
	}
	return false, "native session no longer active"
}

func joinAgentReason(summary, reason string) string {
	if summary == "" {
		return reason
	}
	return summary
}

func limitRawEvent(raw json.RawMessage) json.RawMessage {
	if len(raw) <= maxRawEventBytes {
		return cloneJSON(raw)
	}
	return json.RawMessage(fmt.Sprintf(`{"truncated":true,"original_bytes":%d}`, len(raw)))
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
		return fmt.Errorf("agent command required (try: tgo agent ingest <harness> <event>)")
	}
	switch args[0] {
	case "event":
		input, err := parseAgentEventArgs(args[1:], stdin)
		if err != nil {
			return err
		}
		store, err := openAgentRegistryStore()
		if err != nil {
			return err
		}
		return store.Apply(input)
	case "ingest":
		input, err := parseAgentIngestArgs(args[1:], stdin)
		if err != nil {
			return err
		}
		store, err := openAgentRegistryStore()
		if err != nil {
			return err
		}
		return store.Apply(input)
	case "explain":
		return runAgentExplain(args[1:], stdin, os.Stdout)
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
}

func parseAgentEventArgs(args []string, stdin io.Reader) (agentEventInput, error) {
	flags := flag.NewFlagSet("tgo agent event", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var fromFlags agentEventInput
	var jsonInput string
	flags.StringVar(&fromFlags.Harness, "harness", "", "harness name")
	flags.StringVar(&fromFlags.Kind, "kind", "", "event kind")
	flags.StringVar(&fromFlags.NativeKind, "native-kind", "", "native event kind")
	flags.StringVar(&fromFlags.SessionID, "session", "", "session ID")
	flags.StringVar(&fromFlags.RunID, "run", "", "run ID")
	flags.StringVar(&fromFlags.TurnID, "turn", "", "turn ID")
	flags.StringVar(&fromFlags.Pane, "pane", "", "tmux pane target")
	flags.IntVar(&fromFlags.PID, "pid", 0, "agent process ID")
	flags.StringVar(&fromFlags.ProcessStart, "process-start", "", "process start token")
	flags.StringVar(&fromFlags.TmuxServer, "tmux-server", "", "tmux server identity")
	flags.StringVar(&fromFlags.TmuxSession, "tmux-session", "", "tmux session identity")
	flags.StringVar(&fromFlags.CWD, "cwd", "", "working directory")
	flags.StringVar(&fromFlags.TranscriptID, "transcript", "", "transcript or run ID")
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
		data, err := io.ReadAll(io.LimitReader(stdin, maxRawEventBytes*2))
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
	for _, field := range []struct {
		name string
		set  bool
		dst  *string
		src  string
	}{
		{"native-kind", visited["native-kind"], &input.NativeKind, fromFlags.NativeKind},
		{"turn", visited["turn"], &input.TurnID, fromFlags.TurnID},
		{"process-start", visited["process-start"], &input.ProcessStart, fromFlags.ProcessStart},
		{"tmux-server", visited["tmux-server"], &input.TmuxServer, fromFlags.TmuxServer},
		{"tmux-session", visited["tmux-session"], &input.TmuxSession, fromFlags.TmuxSession},
		{"cwd", visited["cwd"], &input.CWD, fromFlags.CWD},
		{"transcript", visited["transcript"], &input.TranscriptID, fromFlags.TranscriptID},
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
	input.RawPayload = append(json.RawMessage(nil), bytes.TrimSpace(data)...)
	if len(input.Data) == 0 {
		input.Data = cloneJSON(input.RawPayload)
	}
	extractNativeAgentFields(input)
	return nil
}

func parseAgentIngestArgs(args []string, stdin io.Reader) (agentEventInput, error) {
	if len(args) < 2 || strings.TrimSpace(args[0]) == "" || strings.TrimSpace(args[1]) == "" {
		return agentEventInput{}, fmt.Errorf("agent ingest requires <harness> <event>")
	}
	harnessName := strings.TrimSpace(args[0])
	nativeKind := strings.TrimSpace(args[1])
	flags := flag.NewFlagSet("tgo agent ingest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var pane, session, run, turn, processStart, tmuxServer, tmuxSession, cwd, transcript, summary, jsonInput string
	var pid int
	flags.StringVar(&pane, "pane", "", "tmux pane target")
	flags.StringVar(&session, "session", "", "session ID")
	flags.StringVar(&run, "run", "", "run ID")
	flags.StringVar(&turn, "turn", "", "turn ID")
	flags.IntVar(&pid, "pid", 0, "agent process ID")
	flags.StringVar(&processStart, "process-start", "", "process start token")
	flags.StringVar(&tmuxServer, "tmux-server", "", "tmux server identity")
	flags.StringVar(&tmuxSession, "tmux-session", "", "tmux session identity")
	flags.StringVar(&cwd, "cwd", "", "working directory")
	flags.StringVar(&transcript, "transcript", "", "transcript or run ID")
	flags.StringVar(&summary, "summary", "", "human-readable summary")
	flags.StringVar(&jsonInput, "json", "", "event JSON")
	if err := flags.Parse(args[2:]); err != nil {
		return agentEventInput{}, err
	}
	if flags.NArg() != 0 {
		return agentEventInput{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	var input agentEventInput
	visited := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if visited["json"] {
		if strings.TrimSpace(jsonInput) == "" {
			return agentEventInput{}, fmt.Errorf("--json must not be empty")
		}
		if err := decodeAgentEventJSON([]byte(jsonInput), &input); err != nil {
			return agentEventInput{}, err
		}
	} else if shouldReadAgentEventStdin(stdin) {
		data, err := io.ReadAll(io.LimitReader(stdin, maxRawEventBytes*2))
		if err != nil {
			return agentEventInput{}, fmt.Errorf("read event JSON: %w", err)
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := decodeAgentEventJSON(data, &input); err != nil {
				return agentEventInput{}, err
			}
		}
	}
	if input.Harness != "" && input.Harness != harnessName {
		return agentEventInput{}, fmt.Errorf("harness argument conflicts with event JSON")
	}
	input.Harness = harnessName
	input.Kind = nativeKind
	input.NativeKind = nativeKind
	if session != "" && input.SessionID != "" && session != input.SessionID {
		return agentEventInput{}, fmt.Errorf("--session conflicts with event JSON")
	}
	if session != "" {
		input.SessionID = session
	}
	if run != "" {
		input.RunID = run
	}
	if turn != "" {
		input.TurnID = turn
	}
	if pane != "" {
		input.Pane = pane
	}
	if pid != 0 {
		input.PID = pid
	}
	if processStart != "" {
		input.ProcessStart = processStart
	}
	if tmuxServer != "" {
		input.TmuxServer = tmuxServer
	}
	if tmuxSession != "" {
		input.TmuxSession = tmuxSession
	}
	if cwd != "" {
		input.CWD = cwd
	}
	if transcript != "" {
		input.TranscriptID = transcript
	}
	if summary != "" {
		input.Summary = summary
	}
	normalizeAgentEventInput(&input)
	if err := validateAgentEvent(input); err != nil {
		return agentEventInput{}, err
	}
	return input, nil
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
