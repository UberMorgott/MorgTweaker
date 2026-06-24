package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// checkboxCol returns the DISPLAY column (rune index) of the first '[' in a
// rendered row, after stripping ANSI styling. The caret '▾' is multi-byte, so a
// rune index (not a byte index) is required to compare columns.
func checkboxCol(row string) int {
	for i, r := range []rune(ansiRE.ReplaceAllString(row, "")) {
		if r == '[' {
			return i
		}
	}
	return -1
}

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	default:
		return tea.KeyPressMsg{Text: s, Code: []rune(s)[0]}
	}
}

// parentRow returns the rendered right-pane row string for prep.group (the parent
// is row index 1: row 0 is prep.leaf). The fixture is built with both children
// appliable unless overridden by the caller before this is invoked.
func parentRow(m model) string {
	return m.rightBody(60)[1]
}

func TestParentGlyphAllSelected(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	m.selected["prep.group.a"] = true
	m.selected["prep.group.b"] = true
	row := parentRow(m)
	if !strings.Contains(row, glyphOn) {
		t.Fatalf("all children selected: want %q in row, got %q", glyphOn, row)
	}
	if strings.Contains(row, "(*)") {
		t.Fatalf("all selected must NOT show (*): %q", row)
	}
}

func TestParentGlyphNoneSelected(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	row := parentRow(m)
	if !strings.Contains(row, glyphOff) {
		t.Fatalf("none selected: want %q in row, got %q", glyphOff, row)
	}
	if strings.Contains(row, "(*)") {
		t.Fatalf("none selected must NOT show (*): %q", row)
	}
}

func TestParentGlyphMixedSelected(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	m.selected["prep.group.a"] = true // one of two
	row := parentRow(m)
	if !strings.Contains(row, glyphPartial) {
		t.Fatalf("mixed: want %q in row, got %q", glyphPartial, row)
	}
	if !strings.Contains(row, "(*)") {
		t.Fatalf("mixed must show (*): %q", row)
	}
}

func TestParentGlyphZeroActionableChildren(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	// Both children blocked → no actionable children.
	m.statuses["prep.group.a"] = core.StatusBlocked
	m.statuses["prep.group.b"] = core.StatusBlocked
	row := parentRow(m)
	if !strings.Contains(row, glyphOff) {
		t.Fatalf("zero actionable: want %q in row, got %q", glyphOff, row)
	}
	if strings.Contains(row, "(*)") {
		t.Fatalf("zero actionable must NOT show (*): %q", row)
	}
}

func TestToggleParentNoneToAll(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	m.activePane = paneRight
	m.twCursor = 1 // parent
	out, _ := m.toggleCurrent()
	gm := out.(model)
	if !gm.selected["prep.group.a"] || !gm.selected["prep.group.b"] {
		t.Fatalf("none->toggle must select all children: %v", gm.selected)
	}
	if gm.selected["prep.group"] {
		t.Fatal("parent must have no own selected entry")
	}
}

func TestToggleParentAllToNone(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	m.selected["prep.group.a"] = true
	m.selected["prep.group.b"] = true
	m.activePane = paneRight
	m.twCursor = 1
	out, _ := m.toggleCurrent()
	gm := out.(model)
	if gm.selected["prep.group.a"] || gm.selected["prep.group.b"] {
		t.Fatalf("all->toggle must clear all children: %v", gm.selected)
	}
}

func TestToggleParentMixedToAll(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	m.selected["prep.group.a"] = true // mixed
	m.activePane = paneRight
	m.twCursor = 1
	out, _ := m.toggleCurrent()
	gm := out.(model)
	if !gm.selected["prep.group.a"] || !gm.selected["prep.group.b"] {
		t.Fatalf("mixed->toggle must select all children: %v", gm.selected)
	}
}

func TestToggleParentZeroActionableNoOp(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusBlocked
	m.statuses["prep.group.b"] = core.StatusBlocked
	m.activePane = paneRight
	m.twCursor = 1
	out, _ := m.toggleCurrent()
	gm := out.(model)
	if gm.selected["prep.group.a"] || gm.selected["prep.group.b"] {
		t.Fatalf("zero actionable toggle must be no-op: %v", gm.selected)
	}
}

func TestArrowRightExpandsCollapsedParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.activePane = paneRight
	m.twCursor = 1 // parent, collapsed
	out, _ := m.onKey(keyPress("right"))
	gm := out.(model)
	if !gm.expanded["prep.group"] {
		t.Fatal("arrow right on a collapsed parent must expand it")
	}
}

func TestArrowLeftCollapsesExpandedParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.activePane = paneRight
	m.twCursor = 1
	m.expanded["prep.group"] = true
	out, _ := m.onKey(keyPress("left"))
	gm := out.(model)
	if gm.expanded["prep.group"] {
		t.Fatal("arrow left on an expanded parent must collapse it")
	}
}

func TestArrowRightOnNonParentFocusesRight(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.activePane = paneLeft
	m.twCursor = 0 // prep.leaf (right pane row), but active pane is left
	out, _ := m.onKey(keyPress("right"))
	gm := out.(model)
	if gm.activePane != paneRight {
		t.Fatalf("arrow right must focus the right pane, got %v", gm.activePane)
	}
	// And it must not panic / expand anything.
	if gm.expanded["prep.leaf"] {
		t.Fatal("arrow right on a leaf must not set expansion")
	}
}

// parentRowY returns the screen Y of the parent row (index 1, scroll 0) for a
// sized model: pane region starts at frameBorderY.
func sizedRedist() model {
	m := New(redistFixture(), engine.New(nil))
	m.w, m.h = 80, 24
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	return m
}

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func TestCaretClickExpandsParent(t *testing.T) {
	m := sizedRedist()
	y := frameBorderY + 1 // parent is right-pane row index 1, scroll 0
	// Caret cell inner-x = leftWidth + dividerW (right content start). Screen x adds
	// the left border. The caret occupies the checkbox-width span before the box.
	caretX := frameBorderX + m.leftWidth() + dividerW
	out, _ := m.Update(click(caretX, y))
	gm := out.(model)
	if !gm.expanded["prep.group"] {
		t.Fatalf("caret-cell click must expand the parent (clicked x=%d)", caretX)
	}
	if gm.selected["prep.group.a"] || gm.selected["prep.group.b"] {
		t.Fatal("caret click must not toggle children")
	}
}

func TestBodyClickChecksParent(t *testing.T) {
	m := sizedRedist()
	y := frameBorderY + 1
	// Click on the name, well past the caret + checkbox span.
	bodyX := frameBorderX + m.leftWidth() + dividerW + len(glyphOff) + 6
	out, _ := m.Update(click(bodyX, y))
	gm := out.(model)
	if !gm.selected["prep.group.a"] || !gm.selected["prep.group.b"] {
		t.Fatalf("body click must toggle-check all children: %v", gm.selected)
	}
	if gm.expanded["prep.group"] {
		t.Fatal("body click must not expand the parent")
	}
}

func TestChildCheckboxAlignedUnderParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	m.expanded["prep.group"] = true
	rows := m.rightBody(60)
	// rows[1] = parent, rows[2] = first child (row 0 is prep.leaf).
	pCol := checkboxCol(rows[1])
	cCol := checkboxCol(rows[2])
	if pCol < 0 || cCol < 0 {
		t.Fatalf("missing checkbox: parent col=%d child col=%d", pCol, cCol)
	}
	if pCol != cCol {
		t.Fatalf("child checkbox col %d must align under parent checkbox col %d", cCol, pCol)
	}
}

func TestArrowRightExpandedParentKeepsRight(t *testing.T) {
	// Arrow right on an already-expanded parent does not collapse; just stays right.
	m := New(redistFixture(), engine.New(nil))
	m.activePane = paneRight
	m.twCursor = 1
	m.expanded["prep.group"] = true
	out, _ := m.onKey(keyPress("right"))
	gm := out.(model)
	if !gm.expanded["prep.group"] {
		t.Fatal("arrow right on an expanded parent must not collapse it")
	}
	if gm.activePane != paneRight {
		t.Fatalf("active pane should stay right, got %v", gm.activePane)
	}
}
