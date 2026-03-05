package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type session struct {
	Name     string
	Attached bool
}

type tmuxClient interface {
	ListSessions() ([]session, error)
	SwitchSession(name string) error
	KillSession(name string) error
	NewSession(name string) error
}

type tmuxCLI struct{}

func (t *tmuxCLI) ListSessions() ([]session, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}|#{?session_attached,1,0}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []session{}, nil
	}

	sessions := make([]session, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		sessions = append(sessions, session{
			Name:     parts[0],
			Attached: parts[1] == "1",
		})
	}

	return sessions, nil
}

func (t *tmuxCLI) SwitchSession(name string) error {
	if name == "" {
		return fmt.Errorf("empty session name")
	}
	cmd := exec.Command("tmux", "switch-client", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("switch session %q: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *tmuxCLI) KillSession(name string) error {
	if name == "" {
		return fmt.Errorf("empty session name")
	}
	cmd := exec.Command("tmux", "kill-session", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kill session %q: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *tmuxCLI) NewSession(name string) error {
	if name == "" {
		return fmt.Errorf("empty session name")
	}
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("new session %q: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func orderSessions(sessions []session, st state) (favorites []session, others []session) {
	nameToSession := make(map[string]session, len(sessions))
	tmuxOrder := make(map[string]int, len(sessions))
	for i, s := range sessions {
		nameToSession[s.Name] = s
		tmuxOrder[s.Name] = i
	}

	for _, name := range st.Favorites {
		s, ok := nameToSession[name]
		if !ok {
			continue
		}
		favorites = append(favorites, s)
	}

	favSet := make(map[string]struct{}, len(favorites))
	for _, s := range favorites {
		favSet[s.Name] = struct{}{}
	}

	nonFav := make([]session, 0, len(sessions)-len(favorites))
	for _, s := range sessions {
		if _, ok := favSet[s.Name]; ok {
			continue
		}
		nonFav = append(nonFav, s)
	}

	orderIndex := make(map[string]int, len(st.Order))
	for i, name := range st.Order {
		orderIndex[name] = i
	}

	sort.SliceStable(nonFav, func(i, j int) bool {
		li, iok := orderIndex[nonFav[i].Name]
		lj, jok := orderIndex[nonFav[j].Name]
		switch {
		case iok && jok:
			return li < lj
		case iok:
			return true
		case jok:
			return false
		default:
			return tmuxOrder[nonFav[i].Name] < tmuxOrder[nonFav[j].Name]
		}
	})

	return favorites, nonFav
}

func assignHotkeys(favorites []session, others []session, alphabet string) map[string]rune {
	out := make(map[string]rune)
	ordered := make([]session, 0, len(favorites)+len(others))
	ordered = append(ordered, favorites...)
	ordered = append(ordered, others...)

	runes := []rune(alphabet)
	for i, s := range ordered {
		if i >= len(runes) {
			break
		}
		out[s.Name] = runes[i]
	}
	return out
}
