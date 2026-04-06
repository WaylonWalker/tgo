package main

import (
	"fmt"
	"os"

	"github.com/gdamore/tcell/v2"
)

const hotkeyRunes = "asdfqwertzxcvb"

// viewID identifies the active view inside the TUI.
type viewID int

const (
	viewDefault viewID = iota
	viewCPU
	viewMem
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
	client := &tmuxCLI{}
	startView := viewDefault
	if len(os.Args) > 1 {
		mode, ok := parseUsageMode(os.Args[1])
		if !ok {
			fmt.Fprintf(os.Stderr, "tgo: unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
		if mode == usageModeCPU {
			startView = viewCPU
		} else {
			startView = viewMem
		}
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

// viewTabs renders the view tab bar, e.g. "tgo  [1:sessions]  2:cpu  3:mem".
func viewTabs(active viewID) string {
	tabs := []struct {
		key   string
		label string
		id    viewID
	}{
		{"1", "sessions", viewDefault},
		{"2", "cpu", viewCPU},
		{"3", "mem", viewMem},
	}
	parts := make([]string, len(tabs))
	for i, tab := range tabs {
		if tab.id == active {
			parts[i] = fmt.Sprintf("[%s:%s]", tab.key, tab.label)
		} else {
			parts[i] = fmt.Sprintf("%s:%s", tab.key, tab.label)
		}
	}
	return "tgo  " + parts[0] + "  " + parts[1] + "  " + parts[2]
}

func SessionHotkeyAlphabet() string {
	return hotkeyRunes
}
