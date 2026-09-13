package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type integrationReport struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	Executable         string   `json:"executable,omitempty"`
	ExecutableDetected bool     `json:"executable_detected"`
	Compatible         bool     `json:"compatible"`
	Compatibility      string   `json:"compatibility"`
	VersionChecked     bool     `json:"version_checked"`
	Installed          bool     `json:"installed"`
	Configured         bool     `json:"configured"`
	Verified           bool     `json:"verified"`
	ActivelyReporting  bool     `json:"actively_reporting"`
	TrustReview        string   `json:"trust_review"`
	Status             string   `json:"status"`
	Details            []string `json:"details,omitempty"`
}

type integrationResult struct {
	Harness string `json:"harness"`
	Changed bool   `json:"changed"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (m *setupManager) report(definition setupHarnessDefinition) integrationReport {
	report := integrationReport{
		ID:            definition.ID,
		Label:         definition.Label,
		TrustReview:   "not-applicable",
		Compatibility: "unknown",
		Details:       []string{},
	}
	lookPath := m.lookPath
	if lookPath == nil {
		lookPath = execLookPath
	}
	for _, executable := range definition.Executables {
		if path, err := lookPath(executable); err == nil {
			report.Executable = path
			report.ExecutableDetected = true
			break
		}
	}
	if report.ExecutableDetected {
		report.Details = append(report.Details, "executable detected (version not probed)")
	} else {
		report.Details = append(report.Details, "executable not detected")
	}
	setupStatus, detail := m.inspect(definition)
	report.Installed = setupStatus != setupStatusMissing && setupStatus != setupStatusConflict
	report.Configured = setupStatus == setupStatusCurrent
	if report.Installed {
		report.Details = append(report.Details, "integration files installed")
	}
	if report.Configured {
		report.Details = append(report.Details, "hook or plugin wiring configured")
	} else {
		report.Details = append(report.Details, detail)
	}
	report.Verified = report.Configured && verifyAgentIngestionPath(definition.ID) == nil
	if report.Verified {
		report.Details = append(report.Details, "synthetic event reached tgo ingestion")
	}
	report.ActivelyReporting = harnessIsActivelyReporting(definition.ID)
	if report.ActivelyReporting {
		report.Details = append(report.Details, "recent native lifecycle event received")
	} else if report.Configured {
		report.Details = append(report.Details, "no recent event; launch the harness to verify delivery")
	}
	if definition.ID == "codex" && report.Configured {
		report.TrustReview = "unknown"
		if report.ActivelyReporting {
			report.TrustReview = "verified-by-event"
		} else {
			report.Details = append(report.Details, "Codex hook trust/review cannot be confirmed until an event arrives")
		}
	}
	switch {
	case !report.ExecutableDetected:
		report.Status = "not-detected"
	case setupStatus == setupStatusConflict:
		report.Status = "conflict"
	case setupStatus == setupStatusMissing:
		report.Status = "not-installed"
	case setupStatus == setupStatusNeedsUpdate:
		report.Status = "update-needed"
	case report.ActivelyReporting:
		report.Status = "actively-reporting"
	case report.Verified:
		report.Status = "verified"
	default:
		report.Status = "installed"
	}
	return report
}

func execLookPath(command string) (string, error) {
	return exec.LookPath(command)
}

func verifyAgentIngestionPath(harness string) error {
	dir, err := os.MkdirTemp("", "tgo-agent-doctor-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	store := &agentRegistryStore{path: filepath.Join(dir, "agents.json")}
	if err := store.Apply(agentEventInput{
		Harness:    harness,
		Kind:       "SessionStart",
		NativeKind: "SessionStart",
		SessionID:  "doctor-synthetic",
		Pane:       "%doctor",
		PID:        os.Getpid(),
		At:         time.Now().UTC(),
		Data:       json.RawMessage(`{"synthetic":true}`),
	}); err != nil {
		return err
	}
	registry, err := store.Load()
	if err != nil {
		return err
	}
	if registry.Harnesses[harness].Sessions["doctor-synthetic"].Runs == nil {
		return errors.New("synthetic event was not persisted")
	}
	return nil
}

func harnessIsActivelyReporting(harness string) bool {
	store, err := openAgentRegistryStore()
	if err != nil {
		return false
	}
	registry, err := store.Load()
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	for _, reference := range agentRunsForHarness(registry, harness) {
		for index := len(reference.Run.Events) - 1; index >= 0; index-- {
			event := reference.Run.Events[index]
			if !event.Accepted {
				continue
			}
			at := event.At
			if !at.IsZero() && !now.Before(at) && now.Sub(at) <= 15*time.Minute {
				return true
			}
		}
	}
	return false
}

func removeManagedHarnessHooks(root map[string]any, definition setupHarnessDefinition, scriptPath string) bool {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for event, value := range hooks {
		cleaned, removed := removeManagedHookValue(value, definition, scriptPath)
		if !removed {
			continue
		}
		changed = true
		if cleaned == nil {
			delete(hooks, event)
		} else {
			hooks[event] = cleaned
		}
	}
	return changed
}

func removeManagedHookValue(value any, definition setupHarnessDefinition, path string) (any, bool) {
	switch typed := value.(type) {
	case []any:
		out := make([]any, 0, len(typed))
		removed := false
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok && isManagedCommandHandler(entry, definition, path) {
				removed = true
				continue
			}
			cleaned, itemRemoved := removeManagedHookValue(item, definition, path)
			if itemRemoved {
				removed = true
			}
			if cleaned != nil {
				out = append(out, cleaned)
			}
		}
		if len(out) == 0 && removed {
			return nil, true
		}
		return out, removed
	case map[string]any:
		if isManagedCommandHandler(typed, definition, path) {
			return nil, true
		}
		removed := false
		if nested, ok := typed["hooks"]; ok {
			cleaned, nestedRemoved := removeManagedHookValue(nested, definition, path)
			if nestedRemoved {
				removed = true
				if cleaned == nil {
					delete(typed, "hooks")
				} else {
					typed["hooks"] = cleaned
				}
			}
		}
		return typed, removed
	default:
		return value, false
	}
}

func (m *setupManager) uninstall(definitions []setupHarnessDefinition) []setupResult {
	lockPath := filepath.Join(m.configHome(), "tgo", "setup.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return setupLockResultsForDefinitions(definitions, err)
	}
	lock, err := acquireSetupLock(lockPath)
	if err != nil {
		return setupLockResultsForDefinitions(definitions, err)
	}
	defer func() {
		_ = lock.Release()
	}()
	results := make([]setupResult, 0, len(definitions))
	for _, definition := range definitions {
		changed, uninstallErr := m.uninstallDefinition(definition)
		result := setupResult{Harness: definition.Label, Changed: changed}
		if uninstallErr != nil {
			result.Err = uninstallErr
		} else if changed {
			result.Message = "uninstalled tgo integration"
		} else {
			result.Message = "already uninstalled"
		}
		results = append(results, result)
	}
	return results
}

func setupLockResultsForDefinitions(definitions []setupHarnessDefinition, err error) []setupResult {
	results := make([]setupResult, 0, len(definitions))
	for _, definition := range definitions {
		results = append(results, setupResult{Harness: definition.Label, Err: err})
	}
	return results
}

func (m *setupManager) uninstallDefinition(definition setupHarnessDefinition) (bool, error) {
	if definition.Integration == integrationOpenCode {
		path := m.openCodePluginPath()
		version, exists, err := readManagedVersion(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		if !exists {
			return false, nil
		}
		if version > definition.IntegrationVersion || !managedOwnedFile(path, version, definition, openCodePluginSource()) {
			return false, fmt.Errorf("refusing to remove changed or newer OpenCode plugin %s", path)
		}
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("remove OpenCode plugin: %w", err)
		}
		return true, nil
	}

	paths := m.hookScriptPaths(definition.ID)
	for _, path := range paths {
		version, exists, err := readManagedVersion(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false, err
		}
		if exists && (version > definition.IntegrationVersion || !managedOwnedFile(path, version, definition, expectedHookSource(definition, path))) {
			return false, fmt.Errorf("refusing to remove changed or newer hook %s", path)
		}
	}
	configPath := m.configPath(definition.ID)
	configSnapshot, snapshotErr := snapshotFile(configPath)
	if snapshotErr != nil {
		return false, fmt.Errorf("snapshot configuration: %w", snapshotErr)
	}
	scriptSnapshots := make([]fileSnapshot, len(paths))
	for index, path := range paths {
		scriptSnapshots[index], snapshotErr = snapshotFile(path)
		if snapshotErr != nil {
			return false, fmt.Errorf("snapshot hook %s: %w", path, snapshotErr)
		}
	}
	root, exists, err := loadJSONConfig(configPath)
	if err != nil {
		return false, err
	}
	changed := false
	rollback := func() error {
		var rollbackErrors []error
		if restoreErr := restoreFile(configPath, configSnapshot); restoreErr != nil {
			rollbackErrors = append(rollbackErrors, restoreErr)
		}
		if restoreErr := restoreFiles(paths, scriptSnapshots); restoreErr != nil {
			rollbackErrors = append(rollbackErrors, restoreErr)
		}
		return errors.Join(rollbackErrors...)
	}
	withRollback := func(operationErr error) error {
		if rollbackErr := rollback(); rollbackErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", operationErr, rollbackErr)
		}
		return operationErr
	}
	if exists {
		changed = removeManagedHarnessHooks(root, definition, m.hookScriptPath(definition.ID))
	}
	if changed {
		data, marshalErr := json.MarshalIndent(root, "", "  ")
		if marshalErr != nil {
			return false, marshalErr
		}
		data = append(data, '\n')
		if writeErr := writeManagedJSONFile(configPath, data); writeErr != nil {
			return false, withRollback(writeErr)
		}
	}
	for _, path := range paths {
		if _, statErr := os.Stat(path); statErr == nil {
			if removeErr := os.Remove(path); removeErr != nil {
				return false, withRollback(fmt.Errorf("remove hook %s: %w", path, removeErr))
			}
			changed = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return false, withRollback(statErr)
		}
	}
	return changed, nil
}

func expectedHookSource(definition setupHarnessDefinition, path string) string {
	if definition.ID == "copilot" && strings.HasSuffix(path, ".ps1") {
		return agentHookPowerShellScript(definition.ID)
	}
	return agentHookScript(definition.ID)
}

func managedOwnedFile(path string, version int, definition setupHarnessDefinition, current string) bool {
	if managedFileMatches(path, current) {
		return true
	}
	if version <= 0 || version >= definition.IntegrationVersion {
		return false
	}
	historical, ok := historicalManagedSource(definition, path, version)
	return ok && managedFileMatches(path, historical)
}

func historicalManagedSource(definition setupHarnessDefinition, path string, version int) (string, bool) {
	if definition.Integration == integrationOpenCode && version == 4 {
		return legacyOpenCodePluginSourceV4(), true
	}
	if definition.ID == "copilot" && version == 2 && strings.HasSuffix(path, ".ps1") {
		return legacyAgentHookPowerShellScriptV2(definition.ID), true
	}
	if version != 1 {
		return "", false
	}
	if definition.Integration == integrationOpenCode {
		return legacyOpenCodePluginSourceV1(), true
	}
	if definition.ID == "copilot" && strings.HasSuffix(path, ".ps1") {
		return legacyAgentHookPowerShellScriptV1(definition.ID), true
	}
	return legacyAgentHookScriptV1(definition.ID), true
}

func legacyAgentHookScriptV1(harness string) string {
	return fmt.Sprintf(`#!/bin/sh
# %s%d

set -eu

kind="${1:-}"
[ -n "$kind" ] || exit 0

pane="${TMUX_PANE:-${HERDR_PANE_ID:-}}"
if [ -z "$pane" ] && command -v tmux >/dev/null 2>&1; then
    pane="$(tmux display-message -p '#{pane_id}' 2>/dev/null || true)"
fi

tgo_bin="${TGO_BIN:-tgo}"
if ! command -v "$tgo_bin" >/dev/null 2>&1 && [ ! -x "$tgo_bin" ]; then
    exit 0
fi

"$tgo_bin" agent event \
    --harness %s \
    --kind "$kind" \
    --pane "$pane" \
    --pid "${PPID:-0}" >/dev/null 2>&1 || true
`, managedIntegrationVersion, 1, harness)
}

func legacyAgentHookPowerShellScriptV1(harness string) string {
	source := fmt.Sprintf(`# %s%d

param(
    [Parameter(Position = 0)]
    [string]$Kind
)

if ([string]::IsNullOrWhiteSpace($Kind)) {
    exit 0
}

$Pane = $env:TMUX_PANE
if ([string]::IsNullOrWhiteSpace($Pane)) {
    $Pane = $env:HERDR_PANE_ID
}
if ([string]::IsNullOrWhiteSpace($Pane) -and (Get-Command tmux -ErrorAction SilentlyContinue)) {
    $Pane = (& tmux display-message -p '#{pane_id}' 2>$null).Trim()
}

$Tgo = $env:TGO_BIN
if ([string]::IsNullOrWhiteSpace($Tgo)) {
    $Tgo = "tgo"
}

try {
    & $Tgo agent event __PS_CONT__
        --harness %s __PS_CONT__
        --kind $Kind __PS_CONT__
        --pane $Pane __PS_CONT__
        --pid $PID 2>$null | Out-Null
} catch {
    # Reporting must never interrupt the harness session.
}
exit 0
`, managedIntegrationVersion, 1, harness)
	return strings.ReplaceAll(source, "__PS_CONT__", "`")
}

func legacyOpenCodePluginSourceV1() string {
	source := fmt.Sprintf(`// %s%d
const TGO = process.env.TGO_BIN || "tgo";
const PANE = process.env.TMUX_PANE || process.env.HERDR_PANE_ID || "";
const children = new Set();

function sessionID(properties) {
  if (typeof properties?.sessionID === "string" && properties.sessionID) {
    return properties.sessionID;
  }
  if (typeof properties?.info?.id === "string" && properties.info.id) {
    return properties.info.id;
  }
  return undefined;
}

async function report(event, kind, id, summary) {
  if (!id || children.has(id)) return;
  const payload = {
    harness: "opencode",
    kind,
    sessionId: id,
    runId: id,
    pane: PANE,
    pid: process.pid,
    data: event,
  };
  if (summary) payload.summary = summary;
  try {
    await $__TGO_TEMPLATE__${TGO} agent event --json ${JSON.stringify(payload)}__TGO_TEMPLATE__;
  } catch {
    // Reporting must never interrupt an OpenCode session.
  }
}

function statusKind(status) {
  const value = typeof status === "string" ? status : status?.type;
  if (value === "idle") return "agent-stop";
  if (value === "retry") return "question";
  return "session-start";
}

export const TgoAgentStatePlugin = async ({ $ }) => ({
  "chat.message": async ({ sessionID }) => {
    await report({ type: "chat.message", properties: { sessionID } }, "user-prompt", sessionID);
  },
  event: async ({ event }) => {
    const properties = event?.properties || {};
    const id = sessionID(properties);
    if (properties.info?.id && properties.info.parentID) {
      children.add(properties.info.id);
    }
    if (!id || children.has(id)) return;

    switch (event?.type) {
      case "session.created":
        await report(event, "session-start", id, properties.info?.title || "");
        break;
      case "session.status":
        await report(event, statusKind(properties.status), id, properties.status?.message || "");
        break;
      case "session.idle":
        await report(event, "agent-stop", id);
        break;
      case "session.error":
        await report(event, "failed", id, properties.error?.message || "");
        break;
      case "session.deleted":
        await report(event, "session-end", id);
        break;
      case "permission.asked":
      case "question.asked":
        await report(event, "question", id);
        break;
      case "permission.replied":
      case "question.replied":
      case "question.rejected":
      case "session.compacted":
        await report(event, "session-start", id);
        break;
    }
  },
});
`, managedIntegrationVersion, 1)
	return strings.ReplaceAll(source, "__TGO_TEMPLATE__", "`")
}

func runIntegrationCommand(args []string, _ io.Reader, output io.Writer) error {
	if len(args) == 0 {
		return usageError{errors.New("integration command required (list, status, install, update, uninstall, doctor)")}
	}
	manager, err := newSetupManager()
	if err != nil {
		return err
	}
	subcommand := args[0]
	flags := flag.NewFlagSet("tgo integration "+subcommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var asJSON, allDetected bool
	flags.BoolVar(&asJSON, "json", false, "print JSON")
	flags.BoolVar(&allDetected, "all-detected", false, "select all detected harnesses")
	flagArgs := make([]string, 0, len(args)-1)
	ids := make([]string, 0, len(args)-1)
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
		} else {
			ids = append(ids, arg)
		}
	}
	if err := flags.Parse(flagArgs); err != nil {
		return usageError{err}
	}
	ids = append(ids, flags.Args()...)
	if subcommand == "list" {
		if len(ids) != 0 {
			return usageError{errors.New("integration list does not accept harness IDs")}
		}
		for _, definition := range setupHarnessDefinitions() {
			fmt.Fprintf(output, "%-10s %-20s v%d  %s\n", definition.ID, definition.Label, definition.IntegrationVersion, definition.LifecycleMode)
		}
		return nil
	}
	if subcommand == "status" || subcommand == "doctor" {
		definitions, selectErr := selectDefinitions(ids, false, manager)
		if selectErr != nil {
			return usageError{selectErr}
		}
		reports := make([]integrationReport, 0, len(definitions))
		for _, definition := range definitions {
			driver, _ := agentDriverByID(definition.ID)
			if subcommand == "doctor" {
				reports = append(reports, driver.Doctor(manager))
			} else {
				reports = append(reports, driver.Inspect(manager))
			}
		}
		if subcommand == "doctor" {
			for index := range reports {
				reports[index].Details = append(reports[index].Details, "doctor checks only tgo ingestion and local wiring; harness interaction is not simulated")
			}
		}
		if asJSON {
			return json.NewEncoder(output).Encode(reports)
		}
		for _, report := range reports {
			printIntegrationReport(output, report)
		}
		return nil
	}
	if subcommand == "install" || subcommand == "update" || subcommand == "uninstall" {
		if allDetected && len(ids) != 0 {
			return usageError{errors.New("--all-detected cannot be combined with harness IDs")}
		}
		detectedOnly := allDetected
		if subcommand == "update" && allDetected {
			return usageError{errors.New("--all-detected is not valid with integration update")}
		}
		definitions, selectErr := selectDefinitions(ids, detectedOnly, manager)
		if selectErr != nil {
			return usageError{selectErr}
		}
		if subcommand == "update" {
			filtered := definitions[:0]
			for _, definition := range definitions {
				status, _ := manager.inspect(definition)
				if status != setupStatusMissing {
					filtered = append(filtered, definition)
				}
			}
			definitions = filtered
		}
		if len(definitions) == 0 {
			return errors.New("no matching harnesses found")
		}
		var results []setupResult
		if subcommand == "uninstall" {
			results = manager.uninstall(definitions)
		} else {
			rows := make([]setupHarnessRow, 0, len(definitions))
			for _, definition := range definitions {
				status, detail := manager.inspect(definition)
				rows = append(rows, setupHarnessRow{Definition: definition, Selected: true, Status: status, Detail: detail})
			}
			results = manager.apply(rows)
		}
		if asJSON {
			encoded := make([]integrationResult, 0, len(results))
			for _, result := range results {
				item := integrationResult{Harness: result.Harness, Changed: result.Changed, Message: result.Message}
				if result.Err != nil {
					item.Error = result.Err.Error()
				}
				encoded = append(encoded, item)
			}
			return json.NewEncoder(output).Encode(encoded)
		}
		failed := false
		for _, result := range results {
			if result.Err != nil {
				failed = true
				fmt.Fprintf(output, "%s: error: %v\n", result.Harness, result.Err)
			} else {
				fmt.Fprintf(output, "%s: %s\n", result.Harness, result.Message)
			}
		}
		if failed {
			return errors.New("one or more integrations failed")
		}
		return nil
	}
	return usageError{fmt.Errorf("unknown integration command %q", subcommand)}
}

func selectDefinitions(ids []string, detectedOnly bool, manager *setupManager) ([]setupHarnessDefinition, error) {
	if len(ids) == 0 && !detectedOnly {
		return setupHarnessDefinitions(), nil
	}
	wanted := make(map[string]bool)
	if len(ids) > 0 {
		for _, id := range ids {
			if harnessDefinitionByID(id).Label == "" {
				return nil, fmt.Errorf("unknown harness %q", id)
			}
			wanted[id] = true
		}
	}
	definitions := make([]setupHarnessDefinition, 0)
	for _, definition := range setupHarnessDefinitions() {
		if len(ids) > 0 && !wanted[definition.ID] {
			continue
		}
		if detectedOnly {
			driver, ok := agentDriverByID(definition.ID)
			detected := ok
			if detected {
				_, detected = driver.Detect(manager.lookPath)
			}
			if !detected {
				continue
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func printIntegrationReport(output io.Writer, report integrationReport) {
	fmt.Fprintf(output, "%s\n", report.Label)
	if report.ExecutableDetected {
		fmt.Fprintln(output, "  ✓ executable detected (version not verified)")
	} else {
		fmt.Fprintln(output, "  - executable not detected")
	}
	if report.Installed {
		fmt.Fprintln(output, "  ✓ integration files installed")
	} else {
		fmt.Fprintln(output, "  ! integration files not installed")
	}
	if report.Configured {
		fmt.Fprintln(output, "  ✓ hook wiring configured")
	} else {
		fmt.Fprintln(output, "  ! hook wiring needs attention")
	}
	if report.Verified {
		fmt.Fprintln(output, "  ✓ synthetic event reached tgo")
	}
	if report.ActivelyReporting {
		fmt.Fprintln(output, "  ✓ actively reporting")
	} else {
		fmt.Fprintln(output, "  ! no recent event received")
	}
	if report.TrustReview != "not-applicable" {
		fmt.Fprintf(output, "  ! hook trust/review: %s\n", report.TrustReview)
	}
	fmt.Fprintf(output, "  status: %s\n", report.Status)
}
