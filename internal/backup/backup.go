// Package backup persists a sidecar JSON snapshot of each action's pre-change raw
// value next to the executable, so any action can be reverted to exactly what was
// there before — including deleting a value we created.
package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"morgtweaker/internal/core"
)

// fileName is the sidecar placed next to the EXE (NOT the cwd).
const fileName = "morgtweaker.backup.json"

// Store is a process-wide, mutex-guarded view of the sidecar file. The whole
// file is loaded, modified and rewritten on each change (load-modify-write),
// which is plenty for a single-process interactive tool.
type Store struct {
	mu   sync.Mutex
	path string
}

// New returns a Store writing to the sidecar next to the running executable.
func New() (*Store, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return NewAt(filepath.Join(filepath.Dir(exe), fileName)), nil
}

// NewAt returns a Store writing to an explicit sidecar path. This is the
// injection seam used by tests (and any caller that needs a non-default
// location); production code uses New, which derives the path from the exe dir.
func NewAt(path string) *Store {
	return &Store{path: path}
}

// readAll loads the whole sidecar; a missing file is an empty map.
func (s *Store) readAll() (map[string]core.Backup, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]core.Backup{}, nil
		}
		return nil, err
	}
	m := map[string]core.Backup{}
	if len(data) == 0 {
		return m, nil
	}
	// UseNumber so a uint64 stored in Backup.Value (any) decodes as json.Number,
	// not float64 — float64 has only 53 mantissa bits and would silently corrupt
	// QWORD values above 2^53 on restore.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeAll persists the whole map atomically (temp file + rename).
func (s *Store) writeAll(m map[string]core.Backup) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ActionKey is the save-once backup key for one action of one tweak.
func ActionKey(tweakID string, actionIndex int) string {
	return tweakID + "#" + strconv.Itoa(actionIndex)
}

// normalize prepares a loaded Backup so it is directly usable by the action layer.
//
// readAll decodes with UseNumber, so an integer Value (DWORD/QWORD) read back
// from disk arrives as json.Number — but the rollback consumer
// (action.Restore -> writeRaw / toU64 in internal/action) accepts ONLY a concrete
// uint64 and rejects json.Number. Without this normalization, every persisted
// DWORD/QWORD restore in a fresh process would fail ("value is json.Number, want
// uint64"). We collapse json.Number to uint64 here (the persistence boundary)
// using the json.Number-aware toUint64 helper, which preserves values above 2^53
// exactly. String values (SZ/EXPAND_SZ) and absent values pass through unchanged.
func normalize(b core.Backup) core.Backup {
	if _, isNum := b.Value.(json.Number); isNum {
		b.Value = toUint64(b.Value)
	}
	return b
}

// SaveAction records (load-modify-write) the backup for one action key,
// overwriting any existing entry. Prefer SaveActionIfAbsent for save-once.
func (s *Store) SaveAction(key string, b core.Backup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readAll()
	if err != nil {
		return err
	}
	m[key] = b
	return s.writeAll(m)
}

// SaveActionIfAbsent records the backup ONLY when no entry for the key exists
// yet, and reports whether it wrote. This is the save-once primitive the engine
// uses before applying an action: the first change captures the user's true
// original value; later toggles must not overwrite it (which would make rollback
// restore the tweaked state instead of the original).
func (s *Store) SaveActionIfAbsent(key string, b core.Backup) (saved bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readAll()
	if err != nil {
		return false, err
	}
	if _, exists := m[key]; exists {
		return false, nil
	}
	m[key] = b
	if err := s.writeAll(m); err != nil {
		return false, err
	}
	return true, nil
}

// LoadAction returns the stored backup for an action key, ok=false when none
// exists. The returned Value is normalized (json.Number collapsed to uint64) so
// it is directly usable by the action layer.
func (s *Store) LoadAction(key string) (core.Backup, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readAll()
	if err != nil {
		return core.Backup{}, false, err
	}
	b, ok := m[key]
	if !ok {
		return core.Backup{}, false, nil
	}
	return normalize(b), true, nil
}

// DeleteAction removes the backup entry for an action key (load-modify-write); a
// missing entry is not an error. The engine calls this after a successful
// rollback so a later fresh apply re-snapshots the (now-restored) original.
func (s *Store) DeleteAction(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readAll()
	if err != nil {
		return err
	}
	if _, exists := m[key]; !exists {
		return nil
	}
	delete(m, key)
	return s.writeAll(m)
}

// toUint64 coerces a JSON-decoded number back to uint64. readAll decodes with
// UseNumber, so a stored uint64 arrives as json.Number (exact); float64 is kept
// as a fallback for hand-edited files.
func toUint64(v any) uint64 {
	switch n := v.(type) {
	case json.Number:
		if u, err := strconv.ParseUint(n.String(), 10, 64); err == nil {
			return u
		}
		if f, err := n.Float64(); err == nil {
			return uint64(f)
		}
		return 0
	case float64:
		return uint64(n)
	case uint64:
		return n
	case int64:
		return uint64(n)
	case int:
		return uint64(n)
	default:
		return 0
	}
}
