package main

import "os"

// AgentDriver is the harness-facing contract. Detection, lifecycle evidence,
// screen semantics, and integration management live behind the same catalog
// entry, while the registry itself remains harness-neutral.
type AgentDriver interface {
	ID() string
	Label() string
	Executables() []string
	IntegrationVersion() int
	MinTestedVersion() string
	Detect(lookup func(string) (string, error)) (string, bool)
	Inspect(*setupManager) integrationReport
	Install(*setupManager) setupResult
	Uninstall(*setupManager) setupResult
	Doctor(*setupManager) integrationReport
	LifecycleMode() lifecycleMode
	LifecycleCapability() capabilityLevel
	ScreenCapability() capabilityLevel
	SessionCapability() capabilityLevel
	ResumeCapability() capabilityLevel
	ScreenRules() []screenRule
}

type catalogAgentDriver struct {
	definition setupHarnessDefinition
}

func (driver catalogAgentDriver) ID() string    { return driver.definition.ID }
func (driver catalogAgentDriver) Label() string { return driver.definition.Label }
func (driver catalogAgentDriver) Executables() []string {
	return append([]string(nil), driver.definition.Executables...)
}
func (driver catalogAgentDriver) IntegrationVersion() int {
	return driver.definition.IntegrationVersion
}
func (driver catalogAgentDriver) MinTestedVersion() string { return driver.definition.MinTestedVersion }
func (driver catalogAgentDriver) LifecycleMode() lifecycleMode {
	return driver.definition.LifecycleMode
}
func (driver catalogAgentDriver) LifecycleCapability() capabilityLevel {
	return driver.definition.LifecycleCapability
}
func (driver catalogAgentDriver) ScreenCapability() capabilityLevel {
	return driver.definition.ScreenCapability
}
func (driver catalogAgentDriver) SessionCapability() capabilityLevel {
	return driver.definition.SessionCapability
}
func (driver catalogAgentDriver) ResumeCapability() capabilityLevel {
	return driver.definition.ResumeCapability
}
func (driver catalogAgentDriver) ScreenRules() []screenRule {
	return append([]screenRule(nil), driver.definition.ScreenRules...)
}
func (driver catalogAgentDriver) Detect(lookup func(string) (string, error)) (string, bool) {
	if lookup == nil {
		lookup = func(command string) (string, error) { return "", os.ErrNotExist }
	}
	for _, executable := range driver.definition.Executables {
		if path, err := lookup(executable); err == nil {
			return path, true
		}
	}
	return "", false
}
func (driver catalogAgentDriver) Inspect(manager *setupManager) integrationReport {
	return manager.report(driver.definition)
}
func (driver catalogAgentDriver) Install(manager *setupManager) setupResult {
	results := manager.apply([]setupHarnessRow{{Definition: driver.definition, Selected: true}})
	if len(results) == 0 {
		return setupResult{Harness: driver.Label(), Err: os.ErrInvalid}
	}
	return results[0]
}
func (driver catalogAgentDriver) Uninstall(manager *setupManager) setupResult {
	results := manager.uninstall([]setupHarnessDefinition{driver.definition})
	if len(results) == 0 {
		return setupResult{Harness: driver.Label(), Err: os.ErrInvalid}
	}
	return results[0]
}
func (driver catalogAgentDriver) Doctor(manager *setupManager) integrationReport {
	report := manager.report(driver.definition)
	report.Details = append(report.Details, "doctor checks local tgo ingestion; harness interaction is not simulated")
	return report
}

func agentDrivers() []AgentDriver {
	definitions := setupHarnessDefinitions()
	drivers := make([]AgentDriver, 0, len(definitions))
	for _, definition := range definitions {
		drivers = append(drivers, catalogAgentDriver{definition: definition})
	}
	return drivers
}

func agentDriverByID(id string) (AgentDriver, bool) {
	for _, driver := range agentDrivers() {
		if driver.ID() == id {
			return driver, true
		}
	}
	return nil, false
}
