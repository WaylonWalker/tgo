package main

import "time"

type agentEvidence struct {
	State        agentState
	Authority    stateAuthority
	Confidence   string
	Reason       string
	Identity     agentIdentity
	Run          *agentRun
	Screen       screenObservation
	ProcessAlive bool
}

func resolveAgentEvidence(definition setupHarnessDefinition, live agentIdentity, references []agentRunReference, captured string, now time.Time) agentEvidence {
	evidence := agentEvidence{
		State:        agentStateUnknown,
		Authority:    authorityUnknown,
		Confidence:   "low",
		Identity:     live,
		ProcessAlive: live.PID != 0,
		Screen:       screenObservation{State: agentStateUnknown},
	}
	if captured != "" {
		evidence.Screen = classifyScreenRules(definition.ScreenRules, captured)
	}

	var lifecycle *agentRun
	for _, reference := range references {
		candidate := reference.Run
		if candidate.Pane == "" {
			candidate.Pane = candidate.Identity.Pane
		}
		if !runMatchesLive(candidate, live) {
			continue
		}
		state := candidate.LifecycleState
		if state == "" {
			state = candidate.State
		}
		if state == "" {
			state = legacyAgentState(candidate.Status)
		}
		if state == agentStateUnknown {
			continue
		}
		if live.ProcessStart != "" && candidate.Identity.ProcessStart == "" {
			// A pane-only record cannot be safely attached to a process
			// generation that exposes a start token.
			continue
		}
		copy := candidate
		copy.State = state
		lifecycle = &copy
		break
	}
	if live.PID == 0 && lifecycle != nil {
		evidence.State = agentStateStopped
		evidence.Authority = authorityLiveness
		evidence.Confidence = "high"
		evidence.Run = lifecycle
		evidence.Reason = "tracked harness process is no longer alive"
		return evidence
	}

	if lifecycle != nil && lifecycleFresh(lifecycle, now) {
		if definition.LifecycleMode == lifecycleHybrid && evidence.Screen.State != agentStateUnknown &&
			(lifecycle.State == agentStateWorking || lifecycle.State == agentStateWaiting) {
			// Hybrid integrations use a recognized prompt/permission screen to
			// correct known hook gaps around cancellation and manual denial.
			if evidence.Screen.State != lifecycle.State {
				return screenEvidence(evidence, lifecycle)
			}
		}
		return lifecycleEvidence(evidence, lifecycle)
	}

	// "Authoritative" means a fresh, generation-matched lifecycle event wins;
	// it does not make stale lifecycle data immortal. Once invalidated, every
	// driver may fall back to live screen evidence before returning unknown.
	if evidence.Screen.State != agentStateUnknown {
		evidence.State = evidence.Screen.State
		evidence.Authority = authorityScreen
		evidence.Confidence = "high"
		if lifecycle != nil {
			evidence.Reason = "lifecycle evidence is stale; screen fallback matched " + evidence.Screen.RuleID
		} else {
			evidence.Reason = "screen matched " + evidence.Screen.RuleID
		}
		return evidence
	}
	if lifecycle != nil {
		evidence.Reason = "lifecycle evidence is stale or generation-mismatched"
	} else {
		evidence.Reason = "no lifecycle event or recognized screen rule"
	}
	return evidence
}

func lifecycleEvidence(evidence agentEvidence, run *agentRun) agentEvidence {
	evidence.State = run.State
	evidence.Authority = authorityLifecycle
	evidence.Confidence = "high"
	evidence.Run = run
	evidence.Reason = "accepted " + run.NativeKind
	if run.NativeKind == "" {
		evidence.Reason = "accepted " + run.Status
	}
	return evidence
}

func screenEvidence(evidence agentEvidence, lifecycle *agentRun) agentEvidence {
	evidence.State = evidence.Screen.State
	evidence.Authority = authorityScreen
	evidence.Confidence = "high"
	evidence.Run = lifecycle
	evidence.Reason = "screen matched " + evidence.Screen.RuleID + "; lifecycle was supplemental"
	return evidence
}

func runMatchesLive(run agentRun, live agentIdentity) bool {
	identity := run.Identity
	if identity.Pane == "" {
		identity.Pane = run.Pane
	}
	if identity.PID == 0 {
		identity.PID = run.PID
	}
	if identity.Pane != "" && live.Pane != "" && identity.Pane != live.Pane {
		return false
	}
	if identity.PID != 0 && live.PID != 0 && identity.PID != live.PID {
		return false
	}
	if identity.ProcessStart != "" && live.ProcessStart != "" && identity.ProcessStart != live.ProcessStart {
		return false
	}
	if identity.TmuxServer != "" && live.TmuxServer != "" && identity.TmuxServer != live.TmuxServer {
		return false
	}
	if identity.TmuxSession != "" && live.TmuxSession != "" && identity.TmuxSession != live.TmuxSession {
		return false
	}
	return true
}

func lifecycleFresh(run *agentRun, now time.Time) bool {
	if run == nil || run.LifecycleAt.IsZero() || now.IsZero() {
		return true
	}
	if now.Before(run.LifecycleAt) {
		return true
	}
	maxAge := 10 * time.Minute
	if run.State == agentStateWorking || run.State == agentStateWaiting {
		maxAge = 2 * time.Minute
	}
	return now.Sub(run.LifecycleAt) <= maxAge
}
