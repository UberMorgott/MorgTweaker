package ui

import (
	"context"
	"fmt"
	"os/exec"
	"time"

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
		m = m.updateDownloadSpeed(msg)
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
		// On a successful apply the row's checkbox clears (it is now applied/grey).
		if msg.Err == nil {
			delete(m.selected, msg.ID)
		}
		// If an apply batch is running, count this item and advance to the next.
		if m.batchKind == batchApply {
			m = m.countBatchItem(msg.Err != nil || msg.Status == core.StatusBlocked)
			return m.advanceBatch()
		}
		return m, nil

	case rollbackDoneMsg:
		m = m.reportRollback(msg)
		// On a successful rollback the checkbox clears (row goes bright again once
		// the re-probe lands).
		if len(msg.Errs) == 0 {
			delete(m.selected, msg.ID)
		}
		// Re-probe the rolled-back tweak so its row reflects the restored state.
		var probe tea.Cmd
		if tw, ok := m.catalog.Find(msg.ID); ok {
			m.probing[msg.ID] = true
			probe = m.engine.ProbeCmd(tw)
		}
		// If a rollback batch is running, count this item, advance it, and run the
		// re-probe alongside the next rollback (concurrent; order between probe and
		// next item doesn't matter — each targets a different tweak).
		if m.batchKind == batchRollback {
			m = m.countBatchItem(len(msg.Errs) > 0)
			var next tea.Cmd
			m, next = m.advanceBatch()
			return m, tea.Batch(probe, next)
		}
		return m, probe

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
		// Bottom button bar first: a click on the footer row maps to a button zone
		// and runs that action (apply/rollback selected, lang toggle, quit).
		if mc.Y == m.buttonRowY() {
			if id, ok := m.buttonAtX(mc.X - frameBorderX); ok {
				return m.onButton(id)
			}
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
			// CLICK = SELECT ONLY: move the cursor and toggle the checkbox; no apply.
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
		// Keyboard equivalent of the [Apply] button: apply all checked appliable.
		return m.applySelected()
	case "r":
		// Keyboard equivalent of the [Rollback] button: rollback all checked applied.
		return m.rollbackSelected()
	case "R":
		return m.rollbackCategory()
	case "o":
		return m.openAction()
	case "esc":
		// Cancel the in-flight apply (the batch's current item, or a lone apply)
		// without quitting. When a batch/progress screen is active, esc ABORTS the
		// whole batch: tear down its queue, return to the list, and show "cancelled".
		// The cancelled apply still emits a late ApplyDoneMsg, but with batchKind now
		// reset its advance branch is skipped, so the batch does not continue.
		cancelled := m.cancelInflightTweaks()
		if m.screen == screenProgress {
			m.batchKind = batchNone
			m.batchQueue = nil
			m.currentID = ""
			m.screen = screenList
			m.status = T(m.lang, kMsgCancelled)
			m.statusErr = false
			return m, nil
		}
		if cancelled {
			m.status = ""
		}
		return m, nil
	}
	return m, nil
}

// onButton runs the action for a bottom-bar button id (mouse or keyboard).
func (m model) onButton(id int) (tea.Model, tea.Cmd) {
	switch id {
	case btnApply:
		m, cmd := m.applySelected()
		return m, cmd
	case btnRollback:
		m, cmd := m.rollbackSelected()
		return m, cmd
	case btnLang:
		m.lang = Next(m.lang)
		m.status = ""
		return m, nil
	case btnQuit:
		m.cancelInflight()
		return m, tea.Quit
	}
	return m, nil
}

// cancelInflightTweaks cancels every in-flight apply (clearing its working
// markers) and reports whether anything was cancelled. Used by esc so it cancels
// the current batch item even when the cursor is on a different row.
func (m model) cancelInflightTweaks() bool {
	ids := make([]string, 0, len(m.cancel))
	for id := range m.cancel {
		ids = append(ids, id)
	}
	cancelled := false
	for _, id := range ids {
		if m.cancelTweak(id) {
			cancelled = true
		}
	}
	return cancelled
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

// toggleCurrent SELECTS ONLY: it flips the right-pane focused tweak's checkbox
// (m.selected), decoupled from probed status, and dispatches NOTHING. Apply and
// rollback happen exclusively via the bottom button bar. Rows with no available
// action (blocked / absent / unprobed / in-flight) cannot be selected. Returns a
// nil Cmd by design.
func (m model) toggleCurrent() (model, tea.Cmd) {
	tw, ok := m.curTweak()
	if !ok {
		return m, nil
	}
	if !statusHasAction(m.statusOf(tw.ID)) {
		return m, nil // no checkbox on this row → nothing to select
	}
	m.selected[tw.ID] = !m.selected[tw.ID]
	return m, nil
}

// selectedByStatus collects, in stable catalog order across ALL categories, the
// IDs of CHECKED tweaks whose current status satisfies `pred`.
func (m model) selectedByStatus(pred func(core.Status) bool) []string {
	var ids []string
	for _, tw := range m.allTweaks() {
		if m.selected[tw.ID] && pred(m.statusOf(tw.ID)) {
			ids = append(ids, tw.ID)
		}
	}
	return ids
}

// applySelected (bottom bar [Apply]) applies ALL checked appliable tweaks across
// every category, SEQUENTIALLY: it queues their IDs and dispatches the first; each
// ApplyDoneMsg advances the queue. Ignored if a batch is already running.
func (m model) applySelected() (model, tea.Cmd) {
	if m.batchKind != batchNone {
		return m, nil // a batch is already in flight — debounce
	}
	q := m.selectedByStatus(statusAppliable)
	if len(q) == 0 {
		return m, nil
	}
	m.batchKind = batchApply
	m.batchQueue = q
	m = m.enterProgress(len(q))
	return m.advanceBatch()
}

// rollbackSelected (bottom bar [Rollback]) rolls back ALL checked applied tweaks
// across every category, SEQUENTIALLY (same queue mechanism as applySelected).
func (m model) rollbackSelected() (model, tea.Cmd) {
	if m.batchKind != batchNone {
		return m, nil
	}
	q := m.selectedByStatus(statusRollbackable)
	if len(q) == 0 {
		return m, nil
	}
	m.batchKind = batchRollback
	m.batchQueue = q
	m = m.enterProgress(len(q))
	return m.advanceBatch()
}

// advanceBatch dispatches the next queued tweak for the active batch. It pops the
// head and skips items that yield no Cmd (e.g. a needs-admin apply, which would
// otherwise never send a Done message to advance the queue), so the batch never
// stalls. When the queue drains it clears batchKind. Returns the dispatched Cmd.
func (m model) advanceBatch() (model, tea.Cmd) {
	for len(m.batchQueue) > 0 {
		id := m.batchQueue[0]
		m.batchQueue = m.batchQueue[1:]
		tw, ok := m.catalog.Find(id)
		if !ok {
			m = m.countBatchItem(false) // dropped (unknown id) still counts as processed
			continue
		}
		var cmd tea.Cmd
		switch m.batchKind {
		case batchApply:
			m, cmd = m.dispatchApply(tw, true)
		case batchRollback:
			cmd = rollbackCmd(m.engine, tw)
		}
		if cmd != nil {
			// This tweak is now in flight: it is the CURRENT-TWEAK bar's subject and
			// the download/install bar follows its streamed progress.
			m.currentID = id
			m = m.resetDownloadSpeed()
			return m, cmd
		}
		// Skipped (no Cmd, e.g. needs-admin apply) — count it and try the next item.
		m = m.countBatchItem(true)
	}
	// Queue drained: settle back to the list with a one-line summary.
	return m.finishBatch(), nil
}

// enterProgress switches to the progress screen at the start of a batch of `total`
// tweaks, zeroing the per-batch counters and clearing the status line.
func (m model) enterProgress(total int) model {
	m.screen = screenProgress
	m.batchTotal = total
	m.batchDone = 0
	m.batchFailed = 0
	m.currentID = ""
	m.status = ""
	m.statusErr = false
	return m
}

// countBatchItem records that one batch item finished, tallying failures so the
// return-to-list summary can report n applied / n failed.
func (m model) countBatchItem(failed bool) model {
	m.batchDone++
	if failed {
		m.batchFailed++
	}
	return m
}

// finishBatch ends the active batch: clears the batch markers, returns to the list
// screen, and sets a one-line summary (n ok / n failed) on the status line.
func (m model) finishBatch() model {
	total, failed := m.batchTotal, m.batchFailed
	ok := total - failed
	m.batchKind = batchNone
	m.batchQueue = nil
	m.currentID = ""
	m.screen = screenList
	if total > 0 {
		m.status = fmt.Sprintf(T(m.lang, kMsgBatchSummary), ok, failed)
		m.statusErr = failed > 0
	}
	return m
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
func (m model) progressSink(id string) func(pct int, note string, done, total int64) {
	if m.prog == nil || *m.prog == nil {
		return nil
	}
	p := *m.prog
	return func(pct int, note string, done, total int64) {
		p.Send(engine.ApplyProgressMsg{ID: id, Pct: pct, Note: note, Done: done, Total: total})
	}
}

// resetDownloadSpeed clears the speed-derivation state at the start of a new
// in-flight tweak so the first download tick of the next tweak doesn't inherit a
// stale byte/time baseline.
func (m model) resetDownloadSpeed() model {
	m.dlSpeed = 0
	m.dlLastDone = 0
	m.dlLastTime = time.Time{}
	return m
}

// updateDownloadSpeed derives the transfer rate (bytes/sec) from the byte delta
// between successive download ticks. It runs in Update (not View) so View stays
// pure: View only reads the cached m.dlSpeed. A tick that carries no byte counter
// (Done==0, e.g. the install phase) leaves the last reading untouched.
func (m model) updateDownloadSpeed(msg engine.ApplyProgressMsg) model {
	if msg.Done <= 0 {
		return m
	}
	now := time.Now()
	if !m.dlLastTime.IsZero() {
		dt := now.Sub(m.dlLastTime).Seconds()
		if db := msg.Done - m.dlLastDone; dt > 0 && db > 0 {
			m.dlSpeed = float64(db) / dt
		}
	}
	m.dlLastDone = msg.Done
	m.dlLastTime = now
	return m
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
		// The block reason is NOT always Tamper Protection: verify-after (the change
		// didn't stick) and access-denied also produce StatusBlocked, and neither
		// carries a gate deep-link. Only show the "Tamper Protection / press o" wording
		// when the tweak's gate actually yields a GateAction URL; otherwise show a
		// generic block message and do NOT advertise the 'o' deep-link.
		if ok && hasGateAction(tw) {
			m.status = fmt.Sprintf(T(m.lang, kMsgBlocked), name)
		} else {
			m.status = fmt.Sprintf(T(m.lang, kMsgBlockedGeneric), name)
		}
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

// hasGateAction reports whether the tweak's gate currently offers a deep-link
// (a non-empty GateAction URL) — i.e. a real Tamper-Protection block the user can
// act on with 'o'. A tweak with no gate, or a gate that returns no URL (e.g. a
// verify-after / access-denied block, which never flows through a gate), reports
// false so the UI shows the generic block wording instead of the Tamper deep-link.
// The gate's TamperCache is already warm from the apply that just blocked, so this
// hits the cache rather than re-running PowerShell.
func hasGateAction(tw core.Tweak) bool {
	if tw.Gate == nil {
		return false
	}
	_, _, ga := tw.Gate.Check(core.ActionContext{Ctx: context.Background()})
	return ga.URL != ""
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
