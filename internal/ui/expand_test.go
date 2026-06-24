package ui

import (
	"testing"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

func TestEnterExpandsParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	m.activePane = paneRight
	m.twCursor = 1 // the parent row (row 0 is prep.leaf)
	out, _ := m.toggleCurrent()
	gm := out.(model)
	if !gm.expanded["prep.group"] {
		t.Fatal("toggleCurrent on a parent must expand it")
	}
	if gm.selected["prep.group"] {
		t.Fatal("expanding a parent must NOT check it")
	}
	// toggling again collapses.
	gm.twCursor = 1
	out2, _ := gm.toggleCurrent()
	if out2.(model).expanded["prep.group"] {
		t.Fatal("second toggle must collapse")
	}
}

func TestApplySelectedExpandsParentToChildren(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	// parent checked; children appliable (Off).
	m.selected["prep.group"] = true
	m.statuses["prep.group.a"] = core.StatusOff
	m.statuses["prep.group.b"] = core.StatusOff
	ids := m.applyQueueIDs() // helper the impl exposes for testing the expansion
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
