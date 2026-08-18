package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestFindCopilotPanesFindsDescendantsAndPrefersRunningProcess(t *testing.T) {
	panes := []paneInfo{
		{SessionName: "dev", WindowIndex: "1", WindowName: "editor", PaneID: "%10", PanePID: 100, PaneIndex: "0"},
		{SessionName: "ops", WindowIndex: "2", WindowName: "logs", PaneID: "%11", PanePID: 200, PaneIndex: "1"},
	}
	procs := []procStat{
		{PID: 100, PPID: 1, Comm: "zsh"},
		{PID: 101, PPID: 100, State: "S", Comm: "node", Command: "node /opt/copilot/index.js"},
		{PID: 102, PPID: 100, State: "R+", Comm: "copilot"},
		{PID: 200, PPID: 1, Comm: "zsh"},
		{PID: 201, PPID: 200, State: "S", Comm: "vim"},
	}

	rows := findCopilotPanes(panes, procs)
	if len(rows) != 1 {
		t.Fatalf("pane count mismatch: got %d want 1", len(rows))
	}
	if rows[0].Target() != "%10" {
		t.Fatalf("target mismatch: got %q want %%10", rows[0].Target())
	}
	if rows[0].PID != 102 || rows[0].Status != "running" {
		t.Fatalf("expected running Copilot PID 102, got PID %d with status %q", rows[0].PID, rows[0].Status)
	}
}

func TestProcessStatus(t *testing.T) {
	tests := map[string]string{
		"R+": "running",
		"S":  "sleeping",
		"D":  "disk-sleep",
		"T":  "stopped",
		"Z":  "zombie",
		"I":  "idle",
		"":   "unknown",
	}
	for state, want := range tests {
		if got := processStatus(state); got != want {
			t.Errorf("processStatus(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestBuildCopilotUsagePreservesPaneAndProcessDetails(t *testing.T) {
	panes := []paneInfo{
		{SessionName: "dev", WindowIndex: "1", WindowName: "editor", PaneID: "%10", PanePID: 100, PaneIndex: "0", Active: true},
	}
	procs := []procStat{
		{PID: 100, PPID: 1, Comm: "zsh"},
		{PID: 101, PPID: 100, State: "S", Comm: "copilot", Command: "copilot --resume"},
	}

	rows := buildCopilotUsage(panes, procs)
	if len(rows) != 1 {
		t.Fatalf("row count mismatch: got %d want 1", len(rows))
	}
	row := rows[0]
	if row.Target != "%10" || row.SessionName != "dev" || !row.Active {
		t.Fatalf("pane details mismatch: %+v", row)
	}
	if row.CopilotStatus != "sleeping" || row.CopilotCommand != "copilot --resume" {
		t.Fatalf("Copilot details mismatch: %+v", row)
	}
}

func TestBuildHarnessUsageIncludesOpenCodeProcessesAndRecordedRuns(t *testing.T) {
	panes := []paneInfo{
		{SessionName: "dev", WindowIndex: "1", WindowName: "editor", PaneID: "%10", PanePID: 100, PaneIndex: "0"},
	}
	procs := []procStat{
		{PID: 100, PPID: 1, Comm: "zsh"},
		{PID: 101, PPID: 100, State: "S", Comm: "opencode", Command: "opencode run"},
	}
	registry := newAgentRegistry()
	registry.apply(agentEventInput{
		Harness: "opencode", Kind: "session-start", SessionID: "oc-1",
		RunID: "run-1", Pane: "%10", Summary: "reviewing",
	})

	rows := buildHarnessUsage("opencode", panes, procs, registry)
	if len(rows) != 1 {
		t.Fatalf("row count mismatch: got %d want 1", len(rows))
	}
	if rows[0].AgentStatus != "working" || rows[0].AgentCommand != "reviewing" {
		t.Fatalf("OpenCode row did not merge process and registry details: %+v", rows[0])
	}
}

func TestCopilotStatusGlyph(t *testing.T) {
	if got := copilotStatusGlyph("sleeping", 0); got != "󰒲" {
		t.Fatalf("sleeping glyph mismatch: got %q", got)
	}
	if got := copilotStatusGlyph("running", 0); got != "󰐊" {
		t.Fatalf("running glyph mismatch: got %q", got)
	}
}

func TestCopilotStatusStyle(t *testing.T) {
	foreground, _, _ := copilotStatusStyle("running").Decompose()
	if foreground != tcell.ColorGreen {
		t.Fatalf("running status color mismatch: got %v want %v", foreground, tcell.ColorGreen)
	}
}

func TestHarnessProcessExcludesTgoPicker(t *testing.T) {
	proc := procStat{Comm: "tgo", Command: "/home/u_walkews/go/bin/tgo copilot"}
	if isHarnessProcess("copilot", proc) {
		t.Fatal("tgo copilot picker should not match as a Copilot process")
	}
}
