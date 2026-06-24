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
// The whole UI is wrapped in a 1-cell rubber border that hugs the terminal and is
// recomputed from the latest tea.WindowSizeMsg (m.w/m.h) on every resize. Screen
// rows/cols (top → bottom, left → right):
//   y=0                top border edge (carries the centered "MorgTweaker vX" title)
//   y=1 .. 1+H-1       pane region (left | divider | right), H = paneViewH rows
//   y=1+H              status bar (full width, inside the frame)
//   y=m.h-1            bottom border edge
//   x=0                left border column
//   x=1 .. m.w-2       inner content (width innerW = m.w-2)
//   x=m.w-1            right border column
//
// Inner columns (offset by the left border, +1):
//   [0, leftWidth)              left pane content (incl. its right-edge scrollbar)
//   leftWidth                   fixed vertical divider column "│"
//   (leftWidth, total)          right pane content (incl. its right-edge scrollbar)

const (
	leftMin    = 12
	leftMax    = 24
	statusRows = 1
	buttonRows = 1 // universal bottom button bar (inside the frame)
	dividerW   = 1
	frameRows  = 2 // top + bottom border edges
	frameCols  = 2 // left + right border columns
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

// innerW is the content width available INSIDE the border (terminal width minus
// the left+right border columns), so panes fit without overflowing the frame.
func (m model) innerW() int {
	return maxi(m.w-frameCols, leftMin+dividerW+rightMin)
}

// paneViewH is the height (rows) of the pane region: terminal height minus the
// border edges (top+bottom), the status bar, and the bottom button bar — so the
// panes never overlap the footer.
func (m model) paneViewH() int {
	return maxi(m.h-frameRows-statusRows-buttonRows, 1)
}

// rightMin keeps the right pane usable on a narrow terminal.
const rightMin = 16

// rightWidth is the elastic right-pane width: inner − left − divider, floored at
// rightMin so the layout never collapses.
func (m model) rightWidth() int {
	return maxi(m.innerW()-m.leftWidth()-dividerW, rightMin)
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
		lines[i] = m.styleRow(label, innerW)
	}
	return lines
}

// --- per-status action predicates (single source of truth) -------------------
//
// These decide checkbox visibility AND which checked rows the bottom-bar batch
// acts on. They mirror the two-color buckets: appliable rows are the BRIGHT ones,
// rollbackable rows are the GREY-but-still-actionable ones. Hard-blocked / absent
// / unprobed / in-flight rows have NO action (no checkbox).

// statusAppliable reports whether a tweak in this status can be APPLIED now.
// StatusOn is included so an already-applied (grey) row that the user explicitly
// checks is RE-applied on [Apply] (RegSet rewrites the same value; an install
// re-downloads + reinstalls). Row COLOUR is unaffected — only this filter widens.
func statusAppliable(st core.Status) bool {
	return st == core.StatusOff || st == core.StatusPartial || st == core.StatusOn
}

// statusRollbackable reports whether a tweak in this status is APPLIED and can be
// rolled back.
func statusRollbackable(st core.Status) bool {
	return st == core.StatusOn || st == core.StatusRebootPending
}

// statusHasAction reports whether the row offers any action (→ shows a checkbox).
func statusHasAction(st core.Status) bool {
	return statusAppliable(st) || statusRollbackable(st)
}

// rightBody returns the right-pane row strings (one per tweak of the selected
// category), with checkbox glyph + name + state/needs-admin marker. It reads ONLY
// cached model state (m.statuses / m.probing / m.progress) — never any I/O.
func (m model) rightBody(innerW int) []string {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return []string{dimStyle.Render(truncDisplay(T(m.lang, kNoTweaks), innerW))}
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		tw := r.tw
		st := m.rowStatus(tw)

		// Row color = exactly TWO states (no third per-row foreground):
		//   BRIGHT  = still appliable: not yet applied AND not blocked/unavailable
		//             (Off / Partial / Working).
		//   GREY    = already applied OR cannot be applied (On / Blocked / Absent /
		//             RebootPending) OR not yet probed (Unknown → not actionable).
		rowStyle := appliableStyle // bright by default
		switch st {
		case core.StatusOn, core.StatusBlocked, core.StatusAbsent,
			core.StatusRebootPending, core.StatusUnknown:
			rowStyle = appliedStyle // grey/dim
		}

		// Parents show an expand caret (▾/▸) instead of a checkbox; leaves/children
		// show a checkbox ONLY when they HAVE an action (appliable OR rollbackable).
		// The caret is padded to the checkbox width so child/leaf boxes stay aligned.
		// Checkbox fill is driven SOLELY by m.selected (decoupled from status).
		var glyphCh string
		switch {
		case tw.IsParent():
			caret := "▸"
			if m.expanded[tw.ID] {
				caret = "▾"
			}
			glyphCh = padLeftCaret(caret)
		case statusHasAction(st):
			glyphCh = glyphOff
			if m.selected[tw.ID] {
				glyphCh = glyphOn
			}
		default:
			glyphCh = strings.Repeat(" ", lipgloss.Width(glyphOff)) // aligned blank
		}

		name := tweakName(m.lang, tw)

		// Children of an expanded parent are indented one step under it.
		indent := ""
		if r.child {
			indent = "  "
		}

		marker := m.rowMarker(tw, st)

		glyph := rowStyle.Render(glyphCh)
		styledName := rowStyle.Render(name)

		// Whole-row color (grey/bright via rowStyle) is the ONLY per-row styling —
		// there is no focus/cursor highlight (mouse-only UX has no keyboard cursor).
		raw := indent + glyph + " " + styledName + marker
		lines[i] = truncDisplay(raw, innerW)
	}
	return lines
}

// padLeftCaret left-pads a single-cell expand caret into the checkbox glyph width
// so a parent's caret occupies the same column span as a leaf/child checkbox,
// keeping the name column aligned across parents, children and leaves.
func padLeftCaret(caret string) string {
	pad := lipgloss.Width(glyphOff) - lipgloss.Width(caret)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + caret
}

// rowMarker returns the trailing state marker for a right-pane row. Marker words
// appear ONLY for states that need explaining; plain on/off carry their meaning
// through color alone (no on/off labels).
func (m model) rowMarker(tw core.Tweak, st core.Status) string {
	switch {
	case m.probing[tw.ID] && st == core.StatusUnknown:
		return "  " + dimStyle.Render(T(m.lang, kProbing))
	case st == core.StatusUnknown:
		return "  " + dimStyle.Render(T(m.lang, kStatusUnknown))
	case st == core.StatusWorking:
		return "  " + appliableStyle.Render(m.progressLabel(tw.ID))
	case tw.NeedsAdmin() && !m.isAdmin:
		return "  " + adminOffStyle.Render(T(m.lang, kNeedsAdmin))
	case st == core.StatusBlocked:
		return "  " + errStyle.Render(T(m.lang, kStatusBlocked))
	case st == core.StatusAbsent:
		return "  " + dimStyle.Render(T(m.lang, kStatusAbsent))
	case st == core.StatusPartial:
		return "  " + appliableStyle.Render(T(m.lang, kStatusPartial))
	case st == core.StatusRebootPending:
		return "  " + adminOffStyle.Render(T(m.lang, kStatusRebootPending))
	}
	return ""
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

// styleRow styles a plain text row (left pane). There is no focus/cursor
// highlight: mouse-only UX has no keyboard cursor, so rows carry no selection
// background — just the normal label foreground.
func (m model) styleRow(text string, innerW int) string {
	return labelStyle.Render(truncDisplay(text, innerW))
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
	total := lw + dividerW + rw // inner content width (== innerW on a normal terminal)
	viewH := m.paneViewH()

	// During an apply/rollback batch the list+button bar are REPLACED by the
	// progress screen (three stacked bars), still inside the same border + title
	// and above the running status line. It reclaims the button row, so it spans
	// viewH+buttonRows rows; the status bar stays for the live status message.
	if m.screen == screenProgress {
		region := m.progressRegion(total, viewH+buttonRows)
		inner := make([]string, 0, len(region)+statusRows)
		inner = append(inner, region...)
		inner = append(inner, m.statusBar(total))
		return m.frame(inner, total)
	}

	// Build the inner block: pane region rows + status bar + button bar, each
	// exactly `total` display cells wide so the surrounding border stays flush.
	inner := make([]string, 0, viewH+statusRows+buttonRows)

	leftCells := m.renderPane(m.leftBody(lw-1), lw-1, viewH, m.catScroll, m.activePane == paneLeft)
	rightCells := m.renderPane(m.rightBody(rw-1), rw-1, viewH, m.twScroll, m.activePane == paneRight)

	divCol := borderStyle.Render("│")
	if m.activePane == paneLeft {
		divCol = activeMarkStyle.Render("│")
	}
	for i := range viewH {
		inner = append(inner, leftCells[i]+divCol+rightCells[i])
	}
	inner = append(inner, m.statusBar(total))
	inner = append(inner, m.buttonBar(total))

	return m.frame(inner, total)
}

// frame wraps the inner content block in a rubber border that hugs the terminal:
// a top edge carrying the centered "MorgTweaker vX" title, left/right border
// columns on every content row, and a bottom edge. `total` is the inner content
// width (border outer width = total + frameCols), so the frame stretches/shrinks
// with the inner layout, which itself is sized from the live window dimensions.
func (m model) frame(inner []string, total int) string {
	b := lipgloss.RoundedBorder()

	var sb strings.Builder
	sb.WriteString(m.frameTop(b, total))
	sb.WriteByte('\n')

	side := borderStyle.Render(b.Left)
	for _, row := range inner {
		sb.WriteString(side)
		sb.WriteString(padRight(row, total)) // guarantee exact inner width
		sb.WriteString(borderStyle.Render(b.Right))
		sb.WriteByte('\n')
	}

	// Bottom edge: corner + dashes + corner.
	bottom := b.BottomLeft + strings.Repeat(b.Bottom, total) + b.BottomRight
	sb.WriteString(borderStyle.Render(bottom))
	return sb.String()
}

// frameTop composes the top border edge with the program title centered on it:
//
//	╭──── MorgTweaker v1.0.0 ────╮
//
// It overlays the title onto the dashed top edge by splitting the available dash
// run into left/right halves around the centered title text, so it always matches
// the live border width and the rounded corner/dash runes.
func (m model) frameTop(b lipgloss.Border, total int) string {
	version := m.version
	if version == "" {
		version = "dev"
	}
	title := " " + T(m.lang, kAppTitle) + " v" + version + " "

	// Clamp the title if the terminal is too narrow to fit it on the edge.
	if lipgloss.Width(title) > total {
		title = truncDisplay(title, total)
	}
	dashes := total - lipgloss.Width(title)
	left := dashes / 2
	right := dashes - left

	// Render the border runes (lime) and the title (lime/bold) as separate styled
	// fragments and concatenate — do NOT wrap the whole edge in one Render, which
	// would nest ANSI resets and bleed the title color into the dashes.
	borderLeft := borderStyle.Render(b.TopLeft + strings.Repeat(b.Top, left))
	borderRight := borderStyle.Render(strings.Repeat(b.Top, right) + b.TopRight)
	return borderLeft + titleStyle.Render(title) + borderRight
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

// --- universal bottom button bar ---------------------------------------------

// button-bar action identifiers (the click target a bar zone maps to).
const (
	btnApply = iota
	btnRollback
	btnLang
	btnQuit
)

// buttonZone is one clickable footer button: its action id and its inner-column
// span [start, end). Computed deterministically by buttonZones so the renderer and
// the mouse hit-test agree on positions without storing layout on the model.
type buttonZone struct {
	id         int
	label      string
	start, end int // inner columns (0-based, excluding the left border)
}

// buttonZones lays out the footer buttons left→right with a fixed gap, returning
// each one's label and inner-column span. A single leading pad cell matches the
// status bar's " " indent.
func (m model) buttonZones() []buttonZone {
	ids := []int{btnApply, btnRollback, btnLang, btnQuit}
	keys := map[int]Key{
		btnApply:    kBtnApply,
		btnRollback: kBtnRollback,
		btnLang:     kBtnLang,
		btnQuit:     kBtnQuit,
	}
	const pad = 1 // leading indent
	const gap = 1 // space between buttons
	zones := make([]buttonZone, 0, len(ids))
	x := pad
	for _, id := range ids {
		label := " " + T(m.lang, keys[id]) + " " // brackets-free; styled fill marks it
		w := lipgloss.Width(label)
		zones = append(zones, buttonZone{id: id, label: label, start: x, end: x + w})
		x += w + gap
	}
	return zones
}

// buttonBar renders the footer row: each zone's label drawn at its span, the
// primary action (Apply) in the loud lime fill, the rest in lime text.
func (m model) buttonBar(total int) string {
	var sb strings.Builder
	sb.WriteString(" ") // leading pad (matches zone start = 1)
	for i, z := range m.buttonZones() {
		if i > 0 {
			sb.WriteString(" ") // inter-button gap
		}
		style := btnStyle
		if z.id == btnApply {
			style = btnPrimaryStyle
		}
		sb.WriteString(style.Render(z.label))
	}
	return padRight(sb.String(), total)
}

// buttonAtX maps an inner-X coordinate (already border-stripped) on the button-bar
// row to a button action id; ok=false if the click missed every zone.
func (m model) buttonAtX(innerX int) (id int, ok bool) {
	for _, z := range m.buttonZones() {
		if innerX >= z.start && innerX < z.end {
			return z.id, true
		}
	}
	return 0, false
}

// buttonRowY is the SCREEN row of the button bar: top border + pane region +
// status bar. (Bottom border is the row after it.)
func (m model) buttonRowY() int {
	return frameBorderY + m.paneViewH() + statusRows
}

// countOn returns how many tweaks across the whole catalog are currently ON,
// reading ONLY the cached statuses (no I/O).
func (m model) countOn() int {
	n := 0
	for _, tw := range m.catalog.Leaves() {
		if m.statusOf(tw.ID).IsOn() {
			n++
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

// paneAtX maps a screen X coordinate to a pane by the fixed left-width boundary.
// The 1-cell left border shifts content right by frameBorderX, so strip it first.
// Inner column layout (lw = leftWidth): left content [0, lw-1), left scrollbar at
// lw-1, divider at lw, right content [lw+1, …). So inner-x < lw is the left pane
// (including its scrollbar column); inner-x >= lw is the right pane.
func (m model) paneAtX(x int) pane {
	if x-frameBorderX < m.leftWidth() {
		return paneLeft
	}
	return paneRight
}

// Border-induced offset of inner content from the screen origin: the top edge
// occupies screen row 0 (panes start at row 1) and the left edge occupies column 0.
const (
	frameBorderX = 1 // left border column
	frameBorderY = 1 // top border edge
)

// rowAtClick maps a screen click (x,y) to (pane, rowIndex). ok=false outside the
// pane region or past the last row. Strips the border offset, then uses each
// pane's own clamped scroll offset and the same body length the renderer drew.
func (m model) rowAtClick(x, y int) (p pane, idx int, ok bool) {
	viewH := m.paneViewH()
	ry := y - frameBorderY // inner row: pane region is inner rows 0..viewH-1
	if ry < 0 || ry >= viewH {
		return 0, 0, false
	}
	p = m.paneAtX(x)
	var total, scroll int
	switch p {
	case paneLeft:
		total, scroll = len(m.catalog), m.catScroll
	case paneRight:
		total, scroll = len(m.visibleRows()), m.twScroll
	}
	off := clampScroll(scroll, total, viewH)
	idx = off + ry
	if idx < 0 || idx >= total {
		return p, 0, false
	}
	return p, idx, true
}
