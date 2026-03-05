# tgo

`tgo` is a fast tmux session switcher built for popup workflows.

The core flow is two keystrokes:

1. open `tgo` in a tmux popup from a tmux key binding
2. press the session letter and switch instantly

`tgo` lists tmux sessions, pins favorites at the top, and keeps hotkeys stable by priority.

## Features

- responsive terminal UI that works in standard terminals and tmux popups
- direct switch hotkeys using `asdfqwertzxcvb`
- favorite pinning with favorites always rendered first
- reorder mode (`space` + `j/k` or arrows) to change session priority and hotkey assignment
- tmux session management from the UI: create (`n`) and kill (`x`)

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

- `asdfqwertzxcvb`: switch directly to listed session
- `j/k` or arrow keys: move cursor
- `tab`: switch active section (`Favorites` / `Others`)
- `space`: toggle reorder mode for selected session
- `enter`: switch to selected session
- `f`: toggle favorite on selected session
- `n`: create new tmux session (type name, `enter`)
- `x`: kill selected tmux session
- `r`: refresh tmux session list
- `esc` or `ctrl+c`: quit

## tmux popup binding

```tmux
bind-key g display-popup -E -w 70% -h 70% "tgo"
```

Pick any key you want instead of `g`.

## State storage

`tgo` stores favorites and non-favorite ordering in:

- `~/.config/tgo/state.json`

Missing or stale sessions are automatically removed from saved state.

## Local development

1. Install [just](https://github.com/casey/just)
2. Run `just build` to produce `bin/tgo`
3. Run `just run` inside tmux to use the app
4. Run `just ci` before pushing changes

## Notes

- `tgo` expects a running tmux server and a tmux client context.
- switching is implemented with `tmux switch-client -t <session>`.
