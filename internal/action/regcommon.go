// Package action holds the fixed set of Action executors the engine interprets.
// One file per kind; this file holds registry read/write helpers shared by
// reg.set and reg.delete (ported from v1 internal/tweak/registry.go, same
// WOW64_64KEY masks and ErrNotExist handling).
package action

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// ValueKind is the registry value type an action operates on.
type ValueKind int

const (
	KindDword ValueKind = iota
	KindQword
	KindString
	KindExpandString
)

// 64-bit view explicitly so a 32-bit build never gets redirected into Wow6432Node.
const queryAccess = registry.QUERY_VALUE | registry.WOW64_64KEY
const writeAccess = registry.QUERY_VALUE | registry.SET_VALUE | registry.WOW64_64KEY

// RegView selects which registry view (WOW64 redirection) a read/write targets.
// The zero value is the 64-bit view, preserving every existing call site's
// behavior; ViewWow6432 forces the 32-bit (WOW6432Node) view, needed e.g. to
// detect the VC++ x86 runtime, which registers ONLY under the 32-bit view.
type RegView int

const (
	ViewDefault64 RegView = iota // 64-bit view (registry.WOW64_64KEY)
	ViewWow6432                  // 32-bit view (registry.WOW64_32KEY / WOW6432Node)
)

// wow64Flag maps a RegView to the WOW64 access bit OR-ed into the open mask.
func (v RegView) wow64Flag() uint32 {
	if v == ViewWow6432 {
		return registry.WOW64_32KEY
	}
	return registry.WOW64_64KEY
}

// readRaw returns the current (existed, type, value) of the target value in the
// 64-bit view. existed==false when either the key or the value is missing.
func readRaw(root registry.Key, path, value string, kind ValueKind) (existed bool, regType uint32, v any, err error) {
	return readRawView(root, path, value, kind, ViewDefault64)
}

// readRawView is readRaw with an explicit registry view (64-bit vs WOW6432Node).
func readRawView(root registry.Key, path, value string, kind ValueKind, view RegView) (existed bool, regType uint32, v any, err error) {
	access := registry.QUERY_VALUE | view.wow64Flag()
	k, err := registry.OpenKey(root, path, access)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, 0, nil, nil
		}
		return false, 0, nil, err
	}
	defer k.Close()
	switch kind {
	case KindDword, KindQword:
		n, typ, gerr := k.GetIntegerValue(value)
		if errors.Is(gerr, registry.ErrNotExist) {
			return false, 0, nil, nil
		}
		if gerr != nil {
			return false, 0, nil, gerr
		}
		return true, typ, n, nil
	case KindString, KindExpandString:
		s, typ, gerr := k.GetStringValue(value)
		if errors.Is(gerr, registry.ErrNotExist) {
			return false, 0, nil, nil
		}
		if gerr != nil {
			return false, 0, nil, gerr
		}
		return true, typ, s, nil
	default:
		return false, 0, nil, fmt.Errorf("readRaw: unknown kind %d", kind)
	}
}

// writeRaw creates the key path if needed and sets the value per kind in the
// 64-bit view.
func writeRaw(root registry.Key, path, value string, kind ValueKind, v any) error {
	return writeRawView(root, path, value, kind, v, ViewDefault64)
}

// writeRawView is writeRaw with an explicit registry view.
func writeRawView(root registry.Key, path, value string, kind ValueKind, v any, view RegView) error {
	access := registry.QUERY_VALUE | registry.SET_VALUE | view.wow64Flag()
	k, _, err := registry.CreateKey(root, path, access)
	if err != nil {
		return err
	}
	defer k.Close()
	switch kind {
	case KindDword:
		n, ok := toU64(v)
		if !ok {
			return fmt.Errorf("writeRaw dword: value is %T, want uint64", v)
		}
		return k.SetDWordValue(value, uint32(n))
	case KindQword:
		n, ok := toU64(v)
		if !ok {
			return fmt.Errorf("writeRaw qword: value is %T, want uint64", v)
		}
		return k.SetQWordValue(value, n)
	case KindString:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("writeRaw string: value is %T, want string", v)
		}
		return k.SetStringValue(value, s)
	case KindExpandString:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("writeRaw expand-string: value is %T, want string", v)
		}
		return k.SetExpandStringValue(value, s)
	default:
		return fmt.Errorf("writeRaw: unknown kind %d", kind)
	}
}

// deleteRaw removes the value in the 64-bit view; a missing key/value is not an
// error.
func deleteRaw(root registry.Key, path, value string) error {
	return deleteRawView(root, path, value, ViewDefault64)
}

// deleteRawView is deleteRaw with an explicit registry view.
func deleteRawView(root registry.Key, path, value string, view RegView) error {
	access := registry.QUERY_VALUE | registry.SET_VALUE | view.wow64Flag()
	k, err := registry.OpenKey(root, path, access)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(value); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// equalRaw compares a read-back raw value against a target, normalizing the
// integer representations.
func equalRaw(kind ValueKind, got, want any) bool {
	switch kind {
	case KindDword, KindQword:
		g, gok := toU64(got)
		w, wok := toU64(want)
		return gok && wok && g == w
	default:
		g, gok := got.(string)
		w, wok := want.(string)
		return gok && wok && g == w
	}
}

// toU64 normalizes uint64/uint32 values read back from or written to the registry.
func toU64(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case uint32:
		return uint64(n), true
	default:
		return 0, false
	}
}
