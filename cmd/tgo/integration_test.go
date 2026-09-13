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
