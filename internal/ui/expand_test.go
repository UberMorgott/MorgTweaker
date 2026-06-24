package ui

import (
	"testing"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

// Space/Enter on a parent now CHECKS it (selects all actionable children); the
// parent itself never gets a selected entry. Expansion moved to the arrow keys.
func TestEnterChecksParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	m.activePane = paneRight
	m.twCursor = 1 // the parent row (row 0 is prep.leaf)
	out, _ := m.toggleCurrent()
	gm := out.(model)
	if !gm.selected["prep.group.a"] || !gm.selected["prep.group.b"] {
		t.Fatal("toggleCurrent on a parent must check all its children")
	}
	if gm.selected["prep.group"] {
		t.Fatal("a parent must never get its own selected entry")
	}
	if gm.expanded["prep.group"] {
		t.Fatal("toggleCurrent on a parent must NOT expand it (expand is arrow-only)")
	}
	// toggling again clears all children.
	gm.twCursor = 1
	out2, _ := gm.toggleCurrent()
	g2 := out2.(model)
	if g2.selected["prep.group.a"] || g2.selected["prep.group.b"] {
		t.Fatal("second toggle must clear all children")
	}
}

// Expansion is now driven by the arrow keys, not Space/Enter.
func TestArrowExpandsParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.activePane = paneRight
	m.twCursor = 1
	out, _ := m.onKey(keyPress("right"))
	gm := out.(model)
	if !gm.expanded["prep.group"] {
		t.Fatal("arrow right must expand a collapsed parent")
	}
	gm.twCursor = 1
	out2, _ := gm.onKey(keyPress("left"))
	if out2.(model).expanded["prep.group"] {
		t.Fatal("arrow left must collapse an expanded parent")
	}
}

// The apply queue is driven entirely by per-child selection now (no parent entry).
func TestApplySelectedChildrenDriveQueue(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	// children selected directly; both appliable (Off).
	m.selected["prep.group.a"] = true
	m.selected["prep.group.b"] = true
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	ids := m.applyQueueIDs()
	want := map[string]bool{"prep.group.a": true, "prep.group.b": true}
	if len(ids) != 2 || !want[ids[0]] || !want[ids[1]] {
		t.Fatalf("queue = %v, want the two children", ids)
	}
	// the parent's own ID must never be queued (it has no actions).
	for _, id := range ids {
		if id == "prep.group" {
			t.Fatal("parent id must not be in the apply queue")
		}
	}
}
