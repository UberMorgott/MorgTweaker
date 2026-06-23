package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"morgtweaker/internal/core"
)

// Layout geometry --------------------------------------------------------------
//
// Rows (top → bottom):
//   y=0           title bar (full width)
//   y=1 .. 1+H-1  pane region (left | divider | right), H = paneViewH rows
//   y=1+H         status bar (full width)
//
// Columns:
//   [0, leftWidth)              left pane content (incl. its right-edge scrollbar)
//   leftWidth                   fixed vertical divider column "│"
//   (leftWidth, totalWidth)     right pane content (incl. its right-edge scrollbar)

const (
	leftMin    = 12
	leftMax    = 24
	titleRows  = 1
	statusRows = 1
	dividerW   = 1
)

// leftWidth is the fixed left-pane width: longest category label (display cells)
// + a little padding, clamped to [leftMin, leftMax]. It does NOT change on resize.
func (m model) leftWidth() int {
	w := leftMin
	for _, c := range m.catalog {
		lw := lipgloss.Width(catName(m.lang, c)) + 4 // marker + checkbox-free pad
		if lw > w {
			w = lw
		}
	}
	if w < leftMin {
		w = leftMin
	}
	if w > leftMax {
		w = leftMax
	}
	return w
}

// paneViewH is the height (rows) of the pane region between the title and status.
func (m model) paneViewH() int {
	return maxi(m.h-titleRows-statusRows, 1)
}

// rightMin keeps the right pane usable on a narrow terminal.
const rightMin = 16

// rightWidth is the elastic right-pane width: total − left − divider, floored at
// rightMin so the layout never collapses.
func (m model) rightWidth() int {
	return maxi(m.w-m.leftWidth()-dividerW, rightMin)
}

// --- pane body builders (single geometry source for render + hit-test) -------

// leftBody returns the left-pane row strings (one per category), styled for the
// given active state + cursor. innerW is the text width available in the pane.
func (m model) leftBody(innerW int) []string {
	if len(m.catalog) == 0 {
		return []string{dimStyle.Render(truncDisplay(T(m.lang, kNoCategories), innerW))}
	}
	lines := make([]string, len(m.catalog))
	for i, c := range m.catalog {
		label := catName(m.lang, c)
		selected := i == m.catCursor
		lines[i] = m.styleRow(label, innerW, selected, m.activePane == paneLeft)
	}
	return lines
}

// rightBody returns the right-pane row strings (one per tweak of the selected
// category), with checkbox glyph + name + state/needs-admin marker. It reads ONLY
// cached model state (m.statuses / m.probing / m.progress) — never any I/O.
func (m model) rightBody(innerW int) []string {
	tws := m.curTweaks()
	if len(tws) == 0 {
		return []string{dimStyle.Render(truncDisplay(T(m.lang, kNoTweaks), innerW))}
	}
	lines := make([]string, len(tws))
	for i, tw := range tws {
		st := m.statusOf(tw.ID)

		// Color encodes state: applied tweaks recede (dim), appliable ones are
		// bright (the call to action), blocked/error demand attention (red),
		// unknown is a dim "…" placeholder until the async probe resolves.
		applied := st == core.StatusOn || st == core.StatusRebootPending || st == core.StatusAbsent
		rowStyle := appliableStyle // off / partial → bright
		glyphCh := glyphOff
		switch {
		case st == core.StatusBlocked:
			rowStyle = errStyle
		case st == core.StatusUnknown:
			rowStyle = dimStyle
		case applied:
			rowStyle = appliedStyle
			glyphCh = glyphOn
		}
		if st == core.StatusPartial {
			glyphCh = glyphOn // partially applied
		}

		name := tweakName(m.lang, tw)

		// Marker words ONLY for states that need explaining; plain on/off carry
		// their meaning through color alone (no on/off labels).
		marker := ""
		switch {
		case m.probing[tw.ID] && st == core.StatusUnknown:
			marker = "  " + dimStyle.Render(T(m.lang, kProbing))
		case st == core.StatusUnknown:
			marker = "  " + dimStyle.Render(T(m.lang, kStatusUnknown))
		case st == core.StatusWorking:
			marker = "  " + appliableStyle.Render(m.progressLabel(tw.ID))
		case tw.NeedsAdmin() && !m.isAdmin:
			marker = "  " + adminOffStyle.Render(T(m.lang, kNeedsAdmin))
		case st == core.StatusBlocked:
			marker = "  " + errStyle.Render(T(m.lang, kStatusBlocked))
		case st == core.StatusAbsent:
			marker = "  " + dimStyle.Render(T(m.lang, kStatusAbsent))
		case st == core.StatusPartial:
			marker = "  " + appliableStyle.Render(T(m.lang, kStatusPartial))
		case st == core.StatusRebootPending:
			marker = "  " + adminOffStyle.Render(T(m.lang, kStatusRebootPending))
		}

		glyph := rowStyle.Render(glyphCh)
		styledName := rowStyle.Render(name)

		selected := i == m.twCursor
		active := m.activePane == paneRight
		// Whole-row color (via styledName) conveys status when not selected;
		// selection styling overlays it.
		raw := glyph + " " + styledName + marker
		lines[i] = m.styleSelectable(raw, name, glyph, marker, innerW, selected, active)
	}
	return lines
}

// progressLabel formats the streamed progress for a working tweak (e.g. "42%").
func (m model) progressLabel(id string) string {
	p, ok := m.progress[id]
	if !ok {
		return T(m.lang, kStatusWorking)
	}
	if p.Note != "" {
		return fmt.Sprintf("%s %d%%", p.Note, p.Pct)
	}
	return fmt.Sprintf("%s %d%%", T(m.lang, kStatusWorking), p.Pct)
}

// styleRow styles a plain text row (left pane) with selection/active emphasis.
func (m model) styleRow(text string, innerW int, selected, active bool) string {
	text = truncDisplay(text, innerW)
	if !selected {
		return labelStyle.Render(text)
	}
	if active {
		// Fill the whole inner width so the lime highlight reads as a bar.
		return selActiveStyle.Render(padRight(text, innerW))
	}
	return selInactiveStyle.Render(text)
}

// styleSelectable styles a composite right-pane row. When selected+active the
// whole row gets the lime fill (dark ink); the glyph/marker colors are dropped
// in that case so they stay readable on the bright background.
func (m model) styleSelectable(raw, name, glyph, marker string, innerW int, selected, active bool) string {
	if selected && active {
		plain := ansiStrip(glyph) + " " + name + ansiStrip(marker)
		return selActiveStyle.Render(padRight(plain, innerW))
	}
	if selected { // inactive pane: lime name, keep glyph/marker colors
		return truncDisplay(glyph+" "+selInactiveStyle.Render(name)+marker, innerW)
	}
	return truncDisplay(raw, innerW)
}

// View returns a tea.View with alt-screen + cell-motion mouse set per-frame (v2).
// It reads ONLY cached model state — no registry/PowerShell/engine I/O.
func (m model) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = T(m.lang, kAppTitle)
	return v
}

func (m model) viewString() string {
	lw := m.leftWidth()
	rw := m.rightWidth()
	total := lw + dividerW + rw
	viewH := m.paneViewH()

	var sb strings.Builder

	// Title bar (full width).
	sb.WriteString(m.titleBar(total))
	sb.WriteByte('\n')

	// Pane region: render each pane to []string of exactly viewH rows, then join
	// each row with the fixed divider column.
	leftCells := m.renderPane(m.leftBody(lw-1), lw-1, viewH, m.catScroll, m.activePane == paneLeft)
	rightCells := m.renderPane(m.rightBody(rw-1), rw-1, viewH, m.twScroll, m.activePane == paneRight)

	divCol := borderStyle.Render("│")
	if m.activePane == paneLeft {
		divCol = activeMarkStyle.Render("│")
	}
	for i := range viewH {
		sb.WriteString(leftCells[i])
		sb.WriteString(divCol)
		sb.WriteString(rightCells[i])
		sb.WriteByte('\n')
	}

	// Status bar (full width).
	sb.WriteString(m.statusBar(total))
	return sb.String()
}

// titleBar renders the full-width top bar: app name (lime) left, hints right-ish.
func (m model) titleBar(total int) string {
	name := titleStyle.Render(" " + T(m.lang, kAppTitle) + " ")
	return padRight(name, total)
}

// statusBar renders the full-width bottom bar: admin badge · N on · hints, or the
// last action result when one is set.
func (m model) statusBar(total int) string {
	var admin string
	if m.isAdmin {
		admin = adminOnStyle.Render(T(m.lang, kAdmin))
	} else {
		admin = adminOffStyle.Render(T(m.lang, kNotAdmin))
	}

	onCount := m.countOn()
	sel := dimStyle.Render(fmt.Sprintf(T(m.lang, kSelectedN), onCount))

	sep := dimStyle.Render(T(m.lang, kStatusSep))

	var right string
	if m.status != "" {
		if m.statusErr {
			right = errStyle.Render(m.status)
		} else {
			right = okStyle.Render(m.status)
		}
	} else {
		right = helpStyle.Render(T(m.lang, kHints))
	}

	line := " " + admin + sep + sel + sep + right
	return padRight(line, total)
}

// countOn returns how many tweaks across the whole catalog are currently ON,
// reading ONLY the cached statuses (no I/O).
func (m model) countOn() int {
	n := 0
	for _, c := range m.catalog {
		for _, tw := range c.Tweaks {
			if m.statusOf(tw.ID).IsOn() {
				n++
			}
		}
	}
	return n
}

// renderPane renders body[scroll:scroll+viewH] into exactly viewH fixed-width
// cells, drawing a proportional scrollbar in the pane's rightmost column when the
// body overflows. Each cell is exactly innerW+1 columns wide: innerW content cells
// + 1 scrollbar column. Callers pass innerW = paneWidth-1 so a full cell totals the
// pane width (left = lw, right = rw), keeping the pane region aligned with the
// full-width title/status bars (total = lw + dividerW + rw). The returned slice
// has exactly viewH entries.
func (m model) renderPane(body []string, innerW, viewH, scroll int, active bool) []string {
	total := len(body)
	off := clampScroll(scroll, total, viewH)
	overflow := total > viewH

	thumbStart, thumbEnd := 0, 0
	if overflow {
		thumb := maxi(viewH*viewH/total, 1)
		maxOff := total - viewH
		pos := 0
		if maxOff > 0 {
			pos = off * (viewH - thumb) / maxOff
		}
		if pos < 0 {
			pos = 0
		}
		if pos > viewH-thumb {
			pos = viewH - thumb
		}
		thumbStart, thumbEnd = pos, pos+thumb
	}

	cells := make([]string, viewH)
	for i := range viewH {
		var text string
		if off+i < total {
			text = body[off+i]
		}
		// Pad/truncate the content area to exactly innerW cells.
		text = padRight(text, innerW)
		// Scrollbar column (1 cell) on the right edge.
		bar := " "
		if overflow {
			if i >= thumbStart && i < thumbEnd {
				if active {
					bar = activeMarkStyle.Render("█")
				} else {
					bar = borderStyle.Render("█")
				}
			} else {
				bar = borderDimStyle.Render("│")
			}
		}
		// No leading space: cell = innerW content cells + 1 scrollbar column, so the
		// cell is exactly innerW+1 (= pane width) and aligns with the chrome bars.
		cells[i] = text + bar
	}
	return cells
}

// --- mouse hit-test (per pane, same geometry as renderPane) ------------------

// paneAtX maps an X coordinate to a pane by the fixed left-width boundary.
// Column layout (lw = leftWidth): left content [0, lw-1), left scrollbar at lw-1,
// divider at lw, right content [lw+1, …). So x < lw is the left pane (including its
// scrollbar column); x >= lw (divider + everything right) is the right pane.
func (m model) paneAtX(x int) pane {
	if x < m.leftWidth() {
		return paneLeft
	}
	return paneRight
}

// rowAtClick maps a click (x,y) to (pane, rowIndex). ok=false outside the pane
// region or past the last row. Uses each pane's own clamped scroll offset and the
// same body length the renderer drew.
func (m model) rowAtClick(x, y int) (p pane, idx int, ok bool) {
	viewH := m.paneViewH()
	if y < titleRows || y >= titleRows+viewH {
		return 0, 0, false
	}
	p = m.paneAtX(x)
	var total, scroll int
	switch p {
	case paneLeft:
		total, scroll = len(m.catalog), m.catScroll
	case paneRight:
		total, scroll = len(m.curTweaks()), m.twScroll
	}
	off := clampScroll(scroll, total, viewH)
	idx = off + (y - titleRows)
	if idx < 0 || idx >= total {
		return p, 0, false
	}
	return p, idx, true
}
