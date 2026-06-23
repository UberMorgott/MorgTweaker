package engine

import (
	"errors"
	"path/filepath"
	"testing"

	"morgtweaker/internal/backup"
	"morgtweaker/internal/core"
	"morgtweaker/internal/elevate"
)

// fakeAction records calls and reports a scripted probe state. It is a pointer
// receiver so the recorded fields (applied / restored / probe count) survive the
// copy the engine takes when ranging t.Actions.
//
// `tag` identifies the action in the shared order/restore recorders so tests can
// assert apply/rollback ordering and broker routing. brokerActive (if set) is the
// shared flag the test broker raises while a RunAs closure is executing; Restore
// snapshots it into restoredInBroker so a test can prove the restore ran inside
// the broker at the right level.
type fakeAction struct {
	level      core.Elevation
	state      core.PointState // what Probe returns
	applied    *bool           // set to the `on` arg on Apply
	applyCount *int
	restored   *bool
	snapErr    error
	applyErr   error

	tag              string
	applyOrder       *[]string // appended on successful Apply
	restoreOrder     *[]string // appended on Restore
	brokerActive     *bool     // raised by the test broker inside a RunAs closure
	restoredInBroker *bool     // snapshot of brokerActive seen during Restore
}

func (f *fakeAction) Level() core.Elevation { return f.level }

func (f *fakeAction) Apply(_ core.ActionContext, on bool) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	if f.applied != nil {
		*f.applied = on
	}
	if f.applyCount != nil {
		*f.applyCount++
	}
	if f.applyOrder != nil {
		*f.applyOrder = append(*f.applyOrder, f.tag)
	}
	return nil
}

func (f *fakeAction) Snapshot(core.ActionContext) (core.Backup, error) {
	if f.snapErr != nil {
		return core.Backup{}, f.snapErr
	}
	return core.Backup{Existed: true, Type: 4, Value: uint64(0)}, nil
}

func (f *fakeAction) Restore(_ core.ActionContext, _ core.Backup) error {
	if f.restored != nil {
		*f.restored = true
	}
	if f.restoreOrder != nil {
		*f.restoreOrder = append(*f.restoreOrder, f.tag)
	}
	if f.restoredInBroker != nil && f.brokerActive != nil {
		*f.restoredInBroker = *f.brokerActive
	}
	return nil
}

func (f *fakeAction) Probe(core.ActionContext) (core.PointState, error) { return f.state, nil }

// revertingAction probes Off no matter what, simulating Defender Tamper
// Protection silently reverting a write (the verify-after → Blocked case).
type revertingAction struct{ level core.Elevation }

func (revertingAction) Level() core.Elevation                { return core.ElevUser }
func (revertingAction) Apply(core.ActionContext, bool) error { return nil }
func (revertingAction) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}
func (revertingAction) Restore(core.ActionContext, core.Backup) error     { return nil }
func (revertingAction) Probe(core.ActionContext) (core.PointState, error) { return core.PointOff, nil }

// errProbeAction returns an error from Probe to exercise the error path.
type errProbeAction struct{}

func (errProbeAction) Level() core.Elevation                            { return core.ElevUser }
func (errProbeAction) Apply(core.ActionContext, bool) error             { return nil }
func (errProbeAction) Snapshot(core.ActionContext) (core.Backup, error) { return core.Backup{}, nil }
func (errProbeAction) Restore(core.ActionContext, core.Backup) error    { return nil }
func (errProbeAction) Probe(core.ActionContext) (core.PointState, error) {
	return core.PointOff, errors.New("probe boom")
}

// noopBroker replaces the real elevation broker with a direct call (no
// impersonation) and records which levels were requested, in order.
func noopBroker(rec *[]elevate.Level) func(elevate.Level, func() error) error {
	return func(lvl elevate.Level, fn func() error) error {
		if rec != nil {
			*rec = append(*rec, lvl)
		}
		return fn()
	}
}

func newTestEngine(store *backup.Store, rec *[]elevate.Level) *Engine {
	e := New(store)
	e.runAs = noopBroker(rec)
	return e
}

// trackingBroker records requested levels and raises `active` for the duration of
// each RunAs closure, so a fake action can prove its Restore/Apply ran inside the
// broker at the right level.
func trackingBroker(rec *[]elevate.Level, active *bool) func(elevate.Level, func() error) error {
	return func(lvl elevate.Level, fn func() error) error {
		if rec != nil {
			*rec = append(*rec, lvl)
		}
		if active != nil {
			*active = true
			defer func() { *active = false }()
		}
		return fn()
	}
}

func TestProbeAggregate(t *testing.T) {
	e := newTestEngine(nil, nil)

	mixed := core.Tweak{Actions: []core.Action{
		&fakeAction{state: core.PointOn}, &fakeAction{state: core.PointOff},
	}}
	if st, _ := e.Probe(mixed); st != core.StatusPartial {
		t.Errorf("mixed Probe = %v want Partial", st)
	}

	allOn := core.Tweak{Actions: []core.Action{
		&fakeAction{state: core.PointOn}, &fakeAction{state: core.PointOn},
	}}
	if st, _ := e.Probe(allOn); st != core.StatusOn {
		t.Errorf("all-on Probe = %v want On", st)
	}

	allOff := core.Tweak{Actions: []core.Action{
		&fakeAction{state: core.PointOff}, &fakeAction{state: core.PointOff},
	}}
	if st, _ := e.Probe(allOff); st != core.StatusOff {
		t.Errorf("all-off Probe = %v want Off", st)
	}

	// absent points excluded: On + Absent → On
	withAbsent := core.Tweak{Actions: []core.Action{
		&fakeAction{state: core.PointOn}, &fakeAction{state: core.PointAbsent},
	}}
	if st, _ := e.Probe(withAbsent); st != core.StatusOn {
		t.Errorf("on+absent Probe = %v want On (absent excluded)", st)
	}

	allAbsent := core.Tweak{Actions: []core.Action{&fakeAction{state: core.PointAbsent}}}
	if st, _ := e.Probe(allAbsent); st != core.StatusAbsent {
		t.Errorf("all-absent Probe = %v want Absent", st)
	}
}

func TestProbeErrorPropagates(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Actions: []core.Action{errProbeAction{}}}
	if _, err := e.Probe(tw); err == nil {
		t.Error("Probe should propagate action probe error")
	}
}

type blockGate struct{}

func (blockGate) Check(core.ActionContext) (bool, core.Status, core.GateAction) {
	return false, core.StatusBlocked, core.GateAction{URL: "windowsdefender://"}
}

type passGate struct{}

func (passGate) Check(core.ActionContext) (bool, core.Status, core.GateAction) {
	return true, core.StatusOff, core.GateAction{}
}

func TestProbeGateBlocks(t *testing.T) {
	e := newTestEngine(nil, nil)
	gated := core.Tweak{Gate: blockGate{}, Actions: []core.Action{&fakeAction{state: core.PointOn}}}
	if st, _ := e.Probe(gated); st != core.StatusBlocked {
		t.Errorf("gated Probe = %v want Blocked", st)
	}
}

func TestApplyGateBlocksSkipsActions(t *testing.T) {
	var applied bool
	e := newTestEngine(nil, nil)
	tw := core.Tweak{
		Gate:    blockGate{},
		Actions: []core.Action{&fakeAction{state: core.PointOff, applied: &applied}},
	}
	st, err := e.Apply(tw, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st != core.StatusBlocked {
		t.Errorf("gated Apply = %v want Blocked", st)
	}
	if applied {
		t.Error("gated Apply must not run actions")
	}
}

func TestApplyHappyPath(t *testing.T) {
	var applied bool
	e := newTestEngine(nil, nil)
	tw := core.Tweak{
		Gate:    passGate{},
		Actions: []core.Action{&fakeAction{state: core.PointOn, applied: &applied}},
	}
	st, err := e.Apply(tw, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !applied {
		t.Error("Apply(on) should call action.Apply(on=true)")
	}
	if st != core.StatusOn {
		t.Errorf("Apply verify-after = %v want On", st)
	}
}

// TestApplyVerifyAfterBlocked is the central verify-after case: an action whose
// write is silently reverted (probes Off after Apply(on)) must report Blocked.
func TestApplyVerifyAfterBlocked(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Actions: []core.Action{revertingAction{}}}
	st, err := e.Apply(tw, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st != core.StatusBlocked {
		t.Errorf("Apply verify-after = %v want Blocked (silent revert)", st)
	}
}

func TestApplyRebootPending(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Reboot: true, Actions: []core.Action{&fakeAction{state: core.PointOn}}}
	st, err := e.Apply(tw, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st != core.StatusRebootPending {
		t.Errorf("reboot tweak Apply = %v want RebootPending", st)
	}
}

func TestApplyErrorPropagates(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Actions: []core.Action{&fakeAction{applyErr: errors.New("boom"), state: core.PointOff}}}
	if _, err := e.Apply(tw, true); err == nil {
		t.Error("Apply should propagate action apply error")
	}
}

func TestApplySnapshotErrorPropagates(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	e := newTestEngine(store, nil)
	tw := core.Tweak{ID: "t.snap", Actions: []core.Action{&fakeAction{snapErr: errors.New("snap boom"), state: core.PointOff}}}
	if _, err := e.Apply(tw, true); err == nil {
		t.Error("Apply should propagate snapshot error")
	}
}

// TestApplyBrokerGroupsByLevel verifies the broker runs one RunAs per distinct
// level, in first-seen order, and groups same-level actions together.
func TestApplyBrokerGroupsByLevel(t *testing.T) {
	var rec []elevate.Level
	e := newTestEngine(nil, &rec)
	tw := core.Tweak{Actions: []core.Action{
		&fakeAction{state: core.PointOn, level: core.ElevSystem},
		&fakeAction{state: core.PointOn, level: core.ElevSystem},
		&fakeAction{state: core.PointOn, level: core.ElevTrustedInstaller},
		&fakeAction{state: core.PointOn, level: core.ElevUser},
		&fakeAction{state: core.PointOn, level: core.ElevAdmin}, // maps to User → already seen
	}}
	if _, err := e.Apply(tw, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []elevate.Level{elevate.System, elevate.TrustedInstaller, elevate.User}
	if len(rec) != len(want) {
		t.Fatalf("broker invoked %d times %v, want %d %v", len(rec), rec, len(want), want)
	}
	for i := range want {
		if rec[i] != want[i] {
			t.Errorf("broker order[%d] = %v want %v", i, rec[i], want[i])
		}
	}
}

func TestLevelOf(t *testing.T) {
	cases := map[core.Elevation]elevate.Level{
		core.ElevUser:             elevate.User,
		core.ElevAdmin:            elevate.User, // admin = already-elevated process, no impersonation
		core.ElevSystem:           elevate.System,
		core.ElevTrustedInstaller: elevate.TrustedInstaller,
	}
	for in, want := range cases {
		if got := levelOf(in); got != want {
			t.Errorf("levelOf(%v) = %v want %v", in, got, want)
		}
	}
}

func TestApplySnapshotsSaveOnce(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	e := newTestEngine(store, nil)
	tw := core.Tweak{ID: "t.x", Actions: []core.Action{&fakeAction{state: core.PointOn, level: core.ElevUser}}}

	if _, err := e.Apply(tw, true); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	// snapshot recorded under the action key
	if _, ok, _ := store.LoadAction(backup.ActionKey("t.x", 0)); !ok {
		t.Fatal("first Apply should have saved a backup")
	}
	// tamper with the stored value, re-apply: save-once must NOT overwrite it
	_ = store.SaveAction(backup.ActionKey("t.x", 0), core.Backup{Existed: true, Value: uint64(999)})
	if _, err := e.Apply(tw, true); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	got, _, _ := store.LoadAction(backup.ActionKey("t.x", 0))
	if v, ok := got.Value.(uint64); !ok || v != 999 {
		t.Errorf("save-once violated: stored value = %v want 999 (unchanged)", got.Value)
	}
}

func TestRollbackRestoresReverseOrderAndDeletes(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	e := newTestEngine(store, nil)

	var r0, r1 bool
	tw := core.Tweak{ID: "t.rb", Actions: []core.Action{
		&fakeAction{state: core.PointOn, restored: &r0},
		&fakeAction{state: core.PointOn, restored: &r1},
	}}

	// snapshot both actions (save-once) via Apply
	if _, err := e.Apply(tw, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	errs := e.Rollback(tw)
	if len(errs) != 0 {
		t.Fatalf("Rollback errs = %v", errs)
	}
	if !r0 || !r1 {
		t.Errorf("both actions should be restored, got r0=%v r1=%v", r0, r1)
	}
	// keys deleted after successful restore
	if _, ok, _ := store.LoadAction(backup.ActionKey("t.rb", 0)); ok {
		t.Error("backup key 0 should be deleted after rollback")
	}
	if _, ok, _ := store.LoadAction(backup.ActionKey("t.rb", 1)); ok {
		t.Error("backup key 1 should be deleted after rollback")
	}
}

func TestRollbackSkipsUnsnapshottedActions(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	e := newTestEngine(store, nil)
	var restored bool
	tw := core.Tweak{ID: "t.none", Actions: []core.Action{&fakeAction{state: core.PointOn, restored: &restored}}}

	// no Apply → no backup recorded
	errs := e.Rollback(tw)
	if len(errs) != 0 {
		t.Fatalf("Rollback with no backups should not error, got %v", errs)
	}
	if restored {
		t.Error("action with no backup must not be restored")
	}
}

func TestRollbackNilStore(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{ID: "t.x", Actions: []core.Action{&fakeAction{state: core.PointOn}}}
	if errs := e.Rollback(tw); len(errs) == 0 {
		t.Error("Rollback with nil store should report an error")
	}
}

// --- FIX 1: per-action verify-after -----------------------------------------

// TestApplyVerifyAfterPerActionOnPlusAbsent: applied on=true, one action sticks
// On and one is Absent (n/a). Every NON-absent action stuck, so this must NOT be
// Blocked. Per the aggregation rule (absent excluded), On+Absent → StatusOn.
func TestApplyVerifyAfterPerActionOnPlusAbsent(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Actions: []core.Action{
		&fakeAction{state: core.PointOn},
		&fakeAction{state: core.PointAbsent},
	}}
	st, err := e.Apply(tw, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st == core.StatusBlocked {
		t.Fatal("on+absent (every non-absent action stuck) must NOT be Blocked")
	}
	if st != core.StatusOn {
		t.Errorf("on+absent verify-after = %v want On (absent excluded)", st)
	}
}

// TestApplyVerifyAfterPerActionOneReverted: applied on=true on two actions; one
// sticks On, the other is silently reverted (probes Off). The reverted action
// must drive StatusBlocked even though the aggregate is only Partial.
func TestApplyVerifyAfterPerActionOneReverted(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Actions: []core.Action{
		&fakeAction{state: core.PointOn},  // stuck
		&fakeAction{state: core.PointOff}, // reverted after Apply(on=true)
	}}
	st, err := e.Apply(tw, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st != core.StatusBlocked {
		t.Errorf("one reverted action verify-after = %v want Blocked", st)
	}
}

// TestApplyOffVerifyAfterRevertBlocked: the OFF direction must be verified too.
// Applied on=false; the action stays On (turning off was silently reverted) →
// Blocked. (The old aggregate code only checked the ON path and missed this.)
func TestApplyOffVerifyAfterRevertBlocked(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Actions: []core.Action{&fakeAction{state: core.PointOn}}}
	st, err := e.Apply(tw, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st != core.StatusBlocked {
		t.Errorf("off-direction silent revert = %v want Blocked", st)
	}
}

func TestApplyOffVerifyAfterClean(t *testing.T) {
	e := newTestEngine(nil, nil)
	tw := core.Tweak{Actions: []core.Action{&fakeAction{state: core.PointOff}}}
	st, err := e.Apply(tw, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st != core.StatusOff {
		t.Errorf("clean off = %v want Off", st)
	}
}

// --- FIX 2: atomic / honest apply -------------------------------------------

// TestApplyMidFailureRollsBackAndHonestStatus: a 2-action tweak where action0
// applies fine but action1 fails. The engine must (a) best-effort roll back the
// already-applied action0, (b) return the action error, and (c) NOT report a
// false-clean StatusOff — it must re-probe for an honest status.
func TestApplyMidFailureRollsBackAndHonestStatus(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	e := newTestEngine(store, nil)

	var r0 bool
	tw := core.Tweak{ID: "t.mid", Actions: []core.Action{
		&fakeAction{state: core.PointOn, restored: &r0, level: core.ElevUser}, // applies, then probes On
		&fakeAction{state: core.PointOff, applyErr: errors.New("apply boom"), level: core.ElevUser},
	}}

	st, err := e.Apply(tw, true)
	if err == nil {
		t.Fatal("mid-apply failure must return the error")
	}
	if !r0 {
		t.Error("already-applied action0 must be rolled back on mid-apply failure")
	}
	// honest status: action0 still probes On, action1 probes Off → aggregate
	// Partial. The key guarantee is it is NOT the hard-coded false-clean Off.
	if st == core.StatusOff {
		t.Errorf("returned status must be honest (re-probed), not hard-coded StatusOff; got %v", st)
	}
	// rolled-back action0's backup key is deleted on successful restore
	if _, ok, _ := store.LoadAction(backup.ActionKey("t.mid", 0)); ok {
		t.Error("rolled-back action0 backup key should be deleted")
	}
}

// --- FIX 3: brokered rollback -----------------------------------------------

// TestRollbackOrderRecorder asserts Restore runs in reverse action order.
func TestRollbackOrderRecorder(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	e := newTestEngine(store, nil)

	var order []string
	tw := core.Tweak{ID: "t.ord", Actions: []core.Action{
		&fakeAction{state: core.PointOn, tag: "a0", restoreOrder: &order, level: core.ElevUser},
		&fakeAction{state: core.PointOn, tag: "a1", restoreOrder: &order, level: core.ElevUser},
		&fakeAction{state: core.PointOn, tag: "a2", restoreOrder: &order, level: core.ElevUser},
	}}
	if _, err := e.Apply(tw, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if errs := e.Rollback(tw); len(errs) != 0 {
		t.Fatalf("Rollback errs = %v", errs)
	}
	want := []string{"a2", "a1", "a0"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("restore order = %v want %v (reverse)", order, want)
	}
}

// TestRollbackBrokeredByLevel asserts each action's Restore runs INSIDE a RunAs
// at the action's level, with level-groups visited in reverse of apply order.
func TestRollbackBrokeredByLevel(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	e := New(store)

	var levels []elevate.Level
	var active bool
	e.runAs = trackingBroker(&levels, &active)

	var in0, in1 bool
	tw := core.Tweak{ID: "t.brk", Actions: []core.Action{
		&fakeAction{state: core.PointOn, level: core.ElevSystem,
			brokerActive: &active, restoredInBroker: &in0},
		&fakeAction{state: core.PointOn, level: core.ElevTrustedInstaller,
			brokerActive: &active, restoredInBroker: &in1},
	}}

	if _, err := e.Apply(tw, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	levels = nil // discard apply-phase broker records; only rollback matters
	if errs := e.Rollback(tw); len(errs) != 0 {
		t.Fatalf("Rollback errs = %v", errs)
	}
	if !in0 || !in1 {
		t.Errorf("both Restores must run inside the broker, got in0=%v in1=%v", in0, in1)
	}
	// apply groups = [System, TI]; rollback visits them in reverse → [TI, System]
	want := []elevate.Level{elevate.TrustedInstaller, elevate.System}
	if len(levels) != len(want) {
		t.Fatalf("rollback broker invoked %v want %v", levels, want)
	}
	for i := range want {
		if levels[i] != want[i] {
			t.Errorf("rollback broker level[%d] = %v want %v", i, levels[i], want[i])
		}
	}
}
