package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clampScroll bounds a scroll offset to [0, max(0,total-viewH)].
func clampScroll(off, total, viewH int) int {
	maxOff := maxi(total-viewH, 0)
	if off < 0 {
		return 0
	}
	if off > maxOff {
		return maxOff
	}
	return off
}

// truncDisplay truncates s to at most w display cells (ANSI/Unicode-safe).
func truncDisplay(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// ansiStrip removes ANSI styling so styled fragments can be re-rendered plain on
// a highlight fill.
func ansiStrip(s string) string { return ansi.Strip(s) }

// padRight pads s with spaces to exactly w display cells (truncating if longer).
func padRight(s string, w int) string {
	s = truncDisplay(s, w)
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}
