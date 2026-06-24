package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

// tamperGateStub is a fake Gate that yields a Tamper-style deep-link, modeling the
// Defender tweak's gate when Tamper Protection is ON.
type tamperGateStub struct{}

func (tamperGateStub) Check(core.ActionContext) (bool, core.Status, core.GateAction) {
	return false, core.StatusBlocked, core.GateAction{URL: "windowsdefender://threatsettings"}
}

// noURLGateStub is a fake Gate present on a tweak but yielding NO deep-link (URL
// empty) — e.g. a gate that currently passes; a StatusBlocked here came from
// verify-after / access-denied, not the gate, so the UI must show generic wording.
type noURLGateStub struct{}

func (noURLGateStub) Check(core.ActionContext) (bool, core.Status, core.GateAction) {
	return true, core.StatusOff, core.GateAction{}
}

// blockedCat is a catalog with three blockable rows: one gated with a Tamper
// deep-link, one with a gate that offers no URL, and one with no gate at all.
func blockedCat() core.Catalog {
	return core.Catalog{
		{ID: "prep", Name: core.I18n{RU: "Подготовка", EN: "Prep"}, Tweaks: []core.Tweak{
			{ID: "prep.tamper", Name: core.I18n{RU: "Т", EN: "T"}, Desc: core.I18n{RU: "д", EN: "d"}, Gate: tamperGateStub{}},
			{ID: "prep.nourl", Name: core.I18n{RU: "Н", EN: "N"}, Desc: core.I18n{RU: "д", EN: "d"}, Gate: noURLGateStub{}},
			{ID: "prep.nogate", Name: core.I18n{RU: "Г", EN: "G"}, Desc: core.I18n{RU: "д", EN: "d"}},
		}},
	}
}

// TestBlockedMessageTamperVsGeneric is the BUG-2 fix: a StatusBlocked carrying a
// real Tamper GateAction (URL present) shows the "Tamper Protection / press o"
// wording; a StatusBlocked WITHOUT a gate deep-link (verify-after on vcredist,
// access-denied) shows the generic wording and never advertises 'o'.
func TestBlockedMessageTamperVsGeneric(t *testing.T) {
	cases := []struct {
		id          string
		wantTamper  bool // expect the Tamper deep-link wording
		wantGeneric bool // expect the generic wording
	}{
		{"prep.tamper", true, false}, // gate yields a URL → Tamper wording + 'o'
		{"prep.nourl", false, true},  // gate but no URL → generic, no 'o'
		{"prep.nogate", false, true}, // no gate → generic, no 'o'
	}
	for _, lang := range []Lang{LangEN, LangRU} {
		for _, c := range cases {
			m := New(blockedCat(), engine.New(nil))
			m.lang = lang
			updated, _ := m.Update(engine.ApplyDoneMsg{ID: c.id, Status: core.StatusBlocked})
			got := updated.(model).status
			if !updated.(model).statusErr {
				t.Errorf("[%v %s] blocked must set statusErr", lang, c.id)
			}
			tamperWording := T(lang, kMsgBlocked)
			// The Tamper wording mentions Tamper + the 'o' deep-link; the generic does not.
			mentionsTamper := strings.Contains(got, "Tamper")
			if c.wantTamper {
				if !mentionsTamper {
					t.Errorf("[%v %s] status %q should use Tamper wording %q", lang, c.id, got, tamperWording)
				}
			}
			if c.wantGeneric {
				if mentionsTamper {
					t.Errorf("[%v %s] status %q must NOT mention Tamper Protection (block had no gate deep-link)", lang, c.id, got)
				}
				// generic wording carries no 'press o' / 'Нажми o' hint
				if strings.Contains(strings.ToLower(got), "press o") || strings.Contains(got, "Нажми o") {
					t.Errorf("[%v %s] generic block status %q must NOT advertise the 'o' deep-link", lang, c.id, got)
				}
			}
		}
	}
}

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

// TestApplySelectedDebounce (FIX A, re-pointed): the apply-selected batch path
// dispatches the first checked appliable tweak; a second applySelected while the
// batch is in flight is a no-op (nil Cmd) and does not overwrite the cancel func.
func TestApplySelectedDebounce(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.activePane = paneRight
	m.statuses["prep.x"] = core.StatusOff
	m.selected["prep.x"] = true // checked + appliable → in the batch

	// First batch dispatch: registers a cancel func and an ApplyCmd.
	gm1, cmd1 := m.applySelected()
	if cmd1 == nil {
		t.Fatal("applySelected should dispatch an ApplyCmd for the checked row")
	}
	cancel1, ok := gm1.cancel["prep.x"]
	if !ok {
		t.Fatal("applySelected should register a cancel func")
	}

	// Second applySelected while the batch is running: no-op (nil Cmd, same cancel).
	gm2, cmd2 := gm1.applySelected()
	if cmd2 != nil {
		t.Error("applySelected while a batch is in flight should be debounced (nil Cmd)")
	}
	if got := gm2.cancel["prep.x"]; fmt.Sprintf("%p", got) != fmt.Sprintf("%p", cancel1) {
		t.Error("second applySelected must not overwrite the in-flight cancel func")
	}
}

// TestEscCancelsInflightApply (FIX D, re-pointed): esc cancels the in-flight apply
// (the batch's current item) and clears its working markers without quitting.
func TestEscCancelsInflightApply(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.activePane = paneRight
	m.statuses["prep.x"] = core.StatusOff
	m.selected["prep.x"] = true
	gm, _ := m.applySelected()
	gm.progress["prep.x"] = engine.ApplyProgressMsg{ID: "prep.x", Pct: 10}

	out, cmd := gm.onKey(escKey())
	got := out.(model)
	if cmd != nil {
		t.Error("esc must not quit the app (nil Cmd expected)")
	}
	if _, ok := got.cancel["prep.x"]; ok {
		t.Error("esc should cancel and remove the in-flight tweak's cancel func")
	}
	if got.probing["prep.x"] {
		t.Error("esc should clear the probing marker")
	}
	if _, ok := got.progress["prep.x"]; ok {
		t.Error("esc should clear the progress entry")
	}
}

// TestApplySelectedSkipsNonAppliable: only CHECKED + appliable rows enter the
// batch; a checked row whose status is hard-blocked (never appliable) is ignored
// by apply-selected. NOTE: StatusOn IS appliable now (force-reapply) — see
// TestApplySelectedReappliesCheckedOn below.
func TestApplySelectedSkipsNonAppliable(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.statuses["prep.x"] = core.StatusBlocked // hard-blocked → never appliable
	m.selected["prep.x"] = true
	_, cmd := m.applySelected()
	if cmd != nil {
		t.Error("applySelected must skip checked rows that are not appliable")
	}
}

// TestApplySelectedReappliesCheckedOn (force-reapply): a checked already-applied
// (StatusOn) row IS now appliable, so apply-selected dispatches a batch for it.
func TestApplySelectedReappliesCheckedOn(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.statuses["prep.x"] = core.StatusOn // applied → re-appliable on [Apply]
	m.selected["prep.x"] = true
	_, cmd := m.applySelected()
	if cmd == nil {
		t.Error("applySelected must re-apply a checked already-applied (StatusOn) row")
	}
}

// TestRollbackSelectedDispatches: rollback-selected dispatches a rollback for a
// CHECKED + applied (rollbackable) row.
func TestRollbackSelectedDispatches(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.statuses["prep.x"] = core.StatusOn // applied → rollbackable
	m.selected["prep.x"] = true
	gm, cmd := m.rollbackSelected()
	if cmd == nil {
		t.Fatal("rollbackSelected should dispatch a rollback for the checked applied row")
	}
	if gm.batchKind != batchRollback {
		t.Errorf("rollbackSelected should set batchKind=batchRollback, got %d", gm.batchKind)
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

// TestToggleSelectsOnly: in the select-then-act model, toggleCurrent SELECTS ONLY
// — it flips m.selected and dispatches NOTHING (nil Cmd). A second toggle clears it.
func TestToggleSelectsOnly(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.activePane = paneRight
	m.statuses["prep.x"] = core.StatusOff // appliable → has a checkbox

	got, cmd := m.toggleCurrent()
	if cmd != nil {
		t.Error("toggleCurrent must NOT dispatch (select-only); want nil Cmd")
	}
	if !got.selected["prep.x"] {
		t.Error("toggleCurrent should check (select) the row")
	}

	got2, cmd2 := got.toggleCurrent()
	if cmd2 != nil {
		t.Error("second toggle must also be select-only (nil Cmd)")
	}
	if got2.selected["prep.x"] {
		t.Error("second toggle should uncheck (deselect) the row")
	}
}

// TestToggleSkipsActionlessRows: a hard-blocked / unavailable row has no checkbox,
// so toggleCurrent cannot select it.
func TestToggleSkipsActionlessRows(t *testing.T) {
	m := New(twoCat(), engine.New(nil))
	m.activePane = paneRight
	m.statuses["prep.x"] = core.StatusBlocked
	got, _ := m.toggleCurrent()
	if got.selected["prep.x"] {
		t.Error("a no-action (blocked) row must not be selectable")
	}
}
