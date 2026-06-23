package action

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// TestRegSetProbeCrossType drives toU64/equalRaw normalization through
// RegSet.Probe: a DWORD read back as uint64 must compare-equal to an On value
// declared as uint32 (and vice-versa), and PointOff when the numbers differ.
func TestRegSetProbeCrossType(t *testing.T) {
	// Direction A: stored uint64 (as registry always returns) vs On declared uint32.
	t.Run("storedU64_vs_onU32", func(t *testing.T) {
		path := testKey(t)
		if err := writeRaw(registry.CURRENT_USER, path, "Flag", KindDword, uint64(1)); err != nil {
			t.Fatalf("seed: %v", err)
		}
		a := RegSet{Root: registry.CURRENT_USER, Path: path, Value: "Flag", Kind: KindDword, On: uint32(1), Elev: core.ElevUser}
		if ps, err := a.Probe(core.ActionContext{}); err != nil || ps != core.PointOn {
			t.Errorf("Probe(stored u64 == on u32) = %v,%v want PointOn,nil", ps, err)
		}
	})

	// Direction B: On declared uint64 compared against a value written via uint32.
	t.Run("storedFromU32_vs_onU64", func(t *testing.T) {
		path := testKey(t)
		if err := writeRaw(registry.CURRENT_USER, path, "Flag", KindDword, uint32(1)); err != nil {
			t.Fatalf("seed: %v", err)
		}
		a := RegSet{Root: registry.CURRENT_USER, Path: path, Value: "Flag", Kind: KindDword, On: uint64(1), Elev: core.ElevUser}
		if ps, err := a.Probe(core.ActionContext{}); err != nil || ps != core.PointOn {
			t.Errorf("Probe(stored via u32 == on u64) = %v,%v want PointOn,nil", ps, err)
		}
	})

	// Genuine mismatch across the boundary → PointOff.
	t.Run("genuineMismatch", func(t *testing.T) {
		path := testKey(t)
		if err := writeRaw(registry.CURRENT_USER, path, "Flag", KindDword, uint64(2)); err != nil {
			t.Fatalf("seed: %v", err)
		}
		a := RegSet{Root: registry.CURRENT_USER, Path: path, Value: "Flag", Kind: KindDword, On: uint32(1), Elev: core.ElevUser}
		if ps, err := a.Probe(core.ActionContext{}); err != nil || ps != core.PointOff {
			t.Errorf("Probe(stored 2 != on 1) = %v,%v want PointOff,nil", ps, err)
		}
	})
}

// TestRegSetStringRoundTrip exercises the KindString path end to end.
func TestRegSetStringRoundTrip(t *testing.T) {
	path := testKey(t)
	a := RegSet{
		Root: registry.CURRENT_USER, Path: path, Value: "Mode",
		Kind: KindString, On: "enabled", Off: "disabled", Elev: core.ElevUser,
	}
	ctx := core.ActionContext{}

	bak, err := a.Snapshot(ctx) // absent before apply
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if bak.Existed {
		t.Error("string snapshot should record absent before apply")
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on): %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOn {
		t.Errorf("after Apply(on) Probe = %v want PointOn", ps)
	}
	existed, _, v, err := readRaw(registry.CURRENT_USER, path, "Mode", KindString)
	if err != nil || !existed {
		t.Fatalf("readRaw: existed=%v err=%v", existed, err)
	}
	if s, ok := v.(string); !ok || s != "enabled" {
		t.Errorf("stored string = %v want %q", v, "enabled")
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if existed, _, _, _ := readRaw(registry.CURRENT_USER, path, "Mode", KindString); existed {
		t.Error("Restore of absent backup should delete the string value")
	}
}

// TestRegSetViewWow6432RoundTrip exercises the BUG-1 seam: a RegSet pinned to the
// 32-bit (WOW6432Node) view reads/writes through readRawView/writeRawView with the
// WOW64_32KEY flag and round-trips correctly. This is the path the VC++ x86 runtime
// detector relies on (the x86 Installed flag exists ONLY in the 32-bit view); the
// old detector read x86 in the 64-bit view and saw it absent. The HKCU test path is
// not WOW64-redirected, so the value is reachable through the pinned view.
func TestRegSetViewWow6432RoundTrip(t *testing.T) {
	path := testKey(t)
	a := RegSet{
		Root: registry.CURRENT_USER, Path: path, Value: "Installed",
		Kind: KindDword, On: uint64(1), Off: uint64(0), Elev: core.ElevUser,
		View: ViewWow6432,
	}
	ctx := core.ActionContext{}

	// A fresh value is absent → the detector must read PointOff, NOT error.
	if ps, err := a.Probe(ctx); err != nil || ps != core.PointOff {
		t.Fatalf("absent Installed via 32-bit view = %v,%v want PointOff,nil", ps, err)
	}
	// Write Installed=1 through the 32-bit view, then it must probe On — mirroring an
	// x86 runtime present only in WOW6432Node reading as installed (not absent).
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on) via 32-bit view: %v", err)
	}
	if ps, err := a.Probe(ctx); err != nil || ps != core.PointOn {
		t.Errorf("Installed=1 via 32-bit view = %v,%v want PointOn,nil", ps, err)
	}
	// And the raw read through the same view confirms the value is really there.
	existed, _, v, err := readRawView(registry.CURRENT_USER, path, "Installed", KindDword, ViewWow6432)
	if err != nil || !existed {
		t.Fatalf("readRawView(32-bit) after Apply: existed=%v err=%v", existed, err)
	}
	if g, ok := toU64(v); !ok || g != 1 {
		t.Errorf("readRawView(32-bit) Installed = %v want 1", v)
	}
}

// TestRegSetQwordRoundTrip exercises the KindQword path end to end.
func TestRegSetQwordRoundTrip(t *testing.T) {
	path := testKey(t)
	// seed a pre-existing OFF qword so Restore must write it back, not delete.
	if err := writeRaw(registry.CURRENT_USER, path, "Limit", KindQword, uint64(100)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := RegSet{
		Root: registry.CURRENT_USER, Path: path, Value: "Limit",
		Kind: KindQword, On: uint64(0xFFFFFFFFF), Off: uint64(100), Elev: core.ElevUser,
	}
	ctx := core.ActionContext{}

	if ps, _ := a.Probe(ctx); ps != core.PointOff {
		t.Fatal("seeded qword (100) should probe PointOff")
	}
	bak, err := a.Snapshot(ctx)
	if err != nil || !bak.Existed {
		t.Fatalf("Snapshot = existed=%v err=%v want existed,nil", bak.Existed, err)
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on): %v", err)
	}
	if ps, _ := a.Probe(ctx); ps != core.PointOn {
		t.Errorf("after Apply(on) Probe = %v want PointOn (0xFFFFFFFFF)", ps)
	}
	if err := a.Restore(ctx, bak); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	existed, _, v, err := readRaw(registry.CURRENT_USER, path, "Limit", KindQword)
	if err != nil || !existed {
		t.Fatalf("readRaw after restore: existed=%v err=%v", existed, err)
	}
	if g, ok := toU64(v); !ok || g != 100 {
		t.Errorf("restored qword = %v want original 100", v)
	}
}
