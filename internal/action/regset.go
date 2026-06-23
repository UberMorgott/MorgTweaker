package action

import (
	"time"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// RegSet toggles a single registry value between On and Off. OffAbsent=true makes
// Apply(false) delete the value instead of writing Off (v1's RegistryTweak as data).
type RegSet struct {
	Root      registry.Key
	Path      string
	Value     string
	Kind      ValueKind
	On        any
	Off       any
	OffAbsent bool
	Elev      core.Elevation
}

func (a RegSet) Level() core.Elevation { return a.Elev }

func (a RegSet) Apply(_ core.ActionContext, on bool) error {
	if !on && a.OffAbsent {
		return deleteRaw(a.Root, a.Path, a.Value)
	}
	v := a.Off
	if on {
		v = a.On
	}
	return writeRaw(a.Root, a.Path, a.Value, a.Kind, v)
}

func (a RegSet) Snapshot(_ core.ActionContext) (core.Backup, error) {
	existed, typ, v, err := readRaw(a.Root, a.Path, a.Value, a.Kind)
	if err != nil {
		return core.Backup{}, err
	}
	return core.Backup{Existed: existed, Type: typ, Value: v, Timestamp: time.Now()}, nil
}

func (a RegSet) Restore(_ core.ActionContext, b core.Backup) error {
	if !b.Existed {
		return deleteRaw(a.Root, a.Path, a.Value)
	}
	return writeRaw(a.Root, a.Path, a.Value, a.Kind, b.Value)
}

func (a RegSet) Probe(_ core.ActionContext) (core.PointState, error) {
	existed, _, v, err := readRaw(a.Root, a.Path, a.Value, a.Kind)
	if err != nil {
		return core.PointOff, err
	}
	if existed && equalRaw(a.Kind, v, a.On) {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}
