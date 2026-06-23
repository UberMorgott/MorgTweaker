package action

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"morgtweaker/internal/backup"
	"morgtweaker/internal/core"
)

// fakePS is a recording PowerShell runner. It captures every -Command string and
// returns a canned reply chosen by matching a substring of the command, so a test
// can serve a Get-MpPreference JSON while asserting the exact Add/Remove/Set
// commands — all without spawning PowerShell.
type fakePS struct {
	cmds    []string          // every command invoked, in order
	replies map[string][]byte // substring -> stdout for that command
	err     error             // returned for every call (e.g. simulate cmdlet missing)
}

func (f *fakePS) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	// args == [-NoProfile -NonInteractive -Command <cmd>]
	cmd := args[len(args)-1]
	f.cmds = append(f.cmds, cmd)
	if f.err != nil {
		return nil, f.err
	}
	for sub, out := range f.replies {
		if strings.Contains(cmd, sub) {
			return out, nil
		}
	}
	return nil, nil
}

func (f *fakePS) commandsContaining(sub string) []string {
	var out []string
	for _, c := range f.cmds {
		if strings.Contains(c, sub) {
			out = append(out, c)
		}
	}
	return out
}

// newSuppress wires a DefenderSuppress with fixed exe/workdir and the given fake
// runner; tamper cache (if any) shares the same fake.
func newSuppress(f *fakePS, exe, dir string, tc *TamperCache) DefenderSuppress {
	return DefenderSuppress{
		tamper:  tc,
		Elev:    core.ElevAdmin,
		run:     f.run,
		exe:     func() (string, error) { return exe, nil },
		workdir: func() string { return dir },
	}
}

const testExe = `C:\Tools\morgtweaker.exe`
const testDir = `C:\Temp`

// errDefenderOff models the failure Add/Set-MpPreference return when Defender is
// not active (HRESULT 0x800106ba "The service has not been started", surfaced as a
// non-zero PowerShell exit). Used to prove the suppress action is best-effort.
var errDefenderOff = errors.New("exit status 1: 0x800106ba The service cannot be started")

// --- pure command/arg construction --------------------------------------------

func TestPSCommandArgsWrapsFlags(t *testing.T) {
	args := psCommandArgs("Get-Thing")
	want := []string{"-NoProfile", "-NonInteractive", "-Command", "Get-Thing"}
	if len(args) != len(want) {
		t.Fatalf("psCommandArgs = %v want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("psCommandArgs[%d] = %q want %q", i, args[i], want[i])
		}
	}
}

func TestPSQuoteEscapesSingleQuotes(t *testing.T) {
	if got := psQuote(`C:\a'b`); got != `'C:\a''b'` {
		t.Errorf("psQuote = %q want %q", got, `'C:\a''b'`)
	}
}

func TestAddExclusionsCmd(t *testing.T) {
	got := addExclusionsCmd(testExe, testDir)
	if !strings.Contains(got, "Add-MpPreference") ||
		!strings.Contains(got, "-ExclusionProcess '"+testExe+"'") ||
		!strings.Contains(got, "-ExclusionPath '"+testDir+"'") {
		t.Errorf("addExclusionsCmd = %q", got)
	}
}

func TestRemoveExclusionsCmdSelective(t *testing.T) {
	both := removeExclusionsCmd(testExe, testDir, true, true)
	if !strings.Contains(both, "-ExclusionProcess") || !strings.Contains(both, "-ExclusionPath") {
		t.Errorf("remove both = %q", both)
	}
	onlyProc := removeExclusionsCmd(testExe, testDir, true, false)
	if !strings.Contains(onlyProc, "-ExclusionProcess") || strings.Contains(onlyProc, "-ExclusionPath") {
		t.Errorf("remove only proc = %q", onlyProc)
	}
	if removeExclusionsCmd(testExe, testDir, false, false) != "" {
		t.Error("remove nothing should yield empty command")
	}
}

func TestRealtimeCmd(t *testing.T) {
	if realtimeCmd(true) != "Set-MpPreference -DisableRealtimeMonitoring $true" {
		t.Errorf("realtimeCmd(true) = %q", realtimeCmd(true))
	}
	if realtimeCmd(false) != "Set-MpPreference -DisableRealtimeMonitoring $false" {
		t.Errorf("realtimeCmd(false) = %q", realtimeCmd(false))
	}
}

// --- Get-MpPreference parsing -------------------------------------------------

func TestParseMpPrefArrayAndString(t *testing.T) {
	// ExclusionProcess as an array, ExclusionPath as a single string.
	out := []byte(`{"ExclusionProcess":["a.exe","b.exe"],"ExclusionPath":"C:\\Work","DisableRealtimeMonitoring":true}`)
	p := parseMpPref(out, nil)
	if !p.ok {
		t.Fatal("parseMpPref ok=false on valid JSON")
	}
	if len(p.procs) != 2 || p.procs[0] != "a.exe" {
		t.Errorf("procs = %v", p.procs)
	}
	if len(p.paths) != 1 || p.paths[0] != `C:\Work` {
		t.Errorf("paths = %v", p.paths)
	}
	if !p.realtimeDisabled {
		t.Error("realtimeDisabled should be true")
	}
}

func TestParseMpPrefNullAndArrayWrapper(t *testing.T) {
	// null exclusion lists, and the whole object wrapped in a 1-element array.
	out := []byte(`[{"ExclusionProcess":null,"ExclusionPath":null,"DisableRealtimeMonitoring":false}]`)
	p := parseMpPref(out, nil)
	if !p.ok || len(p.procs) != 0 || len(p.paths) != 0 || p.realtimeDisabled {
		t.Errorf("parseMpPref(null/array) = %+v", p)
	}
}

func TestParseMpPrefUnavailable(t *testing.T) {
	for _, c := range []struct {
		name string
		out  []byte
		err  error
	}{
		{"error", nil, context.DeadlineExceeded},
		{"empty", []byte("   "), nil},
		{"garbage", []byte("not json"), nil},
	} {
		if p := parseMpPref(c.out, c.err); p.ok {
			t.Errorf("%s: parseMpPref ok=true, want false (Defender unavailable)", c.name)
		}
	}
}

// --- Probe --------------------------------------------------------------------

func TestProbeOnWhenOurProcessExcluded(t *testing.T) {
	f := &fakePS{replies: map[string][]byte{
		"Get-MpPreference": []byte(`{"ExclusionProcess":["` + jsonEsc(testExe) + `"],"ExclusionPath":null,"DisableRealtimeMonitoring":false}`),
	}}
	a := newSuppress(f, testExe, testDir, nil)
	if ps, _ := a.Probe(core.ActionContext{}); ps != core.PointOn {
		t.Errorf("Probe = %v want PointOn (our exe is excluded)", ps)
	}
}

func TestProbeOffWhenNotExcluded(t *testing.T) {
	f := &fakePS{replies: map[string][]byte{
		"Get-MpPreference": []byte(`{"ExclusionProcess":["other.exe"],"ExclusionPath":null,"DisableRealtimeMonitoring":false}`),
	}}
	a := newSuppress(f, testExe, testDir, nil)
	if ps, _ := a.Probe(core.ActionContext{}); ps != core.PointOff {
		t.Errorf("Probe = %v want PointOff", ps)
	}
}

func TestProbeAbsentWhenDefenderUnavailable(t *testing.T) {
	f := &fakePS{err: context.DeadlineExceeded}
	a := newSuppress(f, testExe, testDir, nil)
	ps, err := a.Probe(core.ActionContext{})
	if err != nil {
		t.Fatalf("Probe should not error when Defender is unavailable, got %v", err)
	}
	if ps != core.PointAbsent {
		t.Errorf("Probe = %v want PointAbsent (graceful)", ps)
	}
}

// --- Apply: exclusions always; realtime only when Tamper off ------------------

func tamperFake(tamperOn bool) (*fakePS, *TamperCache) {
	val := "false"
	if tamperOn {
		val = "true"
	}
	f := &fakePS{replies: map[string][]byte{
		"Get-MpComputerStatus": []byte(`{"AMServiceEnabled":true,"IsTamperProtected":` + val + `}`),
		"Get-MpPreference":     []byte(`{"ExclusionProcess":null,"ExclusionPath":null,"DisableRealtimeMonitoring":false}`),
	}}
	tc := NewTamperCacheCtx(f.run, time.Minute)
	return f, tc
}

func TestApplyAddsExclusionsAndDisablesRealtimeWhenTamperOff(t *testing.T) {
	f, tc := tamperFake(false)
	a := newSuppress(f, testExe, testDir, tc)
	if err := a.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(f.commandsContaining("Add-MpPreference")) != 1 {
		t.Errorf("expected one Add-MpPreference, got %v", f.commandsContaining("Add-MpPreference"))
	}
	if got := f.commandsContaining("Set-MpPreference -DisableRealtimeMonitoring $true"); len(got) != 1 {
		t.Errorf("Tamper off should disable realtime once, got %v", got)
	}
}

func TestApplyAddsExclusionsButSkipsRealtimeWhenTamperOn(t *testing.T) {
	f, tc := tamperFake(true)
	a := newSuppress(f, testExe, testDir, tc)
	if err := a.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(f.commandsContaining("Add-MpPreference")) != 1 {
		t.Error("exclusions must be added even with Tamper ON")
	}
	if got := f.commandsContaining("Set-MpPreference -DisableRealtimeMonitoring $true"); len(got) != 0 {
		t.Errorf("Tamper ON must NOT touch realtime, got %v", got)
	}
}

// --- Snapshot / Restore idempotence ------------------------------------------

func TestSnapshotRecordsPriorState(t *testing.T) {
	// Our exe already excluded, realtime already off => Restore must NOT remove the
	// proc exclusion and must NOT re-enable realtime.
	f := &fakePS{replies: map[string][]byte{
		"Get-MpPreference": []byte(`{"ExclusionProcess":["` + jsonEsc(testExe) + `"],"ExclusionPath":null,"DisableRealtimeMonitoring":true}`),
	}}
	a := newSuppress(f, testExe, testDir, nil)
	b, err := a.Snapshot(core.ActionContext{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !b.Existed {
		t.Error("Existed should be true when our process exclusion already present")
	}
	snap := decodeSnap(b.Value)
	if !snap.ProcAlreadyExcluded || !snap.RealtimeWasOff {
		t.Errorf("snapshot = %+v want ProcAlreadyExcluded & RealtimeWasOff true", snap)
	}
}

func TestRestoreRemovesOnlyOurExclusions(t *testing.T) {
	// Snapshot: NOTHING was excluded before, realtime was on, Tamper nil so no
	// realtime change. Restore should remove BOTH exclusions, and NOT re-enable
	// realtime (we never disabled it: tamper==nil => wouldDisableRealtime false).
	f := &fakePS{}
	a := newSuppress(f, testExe, testDir, nil)
	b := core.Backup{Value: suppressSnap{ProcAlreadyExcluded: false, PathAlreadyExcluded: false, RealtimeWasOff: false}}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rm := f.commandsContaining("Remove-MpPreference")
	if len(rm) != 1 || !strings.Contains(rm[0], "-ExclusionProcess") || !strings.Contains(rm[0], "-ExclusionPath") {
		t.Errorf("Restore should remove both exclusions in one command, got %v", rm)
	}
	if got := f.commandsContaining("Set-MpPreference"); len(got) != 0 {
		t.Errorf("Restore must not touch realtime when we never disabled it, got %v", got)
	}
}

func TestRestoreSkipsPreexistingExclusion(t *testing.T) {
	// Our process exclusion pre-existed => Restore must NOT remove it (only the path).
	f := &fakePS{}
	a := newSuppress(f, testExe, testDir, nil)
	b := core.Backup{Value: suppressSnap{ProcAlreadyExcluded: true, PathAlreadyExcluded: false}}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rm := f.commandsContaining("Remove-MpPreference")
	if len(rm) != 1 || strings.Contains(rm[0], "-ExclusionProcess") || !strings.Contains(rm[0], "-ExclusionPath") {
		t.Errorf("Restore should remove ONLY the path (proc pre-existed), got %v", rm)
	}
}

func TestRestoreReenablesRealtimeWhenWeDisabledIt(t *testing.T) {
	// Realtime was ON before (RealtimeWasOff=false), Tamper OFF => Apply disabled it,
	// so Restore must re-enable realtime ($false).
	f, tc := tamperFake(false)
	a := newSuppress(f, testExe, testDir, tc)
	b := core.Backup{Value: suppressSnap{RealtimeWasOff: false}}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := f.commandsContaining("Set-MpPreference -DisableRealtimeMonitoring $false"); len(got) != 1 {
		t.Errorf("Restore should re-enable realtime once, got %v", got)
	}
}

func TestLevelIsAdmin(t *testing.T) {
	if NewDefenderSuppress(nil, core.ElevAdmin).Level() != core.ElevAdmin {
		t.Error("DefenderSuppress.Level() should be ElevAdmin")
	}
}

// TestRollbackDoesNotEnableRealtimeWhenItWasOff is the invariant guard: if
// realtime was ALREADY off before Apply (RealtimeWasOff=true), Restore must NOT
// re-enable it — never issue Set-MpPreference -DisableRealtimeMonitoring $false.
// (Tamper off here, so the only thing stopping a re-enable is the snapshot.)
func TestRollbackDoesNotEnableRealtimeWhenItWasOff(t *testing.T) {
	f, tc := tamperFake(false)
	a := newSuppress(f, testExe, testDir, tc)
	b := core.Backup{Value: suppressSnap{RealtimeWasOff: true}}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := f.commandsContaining("Set-MpPreference"); len(got) != 0 {
		t.Errorf("realtime was off before Apply; Restore must not touch realtime, got %v", got)
	}
}

// TestApplyOffDoesNotTouchRealtime: a plain Apply(on=false) (no snapshot) must
// only remove exclusions and never issue a realtime Set-MpPreference — realtime
// restoration is routed solely through the snapshot-aware Restore.
func TestApplyOffDoesNotTouchRealtime(t *testing.T) {
	f := &fakePS{}
	a := newSuppress(f, testExe, testDir, nil)
	if err := a.Apply(core.ActionContext{}, false); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}
	if len(f.commandsContaining("Remove-MpPreference")) != 1 {
		t.Error("Apply(off) should remove our exclusions")
	}
	if got := f.commandsContaining("Set-MpPreference"); len(got) != 0 {
		t.Errorf("Apply(off) must NOT touch realtime, got %v", got)
	}
}

// TestApplyNonFatalWhenDefenderNotActive is the BUG-3a fix: when Defender is not
// active, Add/Set-MpPreference fail (0x800106ba "service not running" / non-zero
// exit). Session-suppression is best-effort, so Apply must treat the cmdlet failure
// as "nothing to suppress" and return SUCCESS — never abort the (atomic) Defender
// apply, which would skip the durable Start=4 layer entirely. The Add is still
// attempted (so a merely-tamper-locked-but-alive Defender still gets the exclusion).
func TestApplyNonFatalWhenDefenderNotActive(t *testing.T) {
	f := &fakePS{err: errDefenderOff} // every cmdlet fails, as on a dead Defender
	a := newSuppress(f, testExe, testDir, nil)
	if err := a.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply(on) with Defender not active must be non-fatal, got %v", err)
	}
	if len(f.commandsContaining("Add-MpPreference")) != 1 {
		t.Error("Apply should still attempt Add-MpPreference (works if Defender is merely tamper-locked)")
	}
}

// TestApplyOffNonFatalWhenDefenderNotActive: un-suppress (Apply off) on a dead
// Defender must also succeed — there is no exclusion to remove.
func TestApplyOffNonFatalWhenDefenderNotActive(t *testing.T) {
	f := &fakePS{err: errDefenderOff}
	a := newSuppress(f, testExe, testDir, nil)
	if err := a.Apply(core.ActionContext{}, false); err != nil {
		t.Fatalf("Apply(off) with Defender not active must be non-fatal, got %v", err)
	}
}

// TestRestoreNonFatalWhenDefenderNotActive: rollback on a dead Defender must not
// hard-fail either (nothing to un-exclude / re-enable).
func TestRestoreNonFatalWhenDefenderNotActive(t *testing.T) {
	f, tc := tamperFake(false)
	f.err = errDefenderOff // override: now every call fails
	a := newSuppress(f, testExe, testDir, tc)
	b := core.Backup{Value: suppressSnap{RealtimeWasOff: false}}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Fatalf("Restore with Defender not active must be non-fatal, got %v", err)
	}
}

// TestProbeCaseInsensitiveExePath: Get-MpPreference returns our exe path in a
// DIFFERENT letter case; Probe must still report On (containsFold/EqualFold
// through the real parse path — Windows paths are case-insensitive).
func TestProbeCaseInsensitiveExePath(t *testing.T) {
	f := &fakePS{replies: map[string][]byte{
		"Get-MpPreference": []byte(`{"ExclusionProcess":["` + jsonEsc(strings.ToUpper(testExe)) + `"],"ExclusionPath":null,"DisableRealtimeMonitoring":false}`),
	}}
	a := newSuppress(f, testExe, testDir, nil) // our exe in original (lower) case
	if ps, _ := a.Probe(core.ActionContext{}); ps != core.PointOn {
		t.Errorf("Probe = %v want PointOn (case-insensitive path match)", ps)
	}
}

// TestSnapshotRoundTripThroughStore saves a suppressSnap via the real backup
// store and reloads it, asserting every field survives the JSON/UseNumber
// round-trip (the store decodes Value into a map[string]any). Guards against the
// any/json.Number normalization quirk the store applies to numeric Values.
func TestSnapshotRoundTripThroughStore(t *testing.T) {
	store := backup.NewAt(filepath.Join(t.TempDir(), "b.json"))
	key := backup.ActionKey("prep.disable_defender", 0)
	orig := suppressSnap{ProcAlreadyExcluded: true, PathAlreadyExcluded: false, RealtimeWasOff: true}
	if _, err := store.SaveActionIfAbsent(key, core.Backup{Existed: true, Value: orig}); err != nil {
		t.Fatalf("SaveActionIfAbsent: %v", err)
	}
	got, ok, err := store.LoadAction(key)
	if err != nil || !ok {
		t.Fatalf("LoadAction ok=%v err=%v", ok, err)
	}
	snap := decodeSnap(got.Value) // Value is now a map[string]any after the round-trip
	if snap != orig {
		t.Errorf("round-trip snapshot = %+v want %+v", snap, orig)
	}
}

// jsonEsc escapes backslashes for embedding a Windows path inside a JSON string
// literal in the test fixtures above.
func jsonEsc(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
