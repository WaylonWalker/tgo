# Changelog

All notable changes to `tgo` will be documented in this file. This project adheres to [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- interactive TUI built with `tcell`, including responsive rendering for tmux popup usage.
- tmux integration for listing sessions, switching clients, creating sessions, and killing sessions.
- sectioned session view with favorites pinned at the top and direct hotkey switching via `asdfqwertzxcvb`.
- reorder mode (`space` + `j/k` or arrows) to change session priority and hotkey assignment.
- persisted state file at `~/.config/tgo/state.json` with normalization for removed/stale sessions.
- unit tests for ordering, normalization, and hotkey assignment logic.
