// Async adapters bridge the Engine to the Bubble Tea v2 runtime. Each method
// returns a tea.Cmd (a func() tea.Msg) that does the blocking work — registry
// reads, PowerShell spawns, network — OFF the UI goroutine when Bubble Tea
// schedules it, then hands the UI a typed result Msg. The View itself never
// performs I/O; it only reads model state updated from these Msgs.
package engine

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"morgtweaker/internal/core"
)

// StatusMsg is the result of probing a single tweak. Err is non-nil when the
// probe failed; the UI then shows StatusUnknown rather than a fabricated state.
type StatusMsg struct {
	ID     string
	Status core.Status
	Err    error
}

// BatchStatusMsg carries the statuses of many tweaks probed together, keyed by
// tweak ID. Used for the initial full-catalog probe on startup.
type BatchStatusMsg struct {
	Statuses map[string]core.Status
}

// ApplyProgressMsg streams incremental progress for a long-running apply
// (download/install). Pct is 0..100; Note is a short human label. (Streaming is
// wired by the download_install action in a later task; the type is defined here
// so the UI can switch on it now.)
type ApplyProgressMsg struct {
	ID   string
	Pct  int
	Note string
}

// ApplyDoneMsg is the terminal result of an apply: the re-probed Status and any
// error from ApplyCtx (gate/apply/verify-after).
type ApplyDoneMsg struct {
	ID     string
	Status core.Status
	Err    error
}

// ProbeBatchCmd probes every given tweak (sequentially, off the UI goroutine)
// and returns a single BatchStatusMsg. A probe error degrades that tweak to
// StatusUnknown so the UI shows "…" rather than a false clean/off row.
func (e *Engine) ProbeBatchCmd(tweaks []core.Tweak) tea.Cmd {
	return func() tea.Msg {
		out := make(map[string]core.Status, len(tweaks))
		for _, t := range tweaks {
			st, err := e.Probe(t)
			if err != nil {
				st = core.StatusUnknown
			}
			out[t.ID] = st
		}
		return BatchStatusMsg{Statuses: out}
	}
}

// ProbeCmd re-probes a single tweak (e.g. after an apply) and returns its
// StatusMsg. The error is carried on the Msg, not swallowed, so the UI can
// surface it.
func (e *Engine) ProbeCmd(t core.Tweak) tea.Cmd {
	return func() tea.Msg {
		st, err := e.Probe(t)
		return StatusMsg{ID: t.ID, Status: st, Err: err}
	}
}

// ApplyCmd runs an apply off the UI goroutine and returns the terminal
// ApplyDoneMsg. ctx threads cancellation into long-running actions; prog (nil-ok)
// is the progress sink reported through ActionContext (consumed by the
// download_install action in a later task). The returned status is whatever
// ApplyCtx re-probes — honest even on error.
func (e *Engine) ApplyCmd(ctx context.Context, t core.Tweak, on bool, prog func(pct int, note string)) tea.Cmd {
	return func() tea.Msg {
		actx := core.ActionContext{Ctx: ctx, Progress: prog}
		st, err := e.ApplyCtx(actx, t, on)
		return ApplyDoneMsg{ID: t.ID, Status: st, Err: err}
	}
}
