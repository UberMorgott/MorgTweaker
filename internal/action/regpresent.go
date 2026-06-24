package action

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// RegPresent is a DETECT-ONLY action: its Probe reports PointOn when a given
// registry value EXISTS (content ignored), else PointOff. It is used as a
// DownloadInstall.Detect for components that have no clean "Installed" flag — e.g.
// VC++ 2005/2008, detected by the existence of their MSI Uninstall\{GUID} key's
// DisplayName value. Apply/Snapshot/Restore are honest no-ops: presence is not
// something this action writes, only reads.
type RegPresent struct {
	Root  registry.Key
	Path  string
	Value string
	Elev  core.Elevation
	View  RegView
}

func (a RegPresent) Level() core.Elevation { return a.Elev }

func (a RegPresent) Apply(core.ActionContext, bool) error { return nil }

func (a RegPresent) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}

func (a RegPresent) Restore(core.ActionContext, core.Backup) error { return nil }

func (a RegPresent) Probe(core.ActionContext) (core.PointState, error) {
	// readRawView maps a missing key OR a missing value to (existed=false, err=nil),
	// so a genuine err is the only error path — mirror RegSet.Probe's behaviour.
	existed, _, _, err := readRawView(a.Root, a.Path, a.Value, KindString, a.View)
	if err != nil {
		return core.PointOff, err
	}
	if existed {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}

var _ core.Action = RegPresent{}
