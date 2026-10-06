package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type multiplexerBackend interface {
	BackendName() string
	ListSessions() ([]session, error)
	SwitchSession(name string) error
	KillSession(name string) error
	NewSession(name string) error
	NewSessionAt(name string, rootDir string) error
	ListPanes() ([]paneInfo, error)
	SwitchPane(target string) error
}

type herdrCLI struct {
	socketPath       string
	workspaceTargets map[string]string
	callFn           func(method string, params any, out any) error
}

type herdrAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type herdrWorkspace struct {
	WorkspaceID string                  `json:"workspace_id"`
	Label       string                  `json:"label"`
	Focused     bool                    `json:"focused"`
	Number      int                     `json:"number"`
	Worktree    *herdrWorkspaceWorktree `json:"worktree"`
}

type herdrWorkspaceWorktree struct {
	CheckoutPath string `json:"checkout_path"`
}

type herdrTab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Focused     bool   `json:"focused"`
	Number      int    `json:"number"`
}

type herdrPane struct {
	PaneID        string `json:"pane_id"`
	WorkspaceID   string `json:"workspace_id"`
	TabID         string `json:"tab_id"`
	Label         string `json:"label"`
	CWD           string `json:"cwd"`
	ForegroundCWD string `json:"foreground_cwd"`
	Focused       bool   `json:"focused"`
	Number        int    `json:"number"`
}

func newHerdrCLI(socketPath string) *herdrCLI {
	return &herdrCLI{
		socketPath:       socketPath,
		workspaceTargets: map[string]string{},
	}
}

func (h *herdrCLI) BackendName() string {
	return "herdr"
}

func detectMultiplexerBackend() multiplexerBackend {
	forced := strings.ToLower(strings.TrimSpace(os.Getenv("TGO_BACKEND")))
	if forced == "tmux" {
		return nil
	}

	socketPath := strings.TrimSpace(os.Getenv("HERDR_SOCKET_PATH"))
	if forced == "herdr" {
		if socketPath == "" {
			return nil
		}
		return newHerdrCLI(socketPath)
	}

	// Herdr popups are not panes and therefore do not receive HERDR_PANE_ID.
	// HERDR_ACTIVE_PANE_ID is the explicit signal Herdr gives popup commands for
	// the tiled pane underneath the popup. Prefer it even if Herdr itself was
	// launched from inside tmux and tmux variables are inherited.
	if paneID := strings.TrimSpace(os.Getenv("HERDR_ACTIVE_PANE_ID")); paneID != "" && socketPath != "" {
		herdr := newHerdrCLI(socketPath)
		if herdr.validatePane(paneID) == nil {
			return herdr
		}
		return nil
	}

	// A Herdr pane can launch tmux. In that nested case, tmux owns the current
	// terminal interaction and inherited Herdr variables must not steal tgo.
	if os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != "" {
		return nil
	}

	paneID := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	if paneID == "" || socketPath == "" {
		return nil
	}
	herdr := newHerdrCLI(socketPath)
	if herdr.validatePane(paneID) != nil {
		return nil
	}
	return herdr
}

func (h *herdrCLI) validatePane(paneID string) error {
	var out struct {
		Pane herdrPane `json:"pane"`
	}
	if err := h.call("pane.get", map[string]any{"pane_id": paneID}, &out); err != nil {
		return err
	}
	if out.Pane.PaneID == "" {
		return fmt.Errorf("herdr pane %q was not returned", paneID)
	}
	return nil
}

func (h *herdrCLI) ListSessions() ([]session, error) {
	workspaces, err := h.listWorkspaces()
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int, len(workspaces))
	for _, workspace := range workspaces {
		counts[workspaceDisplayName(workspace)]++
	}

	// WorkspaceInfo intentionally does not expose cwd. Derive a useful root
	// from pane metadata, then prefer an explicit worktree checkout when Herdr
	// reports one.
	roots := make(map[string]string, len(workspaces))
	if panes, paneErr := h.listHerdrPanes(); paneErr == nil {
		for _, pane := range panes {
			root := herdrPaneRoot(pane)
			if root == "" {
				continue
			}
			if roots[pane.WorkspaceID] == "" || pane.Focused {
				roots[pane.WorkspaceID] = root
			}
		}
	}
	for _, workspace := range workspaces {
		if workspace.Worktree != nil {
			if root := strings.TrimSpace(workspace.Worktree.CheckoutPath); root != "" {
				roots[workspace.WorkspaceID] = root
			}
		}
	}

	h.workspaceTargets = make(map[string]string, len(workspaces)*3)
	sessions := make([]session, 0, len(workspaces))
	for _, workspace := range workspaces {
		baseName := workspaceDisplayName(workspace)
		displayName := baseName
		if counts[baseName] > 1 {
			displayName = fmt.Sprintf("%s [%s]", baseName, workspace.WorkspaceID)
		}

		// Workspace ids are the state/switching identity. Labels are mutable
		// presentation text and may collide.
		h.workspaceTargets[workspace.WorkspaceID] = workspace.WorkspaceID
		h.workspaceTargets[displayName] = workspace.WorkspaceID
		if counts[baseName] == 1 {
			h.workspaceTargets[baseName] = workspace.WorkspaceID
		}
		sessions = append(sessions, session{
			Name:        workspace.WorkspaceID,
			DisplayName: displayName,
			LegacyName:  baseName,
			Attached:    workspace.Focused,
			RootDir:     roots[workspace.WorkspaceID],
		})
	}
	return sessions, nil
}

func (h *herdrCLI) SwitchSession(name string) error {
	target, err := h.resolveWorkspace(name)
	if err != nil {
		return err
	}
	return h.call("workspace.focus", map[string]any{"workspace_id": target}, nil)
}

func (h *herdrCLI) KillSession(name string) error {
	target, err := h.resolveWorkspace(name)
	if err != nil {
		return err
	}
	return h.call("workspace.close", map[string]any{"workspace_id": target}, nil)
}

func (h *herdrCLI) NewSession(name string) error {
	return h.NewSessionAt(name, "")
}

func (h *herdrCLI) NewSessionAt(name string, rootDir string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty workspace name")
	}
	params := map[string]any{
		"label": name,
		"focus": false,
	}
	if strings.TrimSpace(rootDir) != "" {
		params["cwd"] = rootDir
	}
	return h.call("workspace.create", params, nil)
}

func (h *herdrCLI) ListPanes() ([]paneInfo, error) {
	workspaces, err := h.listWorkspaces()
	if err != nil {
		return nil, err
	}
	workspaceNames := make(map[string]string, len(workspaces))
	for _, workspace := range workspaces {
		workspaceNames[workspace.WorkspaceID] = workspaceDisplayName(workspace)
	}

	var tabResult struct {
		Tabs []herdrTab `json:"tabs"`
	}
	if err := h.call("tab.list", map[string]any{}, &tabResult); err != nil {
		return nil, fmt.Errorf("list herdr tabs: %w", err)
	}
	tabs := make(map[string]herdrTab, len(tabResult.Tabs))
	for _, tab := range tabResult.Tabs {
		tabs[tab.TabID] = tab
	}

	paneResult, err := h.listHerdrPanes()
	if err != nil {
		return nil, err
	}

	panes := make([]paneInfo, 0, len(paneResult))
	for _, pane := range paneResult {
		tab := tabs[pane.TabID]
		windowName := strings.TrimSpace(tab.Label)
		if windowName == "" {
			windowName = pane.TabID
		}
		panePID, err := h.paneShellPID(pane.PaneID)
		if err != nil {
			return nil, fmt.Errorf("herdr pane %s process info: %w", pane.PaneID, err)
		}
		panes = append(panes, paneInfo{
			SessionName: workspaceName(workspaceNames, pane.WorkspaceID),
			WindowIndex: herdrIndex(tab.Number, pane.TabID, ":t"),
			WindowName:  windowName,
			PaneID:      pane.PaneID,
			PanePID:     panePID,
			PaneIndex:   herdrIndex(pane.Number, pane.PaneID, ":p"),
			Active:      pane.Focused,
		})
	}
	return panes, nil
}

func (h *herdrCLI) SwitchPane(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("empty pane target")
	}
	// pane.focus is intentionally socket-only in Herdr. It can focus ordinary
	// terminal panes as well as agent panes, unlike the CLI's agent focus path.
	return h.call("pane.focus", map[string]any{"pane_id": target}, nil)
}

func (h *herdrCLI) listWorkspaces() ([]herdrWorkspace, error) {
	var out struct {
		Workspaces []herdrWorkspace `json:"workspaces"`
	}
	if err := h.call("workspace.list", map[string]any{}, &out); err != nil {
		return nil, fmt.Errorf("list herdr workspaces: %w", err)
	}
	return out.Workspaces, nil
}

func (h *herdrCLI) listHerdrPanes() ([]herdrPane, error) {
	var out struct {
		Panes []herdrPane `json:"panes"`
	}
	if err := h.call("pane.list", map[string]any{}, &out); err != nil {
		return nil, fmt.Errorf("list herdr panes: %w", err)
	}
	return out.Panes, nil
}

func herdrPaneRoot(pane herdrPane) string {
	if cwd := strings.TrimSpace(pane.ForegroundCWD); cwd != "" {
		return cwd
	}
	return strings.TrimSpace(pane.CWD)
}

func (h *herdrCLI) resolveWorkspace(name string) (string, error) {
	if target := h.workspaceTargets[name]; target != "" {
		return target, nil
	}
	if _, err := h.ListSessions(); err != nil {
		return "", err
	}
	if target := h.workspaceTargets[name]; target != "" {
		return target, nil
	}
	for _, workspace := range h.workspaceTargets {
		if workspace == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("herdr workspace %q not found", name)
}

func (h *herdrCLI) paneShellPID(paneID string) (int, error) {
	var out struct {
		ProcessInfo struct {
			ShellPID *int `json:"shell_pid"`
		} `json:"process_info"`
	}
	if err := h.call("pane.process_info", map[string]any{"pane_id": paneID}, &out); err != nil {
		return 0, err
	}
	if out.ProcessInfo.ShellPID == nil || *out.ProcessInfo.ShellPID <= 0 {
		return 0, nil
	}
	return *out.ProcessInfo.ShellPID, nil
}

func (h *herdrCLI) call(method string, params any, out any) error {
	if h.callFn != nil {
		return h.callFn(method, params, out)
	}
	return h.callSocket(method, params, out)
}

func (h *herdrCLI) callSocket(method string, params any, out any) error {
	if strings.TrimSpace(h.socketPath) == "" {
		return fmt.Errorf("HERDR_SOCKET_PATH is empty")
	}
	conn, err := net.DialTimeout("unix", h.socketPath, 750*time.Millisecond)
	if err != nil {
		return fmt.Errorf("connect herdr socket: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	request := struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{
		ID:     "tgo",
		Method: method,
		Params: params,
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return fmt.Errorf("send herdr %s: %w", method, err)
	}

	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *herdrAPIError  `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return fmt.Errorf("read herdr %s: %w", method, err)
	}
	if response.Error != nil {
		if response.Error.Code == "" {
			return fmt.Errorf("herdr %s: %s", method, response.Error.Message)
		}
		return fmt.Errorf("herdr %s: %s: %s", method, response.Error.Code, response.Error.Message)
	}
	if out == nil || len(response.Result) == 0 || string(response.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(response.Result, out); err != nil {
		return fmt.Errorf("decode herdr %s response: %w", method, err)
	}
	return nil
}

func workspaceDisplayName(workspace herdrWorkspace) string {
	if name := strings.TrimSpace(workspace.Label); name != "" {
		return name
	}
	return workspace.WorkspaceID
}

func workspaceName(names map[string]string, workspaceID string) string {
	if name := names[workspaceID]; name != "" {
		return name
	}
	return workspaceID
}

func herdrIndex(number int, id string, marker string) string {
	if number > 0 {
		return strconv.Itoa(number)
	}
	if index := strings.LastIndex(id, marker); index >= 0 {
		return id[index+len(marker):]
	}
	return id
}
