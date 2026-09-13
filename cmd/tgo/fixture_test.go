package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReplayHarnessFixtures(t *testing.T) {
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	tests := []harnessFixture{
		{Harness: "opencode", Name: "opencode prompt", At: base, Pane: "%1", PID: 1, ProcessStart: "a", SessionID: "oc", NativeEvent: "chat.message", RawPayload: json.RawMessage(`{"sessionId":"oc"}`), ExpectedState: agentStateWorking},
		{Harness: "codex", Name: "codex permission", At: base, Pane: "%2", PID: 2, ProcessStart: "b", SessionID: "cx", NativeEvent: "PermissionRequest", RawPayload: json.RawMessage(`{"session_id":"cx"}`), ExpectedState: agentStateWaiting},
		{Harness: "claude", Name: "claude stop", At: base, Pane: "%3", PID: 3, ProcessStart: "c", SessionID: "cl", NativeEvent: "Stop", RawPayload: json.RawMessage(`{"session_id":"cl"}`), ExpectedState: agentStateIdle},
		{Harness: "gemini", Name: "gemini session end", At: base, Pane: "%4", PID: 4, ProcessStart: "d", SessionID: "gm", NativeEvent: "SessionEnd", RawPayload: json.RawMessage(`{"session_id":"gm"}`), ExpectedState: agentStateStopped},
		{Harness: "copilot", Name: "copilot prompt", At: base, Pane: "%5", PID: 5, ProcessStart: "e", SessionID: "co", NativeEvent: "userPromptSubmitted", RawPayload: json.RawMessage(`{"sessionId":"co"}`), ExpectedState: agentStateWorking},
	}
	for _, fixture := range tests {
		result, err := replayHarnessFixture(fixture)
		if err != nil {
			t.Fatalf("replay %s: %v", fixture.Name, err)
		}
		if result.State != fixture.ExpectedState || result.Authority != authorityLifecycle {
			t.Errorf("%s = (%q, %q), want (%q, lifecycle)", fixture.Name, result.State, result.Authority, fixture.ExpectedState)
		}
	}
}
