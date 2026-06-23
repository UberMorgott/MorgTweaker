package action

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

func TestRegDeleteApplyProbeRestore(t *testing.T) {
	path := testKey(t)
	// seed a value so deletion is observable
	if err := writeRaw(registry.CURRENT_USER, path, "Policy", KindDword, uint64(1)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := RegDelete{Root: registry.CURRENT_USER, Path: path, Value: "Policy", Kind: KindDword, Elev: core.ElevUser}
	ctx := core.ActionContext{}

	if ps, _ := a.Probe(ctx); ps != core.PointOff {
		t.Fatal("seeded value should probe PointOff (present)")
	}
	bak, _ := a.Snapshot(ctx)
	if !bak.Existed {
		t.Error("snapshot of seeded value should record Existed")
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOn {
		t.Error("after delete should probe PointOn (absent)")
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOff {
		t.Error("after restore should probe PointOff (present again)")
	}
	existed, _, v, err := readRaw(registry.CURRENT_USER, path, "Policy", KindDword)
	if err != nil || !existed {
		t.Fatalf("readRaw after restore: existed=%v err=%v", existed, err)
	}
	if g, ok := toU64(v); !ok || g != 1 {
		t.Errorf("restored value = %v want original 1", v)
	}
}

func TestRegDeleteApplyOffNoop(t *testing.T) {
	path := testKey(t)
	if err := writeRaw(registry.CURRENT_USER, path, "Policy", KindDword, uint64(1)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := RegDelete{Root: registry.CURRENT_USER, Path: path, Value: "Policy", Kind: KindDword, Elev: core.ElevUser}
	if err := a.Apply(core.ActionContext{}, false); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}
	if existed, _, _, _ := readRaw(registry.CURRENT_USER, path, "Policy", KindDword); !existed {
		t.Error("Apply(off) must leave the value untouched (no inverse)")
	}
}

func TestRegDeleteRestoreAbsentDeletes(t *testing.T) {
	path := testKey(t)
	a := RegDelete{Root: registry.CURRENT_USER, Path: path, Value: "Policy", Kind: KindDword, Elev: core.ElevUser}
	ctx := core.ActionContext{}
	// snapshot when absent → Existed=false
	bak, _ := a.Snapshot(ctx)
	if bak.Existed {
		t.Fatal("snapshot of absent value should not record Existed")
	}
	// write something, then restoring the absent backup should re-delete it
	if err := writeRaw(registry.CURRENT_USER, path, "Policy", KindDword, uint64(5)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if existed, _, _, _ := readRaw(registry.CURRENT_USER, path, "Policy", KindDword); existed {
		t.Error("restoring an absent backup should delete the value")
	}
}

func TestRegDeleteLevel(t *testing.T) {
	if (RegDelete{Elev: core.ElevSystem}).Level() != core.ElevSystem {
		t.Error("Level() should echo Elev")
	}
}

// compile-time guarantee RegDelete implements core.Action.
var _ core.Action = RegDelete{}
