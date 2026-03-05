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
	RootDir  string
}

type tmuxClient interface {
	ListSessions() ([]session, error)
	SwitchSession(name string) error
	KillSession(name string) error
	NewSession(name string) error
	NewSessionAt(name string, rootDir string) error
}

type tmuxCLI struct{}

func (t *tmuxCLI) ListSessions() ([]session, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}|#{?session_attached,1,0}|#{session_path}")
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
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		sessions = append(sessions, session{
			Name:     parts[0],
			Attached: parts[1] == "1",
			RootDir:  parts[2],
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
	return t.NewSessionAt(name, "")
}

func (t *tmuxCLI) NewSessionAt(name string, rootDir string) error {
	if name == "" {
		return fmt.Errorf("empty session name")
	}
	args := []string{"new-session", "-d", "-s", name}
	if rootDir != "" {
		args = append(args, "-c", rootDir)
	}
	cmd := exec.Command("tmux", args...)
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

	all := append([]session(nil), sessions...)

	orderIndex := make(map[string]int, len(st.Order))
	for i, name := range st.Order {
		orderIndex[name] = i
	}

	sort.SliceStable(all, func(i, j int) bool {
		li, iok := orderIndex[all[i].Name]
		lj, jok := orderIndex[all[j].Name]
		switch {
		case iok && jok:
			return li < lj
		case iok:
			return true
		case jok:
			return false
		default:
			return tmuxOrder[all[i].Name] < tmuxOrder[all[j].Name]
		}
	})

	return favorites, all
}

func assignHotkeys(rows []session, alphabet string) map[string]rune {
	out := make(map[string]rune)
	runes := []rune(alphabet)
	for i, s := range rows {
		if i >= len(runes) {
			break
		}
		out[s.Name] = runes[i]
	}
	return out
}
