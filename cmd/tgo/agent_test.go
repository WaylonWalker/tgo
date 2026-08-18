package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentRegistryPersistsAndUpdatesRunBySessionAndPane(t *testing.T) {
	store := &agentRegistryStore{path: filepath.Join(t.TempDir(), "state", "agents.json")}
	started := agentEventInput{
		Harness:   "copilot",
		Kind:      "session-start",
		SessionID: "session-1",
		Pane:      "%4",
		PID:       1234,
		At:        time.Date(2026, 8, 18, 20, 0, 0, 0, time.UTC),
		Data:      json.RawMessage(`{"hook":"started"}`),
	}
	if err := store.Apply(started); err != nil {
		t.Fatalf("apply start: %v", err)
	}
	if err := store.Apply(agentEventInput{
		Harness:   "copilot",
		Kind:      "agent-stop",
		SessionID: "session-1",
		Pane:      "%4",
		PID:       1234,
		At:        started.At.Add(time.Minute),
	}); err != nil {
		t.Fatalf("apply stop: %v", err)
	}

	registry, err := store.Load()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	run := registry.Harnesses["copilot"].Sessions["session-1"].Runs["%4"]
	if run.Status != "agent-stop" || run.Pane != "%4" || run.PID != 1234 {
		t.Fatalf("run not updated: %+v", run)
	}
	if run.EndedAt != nil || len(run.Events) != 2 {
		t.Fatalf("lifecycle event history missing: %+v", run)
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read persisted registry: %v", err)
	}
	if !strings.Contains(string(data), `"version": 1`) {
		t.Fatalf("missing registry version: %s", data)
	}
}

func TestOpenAgentRegistryStoreUsesXDGStateHome(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	store, err := openAgentRegistryStore()
	if err != nil {
		t.Fatalf("open registry store: %v", err)
	}
	want := filepath.Join(stateHome, "tgo", "agents.json")
	if store.path != want {
		t.Fatalf("registry path = %q, want %q", store.path, want)
	}
}

func TestParseAgentEventArgsMergesFlagsAndJSONPayload(t *testing.T) {
	input, err := parseAgentEventArgs(
		[]string{"--harness", "opencode", "--kind", "session-start", "--pane", "%9", "--pid", "456"},
		strings.NewReader(`{"sessionId":"oc-1","summary":"working","extra":{"accepted":true}}`),
	)
	if err != nil {
		t.Fatalf("parse event: %v", err)
	}
	if input.Harness != "opencode" || input.Kind != "session-start" || input.SessionID != "oc-1" {
		t.Fatalf("scalar fields not merged: %+v", input)
	}
	if input.RunID != "%9" || input.Pane != "%9" || input.PID != 456 {
		t.Fatalf("run identity not derived from pane: %+v", input)
	}
	if !json.Valid(input.Data) || !strings.Contains(string(input.Data), `"extra"`) {
		t.Fatalf("payload was not retained: %s", input.Data)
	}
}

func TestParseAgentEventArgsRejectsUnknownFlagAndConflicts(t *testing.T) {
	if _, err := parseAgentEventArgs([]string{"--unknown"}, strings.NewReader("")); err == nil {
		t.Fatal("unknown flag was accepted")
	}
	if _, err := parseAgentEventArgs(
		[]string{"--harness", "copilot", "--kind", "session-start", "extra"},
		strings.NewReader(`{"sessionId":"s"}`),
	); err == nil {
		t.Fatal("positional argument was accepted")
	}
	if _, err := parseAgentEventArgs(
		[]string{"--harness", "copilot", "--kind", "session-start", "--json", `{"harness":"opencode","sessionId":"s"}`},
		strings.NewReader(""),
	); err == nil {
		t.Fatal("conflicting flag and JSON were accepted")
	}
}

func TestAgentEventDescriptionUsesInitialPrompt(t *testing.T) {
	got := agentEventDescription(json.RawMessage(`{"initialPrompt":"  Fix\n the  session picker  ","prompt":"ignored"}`))
	if got != "Fix the session picker" {
		t.Fatalf("description = %q", got)
	}
}

func TestAgentEventDescriptionTruncatesPrompt(t *testing.T) {
	prompt := strings.Repeat("x", 80)
	got := agentEventDescription(json.RawMessage(`{"prompt":"` + prompt + `"}`))
	if len([]rune(got)) != 72 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated description = %q", got)
	}
}
