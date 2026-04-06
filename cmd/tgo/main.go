package main

import (
	"fmt"
	"os"
)

const hotkeyRunes = "asdfqwertzxcvb"

func main() {
	client := &tmuxCLI{}
	if len(os.Args) > 1 {
		mode, ok := parseUsageMode(os.Args[1])
		if !ok {
			fmt.Fprintf(os.Stderr, "tgo: unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
		if err := runUsagePicker(client, mode); err != nil {
			fmt.Fprintf(os.Stderr, "tgo: %v\n", err)
			os.Exit(1)
		}
		return
	}

	store, err := openStateStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tgo: state init failed: %v\n", err)
		os.Exit(1)
	}

	app, err := newApp(client, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tgo: startup failed: %v\n", err)
		os.Exit(1)
	}

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tgo: %v\n", err)
		os.Exit(1)
	}
}

func SessionHotkeyAlphabet() string {
	return hotkeyRunes
}
