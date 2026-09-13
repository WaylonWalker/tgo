package main

import "testing"

func TestScreenRulesClassifyKnownBottomStates(t *testing.T) {
	tests := []struct {
		name    string
		harness string
		screen  string
		state   agentState
		rule    string
	}{
		{"permission", "codex", "output\nWould you like to run this command?", agentStateWaiting, "codex.permission"},
		{"working", "opencode", "answer\n\x1b[31mesc to interrupt\x1b[0m", agentStateWorking, "opencode.working"},
		{"prompt", "claude", "finished\n❯", agentStateIdle, "claude.prompt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := classifyScreen(test.harness, test.screen)
			if observation.State != test.state || observation.RuleID != test.rule {
				t.Fatalf("observation = (%q, %q), want (%q, %q)", observation.State, observation.RuleID, test.state, test.rule)
			}
		})
	}
}

func TestScreenUnknownDoesNotFallThroughToIdle(t *testing.T) {
	observation := classifyScreen("gemini", "tool output\nno prompt marker")
	if observation.State != agentStateUnknown || observation.RuleID != "" {
		t.Fatalf("unknown screen = (%q, %q), want unknown", observation.State, observation.RuleID)
	}
}

func TestScreenBottomPromptWinsOverOlderPermissionText(t *testing.T) {
	observation := classifyScreen("copilot", "Allow this command?\n\nresult\n›")
	if observation.State != agentStateIdle {
		t.Fatalf("bottom prompt state = %q, want idle", observation.State)
	}
}

func TestNormalizeScreenRemovesTerminalArtifacts(t *testing.T) {
	got := normalizeScreen("\x1b]0;title\a\x1b[2K\r\x1b[32m›\x1b[0m\n")
	if got != "›" {
		t.Fatalf("normalized screen = %q, want prompt", got)
	}
}
