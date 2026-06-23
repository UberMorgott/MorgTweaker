package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

// dlAction is a stub action used to give a tweak a download_install-like row in
// tests. It is never actually run (tests drive the model via synthetic messages),
// so its methods are honest no-ops.
type dlAction struct{}

func (dlAction) Level() core.Elevation                { return core.ElevAdmin }
func (dlAction) Apply(core.ActionContext, bool) error { return nil }
func (dlAction) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{}, nil
}
func (dlAction) Restore(core.ActionContext, core.Backup) error     { return nil }
func (dlAction) Probe(core.ActionContext) (core.PointState, error) { return core.PointOff, nil }

// progCat is a small catalog with three appliable-by-default rows so apply
// batches of size 1 and >1 are easy to construct.
func progCat() core.Catalog {
	return core.Catalog{
		{ID: "c", Name: core.I18n{RU: "К", EN: "C"}, Tweaks: []core.Tweak{
			{ID: "c.a", Name: core.I18n{RU: "А", EN: "A"}, Desc: core.I18n{RU: "д", EN: "d"}, Actions: []core.Action{dlAction{}}},
			{ID: "c.b", Name: core.I18n{RU: "Б", EN: "B"}, Desc: core.I18n{RU: "д", EN: "d"}, Actions: []core.Action{dlAction{}}},
			{ID: "c.c", Name: core.I18n{RU: "В", EN: "C2"}, Desc: core.I18n{RU: "д", EN: "d"}, Actions: []core.Action{dlAction{}}},
		}},
	}
}

// TestApplyEntersProgressScreen: applySelected switches the screen from list to
// progress and records the in-flight tweak.
func TestApplyEntersProgressScreen(t *testing.T) {
	m := New(progCat(), engine.New(nil))
	m.statuses["c.a"] = core.StatusOff
	m.selected["c.a"] = true
	gm, cmd := m.applySelected()
	if cmd == nil {
		t.Fatal("applySelected should dispatch the first item")
	}
	if gm.screen != screenProgress {
		t.Errorf("screen = %v, want screenProgress after applySelected", gm.screen)
	}
	if gm.currentID != "c.a" {
		t.Errorf("currentID = %q, want c.a", gm.currentID)
	}
	if gm.batchTotal != 1 {
		t.Errorf("batchTotal = %d, want 1", gm.batchTotal)
	}
}

// TestBatchDoneReturnsToList: when the last item's ApplyDoneMsg lands, the batch
// drains and the view returns to the list with a summary status.
func TestBatchDoneReturnsToList(t *testing.T) {
	m := New(progCat(), engine.New(nil))
	m.statuses["c.a"] = core.StatusOff
	m.selected["c.a"] = true
	gm, _ := m.applySelected() // batch of 1, c.a in flight, screen=progress

	updated, _ := gm.Update(engine.ApplyDoneMsg{ID: "c.a", Status: core.StatusOn})
	got := updated.(model)
	if got.screen != screenList {
		t.Errorf("screen = %v, want screenList after the batch finished", got.screen)
	}
	if got.batchKind != batchNone {
		t.Errorf("batchKind = %d, want batchNone", got.batchKind)
	}
	if got.status == "" {
		t.Error("returning to the list should leave a one-line summary in the status")
	}
	if got.statusErr {
		t.Errorf("a clean batch summary should not be an error, got %q", got.status)
	}
}

// TestOverallBarHiddenForSingle / shown for many: the overall bar is gated on
// batchTotal > 1 via progressBlocks.
func TestOverallBarVisibility(t *testing.T) {
	// Single-item batch: overall bar hidden.
	m := New(progCat(), engine.New(nil))
	m.screen = screenProgress
	m.batchKind = batchApply
	m.batchTotal = 1
	m.currentID = "c.a"
	if hasOverall(m, 60) {
		t.Error("overall bar must be HIDDEN when the batch has a single tweak")
	}

	// Multi-item batch: overall bar shown.
	m.batchTotal = 3
	m.batchDone = 1
	if !hasOverall(m, 60) {
		t.Error("overall bar must be SHOWN when the batch has more than one tweak")
	}
}

// hasOverall reports whether the rendered progress region contains the localized
// overall label for the model's current batch counters.
func hasOverall(m model, total int) bool {
	region := strings.Join(m.progressRegion(total, 12), "\n")
	want := T(m.lang, kProgOverall) // a format string; match its stable prefix
	prefix := want
	if i := strings.IndexByte(want, '%'); i >= 0 {
		prefix = strings.TrimSpace(want[:i])
	}
	return strings.Contains(ansiStrip(region), prefix)
}

// TestCurrentBarAdvancesOnProgress: a streamed download progress for the in-flight
// tweak drives the current-tweak bar (percent reflected in the caption) and is
// recorded in m.progress.
func TestCurrentBarAdvancesOnProgress(t *testing.T) {
	m := New(progCat(), engine.New(nil))
	m.statuses["c.a"] = core.StatusOff
	m.selected["c.a"] = true
	gm, _ := m.applySelected()
	gm.probing["c.a"] = true // in-flight so progress is accepted

	updated, _ := gm.Update(engine.ApplyProgressMsg{ID: "c.a", Pct: 50, Note: "downloading", Done: 50, Total: 100})
	got := updated.(model)
	if got.progress["c.a"].Pct != 50 {
		t.Errorf("current progress pct = %d, want 50", got.progress["c.a"].Pct)
	}
	region := ansiStrip(strings.Join(got.progressRegion(60, 12), "\n"))
	if !strings.Contains(region, "50%") {
		t.Errorf("current-tweak caption should reflect 50%%, region:\n%s", region)
	}
}

// TestDownloadBarShowsBytesAndPercent: a download progress with byte counts makes
// the situational bar appear with the MB detail and a determinate fill (~half).
func TestDownloadBarShowsBytesAndPercent(t *testing.T) {
	m := New(progCat(), engine.New(nil))
	m.lang = LangEN
	m.statuses["c.a"] = core.StatusOff
	m.selected["c.a"] = true
	gm, _ := m.applySelected()
	gm.probing["c.a"] = true

	const half = 5 << 20 // 5 MiB
	const full = 10 << 20
	updated, _ := gm.Update(engine.ApplyProgressMsg{ID: "c.a", Pct: 50, Note: "downloading", Done: half, Total: full})
	got := updated.(model)

	region := ansiStrip(strings.Join(got.progressRegion(60, 12), "\n"))
	if !strings.Contains(region, T(LangEN, kProgDownloading)) {
		t.Errorf("download stage label missing, region:\n%s", region)
	}
	if !strings.Contains(region, "MB") {
		t.Errorf("download bar should show MB detail, region:\n%s", region)
	}
	// The download bar fill at 50% must be ~half of its width.
	barW := 58 // total(60)-2 indent
	if got := barFraction(0.5, barW); got != barW/2 {
		t.Errorf("barFraction(0.5,%d) = %d, want %d", barW, got, barW/2)
	}
}

// TestDownloadBarHiddenWithoutDownload: with no download/install progress for the
// current tweak, the situational bar is absent (only overall+current may show).
func TestDownloadBarHiddenWithoutDownload(t *testing.T) {
	m := New(progCat(), engine.New(nil))
	m.lang = LangEN
	m.screen = screenProgress
	m.batchKind = batchApply
	m.batchTotal = 1
	m.currentID = "c.a"
	// No m.progress entry → download stage unknown → bar hidden.
	if _, ok := m.downloadBlock(60, 58, " "); ok {
		t.Error("download bar must be hidden when no download/install progress is streaming")
	}
}

// TestEscReturnsToListCancelled: esc during the progress screen aborts the batch,
// returns to the list, and sets the cancelled status.
func TestEscReturnsToListCancelled(t *testing.T) {
	m := New(progCat(), engine.New(nil))
	m.statuses["c.a"] = core.StatusOff
	m.selected["c.a"] = true
	gm, _ := m.applySelected() // screen=progress, c.a in flight w/ a cancel func

	out, cmd := gm.onKey(escKey())
	got := out.(model)
	if cmd != nil {
		t.Error("esc must not quit the app (nil Cmd expected)")
	}
	if got.screen != screenList {
		t.Errorf("esc should return to the list, screen = %v", got.screen)
	}
	if got.batchKind != batchNone {
		t.Errorf("esc should clear the batch, batchKind = %d", got.batchKind)
	}
	if got.status != T(got.lang, kMsgCancelled) {
		t.Errorf("esc status = %q, want %q", got.status, T(got.lang, kMsgCancelled))
	}
	if _, ok := got.cancel["c.a"]; ok {
		t.Error("esc should cancel the in-flight apply")
	}
}

// TestBarMath covers the percent→filled-width mapping incl. 0%, 100%, and clamp.
func TestBarMath(t *testing.T) {
	cases := []struct {
		frac float64
		w    int
		want int
	}{
		{0, 10, 0},
		{1, 10, 10},
		{0.5, 10, 5},
		{-0.3, 10, 0}, // clamp below
		{1.7, 10, 10}, // clamp above
		{0.25, 8, 2},  // rounding
		{0.5, 0, 0},   // zero width
		{0.99, 100, 99},
	}
	for _, c := range cases {
		if got := barFraction(c.frac, c.w); got != c.want {
			t.Errorf("barFraction(%v,%d) = %d, want %d", c.frac, c.w, got, c.want)
		}
	}
	// renderBar must produce exactly w display cells (fill+track) for any in-range frac.
	for _, frac := range []float64{0, 0.5, 1} {
		got := lipgloss.Width(renderBar(frac, 20))
		if got != 20 {
			t.Errorf("renderBar(%v,20) width = %d, want 20", frac, got)
		}
	}
}

// TestProgressRegionGeometry: the region is exactly regionH rows, each padded to
// `total` cells, at small sizes — no panic, no overflow.
func TestProgressRegionGeometry(t *testing.T) {
	sizes := []struct{ total, h int }{
		{20, 1}, {16, 2}, {40, 6}, {60, 10}, {8, 3},
	}
	m := New(progCat(), engine.New(nil))
	m.screen = screenProgress
	m.batchKind = batchApply
	m.batchTotal = 3
	m.batchDone = 1
	m.currentID = "c.a"
	m.progress["c.a"] = engine.ApplyProgressMsg{ID: "c.a", Pct: 40, Note: "downloading", Done: 4 << 20, Total: 10 << 20}
	for _, s := range sizes {
		rows := m.progressRegion(s.total, s.h)
		if len(rows) != s.h {
			t.Errorf("progressRegion(%d,%d) returned %d rows, want %d", s.total, s.h, len(rows), s.h)
		}
		for i, r := range rows {
			if w := lipgloss.Width(r); w != s.total {
				t.Errorf("progressRegion(%d,%d) row %d width = %d, want %d", s.total, s.h, i, w, s.total)
			}
		}
	}
}

// TestProgressViewNoPanicTiny: the whole View renders on the progress screen at a
// tiny terminal without panicking and without I/O.
func TestProgressViewNoPanicTiny(t *testing.T) {
	m := New(progCat(), engine.New(nil))
	m.w, m.h = 10, 6
	m.screen = screenProgress
	m.batchKind = batchApply
	m.batchTotal = 2
	m.currentID = "c.a"
	m.progress["c.a"] = engine.ApplyProgressMsg{ID: "c.a", Pct: 10, Note: "downloading", Done: 1, Total: 100}
	if s := m.viewString(); s == "" {
		t.Fatal("progress view returned empty output")
	}
}
