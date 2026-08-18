package main

import "testing"

func TestSessionHotkeyAlphabet(t *testing.T) {
	want := "asdfqwertzxcvb"
	if got := SessionHotkeyAlphabet(); got != want {
		t.Fatalf("hotkey alphabet mismatch: got %q want %q", got, want)
	}
}

func TestViewForUsageMode(t *testing.T) {
	tests := map[usageMode]viewID{
		usageModeCPU:    viewCPU,
		usageModeMem:    viewMem,
		usageModeAgents: viewAgents,
	}
	for mode, want := range tests {
		if got := viewForUsageMode(mode); got != want {
			t.Errorf("viewForUsageMode(%d) = %d, want %d", mode, got, want)
		}
	}
}

func TestViewTabsIncludesAgentPickers(t *testing.T) {
	want := "tgo  1:sessions  2:cpu  3:mem  [4:agents]"
	if got := viewTabs(viewAgents); got != want {
		t.Fatalf("agents tab mismatch: got %q want %q", got, want)
	}
}

func TestParseUsageModeAliasesAgentPickers(t *testing.T) {
	for _, command := range []string{"agents", "copilot", "opencode"} {
		mode, ok := parseUsageMode(command)
		if !ok || mode != usageModeAgents {
			t.Errorf("parseUsageMode(%q) = (%d, %t), want agents mode", command, mode, ok)
		}
	}
}
