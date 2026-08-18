package main

import (
	"testing"
	"time"
)

func TestBuildHarnessUsageExcludesRecordedRunsWithoutLiveProcess(t *testing.T) {
	registry := newAgentRegistry()
	registry.apply(agentEventInput{
		Harness:   "copilot",
		Kind:      "agent-stop",
		SessionID: "session-1",
		RunID:     "%99",
		Pane:      "%99",
		At:        time.Now(),
	})

	rows := buildHarnessUsage("copilot", nil, nil, registry)
	if len(rows) != 0 {
		t.Fatalf("stale registry run should not render: got %d rows", len(rows))
	}
}

func TestBuildAgentsUsageIncludesEachHarness(t *testing.T) {
	panes := []paneInfo{
		{SessionName: "dev", WindowIndex: "1", WindowName: "copilot", PaneID: "%1", PanePID: 100, PaneIndex: "0"},
		{SessionName: "dev", WindowIndex: "2", WindowName: "opencode", PaneID: "%2", PanePID: 200, PaneIndex: "0"},
	}
	procs := []procStat{
		{PID: 100, PPID: 1, Comm: "copilot", Command: "copilot"},
		{PID: 200, PPID: 1, Comm: "opencode", Command: "opencode"},
	}

	rows := buildAgentsUsage(panes, procs, newAgentRegistry())
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}
	if rows[0].AgentHarness != "copilot" || rows[1].AgentHarness != "opencode" {
		t.Fatalf("harness labels = %q, %q", rows[0].AgentHarness, rows[1].AgentHarness)
	}
}
