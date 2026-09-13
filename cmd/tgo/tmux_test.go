package main

import "testing"

func TestParsePaneInfoOutputKeepsGenerationFields(t *testing.T) {
	output := "/tmp/tmux.sock\tdev session\t$7\t2\teditor pane\t%9\t4321\t0\t1\n"
	panes := parsePaneInfoOutput(output)
	if len(panes) != 1 {
		t.Fatalf("pane count = %d, want 1", len(panes))
	}
	pane := panes[0]
	if pane.ServerID != "/tmp/tmux.sock" || pane.SessionName != "dev session" || pane.SessionID != "$7" || pane.WindowName != "editor pane" {
		t.Fatalf("generation fields were not preserved: %+v", pane)
	}
	if pane.PaneID != "%9" || pane.PanePID != 4321 || !pane.Active {
		t.Fatalf("pane fields were not parsed: %+v", pane)
	}
}

func TestParsePaneInfoOutputSkipsMalformedRows(t *testing.T) {
	panes := parsePaneInfoOutput("bad\n/tmp/sock\ts\t$1\t0\tw\t%1\tnot-pid\t0\t1\n")
	if len(panes) != 0 {
		t.Fatalf("malformed pane rows = %+v", panes)
	}
}
