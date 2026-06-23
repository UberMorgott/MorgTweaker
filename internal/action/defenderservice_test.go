package action

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"morgtweaker/internal/backup"
	"morgtweaker/internal/core"
)

// --- fakes for the three injectable seams -------------------------------------

// fakeReg is an in-memory Start-DWORD store. writeErr (keyed by svcKeyPath) lets a
// test force a specific write to fail (e.g. ACCESS_DENIED to exercise the
// take-ownership fallback). absent keys report present=false.
type fakeReg struct {
	start    map[string]uint64 // svcKeyPath -> Start (present iff in map)
	readErr  map[string]error
	writeErr map[string]error
	writes   []string // svcKeyPath, in write order
}

func newFakeReg() *fakeReg {
	return &fakeReg{start: map[string]uint64{}, readErr: map[string]error{}, writeErr: map[string]error{}}
}

func (f *fakeReg) readStart(path string) (bool, uint64, error) {
	if err := f.readErr[path]; err != nil {
		return false, 0, err
	}
	v, ok := f.start[path]
	return ok, v, nil
}

func (f *fakeReg) writeStart(path string, start uint64) error {
	if err := f.writeErr[path]; err != nil {
		return err
	}
	f.start[path] = start
	f.writes = append(f.writes, path)
	return nil
}

// fakeOwn records take-ownership / restore calls and serves canned SDDL snapshots.
type fakeOwn struct {
	sddl       map[string]string // svcKeyPath -> snapshot SDDL
	seized     []string          // svcKeyPath, in takeOwnership order
	restored   map[string]string // svcKeyPath -> SDDL written back on restore
	snapErr    map[string]error
	restoreErr map[string]error // svcKeyPath -> error restoreSecurity returns
	takeUnlock map[string]bool  // svcKeyPath -> on takeOwnership, clear the writeErr so the retry write succeeds
	reg        *fakeReg         // so takeOwnership can unlock the paired write
}

func newFakeOwn(reg *fakeReg) *fakeOwn {
	return &fakeOwn{
		sddl: map[string]string{}, restored: map[string]string{},
		snapErr: map[string]error{}, restoreErr: map[string]error{}, takeUnlock: map[string]bool{}, reg: reg,
	}
}

func (f *fakeOwn) snapshotSecurity(path string) (string, error) {
	if err := f.snapErr[path]; err != nil {
		return "", err
	}
	return f.sddl[path], nil
}

func (f *fakeOwn) takeOwnership(path string) error {
	f.seized = append(f.seized, path)
	if f.takeUnlock[path] && f.reg != nil {
		delete(f.reg.writeErr, path) // seizing ownership lets the subsequent write through
	}
	return nil
}

func (f *fakeOwn) restoreSecurity(path, sddl string) error {
	if err := f.restoreErr[path]; err != nil {
		return err
	}
	f.restored[path] = sddl
	return nil
}

// fakeTasks is an in-memory scheduled-task store.
type fakeTasks struct {
	present map[string]bool
	enabled map[string]bool
	sets    []taskSet // every setState, in order
}

type taskSet struct {
	path   string
	enable bool
}

func newFakeTasks() *fakeTasks {
	return &fakeTasks{present: map[string]bool{}, enabled: map[string]bool{}}
}

func (f *fakeTasks) setState(_ core.ActionContext, path string, enable bool) error {
	f.sets = append(f.sets, taskSet{path, enable})
	f.enabled[path] = enable
	return nil
}

func (f *fakeTasks) readState(path string) (bool, bool, error) {
	if !f.present[path] {
		return false, false, nil
	}
	return true, f.enabled[path], nil
}

// newServiceDisable wires a DefenderServiceDisable over the three fakes with a
// fixed, small service/task set so assertions are deterministic.
func newServiceDisable(reg *fakeReg, own *fakeOwn, tasks *fakeTasks, svcs, tsks []string) DefenderServiceDisable {
	return DefenderServiceDisable{
		Elev: core.ElevTrustedInstaller, Services: svcs, Tasks: tsks,
		reg: reg, own: own, runTask: tasks,
	}
}

var errAccessDenied5 = syscall.Errno(5)

// --- command / arg construction ------------------------------------------------

func TestTaskChangeArgs(t *testing.T) {
	dis := taskChangeArgs(`\MS\Defender\Scan`, false)
	want := []string{"/Change", "/TN", `\MS\Defender\Scan`, "/Disable"}
	if len(dis) != len(want) {
		t.Fatalf("disable args = %v want %v", dis, want)
	}
	for i := range want {
		if dis[i] != want[i] {
			t.Errorf("disable arg[%d] = %q want %q", i, dis[i], want[i])
		}
	}
	en := taskChangeArgs(`\MS\Defender\Scan`, true)
	if en[len(en)-1] != "/Enable" {
		t.Errorf("enable args last = %q want /Enable", en[len(en)-1])
	}
}

func TestParseTaskEnabled(t *testing.T) {
	if parseTaskEnabled([]byte("Folder: \\MS\nTaskName: x\nScheduled Task State: Disabled\n")) {
		t.Error("Disabled state should parse as not enabled")
	}
	if !parseTaskEnabled([]byte("Scheduled Task State: Ready\n")) {
		t.Error("Ready state should parse as enabled")
	}
	if !parseTaskEnabled([]byte("Status: Running\n")) {
		t.Error("Running status should parse as enabled")
	}
}

func TestRegSecObjectNamePrefix(t *testing.T) {
	if got := regSecObjectName(svcKeyPath("WdFilter")); got != `MACHINE\SYSTEM\CurrentControlSet\Services\WdFilter` {
		t.Errorf("regSecObjectName = %q", got)
	}
}

// --- Apply: Start=4 on present services, skip absent ---------------------------

func TestApplyDisablesPresentServicesSetsStart4(t *testing.T) {
	reg := newFakeReg()
	reg.start[svcKeyPath("WinDefend")] = 2 // present, auto
	// WdNisSvc absent (not in map)
	own := newFakeOwn(reg)
	tasks := newFakeTasks()
	a := newServiceDisable(reg, own, tasks, []string{"WinDefend", "WdNisSvc"}, []string{`\Defender\Scan`})

	if err := a.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if reg.start[svcKeyPath("WinDefend")] != startDisabled {
		t.Errorf("WinDefend Start = %d want 4", reg.start[svcKeyPath("WinDefend")])
	}
	if _, ok := reg.start[svcKeyPath("WdNisSvc")]; ok {
		t.Error("absent service must NOT be fabricated with a Start value")
	}
	if len(own.seized) != 0 {
		t.Errorf("no take-ownership expected when write succeeds, got %v", own.seized)
	}
	// the scheduled task was disabled.
	if len(tasks.sets) != 1 || tasks.sets[0].enable {
		t.Errorf("task setState = %v want one disable", tasks.sets)
	}
}

// TestIsTaskNotFound is the BUG-3b classifier: schtasks output that signals the
// task does not exist (the "cannot find the path" / "does not exist" wordings, exit
// 1) must classify as not-found so realTaskRunner.setState treats an absent Defender
// task as already-satisfied (success). Unrelated errors must NOT classify.
func TestIsTaskNotFound(t *testing.T) {
	notFound := []string{
		"ERROR: The system cannot find the path specified.",
		"ERROR: The specified task name \"\\X\" does not exist in the system.",
		"cannot find",
	}
	for _, s := range notFound {
		if !isTaskNotFound([]byte(s)) {
			t.Errorf("isTaskNotFound(%q) = false, want true", s)
		}
	}
	other := []string{
		"ERROR: Access is denied.",
		"ERROR: The parameter is incorrect.",
		"",
	}
	for _, s := range other {
		if isTaskNotFound([]byte(s)) {
			t.Errorf("isTaskNotFound(%q) = true, want false (genuine error must propagate)", s)
		}
	}
}

// errTaskRunner is a fake taskRunner that mirrors the real runner's contract: an
// absent task's setState is a no-op success (the real runner swallows the schtasks
// not-found exit), while a present task disable proceeds. notFound lists task paths
// to treat as absent.
type errTaskRunner struct {
	notFound map[string]bool
	sets     []taskSet
	hardErr  map[string]error // task -> a genuine (non-not-found) error to surface
}

func (f *errTaskRunner) setState(_ core.ActionContext, path string, enable bool) error {
	if err := f.hardErr[path]; err != nil {
		return err
	}
	if f.notFound[path] {
		return nil // absent task: already satisfied, like the real runner
	}
	f.sets = append(f.sets, taskSet{path, enable})
	return nil
}

func (f *errTaskRunner) readState(string) (bool, bool, error) { return false, false, nil }

// TestApplySucceedsWhenDefenderAlreadyOff is the BUG-3c net behavior: on a machine
// where Defender is already off/not-running, the Defender tweak's durable layer
// must SUCCEED — Start=4 on present service keys, absent scheduled tasks skipped
// (not-found = success), no hard "exit status 1".
func TestApplySucceedsWhenDefenderAlreadyOff(t *testing.T) {
	reg := newFakeReg()
	reg.start[svcKeyPath("WinDefend")] = 2 // present (Tamper off → write succeeds)
	// WdFilter absent in this scenario (already gutted) — must be skipped.
	own := newFakeOwn(reg)
	tasks := &errTaskRunner{notFound: map[string]bool{`\Defender\Scan`: true}} // task absent
	a := DefenderServiceDisable{
		Elev:     core.ElevTrustedInstaller,
		Services: []string{"WinDefend", "WdFilter"},
		Tasks:    []string{`\Defender\Scan`},
		reg:      reg, own: own, runTask: tasks,
	}
	if err := a.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply with Defender already off must succeed, got %v", err)
	}
	if reg.start[svcKeyPath("WinDefend")] != startDisabled {
		t.Errorf("present WinDefend Start = %d want 4", reg.start[svcKeyPath("WinDefend")])
	}
	if _, ok := reg.start[svcKeyPath("WdFilter")]; ok {
		t.Error("absent WdFilter must not be fabricated")
	}
	if len(tasks.sets) != 0 {
		t.Errorf("absent task must be skipped (not-found = success), got sets %v", tasks.sets)
	}
}

// TestApplyPropagatesGenuineTaskError guards the scope of the not-found tolerance:
// a GENUINE task error (e.g. access denied) must still fail Apply — only not-found
// is swallowed.
func TestApplyPropagatesGenuineTaskError(t *testing.T) {
	reg := newFakeReg()
	reg.start[svcKeyPath("WinDefend")] = 2
	own := newFakeOwn(reg)
	boom := errAccessDenied5
	tasks := &errTaskRunner{hardErr: map[string]error{`\Defender\Scan`: boom}}
	a := DefenderServiceDisable{
		Elev:     core.ElevTrustedInstaller,
		Services: []string{"WinDefend"},
		Tasks:    []string{`\Defender\Scan`},
		reg:      reg, own: own, runTask: tasks,
	}
	if err := a.Apply(core.ActionContext{}, true); err == nil {
		t.Fatal("a genuine task error must fail Apply (only not-found is swallowed)")
	}
}

// --- Apply: take-ownership fallback on ACCESS_DENIED ---------------------------

func TestApplyTakeOwnershipFallbackOnAccessDenied(t *testing.T) {
	reg := newFakeReg()
	path := svcKeyPath("WdFilter")
	reg.start[path] = 0                   // present, boot-start
	reg.writeErr[path] = errAccessDenied5 // first write denied even under TI
	own := newFakeOwn(reg)
	own.takeUnlock[path] = true // seizing ownership clears the write error
	tasks := newFakeTasks()
	a := newServiceDisable(reg, own, tasks, []string{"WdFilter"}, nil)

	if err := a.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply with take-ownership fallback: %v", err)
	}
	if len(own.seized) != 1 || own.seized[0] != path {
		t.Errorf("expected one take-ownership of %s, got %v", path, own.seized)
	}
	if reg.start[path] != startDisabled {
		t.Errorf("WdFilter Start after fallback = %d want 4", reg.start[path])
	}
}

// TestApplyDoesNotSeizeWhenSnapshotSecurityFails (FIX 2): if the key's original
// security cannot be captured (snapshotSecurity errors), Apply MUST NOT take
// ownership — seizing without a restorable snapshot would leave the key
// Administrators-owned/FullControl forever after rollback. The disable hard-fails
// for that key and no ownership is seized.
func TestApplyDoesNotSeizeWhenSnapshotSecurityFails(t *testing.T) {
	reg := newFakeReg()
	path := svcKeyPath("WdFilter")
	reg.start[path] = 0                   // present, boot-start
	reg.writeErr[path] = errAccessDenied5 // plain write denied → would fall back to seize
	own := newFakeOwn(reg)
	own.snapErr[path] = errors.New("GetNamedSecurityInfo denied") // cannot capture original security
	own.takeUnlock[path] = true                                   // would unlock the write IF we seized
	a := newServiceDisable(reg, own, newFakeTasks(), []string{"WdFilter"}, nil)

	err := a.Apply(core.ActionContext{}, true)
	if err == nil {
		t.Fatal("Apply must hard-fail when original security cannot be captured (no restorable snapshot)")
	}
	if len(own.seized) != 0 {
		t.Errorf("ownership must NOT be seized without a restorable snapshot, seized %v", own.seized)
	}
	if reg.start[path] == startDisabled {
		t.Error("Start must not be forced to disabled via an un-restorable seize")
	}
}

// TestRestoreCollectsBothErrors (FIX 3): when writeStart succeeds but
// restoreSecurity fails (or vice-versa), Restore must attempt BOTH and combine the
// errors — the Start revert must not skip/mask the ACL revert. Here both sub-steps
// fail; both errors must surface in the combined result and the OTHER key must
// still be fully reverted.
func TestRestoreCollectsBothErrors(t *testing.T) {
	reg := newFakeReg()
	own := newFakeOwn(reg)
	wd := svcKeyPath("WinDefend")
	wf := svcKeyPath("WdFilter")
	writeBoom := errors.New("write boom")
	aclBoom := errors.New("acl boom")
	reg.writeErr[wd] = writeBoom // WinDefend Start write fails
	own.restoreErr[wd] = aclBoom // AND its ACL restore fails — both must surface
	a := newServiceDisable(reg, own, newFakeTasks(), []string{"WinDefend", "WdFilter"}, nil)

	snap := serviceSnap{Services: map[string]svcSnap{
		"WinDefend": {Present: true, Start: 2, OwnerSDDL: "O:BAD:(A;;KA;;;BA)"},
		"WdFilter":  {Present: true, Start: 0, OwnerSDDL: "O:SYD:(A;;KA;;;SY)"},
	}}
	err := a.Restore(core.ActionContext{}, core.Backup{Value: snap})
	if err == nil {
		t.Fatal("Restore must return an error when sub-steps fail")
	}
	if !errors.Is(err, writeBoom) {
		t.Error("combined error must include the Start-write failure")
	}
	if !errors.Is(err, aclBoom) {
		t.Error("combined error must include the ACL-restore failure (not masked by the write error)")
	}
	// The ACL restore was ATTEMPTED for WinDefend even though its Start write failed
	// (the failure did not short-circuit the key) — and WdFilter is fully reverted.
	if reg.start[wf] != 0 {
		t.Errorf("WdFilter Start should still be reverted to 0, got %d", reg.start[wf])
	}
	if own.restored[wf] != "O:SYD:(A;;KA;;;SY)" {
		t.Errorf("WdFilter ACL should still be reverted, got %q", own.restored[wf])
	}
}

// --- Snapshot captures Start + SDDL + task state; round-trips through store ----

func TestSnapshotCapturesStateAndRoundTrips(t *testing.T) {
	reg := newFakeReg()
	reg.start[svcKeyPath("WinDefend")] = 2
	reg.start[svcKeyPath("WdFilter")] = 0
	own := newFakeOwn(reg)
	own.sddl[svcKeyPath("WinDefend")] = "O:BAD:(A;;KA;;;BA)"
	own.sddl[svcKeyPath("WdFilter")] = "O:SYD:(A;;KA;;;SY)"
	tasks := newFakeTasks()
	tasks.present[`\Defender\Scan`] = true
	tasks.enabled[`\Defender\Scan`] = true
	svcs := []string{"WinDefend", "WdFilter", "WdNisSvc"} // last absent
	a := newServiceDisable(reg, own, tasks, svcs, []string{`\Defender\Scan`})

	bak, err := a.Snapshot(core.ActionContext{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Round-trip through the REAL backup store (Value becomes map[string]any).
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	key := backup.ActionKey("prep.disable_defender", 1)
	if _, err := store.SaveActionIfAbsent(key, bak); err != nil {
		t.Fatalf("SaveActionIfAbsent: %v", err)
	}
	got, ok, err := store.LoadAction(key)
	if err != nil || !ok {
		t.Fatalf("LoadAction ok=%v err=%v", ok, err)
	}
	snap := decodeServiceSnap(got.Value)

	wd := snap.Services["WinDefend"]
	if !wd.Present || wd.Start != 2 || wd.OwnerSDDL != "O:BAD:(A;;KA;;;BA)" {
		t.Errorf("WinDefend snap = %+v want present/start2/sddl", wd)
	}
	wf := snap.Services["WdFilter"]
	if !wf.Present || wf.Start != 0 || wf.OwnerSDDL != "O:SYD:(A;;KA;;;SY)" {
		t.Errorf("WdFilter snap = %+v", wf)
	}
	if snap.Services["WdNisSvc"].Present {
		t.Error("absent WdNisSvc must snapshot Present=false")
	}
	ts := snap.Tasks[`\Defender\Scan`]
	if !ts.Present || !ts.Enabled {
		t.Errorf("task snap = %+v want present/enabled", ts)
	}
}

// --- Restore: exact Start + ACL + task round-trip ------------------------------

func TestRestoreRevertsStartAclAndTasks(t *testing.T) {
	reg := newFakeReg()
	own := newFakeOwn(reg)
	tasks := newFakeTasks()
	svcs := []string{"WinDefend", "WdFilter", "WdNisSvc"}
	a := newServiceDisable(reg, own, tasks, svcs, []string{`\Defender\Scan`, `\Defender\Cleanup`})

	// Snapshot: WinDefend present start=2 sddl A, WdFilter present start=0 sddl B,
	// WdNisSvc absent. Task Scan was enabled; Cleanup was disabled.
	snap := serviceSnap{
		Services: map[string]svcSnap{
			"WinDefend": {Present: true, Start: 2, OwnerSDDL: "O:BAD:(A;;KA;;;BA)"},
			"WdFilter":  {Present: true, Start: 0, OwnerSDDL: "O:SYD:(A;;KA;;;SY)"},
			"WdNisSvc":  {Present: false},
		},
		Tasks: map[string]taskSnap{
			`\Defender\Scan`:    {Present: true, Enabled: true},
			`\Defender\Cleanup`: {Present: true, Enabled: false},
		},
	}
	if err := a.Restore(core.ActionContext{}, core.Backup{Value: snap}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if reg.start[svcKeyPath("WinDefend")] != 2 {
		t.Errorf("WinDefend Start restored = %d want 2", reg.start[svcKeyPath("WinDefend")])
	}
	if reg.start[svcKeyPath("WdFilter")] != 0 {
		t.Errorf("WdFilter Start restored = %d want 0", reg.start[svcKeyPath("WdFilter")])
	}
	if _, ok := reg.start[svcKeyPath("WdNisSvc")]; ok {
		t.Error("absent service must not get a Start on restore")
	}
	// ACL restored exactly for both present keys.
	if own.restored[svcKeyPath("WinDefend")] != "O:BAD:(A;;KA;;;BA)" {
		t.Errorf("WinDefend ACL restore = %q", own.restored[svcKeyPath("WinDefend")])
	}
	if own.restored[svcKeyPath("WdFilter")] != "O:SYD:(A;;KA;;;SY)" {
		t.Errorf("WdFilter ACL restore = %q", own.restored[svcKeyPath("WdFilter")])
	}
	// Only the originally-enabled task is re-enabled; the originally-disabled one is left.
	var reEnabled []string
	for _, s := range tasks.sets {
		if s.enable {
			reEnabled = append(reEnabled, s.path)
		}
	}
	if len(reEnabled) != 1 || reEnabled[0] != `\Defender\Scan` {
		t.Errorf("re-enabled tasks = %v want only \\Defender\\Scan", reEnabled)
	}
}

// --- full snapshot -> apply -> restore round-trip lands back at original --------

func TestApplyRestoreRoundTripExact(t *testing.T) {
	reg := newFakeReg()
	reg.start[svcKeyPath("WinDefend")] = 2
	reg.start[svcKeyPath("WdFilter")] = 0
	own := newFakeOwn(reg)
	own.sddl[svcKeyPath("WinDefend")] = "O:BAD:(A;;KA;;;BA)"
	own.sddl[svcKeyPath("WdFilter")] = "O:SYD:(A;;KA;;;SY)"
	tasks := newFakeTasks()
	tasks.present[`\Defender\Scan`] = true
	tasks.enabled[`\Defender\Scan`] = true
	a := newServiceDisable(reg, own, tasks, []string{"WinDefend", "WdFilter"}, []string{`\Defender\Scan`})
	ctx := core.ActionContext{}

	bak, err := a.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOn {
		t.Errorf("after Apply Probe = %v want PointOn (all present services Start=4)", ps)
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if reg.start[svcKeyPath("WinDefend")] != 2 || reg.start[svcKeyPath("WdFilter")] != 0 {
		t.Errorf("Start not restored exactly: WinDefend=%d WdFilter=%d",
			reg.start[svcKeyPath("WinDefend")], reg.start[svcKeyPath("WdFilter")])
	}
	if !tasks.enabled[`\Defender\Scan`] {
		t.Error("task should be re-enabled after restore")
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOff {
		t.Errorf("after Restore Probe = %v want PointOff", ps)
	}
}

// --- idempotent rollback: running Restore twice is safe ------------------------

func TestRestoreIdempotent(t *testing.T) {
	reg := newFakeReg()
	own := newFakeOwn(reg)
	tasks := newFakeTasks()
	a := newServiceDisable(reg, own, tasks, []string{"WinDefend"}, []string{`\Defender\Scan`})
	snap := serviceSnap{
		Services: map[string]svcSnap{"WinDefend": {Present: true, Start: 2, OwnerSDDL: "O:BAD:"}},
		Tasks:    map[string]taskSnap{`\Defender\Scan`: {Present: true, Enabled: true}},
	}
	b := core.Backup{Value: snap}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Fatalf("Restore #1: %v", err)
	}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Fatalf("Restore #2 (idempotent): %v", err)
	}
	if reg.start[svcKeyPath("WinDefend")] != 2 {
		t.Errorf("Start after double restore = %d want 2", reg.start[svcKeyPath("WinDefend")])
	}
}

// --- Probe: On when all disabled, Off when any not, Absent when none present ---

func TestProbeStates(t *testing.T) {
	mk := func(starts map[string]uint64) DefenderServiceDisable {
		reg := newFakeReg()
		for k, v := range starts {
			reg.start[svcKeyPath(k)] = v
		}
		return newServiceDisable(reg, newFakeOwn(reg), newFakeTasks(), []string{"WinDefend", "WdFilter"}, nil)
	}
	// all present + disabled => On
	if ps, _ := mk(map[string]uint64{"WinDefend": 4, "WdFilter": 4}).Probe(core.ActionContext{}); ps != core.PointOn {
		t.Errorf("all-disabled Probe = %v want PointOn", ps)
	}
	// one not disabled => Off
	if ps, _ := mk(map[string]uint64{"WinDefend": 4, "WdFilter": 2}).Probe(core.ActionContext{}); ps != core.PointOff {
		t.Errorf("mixed Probe = %v want PointOff", ps)
	}
	// none present => Absent (3rd-party AV / gutted build), graceful, no error
	if ps, err := mk(map[string]uint64{}).Probe(core.ActionContext{}); err != nil || ps != core.PointAbsent {
		t.Errorf("no-services Probe = %v,%v want PointAbsent,nil", ps, err)
	}
}

// TestProbeGracefulOnReadError: a per-key read failure (ACL-blocked) must not
// crash or error — the key is skipped and the rest still aggregate.
func TestProbeGracefulOnReadError(t *testing.T) {
	reg := newFakeReg()
	reg.start[svcKeyPath("WinDefend")] = 4
	reg.readErr[svcKeyPath("WdFilter")] = errAccessDenied5
	a := newServiceDisable(reg, newFakeOwn(reg), newFakeTasks(), []string{"WinDefend", "WdFilter"}, nil)
	ps, err := a.Probe(core.ActionContext{})
	if err != nil {
		t.Fatalf("Probe should not error on a per-key read failure: %v", err)
	}
	if ps != core.PointOn { // WinDefend disabled, WdFilter skipped
		t.Errorf("Probe = %v want PointOn (failed key skipped)", ps)
	}
}

// --- Apply(off): re-enables tasks, never guesses a Start -----------------------

func TestApplyOffReenablesTasksOnly(t *testing.T) {
	reg := newFakeReg()
	reg.start[svcKeyPath("WinDefend")] = 4 // disabled
	a := newServiceDisable(reg, newFakeOwn(reg), newFakeTasks(), []string{"WinDefend"}, []string{`\Defender\Scan`})
	tasks := a.runTask.(*fakeTasks)

	if err := a.Apply(core.ActionContext{}, false); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}
	if len(reg.writes) != 0 {
		t.Errorf("Apply(off) must NOT write any Start (exact reversal is Restore's job), wrote %v", reg.writes)
	}
	if len(tasks.sets) != 1 || !tasks.sets[0].enable {
		t.Errorf("Apply(off) should re-enable the task, got %v", tasks.sets)
	}
}

func TestIsAccessDeniedErr(t *testing.T) {
	if !isAccessDeniedErr(syscall.Errno(5)) {
		t.Error("Errno(5) should be access-denied")
	}
	if isAccessDeniedErr(nil) || isAccessDeniedErr(syscall.Errno(2)) {
		t.Error("nil / Errno(2) should not be access-denied")
	}
}

func TestDefenderServiceDisableLevel(t *testing.T) {
	if NewDefenderServiceDisable(core.ElevTrustedInstaller).Level() != core.ElevTrustedInstaller {
		t.Error("Level() should echo Elev")
	}
}

// --- gate on Tamper blocks the whole tweak (deep-link) -------------------------

// TestTamperGateBlocksDefenderTweak proves the gate this tweak carries short-
// circuits to StatusBlocked with the windowsdefender:// deep-link while Tamper
// Protection is ON — the durable service-key writes are reverted by WdFilter under
// Tamper, so the user must turn Tamper off and retry.
func TestTamperGateBlocksDefenderTweak(t *testing.T) {
	g := NewTamperGate(NewTamperCache(func(string, ...string) ([]byte, error) {
		return []byte(`{"AMServiceEnabled":true,"IsTamperProtected":true}`), nil
	}, time.Minute))
	ok, st, act := g.Check(core.ActionContext{})
	if ok || st != core.StatusBlocked {
		t.Errorf("Tamper on -> gate = %v,%v want false,Blocked", ok, st)
	}
	if act.URL != "windowsdefender://threatsettings" {
		t.Errorf("blocked gate URL = %q want windowsdefender://threatsettings", act.URL)
	}
}
