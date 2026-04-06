package main

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

func (a *app) draw(screen tcell.Screen) {
	width, height := screen.Size()
	screen.Clear()

	headerStyle := tcell.StyleDefault.Foreground(tcell.ColorAqua).Bold(true)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	statusStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen)
	errorStyle := tcell.StyleDefault.Foreground(tcell.ColorRed)

	line := 0
	previewAllHotkeys := a.reorderPreviewHotkeys()
	previewFavHotkeys := a.reorderPreviewFavoriteHotkeys()
	previewFavorites, previewOthers := a.reorderPreviewSections()
	baseAllHotkeys := a.hotkeys
	baseFavHotkeys := a.favKeys
	if a.mode == modeReorder && a.reorderBase != nil {
		baseAllHotkeys = a.reorderBase
	}
	if a.mode == modeReorder && a.reorderBaseF != nil {
		baseFavHotkeys = a.reorderBaseF
	}

	styleTag := "push"
	if a.reorderStyle == reorderSwap {
		styleTag = "swap"
	}
	a.drawText(screen, 0, line, headerStyle, fmt.Sprintf("%s  [reorder:%s]", viewTabs(viewDefault), styleTag))
	line++

	var help string
	var helpLineStyle tcell.Style
	switch a.mode {
	case modeReorder:
		if a.reorderStyle == reorderPush {
			help = "PUSH MODE  [j/k/↑↓] move  [space/enter] place  [m] mode  [esc] cancel"
			helpLineStyle = tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
		} else {
			help = "SWAP MODE  [j/k/↑↓] navigate target  [space/enter] swap  [m] mode  [esc] cancel"
			helpLineStyle = tcell.StyleDefault.Foreground(tcell.ColorTeal).Bold(true)
		}
	case modeFilter:
		help = "FILTER  [type] filter  [↑↓] move  [tab] section  [enter] switch  [esc] cancel"
		helpLineStyle = tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
	default:
		help = "[letters] all  [ctrl+letters] favorite  [j/k] move  [tab] section  [1/2/3] view  [space] reorder  [m] cycle-mode  [.] fav  [Shift+K] kill  [/] filter  [l] refresh  [enter] switch  [esc/ctrl+c] quit"
		helpLineStyle = helpStyle
	}
	a.drawText(screen, 0, line, helpLineStyle, truncate(help, width))
	line++

	if a.mode == modeCreate {
		prompt := "new session name: " + a.createInput
		a.drawText(screen, 0, line, tcell.StyleDefault.Foreground(tcell.ColorYellow), truncate(prompt, width))
		line++
	}

	if a.mode == modeFilter {
		prompt := "/" + a.filterInput
		a.drawText(screen, 0, line, tcell.StyleDefault.Foreground(tcell.ColorYellow), truncate(prompt, width))
		line++
	}

	line = a.drawSection(screen, line, width, height, "Favorites", previewFavorites, a.cursorFav, a.section == 0, previewFavHotkeys, baseFavHotkeys, true)
	a.drawSection(screen, line, width, height, "All", previewOthers, a.cursorOther, a.section == 1, previewAllHotkeys, baseAllHotkeys, false)

	status := a.visibleStatus()
	if status != "" {
		style := statusStyle
		if strings.HasPrefix(status, "error:") {
			style = errorStyle
		}
		a.drawText(screen, 0, height-1, style, truncate(status, width))
	}

	screen.Show()
}

func (a *app) drawSection(screen tcell.Screen, y int, width int, height int, title string, rows []session, cursor int, active bool, previewHotkeys map[string]rune, baseHotkeys map[string]rune, ctrlHotkeys bool) int {
	if y >= height-1 {
		return y
	}

	titleStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite)
	if active {
		titleStyle = titleStyle.Bold(true).Foreground(tcell.ColorBlue)
	}
	a.drawText(screen, 0, y, titleStyle, fmt.Sprintf("%s (%d)", title, len(rows)))
	y++

	if len(rows) == 0 {
		a.drawText(screen, 2, y, tcell.StyleDefault.Foreground(tcell.ColorGray), "- no sessions -")
		return y + 1
	}

	available := max((height-1)-y, 1)
	if cursor < 0 {
		cursor = 0
	}
	start := 0
	if cursor >= available {
		start = cursor - available + 1
	}
	end := min(start+available, len(rows))
	swapTargetName, hasSwapTarget := a.selectedName()

	for i := start; i < end; i++ {
		if y >= height-1 {
			break
		}
		s := rows[i]
		keyLabel := " "
		if r, ok := previewHotkeys[s.Name]; ok {
			if ctrlHotkeys {
				keyLabel = "^" + string(r)
			} else {
				keyLabel = string(r)
			}
		}
		keyChangeLabel := ""
		if a.mode == modeReorder {
			if before, ok := baseHotkeys[s.Name]; ok {
				beforeLabel := string(before)
				if ctrlHotkeys {
					beforeLabel = "^" + beforeLabel
				}
				if beforeLabel != keyLabel {
					keyChangeLabel = fmt.Sprintf(" ~%s→%s", beforeLabel, keyLabel)
				}
			}
		}
		attached := " "
		if s.Attached {
			attached = "*"
		}
		prefix := "  "
		style := tcell.StyleDefault

		inReorder := a.mode == modeReorder && active
		isPickedUp := inReorder && a.reorderStyle == reorderSwap && s.Name == a.pickupName
		isSwapTarget := inReorder && a.reorderStyle == reorderSwap && hasSwapTarget && s.Name == swapTargetName && s.Name != a.pickupName
		actionLabel := ""

		switch {
		case isPickedUp:
			// The session being held — yellow, floats visually at its current slot.
			style = tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack)
			prefix = "↑ "
			actionLabel = " (picked)"
		case isSwapTarget:
			// The session that will be swapped with the picked-up one.
			style = tcell.StyleDefault.Background(tcell.ColorTeal).Foreground(tcell.ColorBlack)
			prefix = "↓ "
			actionLabel = " (target)"
		case i == cursor && active && a.mode == modeReorder && a.reorderStyle == reorderPush:
			// Push mode — show the session being moved in yellow.
			style = tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack)
			prefix = "> "
			actionLabel = " (moving)"
		case i == cursor && active:
			style = tcell.StyleDefault.Background(tcell.ColorGray).Foreground(tcell.ColorBlack)
			prefix = "> "
		}
		if keyChangeLabel != "" && !(i == cursor && active) {
			style = style.Foreground(tcell.NewRGBColor(130, 150, 160))
		}

		row := fmt.Sprintf("%s[%s] %s %s%s%s", prefix, keyLabel, attached, s.Name, keyChangeLabel, actionLabel)
		a.drawText(screen, 0, y, style, truncate(row, width))
		y++
	}

	return y
}

func (a *app) drawText(screen tcell.Screen, x int, y int, style tcell.Style, text string) {
	for _, r := range text {
		screen.SetContent(x, y, r, nil, style)
		x++
	}
}

func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
