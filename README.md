# tgo

`tgo` is a fast tmux session switcher built for popup workflows.

The core flow is two keystrokes:

1. open `tgo` in a tmux popup from a tmux key binding
2. press the session letter and switch instantly

`tgo` lists tmux sessions, pins favorites at the top, and keeps hotkeys stable by priority.

`tgo cpu` and `tgo mem` open a tmux pane picker sorted by live process usage, so you can jump straight to the busiest pane.

## Features

- responsive terminal UI that works in standard terminals and tmux popups
- direct switch hotkeys using `asdfqwertzxcvb` for the All list
- favorite hotkeys on `ctrl+asdfqwertzxcvb`
- favorite pinning with favorites always rendered first
- reorder modes (`push` / `swap`) with previews and key-change indicators
- tmux session management from the UI: create (`n`) and kill (`Shift+K`)
- tmux pane pickers sorted by live CPU or memory usage (`tgo cpu`, `tgo mem`)

## Install

Common release install options:

```bash
mise use -g github:waylonwalker/tgo
```

```bash
curl https://i.jpillora.com/waylonwalker/tgo | bash
```

Install with Go:

```bash
go install github.com/waylonwalker/tgo/cmd/tgo@latest
```

Download a release asset with GitHub CLI (example for Linux amd64):

```bash
gh release download --repo waylonwalker/tgo --pattern 'tgo-linux-amd64.zip'
unzip tgo-linux-amd64.zip
chmod +x tgo-linux-amd64
mv tgo-linux-amd64 /usr/local/bin/tgo
```

Manual install from the Releases page:

1. Download the archive for your OS/arch from `https://github.com/WaylonWalker/tgo/releases`.
2. Unzip it.
3. Move the binary to a directory in your `PATH` (for example `/usr/local/bin/tgo`).

## Keymap

- `asdfqwertzxcvb`: switch directly to listed session in `All`
- `ctrl+asdfqwertzxcvb`: switch directly to listed session in `Favorites`
- `j/k` or arrow keys: move cursor
- `tab`: switch active section (`Favorites` / `All`)
- `space`: toggle reorder mode for selected session
- `m`: cycle reorder mode (`push` / `swap`)
- `enter`: switch to selected session
- `.`: toggle favorite on selected session
- `n`: create new tmux session (type name, `enter`)
- `Shift+K`: kill selected tmux session
- `l`: refresh tmux session list
- `esc` or `ctrl+c`: quit

## tmux popup binding

```tmux
bind-key g display-popup -E -w 70% -h 70% "tgo"
```

Pick any key you want instead of `g`.

## Usage Pickers

```bash
tgo cpu
tgo mem
```

Both commands inspect tmux pane PIDs, sum descendant process usage per tmux pane, sort the picker by the requested metric, and switch to the chosen pane target.

## Agent Pane Reports

```bash
tgo agents
```

Opens one pane picker for all supported harnesses. Each row identifies its
harness and merges recorded lifecycle data when available. `tgo copilot` and
`tgo opencode` remain command aliases for the unified picker.

## Agent integration setup

```bash
tgo setup
```

`setup` detects supported harnesses that are already installed, including
OpenCode, Codex, Gemini CLI, GitHub Copilot, and Claude Code. It opens a small
picker with every detected harness selected by default. Press `space` to
toggle a harness, `a` to select all, or `n` to select none. Press `enter` to
install or refresh the tgo lifecycle integration. Use `j/k` or arrow keys to
move and `esc` to cancel.

Detection checks for the harness executable on `PATH`. Setup only installs or
updates tgo hooks and plugins. It never installs or updates a harness binary.

Existing configuration is preserved and backed up before tgo changes it. A
second `tgo setup` run reports integrations that are already up to date and
does not rewrite them.

## Agent hook registry

Hook integrations can write generic lifecycle events without a database:

```bash
printf '%s\n' '{"sessionId":"session-1","extra":{"source":"hook"}}' |
  tgo agent event --harness copilot --kind session-start --pane %4 --pid 1234
```

`--harness`, `--kind`, `--session`, `--run`, `--pane`, `--pid`, `--summary`,
and `--json` are parsed strictly; a JSON object can be supplied on stdin or
with `--json`. A missing run ID defaults to the pane (then PID, then session).
The original JSON payload is retained with the event.

## State storage

`tgo` stores favorites, favorite root dirs, and ordering in:

- `$XDG_CONFIG_HOME/tgo/state.json` (falls back to `~/.config/tgo/state.json`)

Favorites persist even if a session is not currently running; missing favorites are recreated using the saved root directory.

Agent lifecycle data is stored atomically in:

- `$XDG_STATE_HOME/tgo/agents.json` (falls back to `~/.local/state/tgo/agents.json`)

Setup-managed hook scripts are stored in `$XDG_CONFIG_HOME/tgo/hooks` (falling
back to `~/.config/tgo/hooks`). When setup changes an existing harness
configuration, it keeps the first copy at `<file>.tgo.bak`.

## Local development

1. Install [just](https://github.com/casey/just)
2. Run `just build` to produce `bin/tgo`
3. Run `just run` inside tmux to use the app
4. Run `just ci` before pushing changes

## Notes

- `tgo` expects a running tmux server and a tmux client context.
- switching is implemented with `tmux switch-client -t <session>`.
