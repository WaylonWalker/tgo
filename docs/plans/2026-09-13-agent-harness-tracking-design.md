# Agent Harness Tracking Design

## Goal

tgo must report meaningful agent states without treating a tmux pane or a Unix
process scheduler state as an agent lifecycle state.

The state pipeline is:

```text
tmux pane -> process and session identity -> lifecycle and screen evidence
          -> authority resolver -> working / waiting / idle / stopped / unknown
```

## Driver catalog

Each supported harness has one catalog entry. The entry contains executable
names, an integration version, lifecycle policy, native hook names, and screen
rules. An `AgentDriver` exposes detection, inspection, installation,
uninstallation, and doctor operations. The registry does not contain
harness-specific parsing rules.

OpenCode uses an authoritative lifecycle policy. Codex uses lifecycle evidence
with a screen fallback. Claude Code, Gemini CLI, and GitHub Copilot use hybrid
policies because interrupt and permission cancellation behavior still needs
verification.

## Identity and state

An event records the tmux server and session when available, pane, harness, PID,
process start token, native session ID, native turn ID, and run ID. A PID or
native session change creates a new generation. Events from an older generation
remain in history but cannot update the current generation.

Lifecycle events are normalized in Go. Generated hook scripts only forward the
native event name and JSON payload to `tgo agent ingest`. Raw payloads are
bounded, and event history is capped.

The resolver selects lifecycle evidence only when its generation matches the
live process and its active evidence is fresh. A fresh lifecycle result wins
for every driver. If that evidence becomes stale or invalid, the resolver
falls back to a recognized screen rule before returning `unknown`. Hybrid
drivers can also use a recognized permission or prompt screen to correct a
known lifecycle gap. Process states such as `S` and `R` are diagnostics only.

## Integration management

`tgo setup` remains the interactive wrapper. The same manager supports:

```text
tgo integration list
tgo integration status [--json]
tgo integration install codex
tgo integration install --all-detected
tgo integration update
tgo integration uninstall codex
tgo integration doctor codex
```

Install keeps unrelated configuration, uses atomic writes, takes the existing
backup, and refuses changed or newer managed files. Uninstall removes only
tgo-owned hook entries and clean managed files. It does not restore a backup
over current user configuration.

## Verification boundary

The doctor command verifies tgo's own ingestion path and local hook wiring. It
does not claim that a harness launched a hook unless a recent native event was
received. Codex trust or review is reported as unknown until an event proves
delivery. Probe fixtures are replayable without launching a harness.
