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
	"time"

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

// screen is the top-level view mode. screenList (the default zero value) is the
// two-pane master-detail list; screenProgress REPLACES it with the apply/rollback
// progress screen (three stacked bars) for the lifetime of a batch, then reverts.
type screen int

const (
	screenList screen = iota
	screenProgress
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
	// selected is the user's own per-row checkbox state, decoupled from the probed
	// status: every tweak starts unchecked (missing entry = false) and a toggle/
	// click flips it. This — NOT core.Status — drives the checkbox glyph fill.
	selected map[string]bool

	// expanded marks expandable PARENT tweak IDs whose children are shown inline.
	// Missing entry = collapsed. Independent of selection/status.
	expanded map[string]bool

	activePane pane

	// LEFT pane (categories): one entry per Category.
	catCursor int // selected category index
	catScroll int // first visible category row

	// RIGHT pane (tweaks of the selected category): independent scroll/cursor.
	twCursor int
	twScroll int

	status    string
	statusErr bool

	// version is the product version shown in the window-frame title (e.g.
	// "1.0.0"). Set by Run from the embedded versioninfo.json; empty in tests/
	// direct New() callers, where the title falls back to "dev".
	version string

	// batch drives the SEQUENTIAL apply/rollback of all CHECKED rows triggered by
	// the bottom bar. batchKind is the active operation (none/apply/rollback);
	// batchQueue holds the tweak IDs still to process (head dispatched next). One
	// item is in flight at a time — its Done message advances the queue.
	batchKind  int
	batchQueue []string

	// --- progress screen state (valid while m.screen == screenProgress) --------
	//
	// screen selects the list vs the progress view. The progress screen reads ONLY
	// these cached fields (never any I/O), all fed from messages handled in Update.
	screen screen

	// batchTotal is the number of tweaks the active batch started with (drives the
	// OVERALL bar, which is shown only when batchTotal > 1). batchDone counts the
	// tweaks finished so far; batchFailed counts those that errored (for the
	// return-to-list summary line).
	batchTotal  int
	batchDone   int
	batchFailed int

	// currentID is the tweak whose apply/rollback is in flight (the CURRENT-TWEAK
	// bar's subject); "" between items and once the batch ends.
	currentID string

	// download speed derivation: dlSpeed is the latest transfer rate in bytes/sec,
	// computed in Update from the byte delta between successive download ticks
	// (dlLastDone bytes at dlLastTime). View only reads dlSpeed — it never calls
	// time.Now, staying pure.
	dlSpeed    float64
	dlLastDone int64
	dlLastTime time.Time
}

// batch operation kinds for model.batchKind.
const (
	batchNone = iota
	batchApply
	batchRollback
)

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
		selected: map[string]bool{},
		expanded: map[string]bool{},
		prog:     new(*tea.Program),
	}
}

// Init kicks off the initial async probe of every LEAF tweak (Catalog.Leaves
// replaces each parent with its children — a parent has no Actions and is never
// engine-probed; the UI aggregates its status from its children). Status resolves
// off the UI goroutine; until BatchStatusMsg arrives every tweak renders as
// StatusUnknown ("…").
func (m model) Init() tea.Cmd {
	tws := m.catalog.Leaves()
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
	rows := m.visibleRows()
	if m.twCursor < 0 || m.twCursor >= len(rows) {
		return core.Tweak{}, false
	}
	return rows[m.twCursor].tw, true
}

// visRow is one flattened right-pane row: a tweak plus whether it is an indented
// child of an expanded parent. visibleRows is the single source the renderer,
// cursor, hit-test, and selection all index, so they never disagree on geometry.
type visRow struct {
	tw    core.Tweak
	child bool
}

// visibleRows flattens the current category's tweaks: each tweak is a row; an
// expanded parent is immediately followed by its children as child rows.
func (m model) visibleRows() []visRow {
	var rows []visRow
	for _, tw := range m.curTweaks() {
		rows = append(rows, visRow{tw: tw})
		if tw.IsParent() && m.expanded[tw.ID] {
			for _, ch := range tw.Children {
				rows = append(rows, visRow{tw: ch, child: true})
			}
		}
	}
	return rows
}

// rowStatus is the status to render/act on for a row: a parent aggregates its
// children's cached statuses (any unknown → Unknown so it shows "…" until all
// children resolve; all-on → On; all-off → Off; otherwise Partial); a leaf is its
// own cached status.
func (m model) rowStatus(tw core.Tweak) core.Status {
	if !tw.IsParent() {
		return m.statusOf(tw.ID)
	}
	on, off := 0, 0
	for _, ch := range tw.Children {
		switch m.statusOf(ch.ID) {
		case core.StatusOn:
			on++
		case core.StatusOff:
			off++
		default:
			return core.StatusUnknown // a child not yet resolved (or blocked/absent)
		}
	}
	switch {
	case on == 0 && off == 0:
		return core.StatusUnknown
	case off == 0:
		return core.StatusOn
	case on == 0:
		return core.StatusOff
	default:
		return core.StatusPartial
	}
}

// parentSelState derives a PARENT row's checkbox state from its actionable
// children — the single source of truth for both the rendered glyph and the (*)
// mixed-state marker. "Actionable" = statusHasAction(childStatus) true (a child
// with no available action can't be selected, so it is excluded from the tally).
//
//	actionable = number of children that have an action
//	selected   = how many of those are checked in m.selected
//	mixed      = 0 < selected < actionable (some-but-not-all checked)
//
// Zero actionable children → selected=0, actionable=0, mixed=false: the parent
// renders [ ] with no (*) and its toggle is a no-op.
func (m model) parentSelState(tw core.Tweak) (selected, actionable int, mixed bool) {
	for _, ch := range tw.Children {
		if !statusHasAction(m.statusOf(ch.ID)) {
			continue
		}
		actionable++
		if m.selected[ch.ID] {
			selected++
		}
	}
	mixed = selected > 0 && selected < actionable
	return selected, actionable, mixed
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
func Run(catalog core.Catalog, eng *engine.Engine, version string) error {
	m := New(catalog, eng)
	m.version = version
	p := tea.NewProgram(m)
	*m.prog = p // model is value-copied into the program, but the slot is shared
	_, err := p.Run()
	return err
}
