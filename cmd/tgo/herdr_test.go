package main

import (
	"encoding/json"
	"errors"
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

func TestHerdrListSessionsUsesStableIDsAndRealRoots(t *testing.T) {
	var focusParams map[string]any
	h := newHerdrCLI("/tmp/herdr.sock")
	h.callFn = func(method string, params any, out any) error {
		switch method {
		case "workspace.list":
			writeHerdrResult(t, out, map[string]any{"workspaces": []map[string]any{
				{"workspace_id": "w1", "label": "api", "focused": true, "number": 1},
				{"workspace_id": "w2", "label": "api", "focused": false, "number": 2, "worktree": map[string]any{"checkout_path": "/src/api-b"}},
				{"workspace_id": "w3", "label": "docs", "focused": false, "number": 3},
			}})
		case "pane.list":
			writeHerdrResult(t, out, map[string]any{"panes": []map[string]any{
				{"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1", "cwd": "/src/api-a", "foreground_cwd": "/src/api-a/pkg", "focused": true},
				{"pane_id": "w2:p1", "workspace_id": "w2", "tab_id": "w2:t1", "cwd": "/wrong/worktree/fallback"},
				{"pane_id": "w3:p1", "workspace_id": "w3", "tab_id": "w3:t1", "cwd": "/src/docs"},
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
	gotKeys := []string{sessions[0].Name, sessions[1].Name, sessions[2].Name}
	wantKeys := []string{"w1", "w2", "w3"}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("session keys: got %v want %v", gotKeys, wantKeys)
	}
	gotLabels := []string{sessions[0].Label(), sessions[1].Label(), sessions[2].Label()}
	wantLabels := []string{"api [w1]", "api [w2]", "docs"}
	if !reflect.DeepEqual(gotLabels, wantLabels) {
		t.Fatalf("session labels: got %v want %v", gotLabels, wantLabels)
	}
	gotRoots := []string{sessions[0].RootDir, sessions[1].RootDir, sessions[2].RootDir}
	wantRoots := []string{"/src/api-a/pkg", "/src/api-b", "/src/docs"}
	if !reflect.DeepEqual(gotRoots, wantRoots) {
		t.Fatalf("session roots: got %v want %v", gotRoots, wantRoots)
	}
	if !sessions[0].Attached || sessions[1].Attached {
		t.Fatalf("focused workspace was not mapped to Attached: %#v", sessions)
	}

	if err := h.SwitchSession("w2"); err != nil {
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
		switch method {
		case "workspace.list":
			writeHerdrResult(t, out, map[string]any{"workspaces": []map[string]any{
				{"workspace_id": "w1", "label": "dev", "focused": true, "number": 1},
			}})
		case "pane.list":
			writeHerdrResult(t, out, map[string]any{"panes": []map[string]any{{
				"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1", "cwd": "/src/dev",
			}}})
		default:
			t.Fatalf("unexpected method %q", method)
		}
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
	if len(sessions) != 1 || sessions[0].Name != "w1" || sessions[0].Label() != "dev" {
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


func TestMigrateHerdrStateUsesStableWorkspaceIDs(t *testing.T) {
	sessions := []session{
		{Name: "w1", DisplayName: "api [w1]", LegacyName: "api", RootDir: "/src/api-a"},
		{Name: "w2", DisplayName: "api [w2]", LegacyName: "api", RootDir: "/src/api-b"},
		{Name: "w3", DisplayName: "docs", LegacyName: "docs", RootDir: "/src/docs"},
	}
	st := state{
		Favorites:     []string{"api [w1]", "docs", "api"},
		Order:         []string{"docs", "api [w2]", "api"},
		FavoriteRoots: map[string]string{"api [w1]": "/old/api-a", "docs": "/old/docs", "api": "/ambiguous"},
	}

	got := migrateSessionStateKeys(st, sessions)
	if want := []string{"w1", "w3", "api"}; !reflect.DeepEqual(got.Favorites, want) {
		t.Fatalf("favorites: got %v want %v", got.Favorites, want)
	}
	if want := []string{"w3", "w2", "api"}; !reflect.DeepEqual(got.Order, want) {
		t.Fatalf("order: got %v want %v", got.Order, want)
	}
	if got.FavoriteRoots["w1"] != "/old/api-a" || got.FavoriteRoots["w3"] != "/old/docs" {
		t.Fatalf("roots were not migrated: %#v", got.FavoriteRoots)
	}
	if got.FavoriteRoots["api"] != "/ambiguous" {
		t.Fatalf("ambiguous legacy label should remain unresolved: %#v", got.FavoriteRoots)
	}
}

func TestHerdrRefreshDoesNotCreatePhantomWorkspaceWhenLabelsCollide(t *testing.T) {
	workspaces := []map[string]any{
		{"workspace_id": "w1", "label": "api", "focused": true, "number": 1},
	}
	createCalls := 0
	h := newHerdrCLI("/tmp/herdr.sock")
	h.callFn = func(method string, params any, out any) error {
		switch method {
		case "workspace.list":
			writeHerdrResult(t, out, map[string]any{"workspaces": workspaces})
		case "pane.list":
			writeHerdrResult(t, out, map[string]any{"panes": []map[string]any{{
				"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1", "cwd": "/src/api",
			}}})
		case "workspace.create":
			createCalls++
		default:
			t.Fatalf("unexpected method %q", method)
		}
		return nil
	}

	a := &app{
		client: &tmuxCLI{backend: h, backendDetected: true},
		store:  &stateStore{path: filepath.Join(t.TempDir(), "state-herdr.json")},
		state: state{
			Favorites:     []string{"api"},
			FavoriteRoots: map[string]string{"api": "/src/api"},
		},
	}
	if err := a.refreshSessions(); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if !reflect.DeepEqual(a.state.Favorites, []string{"w1"}) {
		t.Fatalf("legacy favorite was not migrated: %v", a.state.Favorites)
	}

	workspaces = append(workspaces, map[string]any{
		"workspace_id": "w2", "label": "api", "focused": false, "number": 2,
	})
	if err := a.refreshSessions(); err != nil {
		t.Fatalf("refresh with duplicate label: %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("duplicate labels created %d phantom workspaces", createCalls)
	}
	if !reflect.DeepEqual(a.state.Favorites, []string{"w1"}) {
		t.Fatalf("favorite identity changed after duplicate label: %v", a.state.Favorites)
	}
}

func TestHerdrMissingShellPIDDoesNotCountPIDZeroTree(t *testing.T) {
	h := newHerdrCLI("/tmp/herdr.sock")
	h.callFn = func(method string, params any, out any) error {
		switch method {
		case "workspace.list":
			writeHerdrResult(t, out, map[string]any{"workspaces": []map[string]any{{
				"workspace_id": "w1", "label": "api", "focused": true, "number": 1,
			}}})
		case "tab.list":
			writeHerdrResult(t, out, map[string]any{"tabs": []map[string]any{{
				"tab_id": "w1:t1", "workspace_id": "w1", "label": "api", "number": 1,
			}}})
		case "pane.list":
			writeHerdrResult(t, out, map[string]any{"panes": []map[string]any{{
				"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1", "focused": true,
			}}})
		case "pane.process_info":
			writeHerdrResult(t, out, map[string]any{"process_info": map[string]any{"shell_pid": nil}})
		default:
			t.Fatalf("unexpected method %q", method)
		}
		return nil
	}

	panes, err := h.ListPanes()
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(panes) != 1 || panes[0].PanePID != 0 {
		t.Fatalf("missing shell pid should map to unknown pid: %#v", panes)
	}
	rows := buildPaneUsage(panes, []procStat{{
		PID: 1, PPID: 0, CPU: 91.5, RSS: 123456, Comm: "system",
	}})
	if len(rows) != 1 || rows[0].CPU != 0 || rows[0].RSS != 0 {
		t.Fatalf("unknown pane pid inherited PID 0 process tree: %#v", rows)
	}
}

func TestHerdrListPanesPropagatesProcessInfoError(t *testing.T) {
	h := newHerdrCLI("/tmp/herdr.sock")
	h.callFn = func(method string, params any, out any) error {
		switch method {
		case "workspace.list":
			writeHerdrResult(t, out, map[string]any{"workspaces": []map[string]any{{
				"workspace_id": "w1", "label": "api", "focused": true, "number": 1,
			}}})
		case "tab.list":
			writeHerdrResult(t, out, map[string]any{"tabs": []map[string]any{{
				"tab_id": "w1:t1", "workspace_id": "w1", "label": "api", "number": 1,
			}}})
		case "pane.list":
			writeHerdrResult(t, out, map[string]any{"panes": []map[string]any{{
				"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1",
			}}})
		case "pane.process_info":
			return errors.New("socket read failed")
		default:
			t.Fatalf("unexpected method %q", method)
		}
		return nil
	}

	if _, err := h.ListPanes(); err == nil {
		t.Fatal("expected pane.process_info error")
	}
}
