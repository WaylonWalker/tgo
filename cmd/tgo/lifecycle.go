package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// lifecycleTransition is the small normalized vocabulary used by the state
// resolver. Vendor event names remain in NativeKind and in the bounded raw
// event payload for diagnostics.
type lifecycleTransition struct {
	NativeKind   string
	State        agentState
	SessionStart bool
}

func normalizeLifecycleEvent(input agentEventInput) lifecycleTransition {
	native := strings.TrimSpace(input.NativeKind)
	if native == "" {
		native = strings.TrimSpace(input.Kind)
	}
	transition := lifecycleTransition{NativeKind: native, State: agentStateUnknown}
	name := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(native, "_", "-"), " ", "-"))

	// Keep the old generic event vocabulary working while new integrations use
	// native event names through `tgo agent ingest`.
	if input.NativeKind == "" {
		switch name {
		case "session-start", "user-prompt":
			transition.State = agentStateWorking
		case "agent-stop", "completed", "stopped", "idle":
			transition.State = agentStateIdle
		case "question", "permission-prompt", "waiting":
			transition.State = agentStateWaiting
		case "session-end", "failed", "cancelled":
			transition.State = agentStateStopped
		}
		transition.SessionStart = name == "session-start"
		return transition
	}

	switch input.Harness {
	case "opencode":
		switch name {
		case "session.created", "session-start", "session.started":
			transition.State = agentStateIdle
			transition.SessionStart = true
		case "chat.message", "user-prompt", "session.status.busy", "session.status.running":
			transition.State = agentStateWorking
		case "session.status.idle", "session.idle", "session.status.completed":
			transition.State = agentStateIdle
		case "session.status.retry", "permission.asked", "question.asked":
			transition.State = agentStateWaiting
		case "permission.replied", "question.replied", "question.rejected", "session.compacted":
			transition.State = agentStateWorking
		case "session.error", "error":
			transition.State = agentStateStopped
		case "session.deleted", "session.end", "session-ended":
			transition.State = agentStateStopped
		}
	case "codex":
		switch name {
		case "sessionstart", "session-start":
			transition.State = agentStateIdle
			transition.SessionStart = true
		case "userpromptsubmit", "user-prompt", "beforeagent":
			transition.State = agentStateWorking
		case "permissionrequest", "permission-request", "question":
			transition.State = agentStateWaiting
		case "stop", "interrupt", "cancel", "cancelled", "agent-stop":
			transition.State = agentStateIdle
		case "sessionend", "session-end":
			transition.State = agentStateStopped
		case "error", "failed", "failure":
			transition.State = agentStateStopped
		}
	case "claude":
		switch name {
		case "sessionstart", "session-start":
			transition.State = agentStateIdle
			transition.SessionStart = true
		case "userpromptsubmit", "user-prompt", "tool-use", "tool-start", "tool-end":
			transition.State = agentStateWorking
		case "permissionrequest", "permission-request", "elicitation", "notification", "question":
			transition.State = agentStateWaiting
		case "elicitationresult", "permissionresult", "permission-replied", "question-replied":
			transition.State = agentStateWorking
		case "stop", "agent-stop":
			transition.State = agentStateIdle
		case "stopfailure", "error", "failed":
			transition.State = agentStateStopped
		case "sessionend", "session-end":
			transition.State = agentStateStopped
		}
	case "gemini":
		switch name {
		case "sessionstart", "session-start":
			transition.State = agentStateIdle
			transition.SessionStart = true
		case "beforeagent", "before-tool", "beforetool":
			transition.State = agentStateWorking
		case "afteragent", "after-tool", "aftertool":
			transition.State = agentStateIdle
		case "toolpermission", "permissionrequest", "notification", "question":
			transition.State = agentStateWaiting
		case "sessionend", "session-end":
			transition.State = agentStateStopped
		case "error", "failed":
			transition.State = agentStateStopped
		}
	case "copilot":
		switch name {
		case "sessionstart", "session-start":
			transition.State = agentStateIdle
			transition.SessionStart = true
		case "userpromptsubmitted", "user-prompt", "prompt-submitted", "tool-start", "tool-use":
			transition.State = agentStateWorking
		case "permissionrequest", "permission-request", "elicitation", "notification", "question":
			transition.State = agentStateWaiting
		case "agentstop", "agent-stop", "stop":
			transition.State = agentStateIdle
		case "erroroccurred", "error", "failed":
			transition.State = agentStateStopped
		case "sessionend", "session-end":
			transition.State = agentStateStopped
		}
	default:
		// Unknown harnesses still get their raw events retained, but no state is
		// inferred from a vocabulary we do not understand.
	}
	if input.Harness == "claude" && strings.Contains(name, "result") && lifecyclePayloadIndicatesCancellation(input) {
		transition.State = agentStateIdle
	}
	return transition
}

func lifecyclePayloadIndicatesCancellation(input agentEventInput) bool {
	raw := input.RawPayload
	if len(raw) == 0 {
		raw = input.Data
	}
	value := strings.ToLower(string(raw))
	for _, marker := range []string{"cancel", "cancelled", "canceled", "rejected", "denied"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func extractNativeAgentFields(input *agentEventInput) {
	raw := input.RawPayload
	if len(raw) == 0 {
		raw = input.Data
	}
	if len(raw) == 0 || !json.Valid(raw) {
		return
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return
	}
	stringField := func(names ...string) string {
		for _, name := range names {
			if value, ok := fields[name].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	if input.SessionID == "" {
		input.SessionID = stringField("sessionId", "session_id", "sessionID", "session")
	}
	if input.TurnID == "" {
		input.TurnID = stringField("turnId", "turn_id", "turnID")
	}
	if input.RunID == "" {
		input.RunID = stringField("runId", "run_id", "runID", "transcriptId", "transcript_id")
	}
	if input.CWD == "" {
		input.CWD = stringField("cwd", "workingDirectory", "working_directory")
	}
	if input.TranscriptID == "" {
		input.TranscriptID = stringField("transcriptId", "transcript_id", "runId", "run_id")
	}
	if input.PermissionID == "" {
		input.PermissionID = stringField("permissionId", "permission_id", "permissionID", "permission")
	}
	if input.QuestionID == "" {
		input.QuestionID = stringField("questionId", "question_id", "questionID", "question")
	}
	if input.NativeKind == "" {
		input.NativeKind = stringField("event", "eventType", "event_type", "hook_event_name", "type")
	}
	if input.At.IsZero() {
		for _, name := range []string{"timestamp", "event_timestamp", "eventTime", "event_time"} {
			if value, ok := fields[name].(string); ok {
				if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
					input.At = parsed
					break
				}
			}
		}
	}
}

func normalizeAgentEventInput(input *agentEventInput) {
	if len(input.RawPayload) == 0 && len(input.Data) > 0 {
		input.RawPayload = cloneJSON(input.Data)
	}
	extractNativeAgentFields(input)
	input.Harness = strings.TrimSpace(input.Harness)
	input.Kind = strings.TrimSpace(input.Kind)
	input.NativeKind = strings.TrimSpace(input.NativeKind)
	if input.Kind == "" {
		input.Kind = input.NativeKind
	}
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.RunID = strings.TrimSpace(input.RunID)
	input.TurnID = strings.TrimSpace(input.TurnID)
	input.Pane = strings.TrimSpace(input.Pane)
	input.ProcessStart = strings.TrimSpace(input.ProcessStart)
	input.TmuxServer = strings.TrimSpace(input.TmuxServer)
	input.TmuxSession = strings.TrimSpace(input.TmuxSession)
	if input.ProcessStart == "" && input.PID != 0 {
		input.ProcessStart = processStartToken(input.PID)
	}
	if input.RunID == "" {
		switch {
		case input.Pane != "":
			input.RunID = input.Pane
		case input.PID != 0:
			input.RunID = fmt.Sprintf("pid:%d", input.PID)
		default:
			input.RunID = input.SessionID
		}
	}
}

// processStartToken reads Linux's monotonic process start tick. It is an
// optional strengthening signal; other platforms simply return an empty token.
func processStartToken(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return ""
	}
	fields := strings.Fields(string(data[closeParen+2:]))
	// The slice starts at stat field 3 (state); starttime is field 22.
	if len(fields) <= 19 {
		return ""
	}
	return fields[19]
}

func limitString(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

func formatAgentAge(at time.Time, now time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	if now.Before(at) {
		return "just now"
	}
	return (now.Sub(at).Round(time.Second)).String()
}
