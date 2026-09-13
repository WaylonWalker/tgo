package main

import (
	"regexp"
	"strings"
)

type screenRule struct {
	ID       string
	State    agentState
	Contains []string
	Regex    string
}

type screenObservation struct {
	State      agentState `json:"state"`
	RuleID     string     `json:"matched,omitempty"`
	Evidence   string     `json:"evidence,omitempty"`
	Normalized string     `json:"-"`
}

var (
	ansiCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSC = regexp.MustCompile(`\x1b\][^\a]*(?:\a|\x1b\\)`)
)

func opencodeScreenRules() []screenRule {
	return []screenRule{
		{ID: "opencode.permission", State: agentStateWaiting, Contains: []string{"Allow this", "Permission required", "Approve"}},
		{ID: "opencode.question", State: agentStateWaiting, Contains: []string{"Select an option", "Question", "Answer:"}},
		{ID: "opencode.working", State: agentStateWorking, Contains: []string{"esc to interrupt", "ctrl+c to interrupt"}},
		{ID: "opencode.prompt", State: agentStateIdle, Regex: `(?m)^\s*[›❯>]\s*$`},
	}
}

func codexScreenRules() []screenRule {
	return []screenRule{
		{ID: "codex.permission", State: agentStateWaiting, Contains: []string{"Would you like to run", "Allow", "Approve", "Permission required"}},
		{ID: "codex.question", State: agentStateWaiting, Contains: []string{"Choose an option", "Press enter to continue"}},
		{ID: "codex.working", State: agentStateWorking, Contains: []string{"esc to interrupt", "ctrl+c to interrupt", "working"}},
		{ID: "codex.prompt", State: agentStateIdle, Regex: `(?m)^\s*[›❯>]\s*$`},
	}
}

func claudeScreenRules() []screenRule {
	return []screenRule{
		{ID: "claude.permission", State: agentStateWaiting, Contains: []string{"Allow", "Do you want to", "Permission required"}},
		{ID: "claude.permission-choice", State: agentStateWaiting, Regex: `^\s*(?:[12][.)])?\s*(?:Yes|No)\s*$`},
		{ID: "claude.question", State: agentStateWaiting, Contains: []string{"Question", "Select an option", "Enter to submit"}},
		{ID: "claude.working", State: agentStateWorking, Contains: []string{"esc to interrupt", "ctrl+c to interrupt"}},
		{ID: "claude.prompt", State: agentStateIdle, Regex: `(?m)^\s*[❯›>]\s*$`},
	}
}

func geminiScreenRules() []screenRule {
	return []screenRule{
		{ID: "gemini.permission", State: agentStateWaiting, Contains: []string{"Allow", "approve", "Tool permission", "Permission required"}},
		{ID: "gemini.question", State: agentStateWaiting, Contains: []string{"Select an option", "Your answer", "Press Enter"}},
		{ID: "gemini.working", State: agentStateWorking, Contains: []string{"esc to cancel", "ctrl+c to interrupt", "Working"}},
		{ID: "gemini.prompt", State: agentStateIdle, Regex: `(?m)^\s*[›❯>]\s*$`},
	}
}

func copilotScreenRules() []screenRule {
	return []screenRule{
		{ID: "copilot.permission", State: agentStateWaiting, Contains: []string{"Allow", "Do you want to proceed", "Permission required"}},
		{ID: "copilot.question", State: agentStateWaiting, Contains: []string{"Choose an option", "Select an option", "Enter to submit"}},
		{ID: "copilot.working", State: agentStateWorking, Contains: []string{"esc to interrupt", "ctrl+c to interrupt"}},
		{ID: "copilot.prompt", State: agentStateIdle, Regex: `(?m)^\s*[›❯>]\s*$`},
	}
}

func classifyScreen(harness, captured string) screenObservation {
	definition := harnessDefinitionByID(harness)
	return classifyScreenRules(definition.ScreenRules, captured)
}

func classifyScreenRules(rules []screenRule, captured string) screenObservation {
	normalized := normalizeScreen(captured)
	observation := screenObservation{State: agentStateUnknown, Normalized: normalized}
	if normalized == "" {
		return observation
	}
	lines := strings.Split(normalized, "\n")
	// Rules are evaluated from the live bottom upward. The first matching
	// specific prompt or permission is the strongest screen evidence.
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		for _, rule := range rules {
			if ruleMatches(rule, normalized, line) {
				observation.State = rule.State
				observation.RuleID = rule.ID
				observation.Evidence = limitString(line, 160)
				return observation
			}
		}
	}
	return observation
}

func ruleMatches(rule screenRule, normalized, line string) bool {
	for _, value := range rule.Contains {
		if strings.Contains(strings.ToLower(line), strings.ToLower(value)) {
			return true
		}
	}
	if rule.Regex == "" {
		return false
	}
	compiled, err := regexp.Compile(rule.Regex)
	return err == nil && compiled.MatchString(line)
}

func normalizeScreen(captured string) string {
	if captured == "" {
		return ""
	}
	clean := ansiOSC.ReplaceAllString(captured, "")
	clean = ansiCSI.ReplaceAllString(clean, "")
	clean = strings.ReplaceAll(clean, "\r", "")
	clean = strings.ReplaceAll(clean, "\b", "")
	lines := strings.Split(clean, "\n")
	for index, line := range lines {
		line = strings.TrimRight(line, " \t")
		lines[index] = strings.Map(func(r rune) rune {
			if r < 0x20 && r != '\t' {
				return -1
			}
			return r
		}, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
