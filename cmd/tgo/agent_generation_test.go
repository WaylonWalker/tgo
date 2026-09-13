package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLifecycleFromOldProcessCannotOverrideNewProcessInSamePane(t *testing.T) {
	registry := newAgentRegistry()
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	registry.apply(agentEventInput{
		Harness: "codex", Kind: "SessionStart", NativeKind: "SessionStart", SessionID: "old",
		RunID: "pane", Pane: "%7", PID: 100, ProcessStart: "start-1", At: base,
	})
	registry.apply(agentEventInput{
		Harness: "codex", Kind: "Stop", NativeKind: "Stop", SessionID: "old",
		RunID: "pane", Pane: "%7", PID: 100, ProcessStart: "start-1", At: base.Add(time.Second),
	})
	registry.apply(agentEventInput{
		Harness: "codex", Kind: "SessionStart", NativeKind: "SessionStart", SessionID: "new",
		RunID: "pane", Pane: "%7", PID: 200, ProcessStart: "start-2", At: base.Add(2 * time.Second),
	})
	// A late prompt from the old generation must remain history only.
	registry.apply(agentEventInput{
		Harness: "codex", Kind: "UserPromptSubmit", NativeKind: "UserPromptSubmit", SessionID: "old",
		RunID: "pane", Pane: "%7", PID: 100, ProcessStart: "start-1", At: base.Add(3 * time.Second),
	})

	runs := registry.Harnesses["codex"].Sessions["new"].Runs
	var newRun agentRun
	for _, run := range runs {
		newRun = run
	}
	evidence := resolveAgentEvidence(
		setupDefinition("codex"),
		agentIdentity{Harness: "codex", Pane: "%7", PID: 200, ProcessStart: "start-2"},
		agentRunsForHarness(registry, "codex"),
		"",
		base.Add(4*time.Second),
	)
	if newRun.State != agentStateIdle {
		t.Fatalf("new process state = %q, want idle", newRun.State)
	}
	if evidence.State != agentStateIdle || evidence.Authority != authorityLifecycle {
		t.Fatalf("resolved evidence = (%q, %q), want (idle, lifecycle)", evidence.State, evidence.Authority)
	}
}

func TestNativeSessionChangeRejectsLateLifecycleEvent(t *testing.T) {
	registry := newAgentRegistry()
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	apply := func(session, event string, at time.Time) {
		registry.apply(agentEventInput{
			Harness: "opencode", Kind: event, NativeKind: event, SessionID: session,
			RunID: "pane", Pane: "%8", PID: 300, ProcessStart: "same", At: at,
		})
	}
	apply("session-1", "session.created", base)
	apply("session-2", "session.created", base.Add(time.Second))
	apply("session-1", "chat.message", base.Add(2*time.Second))
	evidence := resolveAgentEvidence(
		setupDefinition("opencode"),
		agentIdentity{Harness: "opencode", Pane: "%8", PID: 300, ProcessStart: "same"},
		agentRunsForHarness(registry, "opencode"),
		"",
		base.Add(3*time.Second),
	)
	if evidence.State != agentStateIdle {
		t.Fatalf("late old session changed state to %q", evidence.State)
	}
	if evidence.Run == nil || evidence.Run.Identity.SessionID != "session-2" {
		t.Fatalf("selected stale session: %+v", evidence.Run)
	}
}

func TestLegacyPaneOnlyLifecycleIsNotAuthoritativeWhenStartTokenIsKnown(t *testing.T) {
	registry := newAgentRegistry()
	registry.apply(agentEventInput{
		Harness: "claude", Kind: "user-prompt", SessionID: "legacy", RunID: "%9", Pane: "%9",
	})
	evidence := resolveAgentEvidence(
		setupDefinition("claude"),
		agentIdentity{Harness: "claude", Pane: "%9", PID: 400, ProcessStart: "known"},
		agentRunsForHarness(registry, "claude"),
		"unrecognized output",
		time.Now().UTC(),
	)
	if evidence.State != agentStateUnknown || evidence.Authority != authorityUnknown {
		t.Fatalf("legacy pane-only evidence = (%q, %q), want unknown", evidence.State, evidence.Authority)
	}
}

func TestStaleLifecycleFallsBackToRecognizedScreen(t *testing.T) {
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	registry := newAgentRegistry()
	registry.apply(agentEventInput{
		Harness: "opencode", Kind: "user-prompt", SessionID: "s", RunID: "r", Pane: "%1",
		PID: 9, ProcessStart: "p", At: base,
	})
	evidence := resolveAgentEvidence(
		setupDefinition("opencode"),
		agentIdentity{Harness: "opencode", Pane: "%1", PID: 9, ProcessStart: "p"},
		agentRunsForHarness(registry, "opencode"),
		"finished\n›",
		base.Add(3*time.Minute),
	)
	if evidence.State != agentStateIdle || evidence.Authority != authorityScreen {
		t.Fatalf("stale lifecycle evidence = (%q, %q), want (idle, screen)", evidence.State, evidence.Authority)
	}
}

func TestLegacyRegistryMigrationCreatesBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")
	store := &agentRegistryStore{path: path}
	legacy := `{"version":1,"harnesses":{"codex":{"sessions":{"s":{"id":"s","runs":{"r":{"id":"r","pane":"%1","status":"user-prompt"}}}}}}}`
	if err := writeAtomicFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy registry: %v", err)
	}
	if err := store.Apply(agentEventInput{Harness: "codex", Kind: "Stop", SessionID: "s", RunID: "r", Pane: "%1"}); err != nil {
		t.Fatalf("apply migrated event: %v", err)
	}
	if _, err := os.Stat(path + ".v1.bak"); err != nil {
		t.Fatalf("legacy backup missing: %v", err)
	}
	registry, err := store.Load()
	if err != nil {
		t.Fatalf("load migrated registry: %v", err)
	}
	if registry.Version != agentRegistryVersion {
		t.Fatalf("registry version = %d, want %d", registry.Version, agentRegistryVersion)
	}
}
