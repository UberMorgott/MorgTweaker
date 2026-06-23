package catalog

import (
	"testing"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// TestVcRuntimeDetectRegistry pins the registry path/value the probe reads for
// each arch (missing key reads as Off via RegSet.Probe).
func TestVcRuntimeDetectRegistry(t *testing.T) {
	for _, arch := range []string{"x64", "x86"} {
		rs := vcRuntimeDetect(arch)
		wantPath := `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\` + arch
		if rs.Path != wantPath {
			t.Errorf("%s detect path = %q, want %q", arch, rs.Path, wantPath)
		}
		if rs.Value != "Installed" {
			t.Errorf("%s detect value = %q, want Installed", arch, rs.Value)
		}
		if rs.Kind != action.KindDword || rs.On != uint64(1) {
			t.Errorf("%s detect kind/on = %v/%v, want Dword/1", arch, rs.Kind, rs.On)
		}
	}
}

// TestVcRuntimeDetectView pins the WOW64 view per arch. This is the BUG-1 fix: the
// VC++ x64 runtime registers under the 64-bit view (...\Runtimes\x64) while the x86
// runtime registers ONLY under the 32-bit view (WOW6432Node ...\Runtimes\x86) and
// is ABSENT from the 64-bit view. Probing x86 in the 64-bit view (the old behavior)
// read absent → false "not installed" → engine verify-after flipped a successful
// install to StatusBlocked. x64 must pin ViewDefault64, x86 must pin ViewWow6432.
func TestVcRuntimeDetectView(t *testing.T) {
	if v := vcRuntimeDetect("x64").View; v != action.ViewDefault64 {
		t.Errorf("x64 detect view = %v, want ViewDefault64 (64-bit view)", v)
	}
	if v := vcRuntimeDetect("x86").View; v != action.ViewWow6432 {
		t.Errorf("x86 detect view = %v, want ViewWow6432 (32-bit/WOW6432Node view)", v)
	}
}

// TestInstallVcredistTweak verifies the rewired tweak: two DownloadInstall
// actions (x64 then x86), each in Authenticode/Microsoft verify mode with the
// VC++ accepted exit set, /install /quiet /norestart args, admin elevation, and
// reboot=false.
func TestInstallVcredistTweak(t *testing.T) {
	tw, ok := Build().Find("prep.install_vcredist")
	if !ok {
		t.Fatal("prep.install_vcredist missing")
	}
	if tw.Elevation != core.ElevAdmin {
		t.Errorf("elevation = %v, want ElevAdmin", tw.Elevation)
	}
	if tw.Reboot {
		t.Error("tweak uses /norestart, must not require reboot")
	}
	if len(tw.Actions) != 2 {
		t.Fatalf("got %d actions, want 2 (x64 then x86)", len(tw.Actions))
	}
	wantURLs := []string{vcredistX64URL, vcredistX86URL}
	wantArgs := []string{"/install", "/quiet", "/norestart"}
	wantExit := []int{0, 3010, 1638, 1641}
	for i, act := range tw.Actions {
		di, ok := act.(action.DownloadInstall)
		if !ok {
			t.Fatalf("action[%d] is %T, want action.DownloadInstall", i, act)
		}
		if di.URL != wantURLs[i] {
			t.Errorf("action[%d] URL = %q, want %q", i, di.URL, wantURLs[i])
		}
		if di.Verify != action.VerifyAuthenticodeMicrosoft {
			t.Errorf("action[%d] Verify = %v, want VerifyAuthenticodeMicrosoft", i, di.Verify)
		}
		if di.SHA256 != "" {
			t.Errorf("action[%d] must NOT carry a SHA256 pin in Authenticode mode, got %q", i, di.SHA256)
		}
		if len(di.Args) != len(wantArgs) {
			t.Errorf("action[%d] args = %v, want %v", i, di.Args, wantArgs)
		} else {
			for j := range wantArgs {
				if di.Args[j] != wantArgs[j] {
					t.Errorf("action[%d] arg[%d] = %q, want %q", i, j, di.Args[j], wantArgs[j])
				}
			}
		}
		if len(di.AcceptExit) != len(wantExit) {
			t.Errorf("action[%d] AcceptExit = %v, want %v", i, di.AcceptExit, wantExit)
		} else {
			for j := range wantExit {
				if di.AcceptExit[j] != wantExit[j] {
					t.Errorf("action[%d] AcceptExit[%d] = %d, want %d", i, j, di.AcceptExit[j], wantExit[j])
				}
			}
		}
		if di.Elev != core.ElevAdmin {
			t.Errorf("action[%d] Elev = %v, want ElevAdmin", i, di.Elev)
		}
		// FIX 1: each action's Detect is its OWN arch's runtime (per-action
		// verify-after must not re-probe the other arch, which may not be installed
		// yet). x64 action → x64 runtime in the 64-bit view; x86 → x86 in WOW6432Node.
		rs, ok := di.Detect.(action.RegSet)
		if !ok {
			t.Fatalf("action[%d] Detect is %T, want action.RegSet (per-arch runtime)", i, di.Detect)
		}
		wantArch := []string{"x64", "x86"}[i]
		wantView := []action.RegView{action.ViewDefault64, action.ViewWow6432}[i]
		if wantPath := `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\` + wantArch; rs.Path != wantPath {
			t.Errorf("action[%d] Detect path = %q, want %q (per-arch)", i, rs.Path, wantPath)
		}
		if rs.View != wantView {
			t.Errorf("action[%d] Detect view = %v, want %v (per-arch view)", i, rs.View, wantView)
		}
	}
}
