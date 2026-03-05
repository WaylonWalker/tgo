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
	a.drawText(screen, 0, line, headerStyle, "tgo - tmux session switcher")
	line++

	help := "[letters] switch  [j/k or arrows] move  [tab] section  [space] reorder  [f] favorite  [n] new  [x] kill  [r] refresh  [enter] switch  [esc/ctrl+c] quit"
	a.drawText(screen, 0, line, helpStyle, truncate(help, width))
	line++

	if a.mode == modeCreate {
		prompt := "new session name: " + a.createInput
		a.drawText(screen, 0, line, tcell.StyleDefault.Foreground(tcell.ColorYellow), truncate(prompt, width))
		line++
	}

	line = a.drawSection(screen, line, width, height, "Favorites", a.favorites, a.cursorFav, a.section == 0)
	line = a.drawSection(screen, line, width, height, "Others", a.others, a.cursorOther, a.section == 1)

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

func (a *app) drawSection(screen tcell.Screen, y int, width int, height int, title string, rows []session, cursor int, active bool) int {
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

	for i := start; i < end; i++ {
		if y >= height-1 {
			break
		}
		s := rows[i]
		keyLabel := " "
		if r, ok := a.hotkeys[s.Name]; ok {
			keyLabel = string(r)
		}
		attached := " "
		if s.Attached {
			attached = "*"
		}
		prefix := "  "
		style := tcell.StyleDefault
		if i == cursor && active {
			prefix = "> "
			style = style.Background(tcell.ColorGray).Foreground(tcell.ColorBlack)
		}
		if a.mode == modeReorder && i == cursor && active {
			style = style.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack)
		}
		row := fmt.Sprintf("%s[%s] %s %s", prefix, keyLabel, attached, s.Name)
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
