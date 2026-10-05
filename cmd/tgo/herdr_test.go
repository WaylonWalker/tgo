package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func writeHerdrResult(t *testing.T, out any, value any) {
	t.Helper()
	if out == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fake herdr result: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal fake herdr result: %v", err)
	}
}

func TestHerdrListSessionsDisambiguatesDuplicateLabels(t *testing.T) {
	var focusParams map[string]any
	h := newHerdrCLI("/tmp/herdr.sock")
	h.callFn = func(method string, params any, out any) error {
		switch method {
		case "workspace.list":
			writeHerdrResult(t, out, map[string]any{"workspaces": []map[string]any{
				{"workspace_id": "w1", "label": "api", "cwd": "/src/api-a", "focused": true, "number": 1},
				{"workspace_id": "w2", "label": "api", "cwd": "/src/api-b", "focused": false, "number": 2},
				{"workspace_id": "w3", "label": "docs", "cwd": "/src/docs", "focused": false, "number": 3},
			}})
		case "workspace.focus":
			focusParams = params.(map[string]any)
		default:
			t.Fatalf("unexpected method %q", method)
		}
		return nil
	}

	sessions, err := h.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	got := []string{sessions[0].Name, sessions[1].Name, sessions[2].Name}
	want := []string{"api [w1]", "api [w2]", "docs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session names: got %v want %v", got, want)
	}
	if !sessions[0].Attached || sessions[1].Attached {
		t.Fatalf("focused workspace was not mapped to Attached: %#v", sessions)
	}

	if err := h.SwitchSession("api [w2]"); err != nil {
		t.Fatalf("SwitchSession: %v", err)
	}
	if got, want := focusParams["workspace_id"], any("w2"); got != want {
		t.Fatalf("workspace.focus id: got %v want %v", got, want)
	}
}

func TestHerdrListPanesAndFocus(t *testing.T) {
	var focused string
	h := newHerdrCLI("/tmp/herdr.sock")
	h.callFn = func(method string, params any, out any) error {
		switch method {
		case "workspace.list":
			writeHerdrResult(t, out, map[string]any{"workspaces": []map[string]any{
				{"workspace_id": "w1", "label": "api", "cwd": "/src/api", "focused": true, "number": 1},
			}})
		case "tab.list":
			writeHerdrResult(t, out, map[string]any{"tabs": []map[string]any{
				{"tab_id": "w1:t2", "workspace_id": "w1", "label": "tests", "focused": true, "number": 2},
			}})
		case "pane.list":
			writeHerdrResult(t, out, map[string]any{"panes": []map[string]any{
				{"pane_id": "w1:p7", "workspace_id": "w1", "tab_id": "w1:t2", "cwd": "/src/api", "focused": true, "number": 7},
			}})
		case "pane.process_info":
			writeHerdrResult(t, out, map[string]any{"process_info": map[string]any{"shell_pid": 4242}})
		case "pane.focus":
			focused = params.(map[string]any)["pane_id"].(string)
		default:
			t.Fatalf("unexpected method %q", method)
		}
		return nil
	}

	panes, err := h.ListPanes()
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("panes: got %d want 1", len(panes))
	}
	pane := panes[0]
	if pane.SessionName != "api" || pane.WindowIndex != "2" || pane.WindowName != "tests" || pane.PaneIndex != "7" || pane.PanePID != 4242 || !pane.Active {
		t.Fatalf("unexpected pane mapping: %#v", pane)
	}
	if pane.Target() != "w1:p7" {
		t.Fatalf("pane target: got %q", pane.Target())
	}

	if err := h.SwitchPane("w1:p7"); err != nil {
		t.Fatalf("SwitchPane: %v", err)
	}
	if focused != "w1:p7" {
		t.Fatalf("focused pane: got %q want w1:p7", focused)
	}
}

func TestTmuxFacadeRoutesToHerdr(t *testing.T) {
	h := newHerdrCLI("/tmp/herdr.sock")
	h.callFn = func(method string, params any, out any) error {
		if method != "workspace.list" {
			t.Fatalf("unexpected method %q", method)
		}
		writeHerdrResult(t, out, map[string]any{"workspaces": []map[string]any{
			{"workspace_id": "w1", "label": "dev", "cwd": "/src/dev", "focused": true, "number": 1},
		}})
		return nil
	}
	client := &tmuxCLI{backend: h, backendDetected: true}
	if got := client.BackendName(); got != "herdr" {
		t.Fatalf("BackendName: got %q want herdr", got)
	}
	sessions, err := client.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Name != "dev" {
		t.Fatalf("sessions: %#v", sessions)
	}
}

func TestDetectionPrefersNestedTmuxOverInheritedHerdrPane(t *testing.T) {
	t.Setenv("TGO_BACKEND", "")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/does-not-need-to-exist.sock")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_ACTIVE_PANE_ID", "")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("TMUX_PANE", "%1")

	if backend := detectMultiplexerBackend(); backend != nil {
		t.Fatalf("nested tmux should win over inherited Herdr pane, got %T", backend)
	}
}

func TestDetectionCanForceTmux(t *testing.T) {
	t.Setenv("TGO_BACKEND", "tmux")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/herdr.sock")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_ACTIVE_PANE_ID", "w1:p1")

	if backend := detectMultiplexerBackend(); backend != nil {
		t.Fatalf("TGO_BACKEND=tmux should disable Herdr detection, got %T", backend)
	}
}

func TestStateFilesAreSeparatedByBackend(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	tmuxStore, err := openStateStoreForBackend("tmux")
	if err != nil {
		t.Fatalf("tmux state store: %v", err)
	}
	herdrStore, err := openStateStoreForBackend("herdr")
	if err != nil {
		t.Fatalf("herdr state store: %v", err)
	}
	if got, want := filepath.Base(tmuxStore.path), "state.json"; got != want {
		t.Fatalf("tmux state filename: got %q want %q", got, want)
	}
	if got, want := filepath.Base(herdrStore.path), "state-herdr.json"; got != want {
		t.Fatalf("herdr state filename: got %q want %q", got, want)
	}
}
