package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const tmuxFieldSeparator = "\t"

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

type paneInfo struct {
	SessionName string
	WindowIndex string
	WindowName  string
	PaneID      string
	PanePID     int
	PaneIndex   string
	Active      bool
}

func (p paneInfo) Target() string {
	return p.PaneID
}

type procStat struct {
	PID  int
	PPID int
	CPU  float64
	RSS  int64
	Comm string
}

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

func (t *tmuxCLI) SwitchWindow(target string) error {
	if target == "" {
		return fmt.Errorf("empty window target")
	}
	parts := strings.SplitN(target, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid window target %q", target)
	}
	if err := t.SwitchSession(parts[0]); err != nil {
		return err
	}
	cmd := exec.Command("tmux", "select-window", "-t", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("select window %q: %w (%s)", target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *tmuxCLI) SwitchPane(target string) error {
	if target == "" {
		return fmt.Errorf("empty pane target")
	}
	cmd := exec.Command("tmux", "display-message", "-p", "-t", target, "#{session_name}:#{window_index}")
	windowTargetOut, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("resolve pane %q: %w (%s)", target, err, strings.TrimSpace(string(windowTargetOut)))
	}
	windowTarget := strings.TrimSpace(string(windowTargetOut))
	if windowTarget == "" {
		return fmt.Errorf("resolve pane %q: empty window target", target)
	}
	if err := t.SwitchWindow(windowTarget); err != nil {
		return err
	}
	cmd = exec.Command("tmux", "select-pane", "-t", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("select pane %q: %w (%s)", target, err, strings.TrimSpace(string(out)))
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

func (t *tmuxCLI) ListPanes() ([]paneInfo, error) {
	format := strings.Join([]string{
		"#{session_name}",
		"#{window_index}",
		"#{window_name}",
		"#{pane_id}",
		"#{pane_pid}",
		"#{pane_index}",
		"#{window_active}",
	}, tmuxFieldSeparator)
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", format)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list panes: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []paneInfo{}, nil
	}

	panes := make([]paneInfo, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, tmuxFieldSeparator, 7)
		if len(parts) != 7 {
			continue
		}
		panePID, err := strconv.Atoi(parts[4])
		if err != nil {
			continue
		}
		panes = append(panes, paneInfo{
			SessionName: parts[0],
			WindowIndex: parts[1],
			WindowName:  parts[2],
			PaneID:      parts[3],
			PanePID:     panePID,
			PaneIndex:   parts[5],
			Active:      parts[6] == "1",
		})
	}

	return panes, nil
}

func (t *tmuxCLI) ListProcesses() ([]procStat, error) {
	cmd := exec.Command("ps", "-eo", "pid=,ppid=,%cpu=,rss=,comm=")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []procStat{}, nil
	}

	procs := make([]procStat, 0, len(lines))
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 5 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		cpu, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			continue
		}
		rss, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			continue
		}
		procs = append(procs, procStat{
			PID:  pid,
			PPID: ppid,
			CPU:  cpu,
			RSS:  rss,
			Comm: strings.Join(parts[4:], " "),
		})
	}

	return procs, nil
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
