package main

import "testing"

func TestBuildPaneUsageAggregatesPaneSubtrees(t *testing.T) {
	panes := []paneInfo{
		{SessionName: "dev", WindowIndex: "1", WindowName: "web", PaneID: "%10", PanePID: 100, PaneIndex: "0", Active: true},
		{SessionName: "dev", WindowIndex: "1", WindowName: "web", PaneID: "%11", PanePID: 200, PaneIndex: "1"},
		{SessionName: "dev", WindowIndex: "2", WindowName: "docs", PaneID: "%12", PanePID: 300, PaneIndex: "0"},
	}
	procs := []procStat{
		{PID: 100, PPID: 1, CPU: 0.5, RSS: 1000, Comm: "bash"},
		{PID: 101, PPID: 100, CPU: 5, RSS: 2000, Comm: "vim"},
		{PID: 102, PPID: 100, CPU: 20, RSS: 3000, Comm: "node"},
		{PID: 103, PPID: 102, CPU: 10, RSS: 500, Comm: "rg"},
		{PID: 200, PPID: 1, CPU: 1, RSS: 1000, Comm: "bash"},
		{PID: 201, PPID: 200, CPU: 15, RSS: 8000, Comm: "python"},
		{PID: 300, PPID: 1, CPU: 2, RSS: 900, Comm: "less"},
	}

	rows := buildPaneUsage(panes, procs)
	if len(rows) != 3 {
		t.Fatalf("pane count mismatch: got %d want 3", len(rows))
	}

	web := usageByTarget(rows, "%10")
	if !web.Active {
		t.Fatalf("expected %%10 to be active")
	}
	if web.CPU != 35.5 {
		t.Fatalf("cpu mismatch: got %.1f want 35.5", web.CPU)
	}
	if web.RSS != 6500 {
		t.Fatalf("rss mismatch: got %d want 6500", web.RSS)
	}
	if web.TopCPUProcess != "node" {
		t.Fatalf("top cpu process mismatch: got %q want %q", web.TopCPUProcess, "node")
	}
	if web.TopMemProcess != "node" {
		t.Fatalf("top mem process mismatch: got %q want %q", web.TopMemProcess, "node")
	}

	python := usageByTarget(rows, "%11")
	if python.CPU != 16 {
		t.Fatalf("cpu mismatch: got %.1f want 16.0", python.CPU)
	}
	if python.RSS != 9000 {
		t.Fatalf("rss mismatch: got %d want 9000", python.RSS)
	}
	if python.TopMemProcess != "python" {
		t.Fatalf("top mem process mismatch: got %q want %q", python.TopMemProcess, "python")
	}
}

func TestSortPaneUsageByMode(t *testing.T) {
	rows := []windowUsage{
		{Target: "%1", SessionName: "dev", WindowIndex: "1", PaneIndex: "0", CPU: 20, RSS: 5000},
		{Target: "%2", SessionName: "dev", WindowIndex: "10", PaneIndex: "1", CPU: 80, RSS: 2000},
		{Target: "%3", SessionName: "dev", WindowIndex: "2", PaneIndex: "0", CPU: 80, RSS: 2000},
		{Target: "%4", SessionName: "dev", WindowIndex: "3", PaneIndex: "0", CPU: 10, RSS: 9000},
	}

	sortPaneUsage(rows, usageModeCPU)
	if got := []string{rows[0].Target, rows[1].Target, rows[2].Target, rows[3].Target}; !sameStrings(got, []string{"%3", "%2", "%1", "%4"}) {
		t.Fatalf("cpu sort mismatch: got %v", got)
	}

	sortPaneUsage(rows, usageModeMem)
	if got := []string{rows[0].Target, rows[1].Target, rows[2].Target, rows[3].Target}; !sameStrings(got, []string{"%4", "%1", "%3", "%2"}) {
		t.Fatalf("mem sort mismatch: got %v", got)
	}
}

func TestFormatRSS(t *testing.T) {
	if got := formatRSS(512); got != "512K" {
		t.Fatalf("kb format mismatch: got %q", got)
	}
	if got := formatRSS(2048); got != "2.0M" {
		t.Fatalf("mb format mismatch: got %q", got)
	}
	if got := formatRSS(3 * 1024 * 1024); got != "3.0G" {
		t.Fatalf("gb format mismatch: got %q", got)
	}
}

func usageByTarget(rows []windowUsage, target string) windowUsage {
	for _, row := range rows {
		if row.Target == target {
			return row
		}
	}
	return windowUsage{}
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
