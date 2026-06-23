package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

// TestButtonBarRendersAllLabels: the footer shows every button label, localized.
func TestButtonBarRendersAllLabels(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.w, m.h = 100, 24
	bar := m.buttonBar(m.innerW())
	for _, k := range []Key{kBtnApply, kBtnRollback, kBtnLang, kBtnQuit} {
		if !strings.Contains(bar, T(m.lang, k)) {
			t.Errorf("button bar missing label %q", T(m.lang, k))
		}
	}
	if w := lipgloss.Width(bar); w != m.innerW() {
		t.Errorf("button bar width = %d, want innerW %d", w, m.innerW())
	}
}

// TestButtonAtXMapsZones: each button label's columns map back to its action id.
func TestButtonAtXMapsZones(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	for _, z := range m.buttonZones() {
		mid := (z.start + z.end) / 2
		id, ok := m.buttonAtX(mid)
		if !ok || id != z.id {
			t.Errorf("buttonAtX(%d) = (%d,%v), want (%d,true)", mid, id, ok, z.id)
		}
	}
	// A click far past the last button hits nothing.
	if _, ok := m.buttonAtX(10000); ok {
		t.Error("buttonAtX past the last zone should miss")
	}
}

// TestButtonClickAppliesSelected: a left-click on the [Apply] zone of the footer
// row dispatches the apply-selected batch for a checked appliable row.
func TestButtonClickAppliesSelected(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.w, m.h = 100, 24
	m.statuses["prep.x"] = core.StatusOff
	m.selected["prep.x"] = true

	zones := m.buttonZones()
	var applyZone buttonZone
	for _, z := range zones {
		if z.id == btnApply {
			applyZone = z
		}
	}
	// onButton is the same path the click handler calls after hit-testing.
	gm, cmd := m.onButton(applyZone.id)
	got := gm.(model)
	if cmd == nil {
		t.Fatal("clicking [Apply] with a checked appliable row should dispatch")
	}
	if got.batchKind != batchApply {
		t.Errorf("clicking [Apply] should start an apply batch, got batchKind=%d", got.batchKind)
	}
}

// TestApplyBatchSequential: a two-item apply batch dispatches one at a time — the
// first ApplyDoneMsg clears that row's checkbox and advances to the second.
func TestApplyBatchSequential(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.statuses["prep.x"] = core.StatusOff
	m.statuses["prep.y"] = core.StatusOff
	m.selected["prep.x"] = true
	m.selected["prep.y"] = true

	gm, cmd := m.applySelected()
	if cmd == nil || gm.batchKind != batchApply {
		t.Fatalf("applySelected should start a batch, got cmd=%v kind=%d", cmd != nil, gm.batchKind)
	}
	// Exactly one item is in flight; the other is still queued.
	if len(gm.batchQueue) != 1 {
		t.Errorf("one item should remain queued, got %d", len(gm.batchQueue))
	}

	// First item completes successfully → checkbox clears, batch advances.
	out, cmd2 := gm.Update(engine.ApplyDoneMsg{ID: "prep.x", Status: core.StatusOn})
	got := out.(model)
	if got.selected["prep.x"] {
		t.Error("successful apply should clear the row's checkbox")
	}
	if cmd2 == nil {
		t.Error("batch should advance to the second checked item (non-nil Cmd)")
	}
	if len(got.batchQueue) != 0 {
		t.Errorf("queue should be drained after dispatching the second item, got %d", len(got.batchQueue))
	}

	// Second item completes → batch ends.
	out2, _ := got.Update(engine.ApplyDoneMsg{ID: "prep.y", Status: core.StatusOn})
	if out2.(model).batchKind != batchNone {
		t.Error("batch should be finished after the last item")
	}
}
