package main

import (
	"errors"
	"testing"
)

func TestAgentDriverCatalogHasIndependentIntegrationVersionsAndPolicies(t *testing.T) {
	seen := make(map[string]int)
	for _, driver := range agentDrivers() {
		seen[driver.ID()] = driver.IntegrationVersion()
		if _, ok := driver.Detect(func(command string) (string, error) { return "/bin/" + command, nil }); !ok {
			t.Errorf("%s did not detect its executable", driver.ID())
		}
		if len(driver.ScreenRules()) == 0 {
			t.Errorf("%s has no screen rules", driver.ID())
		}
	}
	if len(seen) != 5 || seen["opencode"] == seen["codex"] && seen["codex"] == seen["claude"] {
		t.Fatalf("integration versions are not per harness: %v", seen)
	}
	if driver, ok := agentDriverByID("opencode"); !ok || driver.LifecycleMode() != lifecycleAuthoritative {
		t.Fatal("OpenCode is not lifecycle authoritative")
	}
	if driver, ok := agentDriverByID("claude"); !ok || driver.LifecycleMode() != lifecycleHybrid {
		t.Fatal("Claude is not marked hybrid")
	}
	if driver, ok := agentDriverByID("codex"); !ok || driver.SessionCapability() != capabilityRich {
		t.Fatal("Codex session capability was not recorded")
	}
	if _, ok := agentDriverByID("missing"); ok {
		t.Fatal("unknown driver was returned")
	}
	driver := catalogAgentDriver{}
	if _, ok := driver.Detect(func(string) (string, error) { return "", errors.New("missing") }); ok {
		t.Fatal("missing executable was detected")
	}
}
