package backup

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// TestActionKey verifies the v2 save-once key format: tweakID#actionIndex.
func TestActionKey(t *testing.T) {
	if got := ActionKey("prep.disable_uac", 0); got != "prep.disable_uac#0" {
		t.Errorf("ActionKey = %q want %q", got, "prep.disable_uac#0")
	}
	if got := ActionKey("defender", 3); got != "defender#3" {
		t.Errorf("ActionKey = %q want %q", got, "defender#3")
	}
}

// TestSaveActionLoadActionRoundTrip verifies a core.Backup survives
// SaveAction->LoadAction intact (metadata + value), keyed by an ActionKey.
func TestSaveActionLoadActionRoundTrip(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), fileName))
	k := ActionKey("t.x", 1)
	want := core.Backup{
		Existed:   true,
		Type:      registry.QWORD,
		Value:     uint64(0xFFFFFFFFFFFFFFFF), // > 2^53: json.Number integrity guard
		Timestamp: time.Now().Truncate(time.Second),
	}
	if err := s.SaveAction(k, want); err != nil {
		t.Fatalf("SaveAction: %v", err)
	}
	got, ok, err := s.LoadAction(k)
	if err != nil {
		t.Fatalf("LoadAction: %v", err)
	}
	if !ok {
		t.Fatal("LoadAction: ok=false, expected backup to exist")
	}
	if got.Existed != want.Existed || got.Type != want.Type {
		t.Fatalf("metadata mismatch: got %+v want %+v", got, want)
	}
	// Consumer contract: action.writeRaw/toU64 require a concrete uint64, NOT a
	// json.Number. Assert the dynamic type directly.
	v, ok := got.Value.(uint64)
	if !ok {
		t.Fatalf("Value type = %T, want uint64 (action layer cannot use json.Number)", got.Value)
	}
	if v != 0xFFFFFFFFFFFFFFFF {
		t.Fatalf("QWORD truncated: got %#x want max_uint64", v)
	}
}

// TestLoadActionValueIsUint64AfterDiskReload is the regression test for the
// json.Number bug: a uint64 backup saved, then reloaded from disk by a FRESH
// Store (forcing readAll's UseNumber decode), must come back as a concrete uint64
// — exactly what the v2 rollback consumer (action.writeRaw/toU64) requires. The
// in-process path keeps Value as uint64; only the disk round-trip exposed the bug.
func TestLoadActionValueIsUint64AfterDiskReload(t *testing.T) {
	cases := []struct {
		name string
		typ  uint32
		v    uint64
	}{
		{"dword", registry.DWORD, uint64(0xDEADBEEF)},
		{"qword_max", registry.QWORD, uint64(0xFFFFFFFFFFFFFFFF)},
		{"qword_above_2pow53", registry.QWORD, uint64(9007194254740993)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), fileName)
			k := ActionKey("reload.id", 0)
			if err := NewAt(path).SaveAction(k, core.Backup{Existed: true, Type: tc.typ, Value: tc.v}); err != nil {
				t.Fatalf("SaveAction: %v", err)
			}
			// Fresh Store → forces a real disk read (readAll UseNumber).
			got, ok, err := NewAt(path).LoadAction(k)
			if err != nil || !ok {
				t.Fatalf("LoadAction after reload: ok=%v err=%v", ok, err)
			}
			v, isU64 := got.Value.(uint64)
			if !isU64 {
				t.Fatalf("after disk reload Value type = %T, want uint64 (action.toU64 rejects json.Number → restore fails)", got.Value)
			}
			if v != tc.v {
				t.Fatalf("value mismatch after reload: got %#x want %#x", v, tc.v)
			}
		})
	}
}

// TestLoadActionMissingKey: an unknown key is not an error; ok=false.
func TestLoadActionMissingKey(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), fileName))
	b, ok, err := s.LoadAction(ActionKey("nope", 0))
	if err != nil {
		t.Fatalf("LoadAction missing key should not error, got %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for missing key, got %+v", b)
	}
}

// TestSaveActionIfAbsentSaveOnce verifies save-once: the first snapshot of an
// action's original value is kept; subsequent toggles must NOT overwrite it.
func TestSaveActionIfAbsentSaveOnce(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), fileName))
	k := ActionKey("t.x", 0)

	first := core.Backup{Existed: true, Type: registry.DWORD, Value: uint64(2)}
	saved, err := s.SaveActionIfAbsent(k, first)
	if err != nil || !saved {
		t.Fatalf("first SaveActionIfAbsent = %v,%v want true,nil", saved, err)
	}

	// second call must NOT overwrite (save-once)
	saved, err = s.SaveActionIfAbsent(k, core.Backup{Existed: true, Value: uint64(99)})
	if err != nil || saved {
		t.Fatalf("second SaveActionIfAbsent = %v,%v want false,nil", saved, err)
	}

	got, ok, _ := s.LoadAction(k)
	if !ok || toUint64(got.Value) != 2 {
		t.Errorf("stored value = %v want original 2", got.Value)
	}
}

// TestDeleteActionClears verifies DeleteAction removes an entry so a later fresh
// apply re-snapshots; deleting a missing key is not an error.
func TestDeleteActionClears(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), fileName))
	k := ActionKey("t.x", 0)

	if err := s.SaveAction(k, core.Backup{Existed: true, Type: registry.DWORD, Value: uint64(5)}); err != nil {
		t.Fatalf("SaveAction: %v", err)
	}
	if err := s.DeleteAction(k); err != nil {
		t.Fatalf("DeleteAction: %v", err)
	}
	if _, ok, err := s.LoadAction(k); err != nil {
		t.Fatalf("LoadAction after delete: %v", err)
	} else if ok {
		t.Fatal("entry should be cleared after DeleteAction")
	}
	// deleting again (missing) is a no-op, not an error
	if err := s.DeleteAction(k); err != nil {
		t.Fatalf("DeleteAction on missing key: %v", err)
	}
}

// TestActionSidecarSurvivesReopen verifies a backup written by one Store is read
// back by a fresh Store on the same path (the on-disk JSON map persists across
// process restarts and the v2 reader normalizes the loaded value to uint64).
func TestActionSidecarSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	s := NewAt(path)
	k := ActionKey("shared.id", 0)
	if err := s.SaveAction(k, core.Backup{Existed: true, Type: registry.DWORD, Value: uint64(7)}); err != nil {
		t.Fatalf("SaveAction: %v", err)
	}
	// A fresh Store on the same path must read it back via the v2 reader.
	s2 := NewAt(path)
	got, ok, err := s2.LoadAction(k)
	if err != nil || !ok {
		t.Fatalf("LoadAction from second store: ok=%v err=%v", ok, err)
	}
	v, isU64 := got.Value.(uint64)
	if !isU64 {
		t.Fatalf("cross-store Value type = %T, want uint64", got.Value)
	}
	if v != 7 {
		t.Fatalf("cross-store value = %v want 7", v)
	}
}
