package action

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// testKey creates a throwaway HKCU subkey and returns its path + a cleanup.
func testKey(t *testing.T) string {
	t.Helper()
	path := `Software\MorgTweakerTest\` + t.Name()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("create test key: %v", err)
	}
	k.Close()
	t.Cleanup(func() { registry.DeleteKey(registry.CURRENT_USER, path) })
	return path
}

func TestRegSetApplyProbeRestore(t *testing.T) {
	path := testKey(t)
	a := RegSet{
		Root: registry.CURRENT_USER, Path: path, Value: "Flag",
		Kind: KindDword, On: uint64(1), Off: uint64(0), Elev: core.ElevUser,
	}
	ctx := core.ActionContext{}

	// pre-change: value absent
	if ps, err := a.Probe(ctx); err != nil || ps != core.PointOff {
		t.Fatalf("initial Probe = %v,%v want PointOff,nil", ps, err)
	}
	bak, err := a.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if bak.Existed {
		t.Error("snapshot should record value as absent")
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on): %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOn {
		t.Errorf("after Apply(on) Probe = %v want PointOn", ps)
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOff {
		t.Errorf("after Restore Probe = %v want PointOff (value re-deleted)", ps)
	}
}

func TestRegSetRestorePreexistingValue(t *testing.T) {
	path := testKey(t)
	// seed a pre-existing OFF value so Restore must write it back, not delete.
	if err := writeRaw(registry.CURRENT_USER, path, "Flag", KindDword, uint64(7)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := RegSet{
		Root: registry.CURRENT_USER, Path: path, Value: "Flag",
		Kind: KindDword, On: uint64(1), Off: uint64(0), Elev: core.ElevUser,
	}
	ctx := core.ActionContext{}

	bak, err := a.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !bak.Existed {
		t.Fatal("snapshot should record pre-existing value as present")
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on): %v", err)
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	existed, _, v, err := readRaw(registry.CURRENT_USER, path, "Flag", KindDword)
	if err != nil || !existed {
		t.Fatalf("readRaw after restore: existed=%v err=%v", existed, err)
	}
	if g, ok := toU64(v); !ok || g != 7 {
		t.Errorf("restored value = %v want original 7", v)
	}
}

func TestRegSetOffAbsentDeletes(t *testing.T) {
	path := testKey(t)
	a := RegSet{
		Root: registry.CURRENT_USER, Path: path, Value: "Flag",
		Kind: KindDword, On: uint64(1), OffAbsent: true, Elev: core.ElevUser,
	}
	ctx := core.ActionContext{}

	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on): %v", err)
	}
	if existed, _, _, _ := readRaw(registry.CURRENT_USER, path, "Flag", KindDword); !existed {
		t.Fatal("Apply(on) should have written the value")
	}
	if err := a.Apply(ctx, false); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}
	if existed, _, _, _ := readRaw(registry.CURRENT_USER, path, "Flag", KindDword); existed {
		t.Error("Apply(off) with OffAbsent should have deleted the value")
	}
}

func TestRegSetLevel(t *testing.T) {
	if (RegSet{Elev: core.ElevAdmin}).Level() != core.ElevAdmin {
		t.Error("Level() should echo Elev")
	}
}

// compile-time guarantee RegSet implements core.Action.
var _ core.Action = RegSet{}
