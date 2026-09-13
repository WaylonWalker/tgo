package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

type agentExplanation struct {
	Agent      string             `json:"agent"`
	State      string             `json:"state"`
	Authority  string             `json:"authority"`
	Confidence string             `json:"confidence"`
	Process    explanationProcess `json:"process"`
	Session    explanationSession `json:"session"`
	Screen     explanationScreen  `json:"screen"`
	Hooks      explanationHooks   `json:"hooks"`
}

type explanationProcess struct {
	PID         int    `json:"pid"`
	Started     string `json:"started,omitempty"`
	Command     string `json:"command,omitempty"`
	Pane        string `json:"pane,omitempty"`
	TmuxServer  string `json:"tmux_server,omitempty"`
	TmuxSession string `json:"tmux_session,omitempty"`
	Liveness    string `json:"liveness"`
}

type explanationSession struct {
	ID        string `json:"id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	Source    string `json:"source,omitempty"`
	LastEvent string `json:"last_event,omitempty"`
	LastAt    string `json:"last_at,omitempty"`
	EventAge  string `json:"event_age,omitempty"`
}

type explanationScreen struct {
	Matched  string `json:"matched,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	State    string `json:"state"`
}

type explanationHooks struct {
	Installed    bool   `json:"installed"`
	Healthy      bool   `json:"healthy"`
	Verified     bool   `json:"verified"`
	TrustReview  bool   `json:"trust_review_required"`
	LastEvent    string `json:"last_event,omitempty"`
	LastEventAge string `json:"last_event_age,omitempty"`
	Authority    string `json:"authority"`
	Detail       string `json:"detail,omitempty"`
}

func runAgentExplain(args []string, _ io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("tgo agent explain", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var paneTarget string
	var asJSON bool
	flags.StringVar(&paneTarget, "pane", "", "tmux pane target")
	flags.BoolVar(&asJSON, "json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return fmt.Errorf("agent explain accepts at most one pane target")
	}
	if flags.NArg() == 1 {
		if paneTarget != "" && paneTarget != flags.Arg(0) {
			return fmt.Errorf("--pane conflicts with pane argument")
		}
		paneTarget = flags.Arg(0)
	}
	client := &tmuxCLI{}
	if paneTarget == "" {
		var err error
		paneTarget, err = client.CurrentPane()
		if err != nil {
			return err
		}
	}
	panes, err := client.ListPanes()
	if err != nil {
		return err
	}
	var pane paneInfo
	found := false
	for _, candidate := range panes {
		if candidate.Target() == paneTarget {
			pane = candidate
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("pane %q not found", paneTarget)
	}
	procs, err := client.ListProcesses()
	if err != nil {
		return err
	}
	store, err := openAgentRegistryStore()
	if err != nil {
		return err
	}
	registry, err := store.Load()
	if err != nil {
		return err
	}
	harness, process := detectHarnessAtPane(pane, procs)
	if harness == "" {
		harness = latestHarnessForPane(registry, paneTarget)
	}
	definition := harnessDefinitionByID(harness)
	if definition.Label == "" {
		definition.Label = "unknown"
	}
	captured, _ := client.CapturePane(paneTarget, 24)
	var references []agentRunReference
	if harness != "" {
		references = agentRunsForHarness(registry, harness)
	}
	live := agentIdentity{
		Harness:     harness,
		TmuxServer:  pane.ServerID,
		TmuxSession: pane.SessionID,
		Pane:        pane.PaneID,
		PID:         pane.PanePID,
	}
	if process.PID != 0 {
		live.PID = process.PID
		live.ProcessStart = process.StartTime
	}
	evidence := resolveAgentEvidence(definition, live, references, captured, time.Now().UTC())
	if process.PID == 0 {
		evidence.ProcessAlive = false
		if evidence.Run != nil && evidence.State == agentStateUnknown {
			evidence.State = agentStateStopped
			evidence.Authority = authorityLiveness
			evidence.Confidence = "high"
			evidence.Reason = "tracked process is no longer present"
		}
	}
	manager, err := newSetupManager()
	if err != nil {
		return err
	}
	explanation := makeAgentExplanation(harness, pane, process, evidence, manager)
	if asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(explanation)
	}
	writeAgentExplanation(output, explanation)
	return nil
}

func detectHarnessAtPane(pane paneInfo, procs []procStat) (string, procStat) {
	for _, definition := range setupHarnessDefinitions() {
		matched := findHarnessPanes(definition.ID, []paneInfo{pane}, procs)
		if len(matched) == 0 {
			continue
		}
		for _, proc := range procs {
			if proc.PID == matched[0].PID {
				return definition.ID, proc
			}
		}
		return definition.ID, procStat{PID: matched[0].PID, Comm: definition.Command}
	}
	return "", procStat{}
}

func latestHarnessForPane(registry agentRegistry, pane string) string {
	var latest string
	var latestAt time.Time
	for harness, references := range registry.Harnesses {
		for _, session := range references.Sessions {
			for _, run := range session.Runs {
				if run.Pane != pane && run.Identity.Pane != pane {
					continue
				}
				if latest == "" || run.UpdatedAt.After(latestAt) {
					latest = harness
					latestAt = run.UpdatedAt
				}
			}
		}
	}
	return latest
}

func makeAgentExplanation(harness string, pane paneInfo, process procStat, evidence agentEvidence, manager *setupManager) agentExplanation {
	definition := harnessDefinitionByID(harness)
	explanation := agentExplanation{
		Agent:      definition.Label,
		State:      string(evidence.State),
		Authority:  string(evidence.Authority),
		Confidence: evidence.Confidence,
		Process: explanationProcess{
			PID:         process.PID,
			Started:     process.StartTime,
			Command:     processCommand(process),
			Pane:        pane.PaneID,
			TmuxServer:  pane.ServerID,
			TmuxSession: pane.SessionID,
			Liveness:    "not detected",
		},
		Screen: explanationScreen{
			Matched:  evidence.Screen.RuleID,
			Evidence: evidence.Screen.Evidence,
			State:    string(evidence.Screen.State),
		},
		Hooks: explanationHooks{
			Authority: string(definition.LifecycleMode),
		},
	}
	if process.PID != 0 {
		explanation.Process.Liveness = "alive"
	}
	if evidence.Run != nil {
		explanation.Session.ID = evidence.Run.Identity.SessionID
		explanation.Session.TurnID = evidence.Run.Identity.TurnID
		if explanation.Session.ID == "" {
			explanation.Session.ID = "unknown"
		}
		explanation.Session.Source = "lifecycle event"
		if len(evidence.Run.Events) > 0 {
			last := evidence.Run.Events[len(evidence.Run.Events)-1]
			explanation.Session.LastEvent = last.NativeKind
			if explanation.Session.LastEvent == "" {
				explanation.Session.LastEvent = last.Kind
			}
			explanation.Session.LastAt = last.At.Format(time.RFC3339)
			explanation.Session.EventAge = formatAgentAge(last.At, time.Now().UTC())
		}
	}
	if manager != nil && harness != "" {
		status, detail := manager.inspect(definition)
		explanation.Hooks.Installed = status != setupStatusMissing && status != setupStatusConflict
		explanation.Hooks.Healthy = status == setupStatusCurrent
		explanation.Hooks.Verified = explanation.Hooks.Healthy
		explanation.Hooks.Detail = detail
	}
	if evidence.Run != nil && len(evidence.Run.Events) > 0 {
		last := evidence.Run.Events[len(evidence.Run.Events)-1]
		explanation.Hooks.LastEvent = last.NativeKind
		if explanation.Hooks.LastEvent == "" {
			explanation.Hooks.LastEvent = last.Kind
		}
		explanation.Hooks.LastEventAge = formatAgentAge(last.At, time.Now().UTC())
	}
	return explanation
}

func writeAgentExplanation(output io.Writer, explanation agentExplanation) {
	fmt.Fprintf(output, "Agent:        %s\n", explanation.Agent)
	fmt.Fprintf(output, "State:        %s\n", explanation.State)
	fmt.Fprintf(output, "Authority:    %s\n", explanation.Authority)
	fmt.Fprintf(output, "Confidence:   %s\n\n", explanation.Confidence)
	fmt.Fprintln(output, "Process:")
	fmt.Fprintf(output, "  pid:        %d\n", explanation.Process.PID)
	fmt.Fprintf(output, "  started:    %s\n", valueOrUnknown(explanation.Process.Started))
	fmt.Fprintf(output, "  command:    %s\n", valueOrUnknown(explanation.Process.Command))
	fmt.Fprintf(output, "  pane:       %s\n", valueOrUnknown(explanation.Process.Pane))
	fmt.Fprintln(output, "\nSession:")
	fmt.Fprintf(output, "  id:         %s\n", valueOrUnknown(explanation.Session.ID))
	fmt.Fprintf(output, "  source:     %s\n", valueOrUnknown(explanation.Session.Source))
	fmt.Fprintf(output, "  last event: %s, %s ago\n", valueOrUnknown(explanation.Session.LastEvent), valueOrUnknown(explanation.Session.EventAge))
	fmt.Fprintln(output, "\nScreen:")
	fmt.Fprintf(output, "  matched:    %s\n", valueOrUnknown(explanation.Screen.Matched))
	fmt.Fprintf(output, "  evidence:   %s\n", valueOrUnknown(explanation.Screen.Evidence))
	fmt.Fprintln(output, "\nHooks:")
	fmt.Fprintf(output, "  installed:  %s\n", yesNo(explanation.Hooks.Installed))
	fmt.Fprintf(output, "  healthy:    %s\n", yesNo(explanation.Hooks.Healthy))
	fmt.Fprintf(output, "  last event: %s, %s ago\n", valueOrUnknown(explanation.Hooks.LastEvent), valueOrUnknown(explanation.Hooks.LastEventAge))
	fmt.Fprintf(output, "  authority:  %s\n", valueOrUnknown(explanation.Hooks.Authority))
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
