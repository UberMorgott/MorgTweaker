package catalog

import (
	"strings"
	"testing"

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
			if tw.IsParent() {
				// A parent carries the Desc and has NO own actions; validate its
				// children instead. Each child must have a namespaced non-empty ID,
				// a RU/EN name, and at least one action. Children need no Desc (the
				// parent carries it). Child IDs join the duplicate-ID check.
				if len(tw.Actions) != 0 {
					t.Errorf("parent tweak %q must have no own actions", tw.ID)
				}
				for _, ch := range tw.Children {
					if ch.ID == "" {
						t.Errorf("parent %q has a child with empty ID", tw.ID)
					}
					if seen[ch.ID] {
						t.Errorf("duplicate tweak id %q", ch.ID)
					}
					seen[ch.ID] = true
					if !strings.HasPrefix(ch.ID, c.ID+".") {
						t.Errorf("child %q not namespaced under category %q", ch.ID, c.ID)
					}
					if ch.Name.RU == "" || ch.Name.EN == "" {
						t.Errorf("child %q missing RU/EN name", ch.ID)
					}
					if len(ch.Actions) == 0 {
						t.Errorf("child %q has no actions", ch.ID)
					}
				}
				continue
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

// TestDefenderTweakDurable proves the Defender tweak is the DURABLE, reversible
// "off until re-enabled" toggle: a TamperGate (the durable service-key writes are
// reverted by WdFilter while Tamper is on, so the whole tweak is hard-blocked),
// TWO actions (immediate DefenderSuppress + durable DefenderServiceDisable),
// TrustedInstaller elevation, and Reboot:true (full effect after one reboot).
func TestDefenderTweakDurable(t *testing.T) {
	tw, ok := Build().Find("prep.disable_defender")
	if !ok {
		t.Fatal("prep.disable_defender missing")
	}
	if _, ok := tw.Gate.(action.TamperGate); !ok {
		t.Errorf("Defender tweak must carry a TamperGate; got %T", tw.Gate)
	}
	if len(tw.Actions) != 2 {
		t.Fatalf("Defender tweak has %d actions, want 2 (Suppress + ServiceDisable)", len(tw.Actions))
	}
	if _, ok := tw.Actions[0].(action.DefenderSuppress); !ok {
		t.Errorf("Defender action[0] is %T, want action.DefenderSuppress", tw.Actions[0])
	}
	sd, ok := tw.Actions[1].(action.DefenderServiceDisable)
	if !ok {
		t.Fatalf("Defender action[1] is %T, want action.DefenderServiceDisable", tw.Actions[1])
	}
	if sd.Level() != core.ElevTrustedInstaller {
		t.Errorf("DefenderServiceDisable elevation = %v, want ElevTrustedInstaller", sd.Level())
	}
	if tw.Elevation != core.ElevTrustedInstaller {
		t.Errorf("Defender tweak elevation = %v, want ElevTrustedInstaller", tw.Elevation)
	}
	if !tw.Reboot {
		t.Error("durable Defender disable must require a reboot (Reboot:true)")
	}
}

var _ = core.StatusOff
