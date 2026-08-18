package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

type usageMode int

const (
	usageModeCPU usageMode = iota
	usageModeMem
	usageModeAgents
)

type usageTotals struct {
	CPU           float64
	RSS           int64
	TopCPUProcess string
	TopCPU        float64
	TopMemProcess string
	TopMemRSS     int64
}

type windowUsage struct {
	Target        string
	SessionName   string
	WindowIndex   string
	WindowName    string
	PaneIndex     string
	Active        bool
	CPU           float64
	RSS           int64
	TopCPUProcess string
	TopMemProcess string
	AgentStatus  string
	AgentCommand string
	AgentHarness string
	// CopilotStatus and CopilotCommand are retained for compatibility with the
	// original Copilot picker data shape. New code uses the agent-prefixed fields.
	CopilotStatus  string
	CopilotCommand string
	topCPU        float64
	topMemRSS     int64
}

type usagePicker struct {
	client        *tmuxCLI
	mode          usageMode
	rows          []windowUsage
	allRows       []windowUsage // unfiltered rows for filter restore
	hotkeys       map[string]rune
	cursor        int
	filtering     bool
	filterInput   string
	nextView      viewID // set when user presses a view shortcut
	status        string
	statusExpiry  time.Time
	procTotals    usageTotals // totals across all processes
	sysMemTotalKB int64
	numCPU        int
	listStart      int
	listOffset     int
}

func parseUsageMode(arg string) (usageMode, bool) {
	switch arg {
	case "cpu":
		return usageModeCPU, true
	case "mem":
		return usageModeMem, true
	case "agents", "copilot", "opencode":
		return usageModeAgents, true
	default:
		return 0, false
	}
}

func runUsageView(client *tmuxCLI, screen tcell.Screen, mode usageMode) (runResult, error) {
	rows, totals, err := loadPaneUsage(client, mode)
	if err != nil {
		return runResult{}, err
	}
	return newUsagePicker(client, mode, rows, totals).Run(screen)
}

func loadPaneUsage(client *tmuxCLI, mode usageMode) ([]windowUsage, usageTotals, error) {
	panes, err := client.ListPanes()
	if err != nil {
		return nil, usageTotals{}, err
	}
	procs, err := client.ListProcesses()
	if err != nil {
		return nil, usageTotals{}, err
	}
	// compute totals across all processes
	totals := usageTotals{}
	for _, proc := range procs {
		totals.CPU += proc.CPU
		totals.RSS += proc.RSS
		// track top processes across all procs
		if proc.CPU > totals.TopCPU {
			totals.TopCPU = proc.CPU
			totals.TopCPUProcess = proc.Comm
		}
		if proc.RSS > totals.TopMemRSS {
			totals.TopMemRSS = proc.RSS
			totals.TopMemProcess = proc.Comm
		}
	}
	if mode.isAgent() {
		store, err := openAgentRegistryStore()
		if err != nil {
			return nil, usageTotals{}, err
		}
		registry, err := store.Load()
		if err != nil {
			return nil, usageTotals{}, err
		}
		return buildAgentsUsage(panes, procs, registry), totals, nil
	}
	rows := buildPaneUsage(panes, procs)
	sortPaneUsage(rows, mode)
	return rows, totals, nil
}

func buildCopilotUsage(panes []paneInfo, procs []procStat) []windowUsage {
	return buildHarnessUsage("copilot", panes, procs, newAgentRegistry())
}

func buildAgentsUsage(panes []paneInfo, procs []procStat, registry agentRegistry) []windowUsage {
	rows := buildHarnessUsage("copilot", panes, procs, registry)
	rows = append(rows, buildHarnessUsage("opencode", panes, procs, registry)...)
	sortPaneUsage(rows, usageModeAgents)
	return rows
}

func buildHarnessUsage(harness string, panes []paneInfo, procs []procStat, registry agentRegistry) []windowUsage {
	var matched []harnessPane
	switch harness {
	case "copilot":
		matched = findCopilotPanes(panes, procs)
	case "opencode":
		matched = findOpenCodePanes(panes, procs)
	default:
		matched = findHarnessPanes(harness, panes, procs)
	}
	rows := make([]windowUsage, 0, len(matched))
	byTarget := make(map[string]int, len(matched))
	for _, pane := range matched {
		rows = append(rows, windowUsage{
			Target:       pane.Target(),
			SessionName:  pane.SessionName,
			WindowIndex:  pane.WindowIndex,
			WindowName:   pane.WindowName,
			PaneIndex:    pane.PaneIndex,
			Active:       pane.Active,
			AgentStatus:  pane.Status,
			AgentCommand: "",
			AgentHarness: harness,
			CopilotStatus:  pane.Status,
			CopilotCommand: pane.Command,
		})
		byTarget[pane.Target()] = len(rows) - 1
	}

	seenTargets := make(map[string]bool)
	for _, reference := range agentRunsForHarness(registry, harness) {
		run := reference.Run
		if run.Pane == "" || seenTargets[run.Pane] {
			continue
		}
		seenTargets[run.Pane] = true
		if index, ok := byTarget[run.Pane]; ok {
			rows[index].AgentStatus = agentRunStatus(run.Status, rows[index].AgentStatus)
			rows[index].AgentCommand = agentRunSummary(reference)
		}
	}
	sortPaneUsage(rows, usageModeAgents)
	return rows
}

func agentRunStatus(kind string, fallback string) string {
	switch kind {
	case "session-start", "user-prompt":
		return "working"
	case "agent-stop":
		return "idle"
	case "question":
		return "waiting"
	case "session-end", "completed", "failed", "cancelled", "stopped":
		return "stopped"
	default:
		return fallback
	}
}

func agentRunSummary(reference agentRunReference) string {
	if reference.Run.Summary != "" {
		return reference.Run.Summary
	}
	for _, event := range reference.Run.Events {
		if description := agentEventDescription(event.Data); description != "" {
			return description
		}
	}
	label := "run " + reference.Run.ID
	if reference.SessionID != "" {
		label = "session " + reference.SessionID + " " + label
	}
	return label
}

func buildPaneUsage(panes []paneInfo, procs []procStat) []windowUsage {
	children := make(map[int][]procStat, len(procs))
	procByPID := make(map[int]procStat, len(procs))
	for _, proc := range procs {
		children[proc.PPID] = append(children[proc.PPID], proc)
		procByPID[proc.PID] = proc
	}

	memo := make(map[int]usageTotals)
	visiting := make(map[int]bool)
	var accumulateSubtree func(int) usageTotals
	accumulateSubtree = func(pid int) usageTotals {
		if total, ok := memo[pid]; ok {
			return total
		}
		if visiting[pid] {
			return usageTotals{}
		}
		visiting[pid] = true
		defer delete(visiting, pid)

		total := usageTotals{}
		if proc, ok := procByPID[pid]; ok {
			total.CPU += proc.CPU
			total.RSS += proc.RSS
			total.TopCPU = proc.CPU
			total.TopCPUProcess = proc.Comm
			total.TopMemRSS = proc.RSS
			total.TopMemProcess = proc.Comm
		}

		for _, child := range children[pid] {
			childTotal := accumulateSubtree(child.PID)
			total.CPU += childTotal.CPU
			total.RSS += childTotal.RSS
			if childTotal.TopCPU > total.TopCPU {
				total.TopCPU = childTotal.TopCPU
				total.TopCPUProcess = childTotal.TopCPUProcess
			}
			if childTotal.TopMemRSS > total.TopMemRSS {
				total.TopMemRSS = childTotal.TopMemRSS
				total.TopMemProcess = childTotal.TopMemProcess
			}
		}

		memo[pid] = total
		return total
	}

	rows := make([]windowUsage, 0, len(panes))
	for _, pane := range panes {
		totals := accumulateSubtree(pane.PanePID)
		rows = append(rows, windowUsage{
			Target:        pane.Target(),
			SessionName:   pane.SessionName,
			WindowIndex:   pane.WindowIndex,
			WindowName:    pane.WindowName,
			PaneIndex:     pane.PaneIndex,
			Active:        pane.Active,
			CPU:           totals.CPU,
			RSS:           totals.RSS,
			TopCPUProcess: totals.TopCPUProcess,
			TopMemProcess: totals.TopMemProcess,
			topCPU:        totals.TopCPU,
			topMemRSS:     totals.TopMemRSS,
		})
	}
	return rows
}

func sortPaneUsage(rows []windowUsage, mode usageMode) {
	sort.SliceStable(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		switch mode {
		case usageModeMem:
			if left.RSS != right.RSS {
				return left.RSS > right.RSS
			}
		case usageModeCPU:
			if left.CPU != right.CPU {
				return left.CPU > right.CPU
			}
		}
		if left.Active != right.Active {
			return left.Active
		}
		if left.SessionName != right.SessionName {
			return left.SessionName < right.SessionName
		}
		if cmp := compareWindowIndex(left.WindowIndex, right.WindowIndex); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareWindowIndex(left.PaneIndex, right.PaneIndex); cmp != 0 {
			return cmp < 0
		}
		return left.WindowName < right.WindowName
	})
}

func newUsagePicker(client *tmuxCLI, mode usageMode, rows []windowUsage, totals usageTotals) *usagePicker {
	p := &usagePicker{client: client, mode: mode}
	p.setRows(rows)
	p.procTotals = totals
	p.numCPU = getNumCPU()
	p.sysMemTotalKB = getSystemMemTotalKB()
	return p
}

func (p *usagePicker) setRows(rows []windowUsage) {
	p.allRows = rows
	p.rows = rows
	p.hotkeys = assignWindowHotkeys(rows, SessionHotkeyAlphabet())
	if p.cursor >= len(p.rows) {
		p.cursor = max(len(p.rows)-1, 0)
	}
}

func (p *usagePicker) Run(screen tcell.Screen) (runResult, error) {
	activeView := viewForUsageMode(p.mode)
	p.nextView = activeView // reset on entry

	screen.HideCursor()
	p.draw(screen)

	var refreshDone chan struct{}
	if p.mode.isAgent() {
		refreshDone = make(chan struct{})
		refreshTicker := time.NewTicker(2 * time.Second)
		defer func() {
			refreshTicker.Stop()
			close(refreshDone)
		}()
		go func() {
			for {
				select {
				case <-refreshTicker.C:
					_ = screen.PostEvent(tcell.NewEventInterrupt("agent-refresh"))
				case <-refreshDone:
					return
				}
			}
		}()
	}

	for {
		ev := screen.PollEvent()
		switch e := ev.(type) {
		case *tcell.EventResize:
			screen.Sync()
			p.draw(screen)
		case *tcell.EventInterrupt:
			if !p.filtering {
				if err := p.refresh(false); err != nil {
					p.setError(err)
				}
			}
			p.draw(screen)
		case *tcell.EventMouse:
			target := p.handleMouse(e)
			if target != "" {
				screen.Fini()
				if err := p.client.SwitchPane(target); err != nil {
					return runResult{}, err
				}
				return runResult{Outcome: outcomeSwitch}, nil
			}
			p.draw(screen)
		case *tcell.EventKey:
			done, target := p.handleKey(e)
			if target != "" {
				screen.Fini()
				if err := p.client.SwitchPane(target); err != nil {
					return runResult{}, err
				}
				return runResult{Outcome: outcomeSwitch}, nil
			}
			if done {
				if p.nextView != activeView {
					return runResult{Outcome: outcomeNav, NextView: p.nextView}, nil
				}
				return runResult{Outcome: outcomeQuit}, nil
			}
			p.draw(screen)
		}
	}
}

func (p *usagePicker) handleMouse(event *tcell.EventMouse) string {
	_, y := event.Position()
	index := p.listOffset + y - p.listStart
	if y < p.listStart || index < 0 || index >= len(p.rows) {
		return ""
	}
	p.cursor = index
	if event.Buttons()&tcell.Button1 != 0 {
		return p.rows[index].Target
	}
	return ""
}

func (p *usagePicker) handleKey(key *tcell.EventKey) (bool, string) {
	if p.filtering {
		return p.handleFilterKey(key)
	}
	if key.Key() == tcell.KeyCtrlC || key.Key() == tcell.KeyEscape {
		return true, ""
	}
	if key.Key() == tcell.KeyEnter {
		if row, ok := p.selected(); ok {
			return false, row.Target
		}
		return false, ""
	}
	if key.Key() == tcell.KeyUp {
		p.moveUp()
		return false, ""
	}
	if key.Key() == tcell.KeyDown {
		p.moveDown()
		return false, ""
	}
	if key.Key() != tcell.KeyRune {
		return false, ""
	}

	r := key.Rune()
	r = unicode.ToLower(r)
	if target, ok := p.hotkeyTarget(r); ok {
		return false, target
	}

	switch r {
	case 'j':
		p.moveDown()
	case 'k':
		p.moveUp()
	case 'l':
		if err := p.refresh(true); err != nil {
			p.setError(err)
		}
	case '/':
		p.filtering = true
		p.filterInput = ""
		p.applyFilter()
	case '1':
		p.nextView = viewDefault
		return true, ""
	case '2':
		if p.mode == usageModeCPU {
			// Already on CPU view, ignore.
			break
		}
		p.nextView = viewCPU
		return true, ""
	case '3':
		if p.mode == usageModeMem {
			// Already on mem view, ignore.
			break
		}
		p.nextView = viewMem
		return true, ""
	case '4':
		if p.mode == usageModeAgents {
			// Already on agents view, ignore.
			break
		}
		p.nextView = viewAgents
		return true, ""
	}
	return false, ""
}

func (p *usagePicker) handleFilterKey(key *tcell.EventKey) (bool, string) {
	switch key.Key() {
	case tcell.KeyCtrlC:
		return true, ""
	case tcell.KeyEsc:
		p.filtering = false
		p.filterInput = ""
		p.rows = p.allRows
		p.hotkeys = assignWindowHotkeys(p.rows, SessionHotkeyAlphabet())
		if p.cursor >= len(p.rows) {
			p.cursor = max(len(p.rows)-1, 0)
		}
		return false, ""
	case tcell.KeyEnter:
		row, ok := p.selected()
		p.filtering = false
		p.filterInput = ""
		p.rows = p.allRows
		p.hotkeys = assignWindowHotkeys(p.rows, SessionHotkeyAlphabet())
		if p.cursor >= len(p.rows) {
			p.cursor = max(len(p.rows)-1, 0)
		}
		if ok {
			return false, row.Target
		}
		return false, ""
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(p.filterInput) > 0 {
			p.filterInput = p.filterInput[:len(p.filterInput)-1]
			p.applyFilter()
		}
		return false, ""
	case tcell.KeyUp:
		p.moveUp()
		return false, ""
	case tcell.KeyDown:
		p.moveDown()
		return false, ""
	case tcell.KeyRune:
		r := key.Rune()
		if r >= 32 && r <= 126 {
			p.filterInput += string(r)
			p.applyFilter()
		}
		return false, ""
	default:
		return false, ""
	}
}

func (p *usagePicker) applyFilter() {
	if p.filterInput == "" {
		p.rows = p.allRows
	} else {
		p.rows = filterWindowUsage(p.allRows, p.filterInput)
	}
	p.hotkeys = assignWindowHotkeys(p.rows, SessionHotkeyAlphabet())
	if p.cursor >= len(p.rows) {
		p.cursor = max(len(p.rows)-1, 0)
	}
}

func (p *usagePicker) moveUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *usagePicker) moveDown() {
	if p.cursor < len(p.rows)-1 {
		p.cursor++
	}
}

func (p *usagePicker) refresh(announce bool) error {
	rows, totals, err := loadPaneUsage(p.client, p.mode)
	if err != nil {
		return err
	}
	p.setRows(rows)
	p.procTotals = totals
	if announce {
		p.setStatus("pane list refreshed")
	}
	return nil
}

func (p *usagePicker) hotkeyTarget(r rune) (string, bool) {
	for target, key := range p.hotkeys {
		if key == r {
			return target, true
		}
	}
	return "", false
}

func (p *usagePicker) selected() (windowUsage, bool) {
	if len(p.rows) == 0 || p.cursor < 0 || p.cursor >= len(p.rows) {
		return windowUsage{}, false
	}
	return p.rows[p.cursor], true
}

func (p *usagePicker) setStatus(msg string) {
	p.status = msg
	p.statusExpiry = time.Now().Add(4 * time.Second)
}

func (p *usagePicker) setError(err error) {
	p.status = "error: " + err.Error()
	p.statusExpiry = time.Now().Add(8 * time.Second)
}

func (p *usagePicker) visibleStatus() string {
	if p.status == "" {
		return ""
	}
	if p.statusExpiry.IsZero() || time.Now().Before(p.statusExpiry) {
		return p.status
	}
	p.status = ""
	return ""
}

func (p *usagePicker) draw(screen tcell.Screen) {
	width, height := screen.Size()
	screen.Clear()

	headerStyle := tcell.StyleDefault.Foreground(tcell.ColorAqua).Bold(true)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	statusStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen)
	errorStyle := tcell.StyleDefault.Foreground(tcell.ColorRed)

	line := 0
	activeView := viewForUsageMode(p.mode)
	p.drawText(screen, 0, line, headerStyle, viewTabs(activeView))
	line++

	helpLine := "[letters] switch  [j/k/↑↓] move  [1/2/3/4] view  [/] filter  [l] refresh  [enter] switch  [esc/ctrl+c] quit"
	if p.filtering {
		helpLine = "FILTER  [type] filter  [↑↓] move  [enter] switch  [esc] cancel"
		p.drawText(screen, 0, line, tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true), truncate(helpLine, width))
	} else {
		p.drawText(screen, 0, line, helpStyle, truncate(helpLine, width))
	}
	line++

	if p.filtering {
		prompt := "/" + p.filterInput
		p.drawText(screen, 0, line, tcell.StyleDefault.Foreground(tcell.ColorYellow), truncate(prompt, width))
		line++
	}
	if p.mode.isAgent() {
		p.drawText(screen, 0, line, helpStyle, fmt.Sprintf("%s panes: %d", p.mode.agentLabel(), len(p.rows)))
		line++
	} else {
		totalCPU := p.procTotals.CPU
		totalMemKB := p.sysMemTotalKB
		memTotalStr := ""
		if totalMemKB > 0 {
			memTotalStr = fmt.Sprintf(" sysMem:%s", formatRSS(totalMemKB))
		}
		cpuPercentStr := fmt.Sprintf("total: %5.1f%%%s", totalCPU, memTotalStr)
		p.drawText(screen, 0, line, helpStyle, truncate(cpuPercentStr, width))
		line++
	}

	if len(p.rows) == 0 {
		p.drawText(screen, 0, line, tcell.StyleDefault.Foreground(tcell.ColorGray), "no tmux panes found")
	} else {
		available := max((height-1)-line, 1)
		start := 0
		if p.cursor >= available {
			start = p.cursor - available + 1
		}
		end := min(start+available, len(p.rows))
		p.listStart = line
		p.listOffset = start
		for i := start; i < end; i++ {
			if line >= height-1 {
				break
			}
			row := p.rows[i]
			style := tcell.StyleDefault
			prefix := "  "
			if i == p.cursor {
				style = style.Background(tcell.ColorGray).Foreground(tcell.ColorBlack)
				prefix = "> "
			}
			active := " "
			if row.Active {
				active = "*"
			}
			hotkey := " "
			if r, ok := p.hotkeys[row.Target]; ok {
				hotkey = string(r)
			}
			var text string
			if p.mode.isAgent() {
				p.drawAgentRow(screen, line, width, row, prefix, hotkey, active, style)
			} else {
				panePct := row.CPU
				if p.mode == usageModeMem && p.sysMemTotalKB > 0 {
					panePct = (float64(row.RSS) / float64(p.sysMemTotalKB)) * 100.0
				}
				text = fmt.Sprintf(
					"%s[%s] %s %8s (%5.1f%%)  %s:%s.%s  %s  top:%s",
					prefix, hotkey, active, p.mode.metric(row), panePct,
					row.SessionName, row.WindowIndex, row.PaneIndex, row.WindowName,
					p.mode.topProcess(row),
				)
			}
			if !p.mode.isAgent() {
				p.drawText(screen, 0, line, style, truncate(text, width))
			}
			line++
		}
	}

	if status := p.visibleStatus(); status != "" {
		style := statusStyle
		if strings.HasPrefix(status, "error:") {
			style = errorStyle
		}
		p.drawText(screen, 0, height-1, style, truncate(status, width))
	}

	screen.Show()
}

func (p *usagePicker) drawAgentRow(screen tcell.Screen, y, width int, row windowUsage, prefix, hotkey, _ string, rowStyle tcell.Style) {
	status := agentStatusGlyph(row.AgentStatus)
	location := fmt.Sprintf("%s:%s.%s", row.SessionName, row.WindowIndex, row.PaneIndex)
	title := row.AgentCommand
	if title == "" {
		title = "session unavailable"
	}
	leading := fmt.Sprintf("%s[%s] ", prefix, hotkey)
	trailing := fmt.Sprintf(" %-7s %-8s %s  %s", agentStatusLabel(row.AgentStatus), row.AgentHarness, location, title)

	lineStyle := agentStatusStyle(row.AgentStatus)
	if p.cursor >= 0 && p.cursor < len(p.rows) && p.rows[p.cursor].Target == row.Target {
		_, background, _ := rowStyle.Decompose()
		lineStyle = lineStyle.Background(background)
	}
	p.drawText(screen, 0, y, lineStyle, truncate(leading, width))
	p.drawText(screen, len([]rune(leading)), y, lineStyle, truncate(status, max(width-len([]rune(leading)), 0)))
	p.drawText(screen, len([]rune(leading))+len([]rune(status)), y, lineStyle, truncate(trailing, max(width-len([]rune(leading))-len([]rune(status)), 0)))
}

func agentStatusGlyph(status string) string {
	switch status {
	case "running", "working", "session-start", "user-prompt":
		return "󰐊"
	case "sleeping", "idle", "session-end", "agent-stop", "completed":
		return "󰒲"
	case "waiting", "permission-prompt":
		return "󰋗"
	case "stopped":
		return "󰓛"
	case "disk-sleep":
		return "󰋊"
	case "zombie":
		return "󰠥"
	default:
		return "󰋗"
	}
}

func agentStatusLabel(status string) string {
	switch status {
	case "running", "working", "session-start", "user-prompt":
		return "working"
	case "sleeping", "idle", "session-end", "agent-stop", "completed":
		return "idle"
	case "waiting", "permission-prompt":
		return "waiting"
	case "stopped", "zombie":
		return "stopped"
	default:
		return status
	}
}

func agentStatusStyle(status string) tcell.Style {
	switch status {
	case "running", "working", "session-start", "user-prompt":
		return tcell.StyleDefault.Foreground(tcell.ColorGreen).Bold(true)
	case "sleeping", "idle", "session-end", "agent-stop", "completed":
		return tcell.StyleDefault.Foreground(tcell.ColorBlue)
	case "waiting", "permission-prompt":
		return tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
	case "stopped", "zombie":
		return tcell.StyleDefault.Foreground(tcell.ColorRed)
	case "disk-sleep":
		return tcell.StyleDefault.Foreground(tcell.ColorYellow)
	default:
		return tcell.StyleDefault.Foreground(tcell.ColorGray)
	}
}

func copilotStatusGlyph(status string, _ int) string {
	return agentStatusGlyph(status)
}

func copilotStatusStyle(status string) tcell.Style {
	return agentStatusStyle(status)
}

func (p *usagePicker) drawText(screen tcell.Screen, x int, y int, style tcell.Style, text string) {
	for _, r := range text {
		screen.SetContent(x, y, r, nil, style)
		x++
	}
}

func assignWindowHotkeys(rows []windowUsage, alphabet string) map[string]rune {
	out := make(map[string]rune)
	runes := []rune(alphabet)
	for i, row := range rows {
		if i >= len(runes) {
			break
		}
		out[row.Target] = runes[i]
	}
	return out
}

func formatRSS(rssKB int64) string {
	const (
		mb = int64(1024)
		gb = 1024 * mb
	)
	switch {
	case rssKB >= gb:
		return fmt.Sprintf("%.1fG", float64(rssKB)/float64(gb))
	case rssKB >= mb:
		return fmt.Sprintf("%.1fM", float64(rssKB)/float64(mb))
	default:
		return fmt.Sprintf("%dK", rssKB)
	}
}

func compareWindowIndex(left string, right string) int {
	li, lerr := strconv.Atoi(left)
	ri, rerr := strconv.Atoi(right)
	if lerr == nil && rerr == nil {
		switch {
		case li < ri:
			return -1
		case li > ri:
			return 1
		default:
			return 0
		}
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func (m usageMode) metric(row windowUsage) string {
	if m == usageModeMem {
		return formatRSS(row.RSS)
	}
	return fmt.Sprintf("%5.1f%%", row.CPU)
}

func viewForUsageMode(mode usageMode) viewID {
	switch mode {
	case usageModeMem:
		return viewMem
	case usageModeAgents:
		return viewAgents
	default:
		return viewCPU
	}
}

func (m usageMode) isAgent() bool {
	return m == usageModeAgents
}

func (m usageMode) agentLabel() string {
	return "Agent"
}

func (m usageMode) topProcess(row windowUsage) string {
	if m == usageModeMem {
		if row.TopMemProcess == "" {
			return "-"
		}
		return row.TopMemProcess
	}
	if row.TopCPUProcess == "" {
		return "-"
	}
	return row.TopCPUProcess
}

// getNumCPU returns number of logical CPUs available.
func getNumCPU() int {
	return runtime.NumCPU()
}

// getSystemMemTotalKB tries to read /proc/meminfo to find MemTotal in kB.
// If it can't, returns 0.
func getSystemMemTotalKB() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return v // already in kB
				}
			}
		}
	}
	return 0
}
