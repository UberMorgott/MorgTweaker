# VC++ redist parent checkbox + tri-state selection — design

- Date: 2026-06-24
- Scope: TUI (`internal/ui`) only. No catalog/engine changes.

## Goal

- The expandable VC++ redist parent (`prep.vcredist`, the only `IsParent` tweak)
  gets a real checkbox. Checking the parent checks ALL its children; unchecking
  unchecks all. Expanding/collapsing the child list is a separate action.
- When only SOME children are checked (changed individually in the expanded
  list), the parent row shows an indeterminate glyph `[~]` plus a `(*)` marker
  after the name — visible even when collapsed.

## Selection model (single source of truth)

- Selection truth = `m.selected[childID]` per child only. The parent has NO own
  `selected` entry. Remove the now-dead `m.selected[parent]` branch in
  `applyQueueIDs` (children carry all selection).
- Parent checkbox glyph is DERIVED from its "actionable" children
  (`statusHasAction(childStatus)` true):
  - all actionable children selected -> `[x]` (filled), no `(*)`
  - none selected -> `[ ]` (empty), no `(*)`
  - mixed (0 < selected < actionable) -> `[~]` + `(*)` after the name
- Children with no available action are excluded from the tally (they cannot be
  selected anyway).
- Edge: zero actionable children -> parent renders `[ ]`, no `(*)`, toggle is a
  no-op.

## Interaction (right pane, cursor on a parent row)

- Toggle CHECK: left-click on the parent row (outside the caret cell) OR Space OR
  Enter. Logic: if all actionable children are selected -> clear all; else select
  all actionable children. (So mixed -> first toggle selects all.)
- Toggle EXPAND/COLLAPSE: left-click on the caret cell, OR `->` (expand), OR `<-`
  (collapse).
- Arrow keys stay pane-switch outside that case:
  - `->`: if active pane is right AND cursor on a parent AND collapsed -> expand;
    otherwise focus the right pane (existing behavior).
  - `<-`: if active pane is right AND cursor on a parent AND expanded -> collapse;
    otherwise focus the left pane (existing behavior).
- Children unchanged: click / Space / Enter toggles the child's own checkbox.

## Rendering

- Parent row layout: `<caret> <checkbox> <name> [ (*) ]`.
  - caret `▾` (expanded) / `▸` (collapsed)
  - checkbox `[x]` / `[ ]` / `[~]` per the derived state
  - `(*)` marker rendered only in the mixed state
- Children: indented one step, checkbox column aligned under the parent checkbox.
- New glyph constant `glyphPartial = "[~]"` in `styles.go`. The `(*)` marker is a
  styled literal (dim/appliable style), language-neutral.

## Apply / rollback

- `applyQueueIDs` queues every selected actionable child in catalog order
  (dedup preserved). Net behavior identical to before because a parent check now
  sets the children directly. The special-case parent branch is removed.

## Mouse hit-test

- `MouseClickMsg` on a parent row: compute the caret cell's inner-X span; click
  inside -> expand/collapse, click elsewhere on the row -> toggle check.

## Tests (TDD, `internal/ui`)

- Selection tally -> glyph: all / none / mixed produce `[x]` / `[ ]` / `[~]`.
- `(*)` rendered only in the mixed state (assert presence/absence in the row
  string).
- toggleCurrent on parent: none->all, all->none, mixed->all.
- Arrow `->`/`<-` expand/collapse on a parent; arrows switch panes otherwise.
- Caret-cell click expands; body click checks.
- Update existing `expand_test.go`:
  - `TestEnterExpandsParent` -> Space/Enter now CHECKS (children set), arrow key
    EXPANDS.
  - `TestApplySelectedExpandsParentToChildren` -> drive via children selection
    (or the parent toggle), not a raw `m.selected[parent]`.

## Files

- `internal/ui/update.go` — toggle logic, arrow handling, mouse caret hit-test,
  `applyQueueIDs` simplification.
- `internal/ui/render.go` — parent row caret+checkbox+`(*)` rendering, child
  alignment.
- `internal/ui/ui.go` — parent-selection-state derivation helper (e.g.
  `parentSelState`).
- `internal/ui/styles.go` — `glyphPartial`.
- `internal/ui/expand_test.go` + new `*_test.go` cases.
