package action

import (
	"time"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// RegSet toggles a single registry value between On and Off. OffAbsent=true makes
// Apply(false) delete the value instead of writing Off (v1's RegistryTweak as data).
//
// View selects the WOW64 registry view. The zero value (ViewDefault64) reads/writes
// the 64-bit view — what every existing pin relies on. Set View=ViewWow6432 to pin
// the 32-bit (WOW6432Node) view, required to detect a value that registers ONLY
// there (e.g. the VC++ x86 runtime's Installed flag).
type RegSet struct {
	Root      registry.Key
	Path      string
	Value     string
	Kind      ValueKind
	On        any
	Off       any
	OffAbsent bool
	Elev      core.Elevation
	View      RegView
}

func (a RegSet) Level() core.Elevation { return a.Elev }

func (a RegSet) Apply(_ core.ActionContext, on bool) error {
	if !on && a.OffAbsent {
		return deleteRawView(a.Root, a.Path, a.Value, a.View)
	}
	v := a.Off
	if on {
		v = a.On
	}
	return writeRawView(a.Root, a.Path, a.Value, a.Kind, v, a.View)
}

func (a RegSet) Snapshot(_ core.ActionContext) (core.Backup, error) {
	existed, typ, v, err := readRawView(a.Root, a.Path, a.Value, a.Kind, a.View)
	if err != nil {
		return core.Backup{}, err
	}
	return core.Backup{Existed: existed, Type: typ, Value: v, Timestamp: time.Now()}, nil
}

func (a RegSet) Restore(_ core.ActionContext, b core.Backup) error {
	if !b.Existed {
		return deleteRawView(a.Root, a.Path, a.Value, a.View)
	}
	return writeRawView(a.Root, a.Path, a.Value, a.Kind, b.Value, a.View)
}

func (a RegSet) Probe(_ core.ActionContext) (core.PointState, error) {
	existed, _, v, err := readRawView(a.Root, a.Path, a.Value, a.Kind, a.View)
	if err != nil {
		return core.PointOff, err
	}
	if existed && equalRaw(a.Kind, v, a.On) {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}
