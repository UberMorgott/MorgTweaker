package action

import (
	"time"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// RegDelete removes a registry value as its ON state (e.g. clearing a policy).
// Probe reports PointOn when the value is ABSENT. Restore re-creates it from backup.
type RegDelete struct {
	Root  registry.Key
	Path  string
	Value string
	Kind  ValueKind // needed to re-create on Restore
	Elev  core.Elevation
}

func (a RegDelete) Level() core.Elevation { return a.Elev }

func (a RegDelete) Apply(_ core.ActionContext, on bool) error {
	if on {
		return deleteRaw(a.Root, a.Path, a.Value)
	}
	return nil // OFF is "leave it"; real restore happens via Restore(backup)
}

func (a RegDelete) Snapshot(_ core.ActionContext) (core.Backup, error) {
	existed, typ, v, err := readRaw(a.Root, a.Path, a.Value, a.Kind)
	if err != nil {
		return core.Backup{}, err
	}
	return core.Backup{Existed: existed, Type: typ, Value: v, Timestamp: time.Now()}, nil
}

func (a RegDelete) Restore(_ core.ActionContext, b core.Backup) error {
	if !b.Existed {
		return deleteRaw(a.Root, a.Path, a.Value)
	}
	return writeRaw(a.Root, a.Path, a.Value, a.Kind, b.Value)
}

func (a RegDelete) Probe(_ core.ActionContext) (core.PointState, error) {
	existed, _, _, err := readRaw(a.Root, a.Path, a.Value, a.Kind)
	if err != nil {
		return core.PointOff, err
	}
	if existed {
		return core.PointOff, nil // still present → not applied
	}
	return core.PointOn, nil
}
