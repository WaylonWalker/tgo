package main

import "testing"

func TestAgentRunStatusUsesLifecycleState(t *testing.T) {
	tests := map[string]string{
		"session-start": "working",
		"user-prompt":   "working",
		"agent-stop":    "idle",
		"question":      "waiting",
		"session-end":   "stopped",
	}
	for kind, want := range tests {
		if got := agentRunStatus(kind, "sleeping"); got != want {
			t.Errorf("agentRunStatus(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestAgentStopDoesNotClearQuestionState(t *testing.T) {
	registry := newAgentRegistry()
	registry.apply(agentEventInput{Harness: "copilot", Kind: "question", SessionID: "session", RunID: "run"})
	registry.apply(agentEventInput{Harness: "copilot", Kind: "agent-stop", SessionID: "session", RunID: "run"})

	run := registry.Harnesses["copilot"].Sessions["session"].Runs["run"]
	if run.Status != "question" {
		t.Fatalf("question state was overwritten: got %q", run.Status)
	}
}
