# Changelog

All notable changes to `tgo` will be documented in this file. This project adheres to [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-04-06

### Added
- tmux pane usage pickers via `tgo cpu` and `tgo mem`, with live process CPU and memory aggregation per pane.
- direct switching to the selected pane target from the usage picker, including session/window focus before pane selection.

### Changed
- documented the new usage picker commands and install flow in the README.

## [0.2.0] - 2026-03-05

### Changed
- stabilized hotkey bindings so newly created sessions append at the bottom instead of displacing existing bindings.
- added reorder styles (`push` and `swap`) with in-list preview indicators for moved/affected rows.
- enabled switching reorder style with `m` while reordering.
- made navigation seamless across `Favorites` and `All`, with cursor starting in `Favorites` when available.
- remapped controls: favorite toggle is now `.`, refresh is now `l`, and kill is now `Shift+K`.
- renamed `Others` section to `All` and kept running favorites visible in `All`.
- split hotkeys by section: `letters` for `All`, `Ctrl+letters` for `Favorites`.
- favorites now persist across tmux restarts and include saved root directories for recreation of missing favorite sessions.

## [0.1.0] - 2026-03-04

### Added
- interactive TUI built with `tcell`, including responsive rendering for tmux popup usage.
- tmux integration for listing sessions, switching clients, creating sessions, and killing sessions.
- sectioned session view with favorites pinned at the top and direct hotkey switching via `asdfqwertzxcvb`.
- reorder mode (`space` + `j/k` or arrows) to change session priority and hotkey assignment.
- persisted state file at `~/.config/tgo/state.json` with normalization for removed/stale sessions.
- unit tests for ordering, normalization, and hotkey assignment logic.
