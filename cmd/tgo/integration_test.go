package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSurgicalUninstallPreservesUnrelatedHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	manager := &setupManager{
		home: home,
		lookPath: func(command string) (string, error) {
			if command == "codex" {
				return "/fake/codex", nil
			}
			return "", errors.New("not installed")
		},
	}
	definition := setupDefinition("codex")
	configPath := manager.configPath("codex")
	root := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "user-hook"}}},
			},
		},
	}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if results := manager.apply([]setupHarnessRow{{Definition: definition, Selected: true}}); len(results) != 1 || results[0].Err != nil {
		t.Fatalf("install result = %+v", results)
	}
	results := manager.uninstall([]setupHarnessDefinition{definition})
	if len(results) != 1 || results[0].Err != nil || !results[0].Changed {
		t.Fatalf("uninstall result = %+v", results)
	}
	cleaned, _, err := loadJSONConfig(configPath)
	if err != nil {
		t.Fatalf("load cleaned config: %v", err)
	}
	cleanedData, err := json.Marshal(cleaned)
	if err != nil {
		t.Fatalf("marshal cleaned config: %v", err)
	}
	if !strings.Contains(string(cleanedData), "user-hook") || strings.Contains(string(cleanedData), manager.hookScriptPath("codex")) {
		t.Fatalf("uninstall changed unrelated config: %s", cleanedData)
	}
	if _, err := os.Stat(manager.hookScriptPath("codex")); !os.IsNotExist(err) {
		t.Fatalf("managed script still exists: %v", err)
	}
}

func TestUninstallRefusesChangedManagedScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	manager := &setupManager{home: home, lookPath: func(string) (string, error) { return "/fake/codex", nil }}
	definition := setupDefinition("codex")
	path := manager.hookScriptPath("codex")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create hook dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(agentHookScript("codex")+"\n# user change\n"), 0o700); err != nil {
		t.Fatalf("write changed script: %v", err)
	}
	results := manager.uninstall([]setupHarnessDefinition{definition})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("changed script was removed: %+v", results)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("changed script disappeared: %v", err)
	}
}

func TestSetupPreservesSymlinkedConfigurationPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	manager := &setupManager{home: home, lookPath: func(string) (string, error) { return "/fake/codex", nil }}
	definition := setupDefinition("codex")
	configPath := manager.configPath(definition.ID)
	targetPath := filepath.Join(home, "shared", "codex-hooks.json")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		t.Fatalf("create shared config directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatalf("write shared config: %v", err)
	}
	if err := os.Symlink(targetPath, configPath); err != nil {
		t.Fatalf("create config symlink: %v", err)
	}
	results := manager.apply([]setupHarnessRow{{Definition: definition, Selected: true}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("install through symlink = %+v", results)
	}
	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatalf("stat config symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("configuration symlink was replaced: mode %v", info.Mode())
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read shared config: %v", err)
	}
	if !strings.Contains(string(data), manager.hookScriptPath(definition.ID)) {
		t.Fatalf("shared config was not updated: %s", data)
	}
}

func TestHistoricalOpenCodeV4PluginRemainsOwned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tgo-agent-state.js")
	if err := os.WriteFile(path, []byte(legacyOpenCodePluginSourceV4()), 0o600); err != nil {
		t.Fatalf("write historical plugin: %v", err)
	}
	definition := setupDefinition("opencode")
	if !managedOwnedFile(path, 4, definition, openCodePluginSource()) {
		t.Fatal("historical OpenCode v4 plugin was not recognized as tgo-owned")
	}
}

func TestIntegrationReportSeparatesVerificationAndActiveReporting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	manager := &setupManager{home: home, lookPath: func(command string) (string, error) {
		if command == "codex" {
			return "/fake/codex", nil
		}
		return "", errors.New("not installed")
	}}
	definition := setupDefinition("codex")
	if results := manager.apply([]setupHarnessRow{{Definition: definition, Selected: true}}); results[0].Err != nil {
		t.Fatalf("install: %+v", results[0])
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	report := manager.report(definition)
	if !report.ExecutableDetected || !report.Installed || !report.Configured || !report.Verified {
		t.Fatalf("report did not separate local health: %+v", report)
	}
	if report.ActivelyReporting {
		t.Fatal("synthetic doctor event was treated as active reporting")
	}
}
