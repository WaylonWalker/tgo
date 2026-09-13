package main

import (
	"path/filepath"
	"strings"
)

type harnessPane struct {
	paneInfo
	PID          int
	Status       string // scheduler state; diagnostic only
	ProcessStart string
	Command      string
}

type copilotPane = harnessPane

func findCopilotPanes(panes []paneInfo, procs []procStat) []copilotPane {
	return findHarnessPanes("copilot", panes, procs)
}

func findHarnessPanes(harness string, panes []paneInfo, procs []procStat) []harnessPane {
	children := make(map[int][]procStat, len(procs))
	procByPID := make(map[int]procStat, len(procs))
	for _, proc := range procs {
		children[proc.PPID] = append(children[proc.PPID], proc)
		procByPID[proc.PID] = proc
	}

	rows := make([]harnessPane, 0)
	for _, pane := range panes {
		var matches []procStat
		if proc, ok := procByPID[pane.PanePID]; ok && isHarnessProcess(harness, proc) {
			matches = append(matches, proc)
		}
		visited := make(map[int]bool)
		var visit func(int)
		visit = func(pid int) {
			if visited[pid] {
				return
			}
			visited[pid] = true
			for _, child := range children[pid] {
				if isHarnessProcess(harness, child) {
					matches = append(matches, child)
				}
				visit(child.PID)
			}
		}
		visit(pane.PanePID)
		if len(matches) == 0 {
			continue
		}

		match := matches[0]
		for _, candidate := range matches[1:] {
			if processStatus(candidate.State) == "running" {
				match = candidate
				break
			}
		}
		rows = append(rows, harnessPane{
			paneInfo:     pane,
			PID:          match.PID,
			Status:       processStatus(match.State),
			ProcessStart: match.StartTime,
			Command:      processCommand(match),
		})
	}
	return rows
}

func isHarnessProcess(harness string, proc procStat) bool {
	if isTgoHarnessPicker(harness, proc.Command) {
		return false
	}
	definition := harnessDefinitionByID(harness)
	command := strings.ToLower(proc.Comm + " " + proc.Command)
	for _, executable := range definition.Executables {
		if strings.Contains(command, strings.ToLower(executable)) {
			return true
		}
	}
	// Preserve discovery for a harness recorded by a newer tgo version.
	return strings.Contains(command, strings.ToLower(harness))
}

func isTgoHarnessPicker(harness string, command string) bool {
	fields := strings.Fields(command)
	if len(fields) < 2 || filepath.Base(fields[0]) != "tgo" {
		return false
	}
	return strings.EqualFold(fields[1], harness)
}

func processCommand(proc procStat) string {
	if proc.Command != "" {
		return proc.Command
	}
	return proc.Comm
}

func processStatus(state string) string {
	if state == "" {
		return "unknown"
	}
	switch state[0] {
	case 'R':
		return "running"
	case 'S':
		return "sleeping"
	case 'D':
		return "disk-sleep"
	case 'T':
		return "stopped"
	case 'Z':
		return "zombie"
	case 'I':
		return "idle"
	default:
		return "unknown"
	}
}
