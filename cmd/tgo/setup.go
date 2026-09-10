package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

const (
	tgoIntegrationVersion      = 1
	managedIntegrationVersion  = "tgo integration version: "
	setupJSONFileMode          = 0o600
	setupManagedScriptFileMode = 0o700
	setupManagedPluginFileMode = 0o600
	setupLockTimeout           = 5 * time.Second
)

type setupHookSpec struct {
	Event string
	Kind  string
}

type setupHarnessDefinition struct {
	ID        string
	Label     string
	Command   string
	HookSpecs []setupHookSpec
	OpenCode  bool
}

func setupHarnessDefinitions() []setupHarnessDefinition {
	return []setupHarnessDefinition{
		{
			ID:       "opencode",
			Label:    "OpenCode",
			Command:  "opencode",
			OpenCode: true,
		},
		{
			ID:      "codex",
			Label:   "Codex",
			Command: "codex",
			HookSpecs: []setupHookSpec{
				{Event: "SessionStart", Kind: "session-start"},
				{Event: "UserPromptSubmit", Kind: "user-prompt"},
				{Event: "PermissionRequest", Kind: "question"},
				{Event: "Stop", Kind: "agent-stop"},
				{Event: "SessionEnd", Kind: "session-end"},
			},
		},
		{
			ID:      "gemini",
			Label:   "Gemini CLI",
			Command: "gemini",
			HookSpecs: []setupHookSpec{
				{Event: "SessionStart", Kind: "session-start"},
				{Event: "BeforeAgent", Kind: "user-prompt"},
				{Event: "AfterAgent", Kind: "agent-stop"},
				{Event: "SessionEnd", Kind: "session-end"},
			},
		},
		{
			ID:      "copilot",
			Label:   "GitHub Copilot",
			Command: "copilot",
			HookSpecs: []setupHookSpec{
				{Event: "sessionStart", Kind: "session-start"},
				{Event: "userPromptSubmitted", Kind: "user-prompt"},
				{Event: "agentStop", Kind: "agent-stop"},
				{Event: "sessionEnd", Kind: "session-end"},
				{Event: "errorOccurred", Kind: "failed"},
			},
		},
		{
			ID:      "claude",
			Label:   "Claude Code",
			Command: "claude",
			HookSpecs: []setupHookSpec{
				{Event: "SessionStart", Kind: "session-start"},
				{Event: "UserPromptSubmit", Kind: "user-prompt"},
				{Event: "PermissionRequest", Kind: "question"},
				{Event: "Stop", Kind: "agent-stop"},
				{Event: "SessionEnd", Kind: "session-end"},
			},
		},
	}
}

type setupStatus int

const (
	setupStatusMissing setupStatus = iota
	setupStatusCurrent
	setupStatusNeedsUpdate
	setupStatusConflict
)

type setupHarnessRow struct {
	Definition setupHarnessDefinition
	BinaryPath string
	Selected   bool
	Status     setupStatus
	Detail     string
}

type setupManager struct {
	home     string
	lookPath func(string) (string, error)
}

func newSetupManager() (*setupManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	return &setupManager{home: home, lookPath: exec.LookPath}, nil
}

func (m *setupManager) discover() []setupHarnessRow {
	rows := make([]setupHarnessRow, 0)
	for _, definition := range setupHarnessDefinitions() {
		binaryPath, err := m.lookPath(definition.Command)
		if err != nil {
			continue
		}
		status, detail := m.inspect(definition)
		rows = append(rows, setupHarnessRow{
			Definition: definition,
			BinaryPath: binaryPath,
			Selected:   true,
			Status:     status,
			Detail:     detail,
		})
	}
	return rows
}

func (m *setupManager) inspect(definition setupHarnessDefinition) (setupStatus, string) {
	if definition.OpenCode {
		return m.inspectOpenCode()
	}

	scriptPath := m.hookScriptPath(definition.ID)
	scriptPaths := m.hookScriptPaths(definition.ID)
	scriptsCurrent := true
	anyScriptExists := false
	for _, path := range scriptPaths {
		scriptVersion, scriptExists, scriptErr := readManagedVersion(path)
		if scriptErr != nil {
			return setupStatusConflict, scriptErr.Error()
		}
		if scriptExists {
			anyScriptExists = true
		}
		if scriptVersion > tgoIntegrationVersion {
			return setupStatusConflict, fmt.Sprintf("newer integration version %d", scriptVersion)
		}
		expected := agentHookScript(definition.ID)
		if definition.ID == "copilot" && strings.HasSuffix(path, ".ps1") {
			expected = agentHookPowerShellScript(definition.ID)
		}
		if !scriptExists || scriptVersion != tgoIntegrationVersion || !managedFileMatches(path, expected) {
			scriptsCurrent = false
		}
	}

	configPath := m.configPath(definition.ID)
	root, configExists, configErr := loadJSONConfig(configPath)
	if configErr != nil {
		return setupStatusConflict, configErr.Error()
	}
	commandsPresent := configExists && configContainsAllCommands(root, definition, scriptPath)
	if scriptsCurrent && commandsPresent {
		return setupStatusCurrent, "up to date"
	}
	if anyScriptExists || configExists {
		return setupStatusNeedsUpdate, "integration needs an update"
	}
	return setupStatusMissing, "not configured"
}

func (m *setupManager) inspectOpenCode() (setupStatus, string) {
	path := m.openCodePluginPath()
	version, exists, err := readManagedVersion(path)
	if err != nil {
		return setupStatusConflict, err.Error()
	}
	if !exists {
		return setupStatusMissing, "plugin not configured"
	}
	if version > tgoIntegrationVersion {
		return setupStatusConflict, fmt.Sprintf("newer integration version %d", version)
	}
	if version == tgoIntegrationVersion {
		if managedFileMatches(path, openCodePluginSource()) {
			return setupStatusCurrent, "up to date"
		}
		return setupStatusNeedsUpdate, "plugin contents changed"
	}
	return setupStatusNeedsUpdate, "plugin needs an update"
}

func (m *setupManager) apply(rows []setupHarnessRow) []setupResult {
	lockPath := filepath.Join(m.configHome(), "tgo", "setup.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return setupLockResults(rows, fmt.Errorf("create setup lock directory: %w", err))
	}
	lock, err := acquireSetupLock(lockPath)
	if err != nil {
		return setupLockResults(rows, err)
	}
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()
	return m.applyUnlocked(rows)
}

func (m *setupManager) applyUnlocked(rows []setupHarnessRow) []setupResult {
	results := make([]setupResult, 0, len(rows))
	for _, row := range rows {
		if !row.Selected {
			continue
		}
		result := setupResult{Harness: row.Definition.Label}
		if row.Definition.OpenCode {
			result.Changed, result.Err = m.applyOpenCode()
		} else {
			result.Changed, result.Err = m.applyJSONHarness(row.Definition)
		}
		if result.Err == nil {
			if result.Changed {
				result.Message = "configured"
			} else {
				result.Message = "already up to date"
			}
		}
		results = append(results, result)
	}
	return results
}

func acquireSetupLock(path string) (*os.File, error) {
	deadline := time.Now().Add(setupLockTimeout)
	for {
		lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock setup: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock setup: timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func setupLockResults(rows []setupHarnessRow, err error) []setupResult {
	results := make([]setupResult, 0, len(rows))
	for _, row := range rows {
		if row.Selected {
			results = append(results, setupResult{Harness: row.Definition.Label, Err: err})
		}
	}
	return results
}

type setupResult struct {
	Harness string
	Message string
	Changed bool
	Err     error
}

func (m *setupManager) applyOpenCode() (bool, error) {
	path := m.openCodePluginPath()
	status, detail := m.inspectOpenCode()
	if status == setupStatusCurrent {
		return false, nil
	}
	if status == setupStatusConflict {
		return false, fmt.Errorf("OpenCode plugin %s: %s", path, detail)
	}
	if err := writeManagedFile(path, []byte(openCodePluginSource()), setupManagedPluginFileMode); err != nil {
		return false, fmt.Errorf("write OpenCode plugin: %w", err)
	}
	return true, nil
}

func (m *setupManager) applyJSONHarness(definition setupHarnessDefinition) (bool, error) {
	status, detail := m.inspect(definition)
	if status == setupStatusCurrent {
		return false, nil
	}
	if status == setupStatusConflict {
		return false, fmt.Errorf("%s: %s", definition.Label, detail)
	}

	configPath := m.configPath(definition.ID)
	root, _, err := loadJSONConfig(configPath)
	if err != nil {
		return false, err
	}
	scriptPath := m.hookScriptPath(definition.ID)
	if err := mergeHarnessHooks(root, definition, scriptPath); err != nil {
		return false, fmt.Errorf("update %s configuration: %w", definition.Label, err)
	}

	scriptPaths := m.hookScriptPaths(definition.ID)
	previousScripts := make([]fileSnapshot, len(scriptPaths))
	for index, path := range scriptPaths {
		previousScripts[index], err = snapshotFile(path)
		if err != nil {
			return false, fmt.Errorf("snapshot %s hook: %w", definition.Label, err)
		}
		content := agentHookScript(definition.ID)
		if definition.ID == "copilot" && strings.HasSuffix(path, ".ps1") {
			content = agentHookPowerShellScript(definition.ID)
		}
		if err := writeManagedFile(path, []byte(content), setupManagedScriptFileMode); err != nil {
			if restoreErr := restoreFiles(scriptPaths[:index], previousScripts[:index]); restoreErr != nil {
				return false, fmt.Errorf("write %s hook: %w (restore hook: %v)", definition.Label, err, restoreErr)
			}
			return false, fmt.Errorf("write %s hook: %w", definition.Label, err)
		}
	}

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode %s configuration: %w", definition.Label, err)
	}
	data = append(data, '\n')
	if err := writeManagedJSONFile(configPath, data); err != nil {
		if restoreErr := restoreFiles(scriptPaths, previousScripts); restoreErr != nil {
			return false, fmt.Errorf("write %s configuration: %w (restore hook: %v)", definition.Label, err, restoreErr)
		}
		return false, fmt.Errorf("write %s configuration: %w", definition.Label, err)
	}
	return true, nil
}

func (m *setupManager) configHome() string {
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		return value
	}
	return filepath.Join(m.home, ".config")
}

func (m *setupManager) hookScriptPath(harness string) string {
	return filepath.Join(m.configHome(), "tgo", "hooks", harness+".sh")
}

func (m *setupManager) hookScriptPaths(harness string) []string {
	paths := []string{m.hookScriptPath(harness)}
	if harness == "copilot" {
		paths = append(paths, filepath.Join(m.configHome(), "tgo", "hooks", harness+".ps1"))
	}
	return paths
}

func (m *setupManager) openCodePluginPath() string {
	return filepath.Join(m.configHome(), "opencode", "plugins", "tgo-agent-state.js")
}

func (m *setupManager) configPath(harness string) string {
	switch harness {
	case "codex":
		return filepath.Join(m.optionalHome("CODEX_HOME", ".codex"), "hooks.json")
	case "gemini":
		return filepath.Join(m.home, ".gemini", "settings.json")
	case "copilot":
		return filepath.Join(m.optionalHome("COPILOT_HOME", ".copilot"), "hooks", "tgo.json")
	case "claude":
		return filepath.Join(m.optionalHome("CLAUDE_CONFIG_DIR", ".claude"), "settings.json")
	default:
		return filepath.Join(m.configHome(), "tgo", harness+".json")
	}
}

func (m *setupManager) optionalHome(environment, suffix string) string {
	if value := os.Getenv(environment); value != "" {
		return value
	}
	return filepath.Join(m.home, suffix)
}

func loadJSONConfig(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]any), false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, true, fmt.Errorf("decode %s: %w", path, err)
	}
	if root == nil {
		return nil, true, fmt.Errorf("decode %s: expected an object", path)
	}
	return root, true, nil
}

func configContainsAllCommands(root map[string]any, definition setupHarnessDefinition, scriptPath string) bool {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return false
	}
	for _, spec := range definition.HookSpecs {
		if definition.ID == "copilot" {
			if !jsonContainsCopilotHookCommand(
				hooks[spec.Event],
				hookCommand(scriptPath, spec.Kind),
				powershellHookCommand(powershellHookPath(scriptPath), spec.Kind),
			) {
				return false
			}
		} else if !jsonContainsHookCommand(hooks[spec.Event], definition.ID, hookCommand(scriptPath, spec.Kind)) {
			return false
		}
	}
	return true
}

func jsonContainsHookCommand(value any, harness, wanted string) bool {
	switch value := value.(type) {
	case string:
		return false
	case []any:
		for _, item := range value {
			if jsonContainsHookCommand(item, harness, wanted) {
				return true
			}
		}
	case map[string]any:
		if isExpectedCommandHandler(value, harness, wanted) {
			return true
		}
		if nested, ok := value["hooks"]; ok && jsonContainsHookCommand(nested, harness, wanted) {
			return true
		}
	}
	return false
}

func jsonContainsCopilotHookCommand(value any, bashCommand, powershellCommand string) bool {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			if jsonContainsCopilotHookCommand(item, bashCommand, powershellCommand) {
				return true
			}
		}
	case map[string]any:
		if value["type"] == "command" && value["bash"] == bashCommand && value["powershell"] == powershellCommand {
			return true
		}
		if nested, ok := value["hooks"]; ok && jsonContainsCopilotHookCommand(nested, bashCommand, powershellCommand) {
			return true
		}
	}
	return false
}

func mergeHarnessHooks(root map[string]any, definition setupHarnessDefinition, scriptPath string) error {
	if definition.ID == "copilot" {
		root["version"] = float64(1)
	}

	hooksValue, ok := root["hooks"]
	if !ok {
		hooksValue = make(map[string]any)
		root["hooks"] = hooksValue
	}
	hooks, ok := hooksValue.(map[string]any)
	if !ok {
		return fmt.Errorf("hooks must be an object")
	}

	for _, spec := range definition.HookSpecs {
		var entries []any
		if value, exists := hooks[spec.Event]; exists {
			var valid bool
			entries, valid = value.([]any)
			if !valid {
				return fmt.Errorf("hooks.%s must be an array", spec.Event)
			}
		}
		filtered := make([]any, 0, len(entries)+1)
		for _, entry := range entries {
			if !jsonContainsManagedCommand(entry, definition, scriptPath) {
				filtered = append(filtered, entry)
			}
		}
		filtered = append(filtered, setupHookEntry(definition.ID, spec, hookCommand(scriptPath, spec.Kind), scriptPath))
		hooks[spec.Event] = filtered
	}
	return nil
}

func jsonContainsManagedCommand(value any, definition setupHarnessDefinition, path string) bool {
	switch value := value.(type) {
	case string:
		return false
	case []any:
		for _, item := range value {
			if jsonContainsManagedCommand(item, definition, path) {
				return true
			}
		}
	case map[string]any:
		if isManagedCommandHandler(value, definition, path) {
			return true
		}
		if nested, ok := value["hooks"]; ok && jsonContainsManagedCommand(nested, definition, path) {
			return true
		}
	}
	return false
}

func isExpectedCommandHandler(value map[string]any, harness, wanted string) bool {
	if value["type"] != "command" {
		return false
	}
	field := "command"
	if harness == "copilot" {
		field = "bash"
	}
	command, ok := value[field].(string)
	return ok && command == wanted
}

func isManagedCommandHandler(value map[string]any, definition setupHarnessDefinition, path string) bool {
	if value["type"] != "command" {
		return false
	}
	if definition.ID == "copilot" {
		bashCommand, bashOK := value["bash"].(string)
		powershellCommand, powershellOK := value["powershell"].(string)
		if !bashOK || !powershellOK {
			return false
		}
		for _, spec := range definition.HookSpecs {
			if bashCommand == hookCommand(path, spec.Kind) && powershellCommand == powershellHookCommand(powershellHookPath(path), spec.Kind) {
				return true
			}
		}
		return false
	}
	field := "command"
	command, ok := value[field].(string)
	if !ok {
		return false
	}
	for _, spec := range definition.HookSpecs {
		if command == hookCommand(path, spec.Kind) {
			return true
		}
	}
	return false
}

func setupHookEntry(harness string, spec setupHookSpec, command, scriptPath string) map[string]any {
	switch harness {
	case "copilot":
		return map[string]any{
			"type":       "command",
			"bash":       command,
			"powershell": powershellHookCommand(powershellHookPath(scriptPath), spec.Kind),
			"timeoutSec": 3,
		}
	case "gemini":
		return map[string]any{
			"hooks": []any{
				map[string]any{
					"name":    "tgo-agent-" + spec.Kind,
					"type":    "command",
					"command": command,
					"timeout": 5000,
				},
			},
		}
	default:
		return map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": command,
					"timeout": 3,
				},
			},
		}
	}
}

func hookCommand(scriptPath, kind string) string {
	return shellQuote(scriptPath) + " " + shellQuote(kind)
}

func powershellHookPath(scriptPath string) string {
	return strings.TrimSuffix(scriptPath, filepath.Ext(scriptPath)) + ".ps1"
}

func powershellHookCommand(scriptPath, kind string) string {
	return "& " + powershellQuote(scriptPath) + " " + powershellQuote(kind)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func readManagedVersion(path string) (int, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, true, fmt.Errorf("read managed file %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, managedIntegrationVersion) {
			continue
		}
		versionText := strings.TrimSpace(strings.TrimPrefix(line, managedIntegrationVersion))
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return 0, true, fmt.Errorf("invalid managed integration version in %s", path)
		}
		return version, true, nil
	}
	return 0, true, fmt.Errorf("%s is not a tgo-managed integration", path)
}

func managedFileMatches(path, expected string) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == expected
}

func writeManagedJSONFile(path string, data []byte) error {
	mode := os.FileMode(setupJSONFileMode)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return writeManagedFile(path, data, mode)
}

type fileSnapshot struct {
	Exists bool
	Data   []byte
	Mode   os.FileMode
}

func snapshotFile(path string) (fileSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileSnapshot{}, nil
		}
		return fileSnapshot{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{Exists: true, Data: data, Mode: info.Mode().Perm()}, nil
}

func restoreFile(path string, snapshot fileSnapshot) error {
	if !snapshot.Exists {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeAtomicFile(path, snapshot.Data, snapshot.Mode)
}

func restoreFiles(paths []string, snapshots []fileSnapshot) error {
	var firstErr error
	for index, path := range paths {
		if err := restoreFile(path, snapshots[index]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func writeManagedFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if _, err := os.Stat(path); err == nil {
		if err := backupManagedFile(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	return writeAtomicFile(path, data, mode)
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tgo-setup-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set permissions for %s: %w", path, err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func backupManagedFile(path string) error {
	backup := path + ".tgo.bak"
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup %s: %w", backup, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read backup source %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat backup source %s: %w", path, err)
	}
	if err := os.WriteFile(backup, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write backup %s: %w", backup, err)
	}
	return nil
}

func agentHookScript(harness string) string {
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
`, managedIntegrationVersion, tgoIntegrationVersion, harness)
}

func agentHookPowerShellScript(harness string) string {
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
	`, managedIntegrationVersion, tgoIntegrationVersion, harness)
	return strings.ReplaceAll(source, "__PS_CONT__", "`")
}

func openCodePluginSource() string {
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
	`, managedIntegrationVersion, tgoIntegrationVersion)
	return strings.ReplaceAll(source, "__TGO_TEMPLATE__", "`")
}

type setupPicker struct {
	rows   []setupHarnessRow
	cursor int
}

func newSetupPicker(rows []setupHarnessRow) *setupPicker {
	return &setupPicker{rows: rows}
}

func (p *setupPicker) Run(screen tcell.Screen) (bool, []setupHarnessRow) {
	screen.HideCursor()
	p.draw(screen)
	for {
		event := screen.PollEvent()
		switch event := event.(type) {
		case *tcell.EventResize:
			screen.Sync()
			p.draw(screen)
		case *tcell.EventKey:
			switch event.Key() {
			case tcell.KeyCtrlC, tcell.KeyEscape:
				return false, nil
			case tcell.KeyEnter:
				return true, p.rows
			case tcell.KeyUp:
				p.move(-1)
			case tcell.KeyDown:
				p.move(1)
			case tcell.KeyRune:
				switch event.Rune() {
				case 'j':
					p.move(1)
				case 'k':
					p.move(-1)
				case ' ':
					p.rows[p.cursor].Selected = !p.rows[p.cursor].Selected
				case 'a':
					p.setSelected(true)
				case 'n':
					p.setSelected(false)
				case 'q':
					return false, nil
				}
			}
			p.draw(screen)
		}
	}
}

func (p *setupPicker) move(delta int) {
	if len(p.rows) == 0 {
		return
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = len(p.rows) - 1
	}
	if p.cursor >= len(p.rows) {
		p.cursor = 0
	}
}

func (p *setupPicker) setSelected(selected bool) {
	for index := range p.rows {
		p.rows[index].Selected = selected
	}
}

func (p *setupPicker) draw(screen tcell.Screen) {
	width, height := screen.Size()
	screen.Clear()
	header := tcell.StyleDefault.Foreground(tcell.ColorAqua).Bold(true)
	help := tcell.StyleDefault.Foreground(tcell.ColorGray)
	selectedStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen)
	updateStyle := tcell.StyleDefault.Foreground(tcell.ColorYellow)
	conflictStyle := tcell.StyleDefault.Foreground(tcell.ColorRed)

	line := 0
	drawSetupText(screen, 0, line, header, "tgo setup - agent integrations")
	line++
	drawSetupText(screen, 0, line, help, truncate("[space] toggle  [a] all  [n] none  [j/k/↑↓] move  [enter] install/update  [esc] cancel", width))
	line++
	drawSetupText(screen, 0, line, help, truncate("Only detected harnesses are shown. Harness binaries are never upgraded.", width))
	line++

	for index, row := range p.rows {
		if line >= height-1 {
			break
		}
		style := tcell.StyleDefault
		if index == p.cursor {
			style = style.Background(tcell.ColorGray).Foreground(tcell.ColorBlack)
		}
		statusStyle := selectedStyle
		if row.Status == setupStatusNeedsUpdate || row.Status == setupStatusMissing {
			statusStyle = updateStyle
		} else if row.Status == setupStatusConflict {
			statusStyle = conflictStyle
		}
		if index == p.cursor {
			statusStyle = style
		}
		check := "[ ]"
		if row.Selected {
			check = "[x]"
		}
		drawSetupText(screen, 0, line, style, truncate(fmt.Sprintf("%s %-16s", check, row.Definition.Label), width))
		drawSetupText(screen, 23, line, statusStyle, truncate(fmt.Sprintf("%-18s", setupStatusLabel(row.Status)), max(width-23, 0)))
		drawSetupText(screen, 42, line, style, truncate(row.Detail, max(width-42, 0)))
		line++
	}

	if len(p.rows) == 0 {
		drawSetupText(screen, 0, line, help, "no supported harnesses found")
	}
	screen.Show()
}

func setupStatusLabel(status setupStatus) string {
	switch status {
	case setupStatusCurrent:
		return "up to date"
	case setupStatusNeedsUpdate:
		return "update needed"
	case setupStatusConflict:
		return "conflict"
	default:
		return "not configured"
	}
}

func drawSetupText(screen tcell.Screen, x, y int, style tcell.Style, text string) {
	for _, r := range text {
		screen.SetContent(x, y, r, nil, style)
		x++
	}
}

func runSetup() error {
	manager, err := newSetupManager()
	if err != nil {
		return err
	}
	rows := manager.discover()
	if len(rows) == 0 {
		fmt.Println("tgo: no supported harnesses are installed")
		return nil
	}

	screen, err := tcell.NewScreen()
	if err != nil {
		return fmt.Errorf("create setup screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("init setup screen: %w", err)
	}
	confirmed, selected := func() (bool, []setupHarnessRow) {
		defer screen.Fini()
		return newSetupPicker(rows).Run(screen)
	}()
	if !confirmed {
		fmt.Println("tgo: setup canceled")
		return nil
	}

	results := manager.apply(selected)
	if len(results) == 0 {
		fmt.Println("tgo: no harnesses selected")
		return nil
	}
	var failures []string
	for _, result := range results {
		if result.Err != nil {
			fmt.Printf("%s: error: %v\n", result.Harness, result.Err)
			failures = append(failures, result.Harness)
			continue
		}
		fmt.Printf("%s: %s\n", result.Harness, result.Message)
	}
	if len(failures) > 0 {
		return fmt.Errorf("setup failed for %s", strings.Join(failures, ", "))
	}
	return nil
}
