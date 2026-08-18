package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
)

const hotkeyRunes = "asdfqwertzxcvb"

// viewID identifies the active view inside the TUI.
type viewID int

const (
	viewDefault viewID = iota
	viewCPU
	viewMem
	viewAgents
)

// runOutcome tells the dispatch loop what happened when a view exited.
type runOutcome int

const (
	outcomeQuit   runOutcome = iota // user wants to exit
	outcomeSwitch                   // user selected a session/pane to switch to
	outcomeNav                      // user pressed 1/2/3 to navigate to another view
)

// runResult is returned by each view's Run method.
type runResult struct {
	Outcome  runOutcome
	NextView viewID // only meaningful when Outcome == outcomeNav
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "agent" {
		if err := runAgentCommand(os.Args[2:], os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "tgo: %v\n", err)
			os.Exit(2)
		}
		return
	}
	client := &tmuxCLI{}
	startView := viewDefault
	if len(os.Args) > 1 {
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "tgo: usage picker accepts exactly one command")
			os.Exit(2)
		}
		mode, ok := parseUsageMode(os.Args[1])
		if !ok {
			fmt.Fprintf(os.Stderr, "tgo: unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
		startView = viewForUsageMode(mode)
	}

	if err := runTUI(client, startView); err != nil {
		fmt.Fprintf(os.Stderr, "tgo: %v\n", err)
		os.Exit(1)
	}
}

// runTUI creates a single tcell screen and runs a dispatch loop that switches
// between views without recreating the terminal.
func runTUI(client *tmuxCLI, startView viewID) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return fmt.Errorf("create screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("init screen: %w", err)
	}
	defer screen.Fini()
	screen.HideCursor()
	screen.EnableMouse()

	// The session-switcher app is created once and reused across view switches
	// so that favorites, cursors, etc. are preserved.
	var sessionApp *app

	current := startView
	for {
		var result runResult
		switch current {
		case viewDefault:
			if sessionApp == nil {
				store, err := openStateStore()
				if err != nil {
					return fmt.Errorf("state init failed: %w", err)
				}
				sessionApp, err = newApp(client, store)
				if err != nil {
					return fmt.Errorf("startup failed: %w", err)
				}
			}
			result, err = sessionApp.Run(screen)
		case viewCPU:
			result, err = runUsageView(client, screen, usageModeCPU)
		case viewMem:
			result, err = runUsageView(client, screen, usageModeMem)
		case viewAgents:
			result, err = runUsageView(client, screen, usageModeAgents)
		}
		if err != nil {
			return err
		}
		switch result.Outcome {
		case outcomeQuit, outcomeSwitch:
			return nil
		case outcomeNav:
			current = result.NextView
		}
	}
}

// viewTabs renders the view tab bar.
func viewTabs(active viewID) string {
	tabs := []struct {
		key   string
		label string
		id    viewID
	}{
		{"1", "sessions", viewDefault},
		{"2", "cpu", viewCPU},
		{"3", "mem", viewMem},
		{"4", "agents", viewAgents},
	}
	parts := make([]string, len(tabs))
	for i, tab := range tabs {
		if tab.id == active {
			parts[i] = fmt.Sprintf("[%s:%s]", tab.key, tab.label)
		} else {
			parts[i] = fmt.Sprintf("%s:%s", tab.key, tab.label)
		}
	}
	return "tgo  " + strings.Join(parts, "  ")
}

func SessionHotkeyAlphabet() string {
	return hotkeyRunes
}
