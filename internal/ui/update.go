package ui

import (
	"context"
	"fmt"
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"morgtweaker/internal/core"
	"morgtweaker/internal/engine"
)

const wheelStep = 3

// rollbackDoneMsg is the result of a local rollback Cmd (the engine has no
// RollbackCmd, so the UI wraps engine.Rollback off the UI goroutine here).
type rollbackDoneMsg struct {
	ID   string
	Errs []error
}

// openDoneMsg is the result of a local open-deep-link Cmd: it runs the focused
// tweak's gate to obtain the deep-link URL (off the UI goroutine, since the gate
// may do I/O), then launches it. Err is non-nil when there was no link or the
// launch failed; URLFound reports whether the gate yielded a link at all.
type openDoneMsg struct {
	ID       string
	URLFound bool
	Err      error
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m = m.clampScrolls()
		return m, nil

	case engine.BatchStatusMsg:
		for id, st := range msg.Statuses {
			m.statuses[id] = st
			delete(m.probing, id)
		}
		return m, nil

	case engine.StatusMsg:
		if msg.Err != nil {
			m.statuses[msg.ID] = core.StatusUnknown
		} else {
			m.statuses[msg.ID] = msg.Status
		}
		delete(m.probing, msg.ID)
		return m, nil

	case engine.ApplyProgressMsg:
		// Guard against a late/stray progress message arriving after the apply has
		// finished (its ApplyDoneMsg already cleared the in-flight markers): ignore
		// it so it cannot resurrect a settled row back to StatusWorking.
		if !m.inflight(msg.ID) {
			return m, nil
		}
		m.progress[msg.ID] = msg
		m.statuses[msg.ID] = core.StatusWorking
		return m, nil

	case engine.ApplyDoneMsg:
		delete(m.progress, msg.ID)
		// The apply has finished, so release its context: CALL the stored cancel
		// func before dropping it (otherwise every apply leaks its context).
		if cancel, ok := m.cancel[msg.ID]; ok {
			cancel()
			delete(m.cancel, msg.ID)
		}
		delete(m.probing, msg.ID)
		m.statuses[msg.ID] = msg.Status
		m = m.reportApply(msg)
		return m, nil

	case rollbackDoneMsg:
		m = m.reportRollback(msg)
		// Re-probe the rolled-back tweak so its row reflects the restored state.
		if tw, ok := m.catalog.Find(msg.ID); ok {
			m.probing[msg.ID] = true
			return m, m.engine.ProbeCmd(tw)
		}
		return m, nil

	case openDoneMsg:
		m = m.reportOpen(msg)
		return m, nil

	case tea.MouseWheelMsg:
		// Wheel scrolls whichever pane the cursor X is over, independent of which
		// pane has keyboard focus.
		mc := msg.Mouse()
		p := m.paneAtX(mc.X)
		var d int
		switch mc.Button {
		case tea.MouseWheelUp:
			d = -wheelStep
		case tea.MouseWheelDown:
			d = wheelStep
		}
		if p == paneLeft {
			m.catScroll = clampScroll(m.catScroll+d, len(m.catalog), m.paneViewH())
		} else {
			m.twScroll = clampScroll(m.twScroll+d, len(m.curTweaks()), m.paneViewH())
		}
		return m, nil

	case tea.MouseClickMsg:
		mc := msg.Mouse()
		if mc.Button != tea.MouseLeft {
			return m, nil
		}
		p, idx, ok := m.rowAtClick(mc.X, mc.Y)
		// A click anywhere in a pane focuses that pane (even if it missed a row).
		m.activePane = p
		if !ok {
			return m, nil
		}
		m.status = ""
		switch p {
		case paneLeft:
			m = m.selectCategory(idx)
			return m, nil
		case paneRight:
			m.twCursor = idx
			return m.toggleCurrent()
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// cancelInflight tears down every still-in-flight apply context so a pending
// ApplyCmd is cleanly cancelled (no leaked contexts on teardown).
func (m model) cancelInflight() {
	for id, cancel := range m.cancel {
		cancel()
		delete(m.cancel, id)
	}
}

// inflight reports whether an apply for the given tweak ID is currently running:
// either its cancel func is registered or it is marked probing/working.
func (m model) inflight(id string) bool {
	if _, ok := m.cancel[id]; ok {
		return true
	}
	return m.probing[id]
}

// cancelTweak cancels an in-flight apply for one tweak (if any) and settles its
// row markers so it is no longer rendered as working. It does NOT quit the app.
// Returns whether anything was cancelled.
func (m model) cancelTweak(id string) bool {
	cancel, ok := m.cancel[id]
	if !ok {
		return false
	}
	cancel()
	delete(m.cancel, id)
	delete(m.probing, id)
	delete(m.progress, id)
	return true
}

func (m model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.cancelInflight()
		return m, tea.Quit
	case "l", "ctrl+l":
		m.lang = Next(m.lang)
		m.status = ""
		return m, nil
	}

	// Empty catalog: nothing below can index safely.
	if len(m.catalog) == 0 {
		return m, nil
	}

	switch msg.String() {
	case "tab", "right":
		m.activePane = paneRight
	case "shift+tab", "left":
		m.activePane = paneLeft

	case "up", "k":
		m = m.moveCursor(-1)
	case "down", "j":
		m = m.moveCursor(1)
	case "pgup":
		m = m.moveCursor(-pageStep(m))
	case "pgdown":
		m = m.moveCursor(pageStep(m))
	case "home", "g":
		m = m.moveCursorTo(0)
	case "end", "G":
		m = m.moveCursorToEnd()

	case " ", "enter":
		if m.activePane == paneLeft {
			// Confirm selection; focus jumps to the right pane.
			m = m.selectCategory(m.catCursor)
			m.activePane = paneRight
		} else {
			return m.toggleCurrent()
		}
	case "a":
		return m.applyFocused()
	case "r":
		return m.rollbackFocused()
	case "R":
		return m.rollbackCategory()
	case "o":
		return m.openAction()
	case "esc":
		// Cancel the FOCUSED tweak's in-flight apply without quitting. A no-op when
		// nothing is in flight under the cursor.
		if tw, ok := m.curTweak(); ok && m.cancelTweak(tw.ID) {
			m.status = ""
		}
		return m, nil
	}
	return m, nil
}

// pageStep is one viewport-height of rows for PgUp/PgDn.
func pageStep(m model) int { return maxi(m.paneViewH()-1, 1) }

// --- navigation -------------------------------------------------------------

// moveCursor moves the active pane's cursor by dir (clamped, no wrap), then
// auto-scrolls that pane and (on the left pane) reselects the category.
func (m model) moveCursor(dir int) model {
	m.status = ""
	if m.activePane == paneLeft {
		n := len(m.catalog)
		if n == 0 {
			return m
		}
		m.catCursor = clampIdx(m.catCursor+dir, n)
		m = m.selectCategory(m.catCursor)
	} else {
		n := len(m.curTweaks())
		if n == 0 {
			return m
		}
		m.twCursor = clampIdx(m.twCursor+dir, n)
	}
	return m.clampScrolls()
}

func (m model) moveCursorTo(idx int) model {
	m.status = ""
	if m.activePane == paneLeft {
		if len(m.catalog) > 0 {
			m.catCursor = clampIdx(idx, len(m.catalog))
			m = m.selectCategory(m.catCursor)
		}
	} else {
		if n := len(m.curTweaks()); n > 0 {
			m.twCursor = clampIdx(idx, n)
		}
	}
	return m.clampScrolls()
}

func (m model) moveCursorToEnd() model {
	if m.activePane == paneLeft {
		return m.moveCursorTo(len(m.catalog) - 1)
	}
	return m.moveCursorTo(len(m.curTweaks()) - 1)
}

// selectCategory sets the left cursor to idx and resets the RIGHT pane to top.
func (m model) selectCategory(idx int) model {
	if idx < 0 || idx >= len(m.catalog) {
		return m
	}
	if idx != m.catCursor {
		m.twCursor = 0
		m.twScroll = 0
	}
	m.catCursor = idx
	return m.clampScrolls()
}

// clampScrolls keeps both pane cursors visible within their own viewport and
// bounds both scroll offsets.
func (m model) clampScrolls() model {
	viewH := m.paneViewH()

	// Left pane.
	if m.catCursor < m.catScroll {
		m.catScroll = m.catCursor
	} else if m.catCursor >= m.catScroll+viewH {
		m.catScroll = m.catCursor - viewH + 1
	}
	m.catScroll = clampScroll(m.catScroll, len(m.catalog), viewH)

	// Right pane.
	nTw := len(m.curTweaks())
	if m.twCursor >= nTw {
		m.twCursor = maxi(nTw-1, 0)
	}
	if m.twCursor < m.twScroll {
		m.twScroll = m.twCursor
	} else if m.twCursor >= m.twScroll+viewH {
		m.twScroll = m.twCursor - viewH + 1
	}
	m.twScroll = clampScroll(m.twScroll, nTw, viewH)
	return m
}

// clampIdx clamps i to [0, n-1] (no wrap).
func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// --- actions (all side effects leave as tea.Cmds) ---------------------------

// toggleCurrent flips the right-pane focused tweak: it applies the OPPOSITE of
// the cached status (unknown → treat as off → apply ON). Returns an ApplyCmd.
func (m model) toggleCurrent() (model, tea.Cmd) {
	tw, ok := m.curTweak()
	if !ok {
		return m, nil
	}
	on := !m.statusOf(tw.ID).IsOn()
	return m.dispatchApply(tw, on)
}

// applyFocused applies the right-pane focused tweak's ON state ('a').
func (m model) applyFocused() (model, tea.Cmd) {
	if m.activePane != paneRight {
		return m, nil
	}
	tw, ok := m.curTweak()
	if !ok {
		return m, nil
	}
	return m.dispatchApply(tw, true)
}

// dispatchApply admin-prechecks, marks the tweak working, and returns the engine
// ApplyCmd. For a streaming action (download_install) the progress sink Sends an
// ApplyProgressMsg back into the loop via the held *tea.Program; the sink runs on
// the engine's worker goroutine, so it must use Program.Send (thread-safe) rather
// than touch the model directly.
func (m model) dispatchApply(tw core.Tweak, on bool) (model, tea.Cmd) {
	// Debounce: if this tweak already has an apply in flight, ignore the new
	// dispatch. Re-dispatching would overwrite m.cancel[id] (leaking the live
	// context) and double-apply, and the first ApplyDoneMsg would then drop the
	// survivor's cancel entry.
	if m.inflight(tw.ID) {
		return m, nil
	}
	if tw.NeedsAdmin() && !m.isAdmin {
		m.status = fmt.Sprintf(T(m.lang, kMsgNeedsAdmin), tweakName(m.lang, tw))
		m.statusErr = true
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel[tw.ID] = cancel
	m.probing[tw.ID] = true
	m.status = ""
	return m, m.engine.ApplyCmd(ctx, tw, on, m.progressSink(tw.ID))
}

// progressSink returns a progress callback that pushes streamed download/install
// progress into the Bubble Tea event loop as an ApplyProgressMsg. It is nil-safe:
// if no program is bound (e.g. a probe-only/test model), it returns nil so the
// engine simply skips progress reporting.
func (m model) progressSink(id string) func(pct int, note string) {
	if m.prog == nil || *m.prog == nil {
		return nil
	}
	p := *m.prog
	return func(pct int, note string) {
		p.Send(engine.ApplyProgressMsg{ID: id, Pct: pct, Note: note})
	}
}

// reportApply turns an ApplyDoneMsg into the status-line message, mirroring the
// v1 status-aware wording (blocked / reboot-pending / applied / failed).
func (m model) reportApply(msg engine.ApplyDoneMsg) model {
	tw, ok := m.catalog.Find(msg.ID)
	name := msg.ID
	if ok {
		name = tweakName(m.lang, tw)
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf(T(m.lang, kMsgFail), name, T(m.lang, kWhatApply), msg.Err)
		m.statusErr = true
		return m
	}
	switch msg.Status {
	case core.StatusBlocked:
		m.status = fmt.Sprintf(T(m.lang, kMsgBlocked), name)
		m.statusErr = true
	case core.StatusRebootPending:
		m.status = fmt.Sprintf(T(m.lang, kMsgRebootPending), name)
		m.statusErr = false
	default:
		verb := T(m.lang, kVerbOff)
		if msg.Status.IsOn() {
			verb = T(m.lang, kVerbOn)
		}
		m.status = fmt.Sprintf(T(m.lang, kMsgApplied), name, verb)
		m.statusErr = false
	}
	return m
}

// rollbackFocused restores the right-pane focused tweak from its backup ('r').
// The engine has no RollbackCmd, so this wraps engine.Rollback in a local Cmd
// that runs off the UI goroutine and reports back a rollbackDoneMsg.
func (m model) rollbackFocused() (model, tea.Cmd) {
	tw, ok := m.curTweak()
	if !ok {
		return m, nil
	}
	m.status = ""
	return m, rollbackCmd(m.engine, tw)
}

// rollbackCategory restores every tweak in the selected category ('R') via one
// local Cmd that rolls back each tweak in turn and aggregates their errors.
func (m model) rollbackCategory() (model, tea.Cmd) {
	c, ok := m.curCategory()
	if !ok {
		return m, nil
	}
	m.status = ""
	return m, rollbackCategoryCmd(m.engine, c)
}

// rollbackCmd wraps engine.Rollback for a single tweak off the UI goroutine.
func rollbackCmd(eng *engine.Engine, tw core.Tweak) tea.Cmd {
	return func() tea.Msg {
		return rollbackDoneMsg{ID: tw.ID, Errs: eng.Rollback(tw)}
	}
}

// rollbackCategoryCmd rolls back every tweak in a category, aggregating errors.
// The result ID is the category ID so reportRollback labels it as a category.
func rollbackCategoryCmd(eng *engine.Engine, c core.Category) tea.Cmd {
	return func() tea.Msg {
		var errs []error
		for _, tw := range c.Tweaks {
			errs = append(errs, eng.Rollback(tw)...)
		}
		return rollbackDoneMsg{ID: "cat:" + c.ID, Errs: errs}
	}
}

// reportRollback turns a rollbackDoneMsg into the status-line message.
func (m model) reportRollback(msg rollbackDoneMsg) model {
	// Category rollback carries a "cat:<id>" sentinel ID.
	if catID, isCat := categoryID(msg.ID); isCat {
		label := catID
		for _, c := range m.catalog {
			if c.ID == catID {
				label = catName(m.lang, c)
				break
			}
		}
		if len(msg.Errs) > 0 {
			m.status = fmt.Sprintf(T(m.lang, kMsgSecErrors), label, len(msg.Errs), msg.Errs[0])
			m.statusErr = true
			return m
		}
		m.status = fmt.Sprintf(T(m.lang, kMsgSecRolledBack), label)
		m.statusErr = false
		return m
	}

	tw, ok := m.catalog.Find(msg.ID)
	name := msg.ID
	if ok {
		name = tweakName(m.lang, tw)
	}
	if len(msg.Errs) > 0 {
		m.status = fmt.Sprintf(T(m.lang, kMsgFail), name, T(m.lang, kWhatRollback), msg.Errs[0])
		m.statusErr = true
		return m
	}
	m.status = fmt.Sprintf(T(m.lang, kMsgRolledBack), name)
	m.statusErr = false
	return m
}

// categoryID unwraps a "cat:<id>" rollback sentinel; ok=false for tweak IDs.
func categoryID(id string) (string, bool) {
	const p = "cat:"
	if len(id) > len(p) && id[:len(p)] == p {
		return id[len(p):], true
	}
	return "", false
}

// openAction runs the focused tweak's gate deep-link ('o'): a local Cmd asks the
// gate for its URL (off the UI goroutine — the gate may do I/O) and launches it.
func (m model) openAction() (model, tea.Cmd) {
	tw, ok := m.curTweak()
	if !ok {
		return m, nil
	}
	if tw.Gate == nil {
		m.status = T(m.lang, kMsgNoAction)
		m.statusErr = true
		return m, nil
	}
	m.status = ""
	return m, openActionCmd(tw)
}

// openActionCmd checks the tweak's gate for a deep-link and opens it via the
// Windows shell. Both the gate check and the launch are I/O, so they run inside
// the Cmd, never in View/Update directly.
func openActionCmd(tw core.Tweak) tea.Cmd {
	return func() tea.Msg {
		ctx := core.ActionContext{Ctx: context.Background()}
		_, _, ga := tw.Gate.Check(ctx)
		if ga.URL == "" {
			return openDoneMsg{ID: tw.ID, URLFound: false}
		}
		// rundll32 url.dll opens any registered protocol (e.g. windowsdefender://)
		// without a shell window.
		err := exec.Command("rundll32", "url.dll,FileProtocolHandler", ga.URL).Start()
		return openDoneMsg{ID: tw.ID, URLFound: true, Err: err}
	}
}

// reportOpen turns an openDoneMsg into the status-line message.
func (m model) reportOpen(msg openDoneMsg) model {
	if !msg.URLFound {
		m.status = T(m.lang, kMsgNoAction)
		m.statusErr = true
		return m
	}
	if msg.Err != nil {
		tw, ok := m.catalog.Find(msg.ID)
		name := msg.ID
		if ok {
			name = tweakName(m.lang, tw)
		}
		m.status = fmt.Sprintf(T(m.lang, kMsgFail), name, T(m.lang, kWhatApply), msg.Err)
		m.statusErr = true
		return m
	}
	m.status = T(m.lang, kMsgActionDone)
	m.statusErr = false
	return m
}
