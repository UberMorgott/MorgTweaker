package catalog

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// TestCatalogValid is the startup-validation test: it walks the assembled catalog
// and asserts the structural invariants every category/tweak must satisfy.
func TestCatalogValid(t *testing.T) {
	cat := Build()
	if len(cat) == 0 {
		t.Fatal("empty catalog")
	}
	seen := map[string]bool{}
	for _, c := range cat {
		if c.ID == "" {
			t.Error("category with empty ID")
		}
		if c.Name.RU == "" || c.Name.EN == "" {
			t.Errorf("category %q missing RU/EN name", c.ID)
		}
		for _, tw := range c.Tweaks {
			if tw.ID == "" {
				t.Errorf("category %q has a tweak with empty ID", c.ID)
			}
			if seen[tw.ID] {
				t.Errorf("duplicate tweak id %q", tw.ID)
			}
			seen[tw.ID] = true
			if !strings.HasPrefix(tw.ID, c.ID+".") {
				t.Errorf("tweak %q not namespaced under category %q", tw.ID, c.ID)
			}
			if tw.Name.RU == "" || tw.Name.EN == "" {
				t.Errorf("tweak %q missing RU/EN name", tw.ID)
			}
			if tw.Desc.RU == "" || tw.Desc.EN == "" {
				t.Errorf("tweak %q missing RU/EN desc", tw.ID)
			}
			if len(tw.Actions) == 0 {
				t.Errorf("tweak %q has no actions", tw.ID)
			}
		}
	}
}

// TestCatalogFindWorks proves a known tweak is reachable via Catalog.Find.
func TestCatalogFindWorks(t *testing.T) {
	if _, ok := Build().Find("prep.disable_uac"); !ok {
		t.Error("expected prep.disable_uac in catalog")
	}
}

// TestKnownTweakRegValues pins a couple of registry paths/values for parity with
// v1 so a future edit cannot silently drift them.
func TestKnownTweakRegValues(t *testing.T) {
	tw, ok := Build().Find("prep.disable_uac")
	if !ok {
		t.Fatal("prep.disable_uac missing")
	}
	rs, ok := tw.Actions[0].(action.RegSet)
	if !ok {
		t.Fatalf("prep.disable_uac action[0] is %T, want action.RegSet", tw.Actions[0])
	}
	if rs.Root != registry.LOCAL_MACHINE {
		t.Errorf("disable_uac root = %v want LOCAL_MACHINE", rs.Root)
	}
	if rs.Path != `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System` {
		t.Errorf("disable_uac path = %q", rs.Path)
	}
	if rs.Value != "EnableLUA" {
		t.Errorf("disable_uac value = %q want EnableLUA", rs.Value)
	}
	if rs.On != uint64(0) || rs.Off != uint64(1) {
		t.Errorf("disable_uac on/off = %v/%v want 0/1", rs.On, rs.Off)
	}

	ex, ok := Build().Find("explorer.show_file_ext")
	if !ok {
		t.Fatal("explorer.show_file_ext missing")
	}
	ers, ok := ex.Actions[0].(action.RegSet)
	if !ok {
		t.Fatalf("show_file_ext action[0] is %T, want action.RegSet", ex.Actions[0])
	}
	if ers.Root != registry.CURRENT_USER {
		t.Errorf("show_file_ext root = %v want CURRENT_USER", ers.Root)
	}
	if ers.Value != "HideFileExt" || ers.On != uint64(0) || ers.Off != uint64(1) {
		t.Errorf("show_file_ext value/on/off = %q/%v/%v want HideFileExt/0/1", ers.Value, ers.On, ers.Off)
	}

	dt, ok := Build().Find("privacy.disable_diagtrack")
	if !ok {
		t.Fatal("privacy.disable_diagtrack missing")
	}
	svc, ok := dt.Actions[0].(action.ServiceStart)
	if !ok {
		t.Fatalf("disable_diagtrack action[0] is %T, want action.ServiceStart", dt.Actions[0])
	}
	if svc.Svc != "DiagTrack" || svc.OnStart != 4 || svc.OffStart != 2 {
		t.Errorf("diagtrack svc/on/off = %q/%v/%v want DiagTrack/4/2", svc.Svc, svc.OnStart, svc.OffStart)
	}
}

// TestDefenderTweakGated proves the Defender tweak carries a non-nil gate and has
// the expected number of action points (6 realtime reg.set + 6 service disables).
func TestDefenderTweakGated(t *testing.T) {
	tw, ok := Build().Find("prep.disable_defender")
	if !ok {
		t.Fatal("prep.disable_defender missing")
	}
	if tw.Gate == nil {
		t.Error("Defender tweak must have a non-nil Gate")
	}
	if len(tw.Actions) != 12 {
		t.Errorf("Defender tweak has %d actions, want 12 (6 reg + 6 svc)", len(tw.Actions))
	}
}

// TestDefenderGateUsesInjectedCache is the anti-lag invariant proved by pointer
// identity: the Defender tweak's gate must wrap the EXACT *TamperCache passed into
// its constructor (not a self-provisioned one), so a full probe over all Defender
// tweaks spawns Get-MpComputerStatus once per TTL, not once per tweak.
//
// action.TamperGate is a comparable value whose only field is its *TamperCache
// pointer, so gate == action.NewTamperGate(tc) holds iff the gate wraps tc. We
// drive the internal defenderTweak(tc) with a KNOWN cache and assert that match —
// proving the wiring with one tweak today and staying correct as more Defender
// tweaks are added (they will all receive the same shared tc from Build()).
func TestDefenderGateUsesInjectedCache(t *testing.T) {
	tc := action.NewTamperCache(nil, 5*time.Second)

	tw := defenderTweak(tc)
	if tw.Gate == nil {
		t.Fatal("Defender tweak has a nil gate")
	}
	tg, ok := tw.Gate.(action.TamperGate)
	if !ok {
		t.Fatalf("Defender gate is %T, want action.TamperGate", tw.Gate)
	}
	if want := action.NewTamperGate(tc); tg != want {
		t.Errorf("Defender gate %+v != NewTamperGate(injected tc) %+v — gate does not wrap the injected cache", tg, want)
	}

	// Negative control: a gate built around a DIFFERENT cache must NOT compare
	// equal, proving the equality above is real pointer identity, not a constant.
	other := action.NewTamperCache(nil, 5*time.Second)
	if tg == action.NewTamperGate(other) {
		t.Error("Defender gate compares equal to a gate around an unrelated cache — pointer identity is not being tested")
	}
}

// TestCatalogGatesAreTamperGates checks that every gated tweak Build() produces is
// an action.TamperGate (so the per-tweak wiring above generalizes across the whole
// assembled catalog as Defender tweaks are added).
func TestCatalogGatesAreTamperGates(t *testing.T) {
	gated := 0
	for _, c := range Build() {
		for _, tw := range c.Tweaks {
			if tw.Gate == nil {
				continue
			}
			if _, ok := tw.Gate.(action.TamperGate); !ok {
				t.Errorf("tweak %q gate is %T, want action.TamperGate", tw.ID, tw.Gate)
			}
			gated++
		}
	}
	if gated == 0 {
		t.Fatal("no gated tweaks found in catalog")
	}
}

var _ = core.StatusOff
