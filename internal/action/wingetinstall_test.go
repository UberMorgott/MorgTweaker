package action

import (
	"context"
	"errors"
	"testing"

	"morgtweaker/internal/core"
)

// contains reports whether want appears as an element of args.
func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// containsSeq reports whether the two-element sequence flag,val appears
// adjacently in args (e.g. "--id","7zip.7zip").
func containsSeq(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

func TestWingetInstall_ApplyBuildsSilentArgs(t *testing.T) {
	var gotName string
	var gotArgs []string
	a := WingetInstall{
		ID: "7zip.7zip",
		runCode: func(_ context.Context, name string, args ...string) (int, error) {
			gotName = name
			gotArgs = args
			return 0, nil
		},
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if gotName != "winget" {
		t.Errorf("runner name = %q, want %q", gotName, "winget")
	}
	if len(gotArgs) == 0 || gotArgs[0] != "install" {
		t.Errorf("first arg = %v, want install as args[0]", gotArgs)
	}
	if !containsSeq(gotArgs, "--id", "7zip.7zip") {
		t.Errorf("args missing --id 7zip.7zip: %v", gotArgs)
	}
	for _, flag := range []string{"-e", "--silent", "--accept-package-agreements", "--accept-source-agreements"} {
		if !contains(gotArgs, flag) {
			t.Errorf("args missing %q: %v", flag, gotArgs)
		}
	}
	if !containsSeq(gotArgs, "--source", "winget") {
		t.Errorf("args missing --source winget: %v", gotArgs)
	}
	if !containsSeq(gotArgs, "--scope", "machine") {
		t.Errorf("args missing --scope machine: %v", gotArgs)
	}
}

func TestWingetInstall_ApplyAcceptedExitZero(t *testing.T) {
	a := WingetInstall{
		ID:      "7zip.7zip",
		runCode: func(_ context.Context, _ string, _ ...string) (int, error) { return 0, nil },
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Fatalf("exit 0 should be accepted, got error: %v", err)
	}
}

func TestWingetInstall_ApplyAcceptedExitInSet(t *testing.T) {
	// -1978335135 = 0x8A150061 PACKAGE_ALREADY_INSTALLED.
	a := WingetInstall{
		ID:         "7zip.7zip",
		AcceptExit: []int{0, -1978335135},
		runCode:    func(_ context.Context, _ string, _ ...string) (int, error) { return -1978335135, nil },
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Fatalf("accepted exit code should not error, got: %v", err)
	}
}

func TestWingetInstall_ApplyUnacceptedExitErrors(t *testing.T) {
	a := WingetInstall{
		ID:      "7zip.7zip",
		runCode: func(_ context.Context, _ string, _ ...string) (int, error) { return 1, nil },
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err == nil {
		t.Fatal("un-accepted exit code 1 should return an error, got nil")
	}
}

func TestWingetInstall_ApplyOffIsNoOp(t *testing.T) {
	called := false
	a := WingetInstall{
		ID:      "7zip.7zip",
		runCode: func(_ context.Context, _ string, _ ...string) (int, error) { called = true; return 0, nil },
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, false); err != nil {
		t.Fatalf("Apply(off) should be a no-op, got error: %v", err)
	}
	if called {
		t.Error("Apply(off) must NOT invoke the runner")
	}
}

func TestWingetInstall_ProbeExitZeroIsOn(t *testing.T) {
	var gotArgs []string
	a := WingetInstall{
		ID: "7zip.7zip",
		runCode: func(_ context.Context, _ string, args ...string) (int, error) {
			gotArgs = args
			return 0, nil
		},
	}
	st, err := a.Probe(core.ActionContext{Ctx: context.Background()})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if st != core.PointOn {
		t.Errorf("Probe exit 0 = %v, want PointOn", st)
	}
	if len(gotArgs) == 0 || gotArgs[0] != "list" {
		t.Errorf("Probe should run `list`, got args: %v", gotArgs)
	}
	if !containsSeq(gotArgs, "--id", "7zip.7zip") {
		t.Errorf("Probe args missing --id 7zip.7zip: %v", gotArgs)
	}
}

func TestWingetInstall_ProbeNonZeroIsOff(t *testing.T) {
	a := WingetInstall{
		ID:      "7zip.7zip",
		runCode: func(_ context.Context, _ string, _ ...string) (int, error) { return 1, nil },
	}
	st, err := a.Probe(core.ActionContext{Ctx: context.Background()})
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if st != core.PointOff {
		t.Errorf("Probe exit 1 = %v, want PointOff", st)
	}
}

func TestWingetInstall_ProbeRunnerErrorIsOff(t *testing.T) {
	a := WingetInstall{
		ID: "7zip.7zip",
		runCode: func(_ context.Context, _ string, _ ...string) (int, error) {
			return -1, errors.New("winget not found")
		},
	}
	st, err := a.Probe(core.ActionContext{Ctx: context.Background()})
	if err != nil {
		t.Fatalf("Probe should swallow runner error, got: %v", err)
	}
	if st != core.PointOff {
		t.Errorf("Probe runner-error = %v, want PointOff", st)
	}
}

func TestWingetInstall_SkipVerifyAfter(t *testing.T) {
	if !(WingetInstall{}).SkipVerifyAfter() {
		t.Error("SkipVerifyAfter() should be true for a one-shot install")
	}
}
