// Package ui is the Bubble Tea v2 front-end: a two-pane master-detail layout —
// a LEFT category list and a RIGHT tweak list for the selected category. Each
// pane has its own cursor + scroll offset + scrollbar and is hit-tested from the
// same row geometry the renderer draws, so mouse clicks never drift.
//
// The View is PURE: it reads only cached model state (statuses/probing). Every
// side effect (probe / apply / rollback / open-link) leaves as a tea.Cmd that
// runs off the UI goroutine and reports back a typed tea.Msg handled in Update.
package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"morgtweaker/internal/core"
	"morgtweaker/internal/elevate"
	"morgtweaker/internal/engine"
)

// pane identifies which side currently has keyboard focus.
type pane int

const (
	paneLeft pane = iota
	paneRight
)

// model is the Bubble Tea model.
type model struct {
	w, h int

	catalog core.Catalog
	engine  *engine.Engine
	isAdmin bool
	lang    Lang

	// prog is the running tea.Program, used to push streamed ApplyProgressMsg from
	// a long-running apply's progress callback back into the event loop. It is held
	// via a pointer slot so the value-copied model still observes the program once
	// Run sets it after tea.NewProgram (the model literal predates the program).
	prog **tea.Program

	// statuses is the ONLY source View reads for a tweak's state. Populated
	// asynchronously by ProbeBatchCmd/ProbeCmd; a missing entry is StatusUnknown
	// (rendered "…") so View never blocks on I/O.
	statuses map[string]core.Status
	// probing marks tweak IDs whose status is being (re)fetched, for a placeholder.
	probing map[string]bool
	// progress carries the latest streamed progress for a long-running apply.
	progress map[string]engine.ApplyProgressMsg
	// cancel holds the cancel func for an in-flight apply per tweak ID.
	cancel map[string]context.CancelFunc

	activePane pane

	// LEFT pane (categories): one entry per Category.
	catCursor int // selected category index
	catScroll int // first visible category row

	// RIGHT pane (tweaks of the selected category): independent scroll/cursor.
	twCursor int
	twScroll int

	status    string
	statusErr bool
}

// New builds the model. eng must be non-nil; pass engine.New(nil) for a
// probe-only engine (apply works, rollback reports backup-disabled).
func New(catalog core.Catalog, eng *engine.Engine) model {
	return model{
		catalog:  catalog,
		engine:   eng,
		isAdmin:  elevate.IsAdmin(),
		lang:     defaultLang,
		statuses: map[string]core.Status{},
		probing:  map[string]bool{},
		progress: map[string]engine.ApplyProgressMsg{},
		cancel:   map[string]context.CancelFunc{},
		prog:     new(*tea.Program),
	}
}

// Init kicks off the initial async probe of the WHOLE catalog so every tweak's
// status resolves off the UI goroutine; until BatchStatusMsg arrives every tweak
// renders as StatusUnknown ("…").
func (m model) Init() tea.Cmd {
	tws := m.allTweaks()
	if len(tws) == 0 {
		return nil
	}
	for _, t := range tws {
		m.probing[t.ID] = true
	}
	return m.engine.ProbeBatchCmd(tws)
}

// --- catalog accessors (empty-state safe) ----------------------------------

// curCategory returns the selected Category and ok=false when there are none.
func (m model) curCategory() (core.Category, bool) {
	if m.catCursor < 0 || m.catCursor >= len(m.catalog) {
		return core.Category{}, false
	}
	return m.catalog[m.catCursor], true
}

// curTweaks returns the tweaks of the selected category (nil when none).
func (m model) curTweaks() []core.Tweak {
	if c, ok := m.curCategory(); ok {
		return c.Tweaks
	}
	return nil
}

// curTweak returns the tweak under the right-pane cursor, ok=false when none.
func (m model) curTweak() (core.Tweak, bool) {
	tws := m.curTweaks()
	if m.twCursor < 0 || m.twCursor >= len(tws) {
		return core.Tweak{}, false
	}
	return tws[m.twCursor], true
}

// allTweaks flattens the catalog into one slice (for the startup batch probe).
func (m model) allTweaks() []core.Tweak {
	var out []core.Tweak
	for _, c := range m.catalog {
		out = append(out, c.Tweaks...)
	}
	return out
}

// statusOf returns the cached status for a tweak (StatusUnknown if not probed).
func (m model) statusOf(id string) core.Status { return m.statuses[id] }

// Run launches the TUI program. Alt-screen + mouse are per-View fields (v2).
// The constructed model's prog slot is filled with the running *tea.Program so a
// long-running apply can Send streamed ApplyProgressMsg back into the loop.
func Run(catalog core.Catalog, eng *engine.Engine) error {
	m := New(catalog, eng)
	p := tea.NewProgram(m)
	*m.prog = p // model is value-copied into the program, but the slot is shared
	_, err := p.Run()
	return err
}
