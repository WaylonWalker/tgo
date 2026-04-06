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
	nextView      viewID // set when user presses 1/2/3
	status        string
	statusExpiry  time.Time
	procTotals    usageTotals // totals across all processes
	sysMemTotalKB int64
	numCPU        int
}

func parseUsageMode(arg string) (usageMode, bool) {
	switch arg {
	case "cpu":
		return usageModeCPU, true
	case "mem":
		return usageModeMem, true
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
	rows := buildPaneUsage(panes, procs)
	sortPaneUsage(rows, mode)
	return rows, totals, nil
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
		default:
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
	activeView := viewCPU
	if p.mode == usageModeMem {
		activeView = viewMem
	}
	p.nextView = activeView // reset on entry

	screen.HideCursor()
	p.draw(screen)

	for {
		ev := screen.PollEvent()
		switch e := ev.(type) {
		case *tcell.EventResize:
			screen.Sync()
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
		if err := p.refresh(); err != nil {
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

func (p *usagePicker) refresh() error {
	rows, totals, err := loadPaneUsage(p.client, p.mode)
	if err != nil {
		return err
	}
	p.setRows(rows)
	p.procTotals = totals
	p.setStatus("usage refreshed")
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
	activeView := viewCPU
	if p.mode == usageModeMem {
		activeView = viewMem
	}
	p.drawText(screen, 0, line, headerStyle, viewTabs(activeView))
	line++

	helpLine := "[letters] switch  [j/k/↑↓] move  [1/2/3] view  [/] filter  [l] refresh  [enter] switch  [esc/ctrl+c] quit"
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
	// show totals and tmux contribution header
	// total system CPU and memory (memory only if available)
	totalCPU := p.procTotals.CPU
	totalMemKB := p.sysMemTotalKB
	memTotalStr := ""
	if totalMemKB > 0 {
		memTotalStr = fmt.Sprintf(" sysMem:%s", formatRSS(totalMemKB))
	}
	// percent of system CPU used by tmux-managed processes (sum of procs)
	cpuPercentStr := fmt.Sprintf("total: %5.1f%%%s", totalCPU, memTotalStr)
	p.drawText(screen, 0, line, helpStyle, truncate(cpuPercentStr, width))
	line++
	// compute per-session aggregates so we can display how much each session contributes
	sessionAgg := make(map[string]usageTotals)
	for _, r := range p.rows {
		agg := sessionAgg[r.SessionName]
		agg.CPU += r.CPU
		agg.RSS += r.RSS
		if r.topCPU > agg.TopCPU {
			agg.TopCPU = r.topCPU
			agg.TopCPUProcess = r.TopCPUProcess
		}
		if r.topMemRSS > agg.TopMemRSS {
			agg.TopMemRSS = r.topMemRSS
			agg.TopMemProcess = r.TopMemProcess
		}
		sessionAgg[r.SessionName] = agg
	}
	// render per-session lines (compact)
	sessLine := "sessions:"
	for name, agg := range sessionAgg {
		pct := 0.0
		if p.mode == usageModeMem {
			if totalMemKB > 0 {
				pct = (float64(agg.RSS) / float64(totalMemKB)) * 100.0
			}
		} else {
			pct = agg.CPU
		}
		sessLine = sessLine + fmt.Sprintf(" %s:%.1f%%", name, pct)
		// avoid overflowing line length
		if len(sessLine) > width-20 {
			break
		}
	}
	p.drawText(screen, 0, line, helpStyle, truncate(sessLine, width))
	line++

	if len(p.rows) == 0 {
		p.drawText(screen, 0, line, tcell.StyleDefault.Foreground(tcell.ColorGray), "no tmux panes found")
	} else {
		available := max((height-1)-line, 1)
		start := 0
		if p.cursor >= available {
			start = p.cursor - available + 1
		}
		end := min(start+available, len(p.rows))
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
			// compute pane contribution percent
			panePct := 0.0
			if p.mode == usageModeMem {
				if p.sysMemTotalKB > 0 {
					panePct = (float64(row.RSS) / float64(p.sysMemTotalKB)) * 100.0
				}
			} else {
				panePct = row.CPU
			}
			text := fmt.Sprintf(
				"%s[%s] %s %8s (%5.1f%%)  %s:%s.%s  %s  top:%s",
				prefix,
				hotkey,
				active,
				p.mode.metric(row),
				panePct,
				row.SessionName,
				row.WindowIndex,
				row.PaneIndex,
				row.WindowName,
				p.mode.topProcess(row),
			)
			p.drawText(screen, 0, line, style, truncate(text, width))
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
	defer f.Close()
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
