package catalog

import (
	"strings"
	"testing"
)

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
