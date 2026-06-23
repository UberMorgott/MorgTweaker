package action

import (
	"errors"
	"time"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// ServiceStart toggles a Windows service's start type via its registry Start value
// under SYSTEM\CurrentControlSet\Services\<Svc>. OnStart/OffStart are the Start
// codes (4=disabled, 3=manual, 2=automatic). A missing service key probes Absent
// so the engine excludes it from the aggregate (the v1 absent-service rule:
// e.g. Defender stripped from a custom build is reported, never an error).
type ServiceStart struct {
	Root     registry.Key // LOCAL_MACHINE in production; CURRENT_USER in tests
	Svc      string       // service name, or a full subkey path in tests
	OnStart  uint64
	OffStart uint64
	Elev     core.Elevation
}

func (a ServiceStart) Level() core.Elevation { return a.Elev }

// keyPath returns the registry path holding the Start value. Under HKLM it is the
// canonical Services\<Svc> path; under any other root (tests) Svc is already a
// ready subkey path.
func (a ServiceStart) keyPath() string {
	if a.Root == registry.LOCAL_MACHINE {
		return `SYSTEM\CurrentControlSet\Services\` + a.Svc
	}
	return a.Svc
}

// keyExists reports whether the service key is present at all.
func (a ServiceStart) keyExists() (bool, error) {
	k, err := registry.OpenKey(a.Root, a.keyPath(), queryAccess)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	k.Close()
	return true, nil
}

func (a ServiceStart) Apply(_ core.ActionContext, on bool) error {
	// An absent service key is a no-op (v1's StatusAbsent rule). Never CreateKey
	// for a service that does not exist — that would fabricate a bogus
	// Services\<Svc> key (e.g. a Defender-stripped build). Defense-in-depth: the
	// engine also pre-gates, but the action must not fabricate keys on its own.
	exists, err := a.keyExists()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	v := a.OffStart
	if on {
		v = a.OnStart
	}
	return writeRaw(a.Root, a.keyPath(), "Start", KindDword, v)
}

func (a ServiceStart) Snapshot(_ core.ActionContext) (core.Backup, error) {
	existed, typ, v, err := readRaw(a.Root, a.keyPath(), "Start", KindDword)
	if err != nil {
		return core.Backup{}, err
	}
	return core.Backup{Existed: existed, Type: typ, Value: v, Timestamp: time.Now()}, nil
}

func (a ServiceStart) Restore(_ core.ActionContext, b core.Backup) error {
	if !b.Existed {
		return nil // no Start value (or service key) existed; nothing to write back
	}
	return writeRaw(a.Root, a.keyPath(), "Start", KindDword, b.Value)
}

func (a ServiceStart) Probe(_ core.ActionContext) (core.PointState, error) {
	exists, err := a.keyExists()
	if err != nil {
		return core.PointOff, err
	}
	if !exists {
		return core.PointAbsent, nil
	}
	_, _, v, err := readRaw(a.Root, a.keyPath(), "Start", KindDword)
	if err != nil {
		return core.PointOff, err
	}
	if g, ok := toU64(v); ok && g == a.OnStart {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}
