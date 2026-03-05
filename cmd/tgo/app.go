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

type reorderStyle int

const (
	reorderPush reorderStyle = iota
	reorderSwap
)

type app struct {
	client tmuxClient
	store  *stateStore

	state     state
	sessions  []session
	favorites []session
	others    []session
	hotkeys   map[string]rune
	favKeys   map[string]rune

	section     int
	cursorFav   int
	cursorOther int

	mode        mode
	createInput string

	reorderStyle  reorderStyle
	pickupName    string
	pickupSection int
	reorderBase   map[string]rune
	reorderBaseF  map[string]rune

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
	if len(a.favorites) > 0 {
		a.section = 0
		a.cursorFav = 0
	} else if len(a.others) > 0 {
		a.section = 1
		a.cursorOther = 0
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

	if key.Key() == tcell.KeyCtrlC {
		return true, ""
	}

	// Esc cancels reorder mode instead of quitting.
	if key.Key() == tcell.KeyEscape {
		if a.mode == modeReorder {
			a.mode = modeNormal
			a.pickupName = ""
			a.reorderBase = nil
			a.reorderBaseF = nil
			a.setStatus("reorder: canceled")
			return false, ""
		}
		return true, ""
	}

	if key.Key() == tcell.KeyTab {
		if a.mode != modeReorder {
			a.toggleSection()
		}
		return false, ""
	}

	// Enter in reorder mode places the session without switching.
	if key.Key() == tcell.KeyEnter {
		if a.mode == modeReorder {
			if a.reorderStyle == reorderSwap && a.pickupName != "" {
				a.commitSwap()
			}
			a.mode = modeNormal
			a.pickupName = ""
			a.reorderBase = nil
			a.reorderBaseF = nil
			return false, ""
		}
		if name, ok := a.selectedName(); ok {
			return false, name
		}
		return false, ""
	}

	if name, ok := a.favoriteHotkeyTarget(key.Key()); ok && a.mode != modeReorder {
		return false, name
	}

	if key.Key() == tcell.KeyRune {
		raw := key.Rune()
		if raw == 'K' && a.mode != modeReorder {
			a.killSelected()
			return false, ""
		}
		r := unicode.ToLower(raw)
		// Hotkeys are disabled while repositioning a session.
		if a.mode != modeReorder {
			if name, ok := a.hotkeyTarget(r); ok {
				return false, name
			}
		}
		switch r {
		case 'j':
			a.moveDown()
		case 'k':
			a.moveUp()
		case ' ':
			a.toggleReorderMode()
		case '.':
			if a.mode != modeReorder {
				a.toggleFavorite()
			}
		case 'n':
			if a.mode != modeReorder {
				a.mode = modeCreate
				a.createInput = ""
				a.status = "new session: type name and press Enter"
				a.statusExpiry = time.Time{}
			}
		case 'l':
			if a.mode != modeReorder {
				if err := a.refreshSessions(); err != nil {
					a.setError(err)
				}
			}
		case 'm':
			a.cycleReorderStyle()
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
	if a.mode == modeReorder && a.reorderStyle == reorderPush {
		a.reorder(-1)
		return
	}
	if a.section == 0 {
		if a.cursorFav > 0 {
			a.cursorFav--
		} else if len(a.others) > 0 {
			a.section = 1
			a.cursorOther = 0
		}
		return
	}
	if a.cursorOther > 0 {
		a.cursorOther--
		return
	}
	if len(a.favorites) > 0 {
		a.section = 0
		a.cursorFav = len(a.favorites) - 1
	}
}

func (a *app) moveDown() {
	if a.mode == modeReorder && a.reorderStyle == reorderPush {
		a.reorder(1)
		return
	}
	if a.section == 0 {
		if a.cursorFav < len(a.favorites)-1 {
			a.cursorFav++
			return
		}
		if len(a.others) > 0 {
			a.section = 1
			a.cursorOther = 0
		}
		return
	}
	if a.cursorOther < len(a.others)-1 {
		a.cursorOther++
		return
	}
	if len(a.favorites) > 0 {
		a.section = 0
		a.cursorFav = 0
	}
}

func (a *app) toggleReorderMode() {
	if _, ok := a.selectedName(); !ok {
		return
	}
	if a.mode == modeReorder {
		if a.reorderStyle == reorderSwap && a.pickupName != "" {
			a.commitSwap()
		}
		a.mode = modeNormal
		a.pickupName = ""
		a.reorderBase = nil
		a.reorderBaseF = nil
		return
	}
	a.mode = modeReorder
	a.reorderBase = cloneHotkeys(a.hotkeys)
	a.reorderBaseF = cloneHotkeys(a.favKeys)
	if a.reorderStyle == reorderSwap {
		name, _ := a.selectedName()
		a.pickupName = name
		a.pickupSection = a.section
		a.setStatus("swap: [j/k] target · [space/enter] swap · [m] mode · [esc] cancel")
	} else {
		a.setStatus("push: [j/k] move · [space/enter] place · [m] mode · [esc] cancel")
	}
}

func (a *app) cycleReorderStyle() {
	if a.reorderStyle == reorderPush {
		a.reorderStyle = reorderSwap
		if a.mode == modeReorder {
			name, ok := a.selectedName()
			if ok {
				a.pickupName = name
				a.pickupSection = a.section
			}
		}
		a.setStatus("reorder mode: swap")
	} else {
		a.reorderStyle = reorderPush
		a.pickupName = ""
		a.setStatus("reorder mode: push")
	}
}

func (a *app) commitSwap() {
	if a.pickupName == "" {
		return
	}
	targetName, ok := a.selectedName()
	if !ok || targetName == a.pickupName {
		return
	}
	if a.section != a.pickupSection {
		a.setStatus("swap: cannot swap across sections")
		return
	}
	if a.section == 0 {
		idx1 := indexOf(a.state.Favorites, a.pickupName)
		idx2 := indexOf(a.state.Favorites, targetName)
		if idx1 >= 0 && idx2 >= 0 {
			a.state.Favorites[idx1], a.state.Favorites[idx2] = a.state.Favorites[idx2], a.state.Favorites[idx1]
			if err := a.persistAndRebuild(); err != nil {
				a.setError(err)
				return
			}
			a.cursorFav = idx2
		}
	} else {
		names := make([]string, len(a.others))
		for i, s := range a.others {
			names[i] = s.Name
		}
		idx1 := indexOf(names, a.pickupName)
		idx2 := indexOf(names, targetName)
		if idx1 >= 0 && idx2 >= 0 {
			names[idx1], names[idx2] = names[idx2], names[idx1]
			a.state.Order = names
			if err := a.persistAndRebuild(); err != nil {
				a.setError(err)
				return
			}
			a.cursorOther = idx2
		}
	}
	a.setStatus(fmt.Sprintf("swapped %s ↔ %s", a.pickupName, targetName))
}

func (a *app) toggleFavorite() {
	name, ok := a.selectedName()
	if !ok {
		return
	}

	idx := indexOf(a.state.Favorites, name)
	if idx >= 0 {
		a.state.Favorites = removeAt(a.state.Favorites, idx)
		delete(a.state.FavoriteRoots, name)
		a.setStatus(fmt.Sprintf("unfavorited %s", name))
	} else {
		a.state.Favorites = append(a.state.Favorites, name)
		if s, ok := a.findSessionByName(name); ok && s.RootDir != "" {
			a.state.FavoriteRoots[name] = s.RootDir
		}
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
	if a.state.FavoriteRoots == nil {
		a.state.FavoriteRoots = map[string]string{}
	}
	sessions, err := a.client.ListSessions()
	if err != nil {
		return err
	}
	sessions, err = a.ensureFavoriteSessions(sessions)
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
	a.favKeys = assignHotkeys(a.favorites, SessionHotkeyAlphabet())
	a.hotkeys = assignHotkeys(a.others, SessionHotkeyAlphabet())
	a.clampCursors()
}

func (a *app) ensureFavoriteSessions(sessions []session) ([]session, error) {
	exists := make(map[string]struct{}, len(sessions))
	for _, s := range sessions {
		exists[s.Name] = struct{}{}
		if s.RootDir != "" && a.state.FavoriteRoots[s.Name] == "" {
			a.state.FavoriteRoots[s.Name] = s.RootDir
		}
	}
	created := false
	for _, name := range a.state.Favorites {
		if _, ok := exists[name]; ok {
			continue
		}
		root := a.state.FavoriteRoots[name]
		if err := a.client.NewSessionAt(name, root); err != nil {
			return nil, err
		}
		created = true
	}
	if !created {
		return sessions, nil
	}
	return a.client.ListSessions()
}

func (a *app) reorderPreviewHotkeys() map[string]rune {
	if a.mode != modeReorder || a.reorderStyle != reorderSwap || a.pickupName == "" {
		return a.hotkeys
	}
	targetName, ok := a.selectedName()
	if !ok || targetName == a.pickupName || a.section != a.pickupSection {
		return a.hotkeys
	}

	previewState := a.state
	if a.section == 0 {
		idx1 := indexOf(previewState.Favorites, a.pickupName)
		idx2 := indexOf(previewState.Favorites, targetName)
		if idx1 < 0 || idx2 < 0 {
			return a.hotkeys
		}
		previewState.Favorites[idx1], previewState.Favorites[idx2] = previewState.Favorites[idx2], previewState.Favorites[idx1]
	} else {
		names := make([]string, len(a.others))
		for i, s := range a.others {
			names[i] = s.Name
		}
		idx1 := indexOf(names, a.pickupName)
		idx2 := indexOf(names, targetName)
		if idx1 < 0 || idx2 < 0 {
			return a.hotkeys
		}
		names[idx1], names[idx2] = names[idx2], names[idx1]
		previewState.Order = names
	}

	_, others := orderSessions(a.sessions, previewState)
	return assignHotkeys(others, SessionHotkeyAlphabet())
}

func (a *app) reorderPreviewFavoriteHotkeys() map[string]rune {
	if a.mode != modeReorder || a.reorderStyle != reorderSwap || a.pickupName == "" {
		return a.favKeys
	}
	targetName, ok := a.selectedName()
	if !ok || targetName == a.pickupName || a.section != a.pickupSection {
		return a.favKeys
	}

	previewState := a.state
	if a.section == 0 {
		idx1 := indexOf(previewState.Favorites, a.pickupName)
		idx2 := indexOf(previewState.Favorites, targetName)
		if idx1 < 0 || idx2 < 0 {
			return a.favKeys
		}
		previewState.Favorites[idx1], previewState.Favorites[idx2] = previewState.Favorites[idx2], previewState.Favorites[idx1]
	}
	favorites, _ := orderSessions(a.sessions, previewState)
	return assignHotkeys(favorites, SessionHotkeyAlphabet())
}

func (a *app) reorderPreviewSections() ([]session, []session) {
	if a.mode != modeReorder || a.reorderStyle != reorderSwap || a.pickupName == "" {
		return a.favorites, a.others
	}
	targetName, ok := a.selectedName()
	if !ok || targetName == a.pickupName || a.section != a.pickupSection {
		return a.favorites, a.others
	}

	favorites := append([]session(nil), a.favorites...)
	others := append([]session(nil), a.others...)
	if a.section == 0 {
		idx1 := indexSession(favorites, a.pickupName)
		idx2 := indexSession(favorites, targetName)
		if idx1 >= 0 && idx2 >= 0 {
			favorites[idx1], favorites[idx2] = favorites[idx2], favorites[idx1]
		}
	} else {
		idx1 := indexSession(others, a.pickupName)
		idx2 := indexSession(others, targetName)
		if idx1 >= 0 && idx2 >= 0 {
			others[idx1], others[idx2] = others[idx2], others[idx1]
		}
	}
	return favorites, others
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

func (a *app) favoriteHotkeyTarget(k tcell.Key) (string, bool) {
	for name, key := range a.favKeys {
		if ctrlKeyForRune(key) == k {
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

func (a *app) findSessionByName(name string) (session, bool) {
	for _, s := range a.sessions {
		if s.Name == name {
			return s, true
		}
	}
	return session{}, false
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

func cloneHotkeys(in map[string]rune) map[string]rune {
	out := make(map[string]rune, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func ctrlKeyForRune(r rune) tcell.Key {
	switch r {
	case 'a':
		return tcell.KeyCtrlA
	case 'b':
		return tcell.KeyCtrlB
	case 'c':
		return tcell.KeyCtrlC
	case 'd':
		return tcell.KeyCtrlD
	case 'e':
		return tcell.KeyCtrlE
	case 'f':
		return tcell.KeyCtrlF
	case 'g':
		return tcell.KeyCtrlG
	case 'h':
		return tcell.KeyCtrlH
	case 'i':
		return tcell.KeyCtrlI
	case 'j':
		return tcell.KeyCtrlJ
	case 'k':
		return tcell.KeyCtrlK
	case 'l':
		return tcell.KeyCtrlL
	case 'm':
		return tcell.KeyCtrlM
	case 'n':
		return tcell.KeyCtrlN
	case 'o':
		return tcell.KeyCtrlO
	case 'p':
		return tcell.KeyCtrlP
	case 'q':
		return tcell.KeyCtrlQ
	case 'r':
		return tcell.KeyCtrlR
	case 's':
		return tcell.KeyCtrlS
	case 't':
		return tcell.KeyCtrlT
	case 'u':
		return tcell.KeyCtrlU
	case 'v':
		return tcell.KeyCtrlV
	case 'w':
		return tcell.KeyCtrlW
	case 'x':
		return tcell.KeyCtrlX
	case 'y':
		return tcell.KeyCtrlY
	case 'z':
		return tcell.KeyCtrlZ
	default:
		return tcell.KeyNUL
	}
}
