package action

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

func TestRegPresentProbe(t *testing.T) {
	const sub = `Software\morgtweaker_test\regpresent`
	k, _, err := registry.CreateKey(registry.CURRENT_USER, sub, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("create scratch key: %v", err)
	}
	defer registry.DeleteKey(registry.CURRENT_USER, sub)
	defer k.Close()

	rp := RegPresent{Root: registry.CURRENT_USER, Path: sub, Value: "Marker", Elev: core.ElevUser}

	// Absent value → PointOff.
	if ps, err := rp.Probe(core.ActionContext{}); err != nil || ps != core.PointOff {
		t.Fatalf("absent probe = (%v,%v), want (PointOff,nil)", ps, err)
	}
	// Present value → PointOn.
	if err := k.SetStringValue("Marker", "anything"); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	if ps, err := rp.Probe(core.ActionContext{}); err != nil || ps != core.PointOn {
		t.Fatalf("present probe = (%v,%v), want (PointOn,nil)", ps, err)
	}
}

func TestRegPresentApplyIsNoop(t *testing.T) {
	rp := RegPresent{Root: registry.CURRENT_USER, Path: `Software\nope`, Value: "x"}
	if err := rp.Apply(core.ActionContext{}, true); err != nil {
		t.Fatalf("Apply must be a no-op, got %v", err)
	}
	if _, err := rp.Snapshot(core.ActionContext{}); err != nil {
		t.Fatalf("Snapshot must be a no-op, got %v", err)
	}
	if err := rp.Restore(core.ActionContext{}, core.Backup{}); err != nil {
		t.Fatalf("Restore must be a no-op, got %v", err)
	}
}
