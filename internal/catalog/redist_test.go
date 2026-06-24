package catalog

import (
	"strings"
	"testing"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// isAuthenticode reports whether id is one of the four children whose download is
// Authenticode-verified with NO SHA256 pin: the 2015-2022 evergreen aka.ms
// redirects and the 2013 aka.ms evergreen permalinks.
func isAuthenticode(id string) bool {
	return id == "prep.vcredist.vc2022_x64" || id == "prep.vcredist.vc2022_x86" ||
		id == "prep.vcredist.vc2013_x64" || id == "prep.vcredist.vc2013_x86"
}

// isHex64 reports whether s is exactly 64 lowercase-hex characters (a valid
// SHA256 pin).
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// findChild returns the redistParent child with the given ID (fatal if absent).
func findChild(t *testing.T, id string) core.Tweak {
	t.Helper()
	for _, ch := range redistParent().Children {
		if ch.ID == id {
			return ch
		}
	}
	t.Fatalf("redist child %q not found", id)
	return core.Tweak{}
}

// childDI extracts a child's single DownloadInstall action (fatal otherwise).
func childDI(t *testing.T, ch core.Tweak) action.DownloadInstall {
	t.Helper()
	if len(ch.Actions) != 1 {
		t.Fatalf("child %q: want exactly 1 action, got %d", ch.ID, len(ch.Actions))
	}
	di, ok := ch.Actions[0].(action.DownloadInstall)
	if !ok {
		t.Fatalf("child %q: action is %T, want action.DownloadInstall", ch.ID, ch.Actions[0])
	}
	return di
}

func TestRedistParentShape(t *testing.T) {
	p := redistParent()
	if !p.IsParent() {
		t.Fatal("redist parent must have children")
	}
	if len(p.Actions) != 0 {
		t.Fatal("redist parent must have NO own actions")
	}
	if len(p.Children) != 12 {
		t.Fatalf("want 12 children (6 versions x 2 arch), got %d", len(p.Children))
	}
	for _, ch := range p.Children {
		if !strings.HasPrefix(ch.ID, "prep.vcredist.vc") {
			t.Errorf("child id %q has wrong prefix", ch.ID)
		}
		if len(ch.Actions) != 1 {
			t.Errorf("child %q must have exactly one DownloadInstall action", ch.ID)
		}
		if ch.Category != "prep" {
			t.Errorf("child %q category = %q, want prep", ch.ID, ch.Category)
		}
	}
}

func TestRedistParentRegisteredUnderPrep(t *testing.T) {
	// Build the full catalog the app uses and assert the parent is present.
	cat := Build()
	if _, ok := cat.Find("prep.vcredist"); !ok {
		t.Fatal("prep.vcredist parent not registered in catalog")
	}
	if _, ok := cat.Find("prep.vcredist.vc2022_x64"); !ok {
		t.Fatal("2022 x64 child not findable")
	}
	if _, ok := cat.Find("prep.install_vcredist"); ok {
		t.Fatal("old combined prep.install_vcredist must be removed")
	}
}

// TestRedistVerifyModeInvariant is the verify-mode guard (security critical).
// Across all 12 children: the four Authenticode children (2015-2022 + 2013, all
// aka.ms evergreen) are Authenticode-verified with NO SHA256 pin; every
// SHA-pinned legacy child (2005/2008/2010/2012) must be in VerifySHA256 mode with
// a VALID 64-lowercase-hex digest (real grounded pin). AcceptExit and Elev are
// asserted for every child.
func TestRedistVerifyModeInvariant(t *testing.T) {
	wantAccept := []int{0, 3010, 1638, 1641}
	children := redistParent().Children
	if len(children) != 12 {
		t.Fatalf("want 12 children, got %d", len(children))
	}
	for _, ch := range children {
		di := childDI(t, ch)

		if di.Elev != core.ElevAdmin {
			t.Errorf("child %q: Elev = %v, want ElevAdmin", ch.ID, di.Elev)
		}
		if !equalIntSlice(di.AcceptExit, wantAccept) {
			t.Errorf("child %q: AcceptExit = %v, want %v", ch.ID, di.AcceptExit, wantAccept)
		}

		if isAuthenticode(ch.ID) {
			if di.Verify != action.VerifyAuthenticodeMicrosoft {
				t.Errorf("child %q: Verify = %v, want VerifyAuthenticodeMicrosoft", ch.ID, di.Verify)
			}
			if di.SHA256 != "" {
				t.Errorf("child %q: SHA256 = %q, want empty (evergreen, no pin)", ch.ID, di.SHA256)
			}
			continue
		}

		// SHA-pinned legacy child: must carry a real, valid 64-lowercase-hex pin.
		if di.Verify != action.VerifySHA256 {
			t.Errorf("legacy child %q: Verify = %v, want VerifySHA256", ch.ID, di.Verify)
		}
		if !isHex64(di.SHA256) {
			t.Errorf("legacy child %q: SHA256 %q must be a valid 64 lowercase-hex pin", ch.ID, di.SHA256)
		}
	}
}

// TestRedistDetectViewPerArch guards BUG-1: each arch must probe its OWN registry
// view (x86 lives ONLY under WOW6432Node), each version family the correct key
// path, and 2005/2008 use RegPresent on the MSI Uninstall key.
func TestRedistDetectViewPerArch(t *testing.T) {
	// 2022 x64 — the ONLY native-64 runtime: VC\Runtimes, 14.0, ViewDefault64.
	// Locks the distinction from the legacy x64 keys which live under WOW6432Node.
	rs := mustRegSet(t, childDI(t, findChild(t, "prep.vcredist.vc2022_x64")).Detect, "vc2022_x64")
	if rs.Path != `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64` {
		t.Errorf("vc2022_x64 Detect.Path = %q", rs.Path)
	}
	if rs.View != action.ViewDefault64 {
		t.Errorf("vc2022_x64 Detect.View = %v, want ViewDefault64 (native 64-bit)", rs.View)
	}

	// 2013 x64 — VC\Runtimes; on 64-bit Windows this key lives under WOW6432Node
	// for x64 too, so the view MUST be ViewWow6432 (grounded correction).
	rs = mustRegSet(t, childDI(t, findChild(t, "prep.vcredist.vc2013_x64")).Detect, "vc2013_x64")
	if rs.Path != `SOFTWARE\Microsoft\VisualStudio\12.0\VC\Runtimes\x64` {
		t.Errorf("vc2013_x64 Detect.Path = %q", rs.Path)
	}
	if rs.Value != "Installed" {
		t.Errorf("vc2013_x64 Detect.Value = %q, want Installed", rs.Value)
	}
	if rs.Kind != action.KindDword {
		t.Errorf("vc2013_x64 Detect.Kind = %v, want KindDword", rs.Kind)
	}
	if rs.View != action.ViewWow6432 {
		t.Errorf("vc2013_x64 Detect.View = %v, want ViewWow6432 (legacy x64 keys live under WOW6432Node)", rs.View)
	}

	// 2013 x86 — same family, also the 32-bit WOW6432Node view.
	rs = mustRegSet(t, childDI(t, findChild(t, "prep.vcredist.vc2013_x86")).Detect, "vc2013_x86")
	if rs.View != action.ViewWow6432 {
		t.Errorf("vc2013_x86 Detect.View = %v, want ViewWow6432 (BUG-1: x86 reads WOW6432Node)", rs.View)
	}
	if !strings.HasSuffix(rs.Path, `\12.0\VC\Runtimes\x86`) {
		t.Errorf("vc2013_x86 Detect.Path = %q, want suffix \\12.0\\VC\\Runtimes\\x86", rs.Path)
	}

	// 2010 x64 — VCRedist (NOT Runtimes); also WOW6432Node on 64-bit Windows.
	rs = mustRegSet(t, childDI(t, findChild(t, "prep.vcredist.vc2010_x64")).Detect, "vc2010_x64")
	if !strings.Contains(rs.Path, `\10.0\VC\VCRedist\x64`) {
		t.Errorf("vc2010_x64 Detect.Path = %q, want to contain \\10.0\\VC\\VCRedist\\x64", rs.Path)
	}
	if rs.View != action.ViewWow6432 {
		t.Errorf("vc2010_x64 Detect.View = %v, want ViewWow6432 (legacy x64 keys live under WOW6432Node)", rs.View)
	}

	// 2008 x86 — RegPresent on the MSI Uninstall key, 32-bit view.
	rp := mustRegPresent(t, childDI(t, findChild(t, "prep.vcredist.vc2008_x86")).Detect, "vc2008_x86")
	if rp.Value != "DisplayName" {
		t.Errorf("vc2008_x86 Detect.Value = %q, want DisplayName", rp.Value)
	}
	if rp.View != action.ViewWow6432 {
		t.Errorf("vc2008_x86 Detect.View = %v, want ViewWow6432", rp.View)
	}
	if !strings.HasPrefix(rp.Path, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\`) {
		t.Errorf("vc2008_x86 Detect.Path = %q, want Uninstall\\ prefix", rp.Path)
	}

	// 2008 x64 — same detect kind, but 64-bit view.
	rp = mustRegPresent(t, childDI(t, findChild(t, "prep.vcredist.vc2008_x64")).Detect, "vc2008_x64")
	if rp.View != action.ViewDefault64 {
		t.Errorf("vc2008_x64 Detect.View = %v, want ViewDefault64", rp.View)
	}
}

// TestRedistArgsPerVersion guards the grounded silent-arg correction: the legacy
// installers do NOT all take "/install /quiet /norestart".
func TestRedistArgsPerVersion(t *testing.T) {
	cases := []struct {
		id   string
		want []string
	}{
		{"prep.vcredist.vc2005_x64", []string{"/Q"}},
		{"prep.vcredist.vc2005_x86", []string{"/Q"}},
		{"prep.vcredist.vc2008_x64", []string{"/q"}},
		{"prep.vcredist.vc2008_x86", []string{"/q"}},
		{"prep.vcredist.vc2010_x64", []string{"/quiet", "/norestart"}},
		{"prep.vcredist.vc2012_x86", []string{"/quiet", "/norestart"}},
		{"prep.vcredist.vc2013_x64", []string{"/quiet", "/norestart"}},
		{"prep.vcredist.vc2022_x64", []string{"/install", "/quiet", "/norestart"}},
	}
	for _, c := range cases {
		di := childDI(t, findChild(t, c.id))
		if !equalStrSlice(di.Args, c.want) {
			t.Errorf("%s Args = %v, want %v", c.id, di.Args, c.want)
		}
	}
}

// equalStrSlice reports whether a and b have identical elements in order.
func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mustRegSet type-asserts a Detect to action.RegSet (fatal otherwise).
func mustRegSet(t *testing.T, d core.Action, who string) action.RegSet {
	t.Helper()
	rs, ok := d.(action.RegSet)
	if !ok {
		t.Fatalf("%s: Detect is %T, want action.RegSet", who, d)
	}
	return rs
}

// mustRegPresent type-asserts a Detect to action.RegPresent (fatal otherwise).
func mustRegPresent(t *testing.T, d core.Action, who string) action.RegPresent {
	t.Helper()
	rp, ok := d.(action.RegPresent)
	if !ok {
		t.Fatalf("%s: Detect is %T, want action.RegPresent", who, d)
	}
	return rp
}

// equalIntSlice reports whether a and b have identical elements in order.
func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
