# Harness Compatibility

The fixture and replay code records native event names, identity fields, raw
payloads, screen tails, and expected states. It does not assert behavior that
has not been observed.

| Harness | Lifecycle policy | Verified in this change | Known gap |
| --- | --- | --- | --- |
| OpenCode | authoritative | Go normalization and fixture replay; plugin syntax | Live permission and question transitions need a live probe |
| Codex | lifecycle with screen fallback | hook payload forwarding and normalization | Hook trust/review and permission cancel need a live probe |
| Claude Code | hybrid | hook payload forwarding and normalization | Stop on interrupt and manual denial need a live probe |
| Gemini CLI | hybrid | hook payload forwarding and normalization | Interrupt and ToolPermission cancel need a live probe |
| GitHub Copilot CLI | hybrid | bash and PowerShell adapter generation | Exact elicitation cancellation behavior needs a live probe |

`integration doctor` reports this boundary. It verifies the synthetic event
path inside tgo, but it does not report harness-side behavior as verified.
