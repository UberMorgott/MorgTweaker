package action

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"morgtweaker/internal/core"
)

// TestRunApplyInvokesRunner verifies Apply(on=true) forwards Path+Args to the
// injected runner, and Apply(on=false) is an honest no-op (a one-off command has
// no inverse — it must not pretend to revert by re-running anything).
func TestRunApplyInvokesRunner(t *testing.T) {
	var gotPath string
	var gotArgs []string
	calls := 0
	a := Run{
		Path: "sc", Args: []string{"stop", "Foo"}, Elev: core.ElevAdmin,
		runner: func(_ context.Context, p string, args ...string) error {
			calls++
			gotPath, gotArgs = p, args
			return nil
		},
	}
	if err := a.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply(on): %v", err)
	}
	if gotPath != "sc" || len(gotArgs) != 2 || gotArgs[0] != "stop" || gotArgs[1] != "Foo" {
		t.Errorf("runner got path=%q args=%v want sc [stop Foo]", gotPath, gotArgs)
	}
	if calls != 1 {
		t.Errorf("Apply(on) calls = %d want 1", calls)
	}

	// Apply(off) is an honest no-op: runner must NOT be invoked again.
	if err := a.Apply(core.ActionContext{}, false); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}
	if calls != 1 {
		t.Errorf("Apply(off) should not invoke runner; calls = %d want 1", calls)
	}
}

// TestRunApplyPropagatesError verifies a failing command surfaces its error.
func TestRunApplyPropagatesError(t *testing.T) {
	a := Run{
		Path:   "whatever",
		runner: func(context.Context, string, ...string) error { return errors.New("boom") },
	}
	if err := a.Apply(core.ActionContext{}, true); err == nil {
		t.Error("Apply should propagate runner error")
	}
}

// TestRunSnapshotRestoreNoop asserts the honest contract: Snapshot returns an
// empty (Existed=false) backup and Restore is a no-op — there is no exact inverse
// of an arbitrary one-off command, so we never pretend to capture/revert one.
func TestRunSnapshotRestoreNoop(t *testing.T) {
	a := Run{Path: "sc", runner: func(context.Context, string, ...string) error { return nil }}
	b, err := a.Snapshot(core.ActionContext{})
	if err != nil {
		t.Fatalf("Snapshot err = %v want nil", err)
	}
	if b.Existed {
		t.Errorf("Snapshot.Existed = true want false (honest empty backup)")
	}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Errorf("Restore should be no-op, got %v", err)
	}
	// Restore of an arbitrary (even Existed=true) backup is still a no-op.
	if err := a.Restore(core.ActionContext{}, core.Backup{Existed: true, Value: uint64(1)}); err != nil {
		t.Errorf("Restore(non-empty) should still be no-op, got %v", err)
	}
}

// TestRunProbeDefault asserts a nil ProbeFn means the command is stateless and
// always re-runnable → PointOff (appliable).
func TestRunProbeDefault(t *testing.T) {
	a := Run{Path: "sc"}
	ps, err := a.Probe(core.ActionContext{})
	if err != nil {
		t.Fatalf("Probe err = %v want nil", err)
	}
	if ps != core.PointOff {
		t.Errorf("nil ProbeFn Probe = %v want PointOff", ps)
	}
}

// TestRunProbeDelegates asserts a supplied ProbeFn drives the result.
func TestRunProbeDelegates(t *testing.T) {
	a := Run{Path: "sc", ProbeFn: func() (core.PointState, error) { return core.PointOn, nil }}
	if ps, _ := a.Probe(core.ActionContext{}); ps != core.PointOn {
		t.Errorf("Probe with ProbeFn = %v want PointOn", ps)
	}
}

func TestRunLevel(t *testing.T) {
	if (Run{Elev: core.ElevAdmin}).Level() != core.ElevAdmin {
		t.Error("Level() should echo Elev")
	}
}

// TestRunRealCommandRuns proves the default (real os/exec) runner actually executes
// the command and that the SENTINEL is created BY THE COMMAND, not by the test: the
// test only writes `src`; the spawned `cmd /c copy` is what produces `dst`. We then
// assert dst contains the bytes copied from src.
//
// We deliberately use `copy /y /b <src> <dst>` rather than `echo ok> <dst>`: both
// paths are ordinary argv elements, which Go quotes correctly for spaces, and cmd's
// COPY parses each as a single argument — there is NO redirect parser involved. The
// old `echo ... > path` form depended on cmd re-parsing the joined command line to
// honor the `>`, and would silently break for a TempDir path containing a space
// (Go quotes the path, cmd's redirect parser then mis-splits it). Harmless,
// deterministic, no admin/network.
func TestRunRealCommandRuns(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "ran.txt")
	want := []byte("morgtweaker-ok")
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatalf("sentinel must not exist before the command runs")
	}
	a := Run{
		Path: "cmd",
		Args: []string{"/c", "copy", "/y", "/b", src, dst},
		Elev: core.ElevUser,
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Fatalf("Apply real command: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("sentinel not created, command did not run: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("sentinel contents = %q want %q", got, want)
	}
}

// TestRunContextCancellation proves Apply honors ctx cancellation: a long-running
// command is aborted when the context deadline expires, so Apply returns promptly
// with an error rather than blocking for the full duration.
//
// We exec `ping` DIRECTLY (no nested `cmd /c` shell): CommandContext kills the
// process it spawned, which here IS the long-living process. Running through
// `cmd /c timeout ...` would let CommandContext kill cmd.exe while orphaning the
// timeout.exe grandchild for its full duration. `ping -n 10 127.0.0.1` runs ~9s;
// the 300ms deadline must abort it well before that.
func TestRunContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	a := Run{
		Path: "ping",
		Args: []string{"-n", "10", "127.0.0.1"},
		Elev: core.ElevUser,
	}
	start := time.Now()
	err := a.Apply(core.ActionContext{Ctx: ctx}, true)
	elapsed := time.Since(start)
	if err == nil {
		t.Error("Apply should return an error when context is cancelled mid-command")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Apply took %v; context cancellation did not abort the command", elapsed)
	}
}
