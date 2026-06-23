package action

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

func TestServiceStartProbeAbsent(t *testing.T) {
	// Root=HKCU + a Svc subkey path that does not exist → PointAbsent (the v1
	// absent-service rule: missing service key is reported, never an error).
	a := ServiceStart{
		Root:     registry.CURRENT_USER,
		Svc:      `Software\MorgTweakerTest\NoSuchSvc_definitely_missing`,
		OnStart:  4,
		OffStart: 2,
		Elev:     core.ElevSystem,
	}
	ps, err := a.Probe(core.ActionContext{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if ps != core.PointAbsent {
		t.Errorf("missing service Probe = %v want PointAbsent", ps)
	}
}

func TestServiceStartApplyProbeRestore(t *testing.T) {
	base := testKey(t) // creates Software\MorgTweakerTest\<test>
	// seed Start=2 so it looks like a present, OFF service.
	if err := writeRaw(registry.CURRENT_USER, base, "Start", KindDword, uint64(2)); err != nil {
		t.Fatalf("seed Start: %v", err)
	}
	a := ServiceStart{Root: registry.CURRENT_USER, Svc: base, OnStart: 4, OffStart: 2, Elev: core.ElevSystem}
	ctx := core.ActionContext{}

	if ps, _ := a.Probe(ctx); ps != core.PointOff {
		t.Fatal("Start=2 should probe PointOff")
	}
	bak, err := a.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !bak.Existed {
		t.Error("snapshot should record seeded Start as present")
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on): %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOn {
		t.Error("after Apply(on) Start should be 4 → PointOn")
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOff {
		t.Error("after Restore Start should be 2 again → PointOff")
	}
}

func TestServiceStartApplyOff(t *testing.T) {
	base := testKey(t)
	if err := writeRaw(registry.CURRENT_USER, base, "Start", KindDword, uint64(4)); err != nil {
		t.Fatalf("seed Start: %v", err)
	}
	a := ServiceStart{Root: registry.CURRENT_USER, Svc: base, OnStart: 4, OffStart: 2, Elev: core.ElevSystem}
	ctx := core.ActionContext{}

	if ps, _ := a.Probe(ctx); ps != core.PointOn {
		t.Fatal("Start=4 should probe PointOn")
	}
	if err := a.Apply(ctx, false); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}
	existed, _, v, err := readRaw(registry.CURRENT_USER, base, "Start", KindDword)
	if err != nil || !existed {
		t.Fatalf("readRaw after Apply(off): existed=%v err=%v", existed, err)
	}
	if g, ok := toU64(v); !ok || g != 2 {
		t.Errorf("after Apply(off) Start = %v want OffStart 2", v)
	}
}

func TestServiceStartRestoreAbsentNoop(t *testing.T) {
	// When the snapshot recorded an absent Start (Existed:false), Restore must not
	// write a value back (honest: the service key had no Start to begin with).
	base := testKey(t) // key exists but no Start value
	a := ServiceStart{Root: registry.CURRENT_USER, Svc: base, OnStart: 4, OffStart: 2, Elev: core.ElevSystem}
	ctx := core.ActionContext{}

	bak, err := a.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if bak.Existed {
		t.Fatal("snapshot of key without Start should record Existed=false")
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if existed, _, _, _ := readRaw(registry.CURRENT_USER, base, "Start", KindDword); existed {
		t.Error("Restore of an absent backup should not create a Start value")
	}
}

func TestServiceStartApplyAbsentNoop(t *testing.T) {
	// Apply against a non-existent service key must be a no-op: return nil and
	// NOT fabricate the key (v1 never wrote an absent service).
	a := ServiceStart{
		Root:     registry.CURRENT_USER,
		Svc:      `Software\MorgTweakerTest\NoSuchSvc_apply_must_not_create`,
		OnStart:  4,
		OffStart: 2,
		Elev:     core.ElevSystem,
	}
	ctx := core.ActionContext{}

	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on) on absent key = %v want nil", err)
	}
	if exists, err := a.keyExists(); err != nil || exists {
		t.Errorf("Apply(on) must not create the key: exists=%v err=%v", exists, err)
	}
	if ps, err := a.Probe(ctx); err != nil || ps != core.PointAbsent {
		t.Errorf("after no-op Apply, Probe = %v,%v want PointAbsent,nil", ps, err)
	}
}

func TestServiceStartLevel(t *testing.T) {
	if (ServiceStart{Elev: core.ElevTrustedInstaller}).Level() != core.ElevTrustedInstaller {
		t.Error("Level() should echo Elev")
	}
}

// compile-time guarantee ServiceStart implements core.Action.
var _ core.Action = ServiceStart{}
