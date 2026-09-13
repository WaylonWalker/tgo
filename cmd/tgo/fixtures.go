package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// harnessFixture is intentionally small and JSON-friendly. Real probe output
// can be saved and replayed without launching a harness again.
type harnessFixture struct {
	Name          string          `json:"name"`
	Harness       string          `json:"harness"`
	Version       string          `json:"version,omitempty"`
	At            time.Time       `json:"timestamp"`
	Pane          string          `json:"pane"`
	PID           int             `json:"pid"`
	ProcessStart  string          `json:"process_start,omitempty"`
	SessionID     string          `json:"session_id"`
	TurnID        string          `json:"turn_id,omitempty"`
	NativeEvent   string          `json:"native_event"`
	RawPayload    json.RawMessage `json:"raw_payload,omitempty"`
	Screen        string          `json:"screen,omitempty"`
	ExpectedState agentState      `json:"expected_state"`
}

type fixtureResult struct {
	Fixture   harnessFixture
	State     agentState
	Authority stateAuthority
	Reason    string
}

func replayHarnessFixture(fixture harnessFixture) (fixtureResult, error) {
	if fixture.At.IsZero() {
		fixture.At = time.Now().UTC()
	}
	if fixture.Harness == "" || fixture.NativeEvent == "" || fixture.SessionID == "" {
		return fixtureResult{}, fmt.Errorf("fixture %q is missing harness, native event, or session ID", fixture.Name)
	}
	registry := newAgentRegistry()
	input := agentEventInput{
		Harness:      fixture.Harness,
		Kind:         fixture.NativeEvent,
		NativeKind:   fixture.NativeEvent,
		SessionID:    fixture.SessionID,
		TurnID:       fixture.TurnID,
		Pane:         fixture.Pane,
		PID:          fixture.PID,
		ProcessStart: fixture.ProcessStart,
		At:           fixture.At,
		RawPayload:   cloneJSON(fixture.RawPayload),
		Data:         cloneJSON(fixture.RawPayload),
	}
	registry.apply(input)
	live := agentIdentity{
		Harness:      fixture.Harness,
		Pane:         fixture.Pane,
		PID:          fixture.PID,
		ProcessStart: fixture.ProcessStart,
		SessionID:    fixture.SessionID,
		TurnID:       fixture.TurnID,
	}
	references := agentRunsForHarness(registry, fixture.Harness)
	evidence := resolveAgentEvidence(harnessDefinitionByID(fixture.Harness), live, references, fixture.Screen, fixture.At.Add(time.Second))
	return fixtureResult{Fixture: fixture, State: evidence.State, Authority: evidence.Authority, Reason: evidence.Reason}, nil
}
