package ui

import (
	"testing"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

func redistFixture() core.Catalog {
	return core.Catalog{{ID: "prep", Tweaks: []core.Tweak{
		{ID: "prep.leaf"},
		{ID: "prep.group", Children: []core.Tweak{
			{ID: "prep.group.a"}, {ID: "prep.group.b"},
		}},
	}}}
}

func TestVisibleRowsCollapsedThenExpanded(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	// collapsed: parent shows as a single row, no children.
	rows := m.visibleRows()
	if len(rows) != 2 || rows[1].tw.ID != "prep.group" || rows[1].child {
		t.Fatalf("collapsed rows wrong: %+v", rows)
	}
	// expand the parent.
	m.expanded["prep.group"] = true
	rows = m.visibleRows()
	if len(rows) != 4 {
		t.Fatalf("expanded want 4 rows, got %d", len(rows))
	}
	if rows[2].tw.ID != "prep.group.a" || !rows[2].child {
		t.Fatalf("row 2 should be child a, got %+v", rows[2])
	}
	if rows[3].tw.ID != "prep.group.b" || !rows[3].child {
		t.Fatalf("row 3 should be child b, got %+v", rows[3])
	}
}

func TestRowStatusAggregatesParent(t *testing.T) {
	m := New(redistFixture(), engine.New(nil))
	parent, _ := m.catalog.Find("prep.group")
	// all children On → parent On.
	m.statuses["prep.group.a"] = core.StatusOn
	m.statuses["prep.group.b"] = core.StatusOn
	if got := m.rowStatus(parent); got != core.StatusOn {
		t.Fatalf("all-on aggregate = %v, want On", got)
	}
	// mix → Partial.
	m.statuses["prep.group.b"] = core.StatusOff
	if got := m.rowStatus(parent); got != core.StatusPartial {
		t.Fatalf("mixed aggregate = %v, want Partial", got)
	}
	// all off → Off.
	m.statuses["prep.group.a"] = core.StatusOff
	if got := m.rowStatus(parent); got != core.StatusOff {
		t.Fatalf("all-off aggregate = %v, want Off", got)
	}
	// any unknown → Unknown (still probing).
	delete(m.statuses, "prep.group.a")
	if got := m.rowStatus(parent); got != core.StatusUnknown {
		t.Fatalf("unknown-present aggregate = %v, want Unknown", got)
	}
}
