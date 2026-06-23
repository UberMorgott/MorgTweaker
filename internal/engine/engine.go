// Package engine orchestrates tweaks generically over their actions: probe
// (aggregate point states into a core.Status), apply (snapshot save-once →
// broker-grouped run → verify-after), and rollback (reverse-order restore from
// the per-action backup store). It never touches the UI goroutine — the async
// tea.Cmd/tea.Msg adapters live in a separate file (Task 7).
//
// Verify-after is the centerpiece: after writing the ON value the engine
// re-probes, and if the change did not stick (still not On) it reports
// StatusBlocked. This catches the Defender Tamper-Protection "written-then-
// reverted" case that v1 special-cased in internal/tweak/registry.go; v2
// centralizes it here so every action kind benefits.
package engine

import (
	"context"

	"morgtweaker/internal/backup"
	"morgtweaker/internal/core"
	"morgtweaker/internal/elevate"
)

// Engine probes, applies and rolls back tweaks. store may be nil (probe-only /
// tests), in which case Apply skips persistence and Rollback reports an error.
// runAs is the elevation broker (defaults to elevate.RunAs); tests stub it.
type Engine struct {
	store *backup.Store
	runAs func(elevate.Level, func() error) error
}

// New returns an Engine backed by the given backup store. Pass nil for a
// probe-only engine with no rollback persistence.
func New(store *backup.Store) *Engine {
	return &Engine{store: store, runAs: elevate.RunAs}
}

// levelOf maps a core.Elevation to the elevate broker Level. ElevUser and
// ElevAdmin both run in the already-elevated process with no token impersonation
// (the app manifest grants admin); only System/TrustedInstaller impersonate.
func levelOf(e core.Elevation) elevate.Level {
	switch e {
	case core.ElevSystem:
		return elevate.System
	case core.ElevTrustedInstaller:
		return elevate.TrustedInstaller
	default:
		return elevate.User
	}
}

// Probe returns the live aggregate status. A Gate (if any) runs first and
// short-circuits with its own status when it blocks. Otherwise the action point
// states are aggregated: absent points are excluded; all-absent → Absent; all
// remaining on → On; all remaining off → Off; a mix → Partial.
func (e *Engine) Probe(t core.Tweak) (core.Status, error) {
	return e.probe(core.ActionContext{Ctx: context.Background()}, t)
}

// probe is Probe with a caller-supplied context, shared by Probe and the
// verify-after step of Apply so the same cancellation/progress sink threads
// through.
func (e *Engine) probe(ctx core.ActionContext, t core.Tweak) (core.Status, error) {
	if t.Gate != nil {
		if ok, st, _ := t.Gate.Check(ctx); !ok {
			return st, nil
		}
	}
	on, off := 0, 0
	for _, a := range t.Actions {
		ps, err := a.Probe(ctx)
		if err != nil {
			return core.StatusOff, err
		}
		switch ps {
		case core.PointOn:
			on++
		case core.PointOff:
			off++
		case core.PointAbsent:
			// absent points are n/a and excluded from the aggregate
		}
	}
	switch {
	case on == 0 && off == 0:
		return core.StatusAbsent, nil // no actions, or every point absent
	case off == 0:
		return core.StatusOn, nil // all non-absent points on
	case on == 0:
		return core.StatusOff, nil // all non-absent points off
	default:
		return core.StatusPartial, nil
	}
}

// Apply snapshots (save-once), runs actions grouped by elevation under one
// RunAs per level, then verifies. Uses a background context.
func (e *Engine) Apply(t core.Tweak, on bool) (core.Status, error) {
	return e.ApplyCtx(core.ActionContext{Ctx: context.Background()}, t, on)
}

// ApplyCtx is Apply with a caller-supplied context (used by the async Cmd so
// cancellation and a progress sink reach long-running actions).
//
// Steps: (1) gate check (blocked → return the gate's status, run nothing);
// (2) snapshot every action save-once into the store; (3) group action indices
// by elevation level and run each group under one broker call, tracking which
// actions actually mutated; (4) per-action verify-after — re-Probe EACH applied
// action and compare to the direction just applied (absent = n/a, skipped); any
// action that did not stick → StatusBlocked; a Reboot tweak that fully stuck →
// RebootPending; otherwise the honest aggregate status.
//
// On any mid-apply error (snapshot/apply/broker), the engine best-effort rolls
// back the actions already mutated in THIS call and returns the error with an
// HONEST re-probed status (StatusUnknown if even the probe fails) — never a
// hard-coded StatusOff that would paint a false-clean row.
func (e *Engine) ApplyCtx(ctx core.ActionContext, t core.Tweak, on bool) (core.Status, error) {
	if t.Gate != nil {
		if ok, st, _ := t.Gate.Check(ctx); !ok {
			return st, nil
		}
	}

	// 1. snapshot save-once (the first change captures the user's true original
	// value; later toggles must not overwrite it).
	if e.store != nil {
		for i, a := range t.Actions {
			bak, err := a.Snapshot(ctx)
			if err != nil {
				// nothing mutated yet → nothing to roll back; report honest status.
				return e.honestStatus(ctx, t), err
			}
			if _, err := e.store.SaveActionIfAbsent(backup.ActionKey(t.ID, i), bak); err != nil {
				return e.honestStatus(ctx, t), err
			}
		}
	}

	// 2. group indices by elevation level, preserving first-seen order so a
	// single RunAs per level covers all its actions.
	groups, order := groupByLevel(t)

	// 3. one broker call per level, recording every action that mutated so a
	// mid-apply failure can be rolled back (best-effort atomic).
	var applied []int
	for _, lvl := range order {
		idxs := groups[lvl]
		runErr := e.runAs(lvl, func() error {
			for _, i := range idxs {
				if ctx.Ctx != nil {
					if err := ctx.Ctx.Err(); err != nil {
						return err
					}
				}
				if err := t.Actions[i].Apply(ctx, on); err != nil {
					return err
				}
				applied = append(applied, i)
			}
			return nil
		})
		if runErr != nil {
			e.restoreIndices(ctx, t, applied) // best-effort; collected errs dropped here
			return e.honestStatus(ctx, t), runErr
		}
	}

	// 4. per-action verify-after: each mutated, non-absent action must have stuck
	// in the applied direction; otherwise the change was silently reverted.
	blocked, err := e.verifyAfter(ctx, t, on)
	if err != nil {
		return e.honestStatus(ctx, t), err
	}
	if blocked {
		return core.StatusBlocked, nil
	}

	st, err := e.probe(ctx, t)
	if err != nil {
		return st, err
	}
	if on && t.Reboot && st == core.StatusOn {
		return core.StatusRebootPending, nil
	}
	return st, nil
}

// verifyAfter re-probes each action and reports whether any non-absent action
// failed to stick in the direction just applied (a silent revert). Applied
// on=true expects PointOn per action; on=false expects PointOff; PointAbsent is
// n/a and skipped (it never blocks).
func (e *Engine) verifyAfter(ctx core.ActionContext, t core.Tweak, on bool) (blocked bool, err error) {
	want := core.PointOff
	if on {
		want = core.PointOn
	}
	for _, a := range t.Actions {
		ps, perr := a.Probe(ctx)
		if perr != nil {
			return false, perr
		}
		if ps == core.PointAbsent {
			continue // not applicable to this action
		}
		if ps != want {
			return true, nil // this action was silently reverted
		}
	}
	return false, nil
}

// honestStatus re-probes for the real aggregate status, falling back to
// StatusUnknown if the probe itself fails. Used on the error path so a caller
// that ignores the error never sees a fabricated clean/off row.
func (e *Engine) honestStatus(ctx core.ActionContext, t core.Tweak) core.Status {
	st, err := e.probe(ctx, t)
	if err != nil {
		return core.StatusUnknown
	}
	return st
}

// groupByLevel buckets a tweak's action indices by broker level, preserving
// first-seen level order (so one RunAs per level covers all its actions).
func groupByLevel(t core.Tweak) (groups map[elevate.Level][]int, order []elevate.Level) {
	groups = map[elevate.Level][]int{}
	for i, a := range t.Actions {
		lvl := levelOf(a.Level())
		if _, seen := groups[lvl]; !seen {
			order = append(order, lvl)
		}
		groups[lvl] = append(groups[lvl], i)
	}
	return groups, order
}

// Rollback restores every action in reverse order from its save-once backup,
// routing each Restore through the elevation broker at the action's level (so a
// System/TrustedInstaller action's Restore runs impersonated, like Apply did),
// then deletes the backup key on success so a later fresh apply re-snapshots the
// (now-restored) original. Actions with no recorded backup are skipped. Errors
// are collected, not fatal — one failed restore does not abort the rest.
func (e *Engine) Rollback(t core.Tweak) []error {
	if e.store == nil {
		return []error{errBackupDisabled}
	}
	ctx := core.ActionContext{Ctx: context.Background()}
	all := make([]int, len(t.Actions))
	for i := range all {
		all[i] = i
	}
	return e.restoreIndices(ctx, t, all)
}

// restoreIndices restores the given action indices from their save-once backups,
// grouped by elevation level and run under one broker call per level. Level
// groups are visited in REVERSE of apply order and indices within a group in
// reverse, so the overall restore unwinds the apply. Each Restore runs inside
// the broker at the action's level. A missing backup is skipped; a failed
// restore is collected and does not abort the rest; a successful restore deletes
// the backup key. Returns nil when there is nothing to do or no store.
func (e *Engine) restoreIndices(ctx core.ActionContext, t core.Tweak, idxs []int) []error {
	if e.store == nil || len(idxs) == 0 {
		return nil
	}
	// Bucket the requested indices by level, preserving the first-seen apply
	// order, then walk the level groups in reverse.
	groups := map[elevate.Level][]int{}
	var order []elevate.Level
	for _, i := range idxs {
		lvl := levelOf(t.Actions[i].Level())
		if _, seen := groups[lvl]; !seen {
			order = append(order, lvl)
		}
		groups[lvl] = append(groups[lvl], i)
	}

	var errs []error
	for gi := len(order) - 1; gi >= 0; gi-- {
		lvl := order[gi]
		members := groups[lvl]
		runErr := e.runAs(lvl, func() error {
			for mi := len(members) - 1; mi >= 0; mi-- {
				i := members[mi]
				key := backup.ActionKey(t.ID, i)
				bak, ok, lerr := e.store.LoadAction(key)
				if lerr != nil {
					errs = append(errs, lerr)
					continue
				}
				if !ok {
					continue // nothing snapshotted for this action
				}
				if rerr := t.Actions[i].Restore(ctx, bak); rerr != nil {
					errs = append(errs, rerr)
					continue
				}
				_ = e.store.DeleteAction(key)
			}
			return nil
		})
		if runErr != nil {
			errs = append(errs, runErr)
		}
	}
	return errs
}

var errBackupDisabled = backupDisabledErr{}

type backupDisabledErr struct{}

func (backupDisabledErr) Error() string { return "engine: backup store disabled, rollback unavailable" }
