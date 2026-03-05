package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

type mode int

const (
	modeNormal mode = iota
	modeReorder
	modeCreate
)

type app struct {
	client tmuxClient
	store  *stateStore

	state     state
	sessions  []session
	favorites []session
	others    []session
	hotkeys   map[string]rune

	section     int
	cursorFav   int
	cursorOther int

	mode        mode
	createInput string

	status       string
	statusExpiry time.Time
}

func newApp(client tmuxClient, store *stateStore) (*app, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	a := &app{client: client, store: store, state: st}
	if err := a.refreshSessions(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *app) Run() error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return fmt.Errorf("create screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("init screen: %w", err)
	}
	defer screen.Fini()

	screen.HideCursor()
	a.draw(screen)

	for {
		ev := screen.PollEvent()
		switch e := ev.(type) {
		case *tcell.EventResize:
			screen.Sync()
			a.draw(screen)
		case *tcell.EventKey:
			done, runSwitch := a.handleKey(e)
			if runSwitch != "" {
				screen.Fini()
				if err := a.client.SwitchSession(runSwitch); err != nil {
					return err
				}
				return nil
			}
			if done {
				return nil
			}
			a.draw(screen)
		}
	}
}

func (a *app) handleKey(key *tcell.EventKey) (done bool, switchTo string) {
	if a.mode == modeCreate {
		return a.handleCreateKey(key)
	}

	if key.Key() == tcell.KeyCtrlC || key.Key() == tcell.KeyEscape {
		return true, ""
	}

	if key.Key() == tcell.KeyTab {
		a.toggleSection()
		return false, ""
	}

	if key.Key() == tcell.KeyEnter {
		if name, ok := a.selectedName(); ok {
			return false, name
		}
		return false, ""
	}

	if key.Key() == tcell.KeyRune {
		r := unicode.ToLower(key.Rune())
		if name, ok := a.hotkeyTarget(r); ok {
			return false, name
		}
		switch r {
		case 'j':
			a.moveDown()
		case 'k':
			a.moveUp()
		case ' ':
			a.toggleReorderMode()
		case 'f':
			a.toggleFavorite()
		case 'x':
			a.killSelected()
		case 'n':
			a.mode = modeCreate
			a.createInput = ""
			a.status = "new session: type name and press Enter"
			a.statusExpiry = time.Time{}
		case 'r':
			if err := a.refreshSessions(); err != nil {
				a.setError(err)
			}
		}
		return false, ""
	}

	switch key.Key() {
	case tcell.KeyUp:
		a.moveUp()
	case tcell.KeyDown:
		a.moveDown()
	}

	return false, ""
}

func (a *app) handleCreateKey(key *tcell.EventKey) (bool, string) {
	switch key.Key() {
	case tcell.KeyEsc:
		a.mode = modeNormal
		a.setStatus("create canceled")
		return false, ""
	case tcell.KeyEnter:
		name := strings.TrimSpace(a.createInput)
		if name == "" {
			a.setStatus("session name cannot be empty")
			return false, ""
		}
		if err := a.client.NewSession(name); err != nil {
			a.setError(err)
			return false, ""
		}
		a.mode = modeNormal
		a.createInput = ""
		if err := a.refreshSessions(); err != nil {
			a.setError(err)
			return false, ""
		}
		a.selectByName(name)
		a.setStatus(fmt.Sprintf("created %s", name))
		return false, ""
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(a.createInput) > 0 {
			a.createInput = a.createInput[:len(a.createInput)-1]
		}
		return false, ""
	case tcell.KeyRune:
		r := key.Rune()
		if r >= 32 && r <= 126 {
			a.createInput += string(r)
		}
		return false, ""
	default:
		return false, ""
	}
}

func (a *app) toggleSection() {
	if len(a.favorites) == 0 && len(a.others) == 0 {
		return
	}
	if a.section == 0 {
		if len(a.others) > 0 {
			a.section = 1
		}
		return
	}
	if len(a.favorites) > 0 {
		a.section = 0
	}
}

func (a *app) moveUp() {
	if a.mode == modeReorder {
		a.reorder(-1)
		return
	}
	if a.section == 0 {
		if a.cursorFav > 0 {
			a.cursorFav--
		}
		return
	}
	if a.cursorOther > 0 {
		a.cursorOther--
	}
}

func (a *app) moveDown() {
	if a.mode == modeReorder {
		a.reorder(1)
		return
	}
	if a.section == 0 {
		if a.cursorFav < len(a.favorites)-1 {
			a.cursorFav++
		}
		return
	}
	if a.cursorOther < len(a.others)-1 {
		a.cursorOther++
	}
}

func (a *app) toggleReorderMode() {
	if _, ok := a.selectedName(); !ok {
		return
	}
	if a.mode == modeReorder {
		a.mode = modeNormal
		a.setStatus("reorder mode off")
		return
	}
	a.mode = modeReorder
	a.setStatus("reorder mode on: j/k moves selected session")
}

func (a *app) toggleFavorite() {
	name, ok := a.selectedName()
	if !ok {
		return
	}

	idx := indexOf(a.state.Favorites, name)
	if idx >= 0 {
		a.state.Favorites = removeAt(a.state.Favorites, idx)
		a.state.Order = append([]string{name}, a.state.Order...)
		a.setStatus(fmt.Sprintf("unfavorited %s", name))
	} else {
		a.state.Favorites = append(a.state.Favorites, name)
		a.state.Order = removeByValue(a.state.Order, name)
		a.setStatus(fmt.Sprintf("favorited %s", name))
	}
	if err := a.persistAndRebuild(); err != nil {
		a.setError(err)
	}
}

func (a *app) reorder(delta int) {
	name, ok := a.selectedName()
	if !ok {
		return
	}
	if a.section == 0 {
		idx := indexOf(a.state.Favorites, name)
		if idx < 0 {
			return
		}
		newIdx := idx + delta
		if newIdx < 0 || newIdx >= len(a.state.Favorites) {
			return
		}
		a.state.Favorites[idx], a.state.Favorites[newIdx] = a.state.Favorites[newIdx], a.state.Favorites[idx]
		a.cursorFav = newIdx
	} else {
		names := make([]string, 0, len(a.others))
		for _, s := range a.others {
			names = append(names, s.Name)
		}
		idx := indexOf(names, name)
		if idx < 0 {
			return
		}
		newIdx := idx + delta
		if newIdx < 0 || newIdx >= len(names) {
			return
		}
		names[idx], names[newIdx] = names[newIdx], names[idx]
		a.state.Order = names
		a.cursorOther = newIdx
	}
	if err := a.persistAndRebuild(); err != nil {
		a.setError(err)
		return
	}
	a.setStatus("priority updated")
}

func (a *app) killSelected() {
	name, ok := a.selectedName()
	if !ok {
		return
	}
	if err := a.client.KillSession(name); err != nil {
		a.setError(err)
		return
	}
	a.state.Favorites = removeByValue(a.state.Favorites, name)
	a.state.Order = removeByValue(a.state.Order, name)
	if err := a.persistAndRebuild(); err != nil {
		a.setError(err)
		return
	}
	a.setStatus(fmt.Sprintf("killed %s", name))
}

func (a *app) refreshSessions() error {
	sessions, err := a.client.ListSessions()
	if err != nil {
		return err
	}
	a.sessions = sessions
	a.state = normalizeState(a.state, sessions)
	a.rebuildLists()
	if err := a.store.Save(a.state); err != nil {
		return err
	}
	return nil
}

func (a *app) persistAndRebuild() error {
	a.state = normalizeState(a.state, a.sessions)
	a.rebuildLists()
	return a.store.Save(a.state)
}

func (a *app) rebuildLists() {
	a.favorites, a.others = orderSessions(a.sessions, a.state)
	a.hotkeys = assignHotkeys(a.favorites, a.others, SessionHotkeyAlphabet())
	a.clampCursors()
}

func (a *app) clampCursors() {
	if a.cursorFav >= len(a.favorites) {
		a.cursorFav = max(len(a.favorites)-1, 0)
	}
	if a.cursorOther >= len(a.others) {
		a.cursorOther = max(len(a.others)-1, 0)
	}
	if a.section == 0 && len(a.favorites) == 0 && len(a.others) > 0 {
		a.section = 1
	}
	if a.section == 1 && len(a.others) == 0 && len(a.favorites) > 0 {
		a.section = 0
	}
}

func (a *app) selectedName() (string, bool) {
	if a.section == 0 {
		if len(a.favorites) == 0 {
			return "", false
		}
		return a.favorites[a.cursorFav].Name, true
	}
	if len(a.others) == 0 {
		return "", false
	}
	return a.others[a.cursorOther].Name, true
}

func (a *app) hotkeyTarget(r rune) (string, bool) {
	for name, key := range a.hotkeys {
		if key == r {
			return name, true
		}
	}
	return "", false
}

func (a *app) selectByName(name string) {
	if idx := indexSession(a.favorites, name); idx >= 0 {
		a.section = 0
		a.cursorFav = idx
		return
	}
	if idx := indexSession(a.others, name); idx >= 0 {
		a.section = 1
		a.cursorOther = idx
	}
}

func (a *app) setStatus(msg string) {
	a.status = msg
	a.statusExpiry = time.Now().Add(4 * time.Second)
}

func (a *app) setError(err error) {
	a.status = "error: " + err.Error()
	a.statusExpiry = time.Now().Add(8 * time.Second)
}

func (a *app) visibleStatus() string {
	if a.status == "" {
		return ""
	}
	if a.statusExpiry.IsZero() || time.Now().Before(a.statusExpiry) {
		return a.status
	}
	a.status = ""
	return ""
}

func indexSession(sessions []session, name string) int {
	for i, s := range sessions {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func indexOf(items []string, target string) int {
	for i, item := range items {
		if item == target {
			return i
		}
	}
	return -1
}

func removeAt(items []string, idx int) []string {
	out := make([]string, 0, len(items)-1)
	out = append(out, items[:idx]...)
	out = append(out, items[idx+1:]...)
	return out
}

func removeByValue(items []string, target string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == target {
			continue
		}
		out = append(out, item)
	}
	return out
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
