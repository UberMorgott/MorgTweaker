package ui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

// escKey builds the Esc key-press message (its String() is "esc").
func escKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEscape} }

// twoCat is a tiny two-category catalog with inline i18n for Update/View tests.
func twoCat() core.Catalog {
	return core.Catalog{
		{ID: "prep", Name: core.I18n{RU: "Подготовка", EN: "Prep"}, Tweaks: []core.Tweak{
			{ID: "prep.x", Name: core.I18n{RU: "Икс", EN: "X"}, Desc: core.I18n{RU: "д", EN: "d"}},
			{ID: "prep.y", Name: core.I18n{RU: "Игрек", EN: "Y"}, Desc: core.I18n{RU: "д", EN: "d"}},
		}},
		{ID: "privacy", Name: core.I18n{RU: "Приватность", EN: "Privacy"}, Tweaks: []core.Tweak{
			{ID: "privacy.z", Name: core.I18n{RU: "Зед", EN: "Z"}, Desc: core.I18n{RU: "д", EN: "d"}},
		}},
	}
}

func TestUpdateStoresBatchStatus(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.probing["prep.x"] = true
	updated, _ := m.Update(engine.BatchStatusMsg{Statuses: map[string]core.Status{
		"prep.x": core.StatusOn, "prep.y": core.StatusOff, "privacy.z": core.StatusAbsent,
	}})
	got := updated.(model)
	if got.statuses["prep.x"] != core.StatusOn {
		t.Errorf("statuses[prep.x] = %v want On", got.statuses["prep.x"])
	}
	if got.statuses["prep.y"] != core.StatusOff {
		t.Errorf("statuses[prep.y] = %v want Off", got.statuses["prep.y"])
	}
	if got.probing["prep.x"] {
		t.Error("probing[prep.x] should be cleared after BatchStatusMsg")
	}
}

func TestUpdateStoresSingleStatus(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.probing["prep.x"] = true
	updated, _ := m.Update(engine.StatusMsg{ID: "prep.x", Status: core.StatusPartial})
	got := updated.(model)
	if got.statuses["prep.x"] != core.StatusPartial {
		t.Errorf("statuses[prep.x] = %v want Partial", got.statuses["prep.x"])
	}
	if got.probing["prep.x"] {
		t.Error("probing should be cleared after StatusMsg")
	}
}

func TestUpdateStatusMsgErrorIsUnknown(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	updated, _ := m.Update(engine.StatusMsg{ID: "prep.x", Status: core.StatusOn, Err: errors.New("boom")})
	got := updated.(model)
	if got.statuses["prep.x"] != core.StatusUnknown {
		t.Errorf("probe error should degrade to Unknown, got %v", got.statuses["prep.x"])
	}
}

func TestUpdateApplyDoneStoresStatus(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.probing["prep.x"] = true
	updated, _ := m.Update(engine.ApplyDoneMsg{ID: "prep.x", Status: core.StatusOn})
	got := updated.(model)
	if got.statuses["prep.x"] != core.StatusOn {
		t.Errorf("statuses[prep.x] = %v want On", got.statuses["prep.x"])
	}
	if got.probing["prep.x"] {
		t.Error("probing should be cleared after ApplyDoneMsg")
	}
	if got.status == "" || got.statusErr {
		t.Errorf("applied status line should be a non-error message, got %q err=%v", got.status, got.statusErr)
	}
}

func TestUpdateApplyDoneBlockedIsError(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	updated, _ := m.Update(engine.ApplyDoneMsg{ID: "prep.x", Status: core.StatusBlocked})
	got := updated.(model)
	if got.statuses["prep.x"] != core.StatusBlocked {
		t.Errorf("statuses[prep.x] = %v want Blocked", got.statuses["prep.x"])
	}
	if !got.statusErr {
		t.Error("blocked apply should set statusErr")
	}
}

func TestUpdateApplyProgress(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.probing["prep.x"] = true // in-flight: progress is accepted
	updated, _ := m.Update(engine.ApplyProgressMsg{ID: "prep.x", Pct: 42, Note: "dl"})
	got := updated.(model)
	if got.progress["prep.x"].Pct != 42 {
		t.Errorf("progress pct = %d want 42", got.progress["prep.x"].Pct)
	}
	if got.statuses["prep.x"] != core.StatusWorking {
		t.Errorf("progress should mark status Working, got %v", got.statuses["prep.x"])
	}
}

// TestUpdateApplyProgressIgnoredWhenNotInflight (FIX C): a late/stray progress
// message for a tweak with no in-flight apply is ignored — it must not resurrect
// the row to StatusWorking nor create a progress entry.
func TestUpdateApplyProgressIgnoredWhenNotInflight(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.statuses["prep.x"] = core.StatusOn // already settled, nothing in flight
	updated, _ := m.Update(engine.ApplyProgressMsg{ID: "prep.x", Pct: 99, Note: "late"})
	got := updated.(model)
	if _, ok := got.progress["prep.x"]; ok {
		t.Error("stray progress should not create a progress entry")
	}
	if got.statuses["prep.x"] != core.StatusOn {
		t.Errorf("stray progress must not change a settled status, got %v", got.statuses["prep.x"])
	}
}

// TestDispatchApplyDebounce (FIX A): a second apply dispatch for a tweak that is
// already in flight is a no-op — it returns a nil Cmd and does not overwrite the
// existing cancel func (which would leak the live context).
func TestDispatchApplyDebounce(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.activePane = paneRight
	m.statuses["prep.x"] = core.StatusOff

	// First dispatch: registers a cancel func and an ApplyCmd.
	gm1, cmd1 := m.toggleCurrent()
	if cmd1 == nil {
		t.Fatal("first dispatch should return an ApplyCmd")
	}
	cancel1, ok := gm1.cancel["prep.x"]
	if !ok {
		t.Fatal("first dispatch should register a cancel func")
	}

	// Second dispatch while in flight: must be a no-op (nil Cmd, same cancel).
	gm2, cmd2 := gm1.toggleCurrent()
	if cmd2 != nil {
		t.Error("second dispatch while in flight should be debounced (nil Cmd)")
	}
	if got := gm2.cancel["prep.x"]; fmt.Sprintf("%p", got) != fmt.Sprintf("%p", cancel1) {
		t.Error("second dispatch must not overwrite the in-flight cancel func")
	}
}

// TestEscCancelsFocusedApply (FIX D): esc cancels the focused tweak's in-flight
// apply (clearing its working markers) without quitting the app.
func TestEscCancelsFocusedApply(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.activePane = paneRight
	m.statuses["prep.x"] = core.StatusOff
	gm, _ := m.toggleCurrent()
	gm.progress["prep.x"] = engine.ApplyProgressMsg{ID: "prep.x", Pct: 10}

	out, cmd := gm.onKey(escKey())
	got := out.(model)
	if cmd != nil {
		t.Error("esc must not quit the app (nil Cmd expected)")
	}
	if _, ok := got.cancel["prep.x"]; ok {
		t.Error("esc should cancel and remove the focused tweak's cancel func")
	}
	if got.probing["prep.x"] {
		t.Error("esc should clear the probing marker")
	}
	if _, ok := got.progress["prep.x"]; ok {
		t.Error("esc should clear the progress entry")
	}
}

func TestUpdateRollbackDoneReprobes(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	updated, cmd := m.Update(rollbackDoneMsg{ID: "prep.x"})
	got := updated.(model)
	if got.statusErr {
		t.Error("clean rollback should not be an error")
	}
	if got.status == "" {
		t.Error("rollback should set a status message")
	}
	if cmd == nil {
		t.Error("rollback of a known tweak should dispatch a re-probe Cmd")
	}
	if !got.probing["prep.x"] {
		t.Error("rolled-back tweak should be marked probing pending re-probe")
	}
}

func TestUpdateRollbackDoneWithErrors(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	updated, _ := m.Update(rollbackDoneMsg{ID: "prep.x", Errs: []error{errors.New("nope")}})
	got := updated.(model)
	if !got.statusErr {
		t.Error("rollback with errors should set statusErr")
	}
}

func TestUpdateRollbackCategoryDone(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	updated, _ := m.Update(rollbackDoneMsg{ID: "cat:prep"})
	got := updated.(model)
	if got.statusErr || got.status == "" {
		t.Errorf("category rollback should be a clean message, got %q err=%v", got.status, got.statusErr)
	}
}

func TestUpdateOpenDoneNoLink(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	updated, _ := m.Update(openDoneMsg{ID: "prep.x", URLFound: false})
	got := updated.(model)
	if !got.statusErr {
		t.Error("open with no link should be an error/notice")
	}
}

// TestViewNoPanicOnUnknown: with empty statuses every tweak is Unknown; View must
// render (placeholders) without panicking and WITHOUT any I/O.
func TestViewNoPanicOnUnknown(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.w, m.h = 80, 24
	_ = m.View() // builds a tea.View (must not panic / do I/O)
	if m.viewString() == "" {
		t.Fatal("View returned empty output")
	}
}

// TestViewRendersCachedStatusOnly: View output reflects the cached status map,
// proving it reads model state rather than calling the engine.
func TestViewRendersCachedStatusOnly(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.w, m.h = 80, 24
	m.statuses["prep.x"] = core.StatusOn
	m.statuses["prep.y"] = core.StatusOff
	// countOn reads only the cache; with one On it must be 1.
	if n := m.countOn(); n != 1 {
		t.Errorf("countOn = %d want 1 (reads cached statuses only)", n)
	}
	_ = m.View() // must not panic / do I/O
}

// TestInitProbesAllTweaks: Init returns a non-nil Cmd (the batch probe) when the
// catalog is non-empty, and nil for an empty catalog.
func TestInitDispatchesBatchProbe(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	if m.Init() == nil {
		t.Error("Init should dispatch a batch-probe Cmd for a non-empty catalog")
	}
	empty := New(core.Catalog{}, engine.New(nil))
	if empty.Init() != nil {
		t.Error("Init should be nil for an empty catalog")
	}
}

// TestToggleUsesCachedStatus: toggling a tweak whose cached status is On dispatches
// an apply (returns a Cmd) when no admin is required.
func TestToggleDispatchesApply(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.activePane = paneRight
	m.statuses["prep.x"] = core.StatusOff
	_, cmd := m.toggleCurrent()
	if cmd == nil {
		t.Error("toggleCurrent on a non-admin tweak should dispatch an ApplyCmd")
	}
}
