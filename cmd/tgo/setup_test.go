package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupDiscoverShowsOnlyInstalledHarnesses(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("COPILOT_HOME", filepath.Join(home, "copilot"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))

	manager := &setupManager{
		home: home,
		lookPath: func(command string) (string, error) {
			if command == "opencode" || command == "gemini" {
				return filepath.Join("/fake/bin", command), nil
			}
			return "", errors.New("not installed")
		},
	}
	rows := manager.discover()
	if len(rows) != 2 {
		t.Fatalf("discovered %d harnesses, want 2", len(rows))
	}
	if rows[0].Definition.ID != "opencode" || rows[1].Definition.ID != "gemini" {
		t.Fatalf("discovered harnesses = %q, %q", rows[0].Definition.ID, rows[1].Definition.ID)
	}
	for _, row := range rows {
		if !row.Selected {
			t.Errorf("%s should be selected by default", row.Definition.ID)
		}
	}
}

func TestMergeHarnessHooksPreservesOtherHooks(t *testing.T) {
	definition := setupDefinition("codex")
	scriptPath := "/tmp/tgo-codex.sh"
	root := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "herdr-session-start",
						},
					},
				},
			},
		},
	}
	if err := mergeHarnessHooks(root, definition, scriptPath); err != nil {
		t.Fatalf("merge hooks: %v", err)
	}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal hooks: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "herdr-session-start") {
		t.Fatalf("existing hook was removed: %s", text)
	}
	for _, spec := range definition.HookSpecs {
		if !strings.Contains(text, hookCommand(scriptPath, spec.Event)) {
			t.Errorf("missing %s hook in %s", spec.Event, text)
		}
	}
}

func TestMergeHarnessHooksDoesNotRemovePathFromDescription(t *testing.T) {
	definition := setupDefinition("codex")
	scriptPath := "/tmp/tgo-codex.sh"
	root := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"description": scriptPath,
					"hooks":       []any{},
				},
			},
		},
	}
	if err := mergeHarnessHooks(root, definition, scriptPath); err != nil {
		t.Fatalf("merge hooks: %v", err)
	}
	entries := root["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 2 {
		t.Fatalf("description-only entry was removed: %+v", entries)
	}
}

func TestSetupInspectRequiresCommandsUnderTheirLifecycleEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	manager := &setupManager{home: home}
	definition := setupDefinition("codex")
	scriptPath := manager.hookScriptPath(definition.ID)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		t.Fatalf("create hook directory: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte(agentHookScript(definition.ID)), 0o700); err != nil {
		t.Fatalf("write hook script: %v", err)
	}

	wrongEventEntries := make([]any, 0, len(definition.HookSpecs))
	for _, spec := range definition.HookSpecs {
		wrongEventEntries = append(wrongEventEntries, map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": hookCommand(scriptPath, spec.Kind),
			}},
		})
	}
	root := map[string]any{"hooks": map[string]any{"SessionStart": wrongEventEntries}}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.configPath(definition.ID)), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(manager.configPath(definition.ID), data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	status, _ := manager.inspect(definition)
	if status != setupStatusNeedsUpdate {
		t.Fatalf("status = %d, want update needed", status)
	}
}

func TestSetupManagerIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("COPILOT_HOME", filepath.Join(home, "copilot"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))

	manager := &setupManager{
		home: home,
		lookPath: func(command string) (string, error) {
			if command == "codex" {
				return filepath.Join("/fake/bin", command), nil
			}
			return "", errors.New("not installed")
		},
	}
	rows := manager.discover()
	if len(rows) != 1 || rows[0].Definition.ID != "codex" {
		t.Fatalf("discovered rows = %+v", rows)
	}
	results := manager.apply(rows)
	if len(results) != 1 || results[0].Err != nil || !results[0].Changed {
		t.Fatalf("first setup result = %+v", results)
	}

	status, detail := manager.inspect(rows[0].Definition)
	if status != setupStatusCurrent || detail != "up to date" {
		t.Fatalf("status after setup = (%d, %q)", status, detail)
	}
	second := manager.apply(rows)
	if len(second) != 1 || second[0].Err != nil || second[0].Changed {
		t.Fatalf("second setup result = %+v", second)
	}

	configPath := manager.configPath("codex")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if strings.Count(string(data), manager.hookScriptPath("codex")) != len(setupDefinition("codex").HookSpecs) {
		t.Fatalf("generated config contains duplicate tgo hooks: %s", data)
	}
}

func TestSetupManagerWritesEverySupportedIntegrationShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("COPILOT_HOME", filepath.Join(home, "copilot"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	manager := &setupManager{
		home: home,
		lookPath: func(command string) (string, error) {
			return filepath.Join("/fake/bin", command), nil
		},
	}
	rows := manager.discover()
	if len(rows) != len(setupHarnessDefinitions()) {
		t.Fatalf("discovered %d harnesses, want %d", len(rows), len(setupHarnessDefinitions()))
	}
	for _, result := range manager.apply(rows) {
		if result.Err != nil {
			t.Fatalf("setup %s: %v", result.Harness, result.Err)
		}
	}
	for _, definition := range setupHarnessDefinitions() {
		var status setupStatus
		if definition.Integration == integrationOpenCode {
			status, _ = manager.inspectOpenCode()
		} else {
			status, _ = manager.inspect(definition)
		}
		if status != setupStatusCurrent {
			t.Errorf("%s status = %d, want current", definition.ID, status)
		}
	}
}

func TestCopilotSetupIncludesBothShells(t *testing.T) {
	path := "/tmp/tgo-copilot.sh"
	entry := setupHookEntry("copilot", setupHookSpec{Kind: "session-start"}, hookCommand(path, "session-start"), path)
	if entry["type"] != "command" {
		t.Fatalf("hook type = %v", entry["type"])
	}
	if entry["bash"] != hookCommand(path, "session-start") {
		t.Fatalf("bash command = %v", entry["bash"])
	}
	if entry["powershell"] != powershellHookCommand(powershellHookPath(path), "session-start") {
		t.Fatalf("PowerShell command = %v", entry["powershell"])
	}
	if !strings.Contains(agentHookPowerShellScript("copilot"), "param(") {
		t.Fatal("PowerShell hook is missing its parameter declaration")
	}
}

func TestSetupDoesNotOverwriteUnmanagedOpenCodePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	manager := &setupManager{home: home, lookPath: func(string) (string, error) { return "/fake/opencode", nil }}
	path := manager.openCodePluginPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create plugin directory: %v", err)
	}
	original := []byte("export const plugin = () => ({})\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	changed, err := manager.applyOpenCode()
	if err == nil || changed {
		t.Fatalf("unmanaged plugin was accepted: changed=%t err=%v", changed, err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read plugin: %v", readErr)
	}
	if string(data) != string(original) {
		t.Fatalf("unmanaged plugin changed: %s", data)
	}
}

func TestSetupRefusesChangedCurrentHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	manager := &setupManager{home: home}
	definition := setupDefinition("codex")
	path := manager.hookScriptPath(definition.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create hook directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(agentHookScript(definition.ID)+"\n# changed\n"), 0o700); err != nil {
		t.Fatalf("write changed hook: %v", err)
	}
	status, detail := manager.inspect(definition)
	if status != setupStatusConflict || !strings.Contains(detail, "changed") {
		t.Fatalf("changed current hook status = (%d, %q), want conflict", status, detail)
	}
}

func TestOpenCodePluginSourceIsValidJavaScript(t *testing.T) {
	source := openCodePluginSource()
	if !strings.Contains(source, "await $`") || strings.Contains(source, "__TGO_TEMPLATE__") {
		t.Fatalf("generated plugin does not contain the OpenCode shell hook: %s", source)
	}
	if !strings.Contains(source, "agent ingest opencode ${nativeKind} --json") ||
		strings.Contains(source, "agent ingest opencode ${JSON.stringify(nativeKind)}") {
		t.Fatalf("native event name is not passed as a Bun shell argument: %s", source)
	}
	if !strings.Contains(source, "// tgo integration version: 5") {
		t.Fatalf("generated plugin has the wrong integration version: %s", source)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	path := filepath.Join(t.TempDir(), "tgo-agent-state.js")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write plugin source: %v", err)
	}
	if output, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("generated plugin is invalid: %v\n%s", err, output)
	}
}

func TestLegacyOpenCodeV4SourceUsesTheHistoricalCommand(t *testing.T) {
	source := legacyOpenCodePluginSourceV4()
	if !strings.Contains(source, "// tgo integration version: 4") {
		t.Fatalf("historical plugin has the wrong integration version: %s", source)
	}
	if !strings.Contains(source, "agent ingest opencode ${JSON.stringify(nativeKind)} --json") {
		t.Fatalf("historical plugin does not preserve its original command: %s", source)
	}
}

func TestAgentHookScriptIsValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	path := filepath.Join(t.TempDir(), "codex.sh")
	if err := os.WriteFile(path, []byte(agentHookScript("codex")), 0o700); err != nil {
		t.Fatalf("write hook script: %v", err)
	}
	if output, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("generated hook script is invalid: %v\n%s", err, output)
	}
}

func TestAgentHookScriptForwardsHarnessPayload(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	tempDir := t.TempDir()
	fakeTgo := filepath.Join(tempDir, "tgo")
	argsPath := filepath.Join(tempDir, "args")
	inputPath := filepath.Join(tempDir, "input")
	fakeSource := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$TGO_CAPTURE_ARGS\"\ncat > \"$TGO_CAPTURE_INPUT\"\n"
	if err := os.WriteFile(fakeTgo, []byte(fakeSource), 0o700); err != nil {
		t.Fatalf("write fake tgo: %v", err)
	}
	hookPath := filepath.Join(tempDir, "codex.sh")
	if err := os.WriteFile(hookPath, []byte(agentHookScript("codex")), 0o700); err != nil {
		t.Fatalf("write hook script: %v", err)
	}
	command := exec.Command(sh, hookPath, "session-start")
	command.Stdin = strings.NewReader(`{"session_id":"codex-session"}`)
	command.Env = append(os.Environ(),
		"TGO_BIN="+fakeTgo,
		"TGO_CAPTURE_ARGS="+argsPath,
		"TGO_CAPTURE_INPUT="+inputPath,
		"TMUX_PANE=%7",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run hook script: %v\n%s", err, output)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read forwarded args: %v", err)
	}
	for _, expected := range []string{"agent ingest codex session-start", "--pane %7"} {
		if !strings.Contains(string(args), expected) {
			t.Errorf("forwarded args %q do not contain %q", args, expected)
		}
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read forwarded input: %v", err)
	}
	if string(input) != `{"session_id":"codex-session"}` {
		t.Fatalf("hook payload = %q", input)
	}
}

func TestCopilotPowerShellHookForwardsHarnessPayload(t *testing.T) {
	powershell, err := exec.LookPath("pwsh")
	if err != nil {
		powershell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("PowerShell is not installed")
	}
	tempDir := t.TempDir()
	fakeTgo := filepath.Join(tempDir, "tgo")
	argsPath := filepath.Join(tempDir, "args")
	inputPath := filepath.Join(tempDir, "input")
	fakeSource := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$TGO_CAPTURE_ARGS\"\ncat > \"$TGO_CAPTURE_INPUT\"\n"
	if err := os.WriteFile(fakeTgo, []byte(fakeSource), 0o700); err != nil {
		t.Fatalf("write fake tgo: %v", err)
	}
	hookPath := filepath.Join(tempDir, "copilot.ps1")
	if err := os.WriteFile(hookPath, []byte(agentHookPowerShellScript("copilot")), 0o600); err != nil {
		t.Fatalf("write PowerShell hook: %v", err)
	}
	command := exec.Command(powershell, "-NoProfile", "-File", hookPath, "sessionStart")
	command.Stdin = strings.NewReader(`{"sessionId":"copilot-session","turnId":"turn-1"}`)
	command.Env = append(os.Environ(),
		"TGO_BIN="+fakeTgo,
		"TGO_CAPTURE_ARGS="+argsPath,
		"TGO_CAPTURE_INPUT="+inputPath,
		"TMUX_PANE=%8",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run PowerShell hook: %v\n%s", err, output)
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read forwarded PowerShell payload: %v", err)
	}
	if string(input) != `{"sessionId":"copilot-session","turnId":"turn-1"}` {
		t.Fatalf("PowerShell hook payload = %q", input)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read PowerShell hook arguments: %v", err)
	}
	for _, expected := range []string{"agent ingest copilot sessionStart", "--pane %8"} {
		if !strings.Contains(string(args), expected) {
			t.Errorf("PowerShell hook arguments %q do not contain %q", args, expected)
		}
	}
}

func TestSetupPickerSelectAllAndNone(t *testing.T) {
	picker := newSetupPicker([]setupHarnessRow{
		{Selected: true},
		{Selected: true},
	})
	picker.setSelected(false)
	for _, row := range picker.rows {
		if row.Selected {
			t.Fatal("setSelected(false) left a row selected")
		}
	}
	picker.setSelected(true)
	for _, row := range picker.rows {
		if !row.Selected {
			t.Fatal("setSelected(true) left a row unselected")
		}
	}
	picker.move(-1)
	if picker.cursor != 1 {
		t.Fatalf("cursor wrap = %d, want 1", picker.cursor)
	}
}

func setupDefinition(id string) setupHarnessDefinition {
	for _, definition := range setupHarnessDefinitions() {
		if definition.ID == id {
			return definition
		}
	}
	return setupHarnessDefinition{}
}
