package action

import (
	"strconv"

	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/core"
)

// BuildGate blocks a tweak on an unsupported Windows build. winget, for example,
// requires Windows 10 1809 (CurrentBuild 17763) and is unsupported on Windows
// 8/8.1, so its tweaks carry NewBuildGate(17763) and render Blocked on older
// builds. The build source is read from the registry; an unreadable/unparseable
// build FAILS CLOSED (Blocked) rather than guessing the host is supported.
type BuildGate struct {
	Min int

	// buildFn resolves the live CurrentBuild. It is a test seam only: production
	// leaves it nil and Check falls back to the registry read below.
	buildFn func(core.ActionContext) (int, error)
}

// NewBuildGate builds a gate that requires CurrentBuild >= min.
func NewBuildGate(min int) BuildGate { return BuildGate{Min: min} }

func (g BuildGate) Check(ctx core.ActionContext) (bool, core.Status, core.GateAction) {
	read := g.buildFn
	if read == nil {
		read = readCurrentBuild
	}
	build, err := read(ctx)
	if err != nil {
		// FAIL CLOSED: the Windows build is unknown, so we cannot vouch for
		// support. Block without asserting a specific version.
		return false, core.StatusBlocked, core.GateAction{
			Label: core.I18n{
				RU: "Не удалось определить версию Windows.",
				EN: "Could not determine the Windows version.",
			},
		}
	}
	if build >= g.Min {
		return true, core.StatusOff, core.GateAction{}
	}
	return false, core.StatusBlocked, core.GateAction{
		Label: core.I18n{
			RU: "winget требует Windows 10 1809+ (Windows 8/8.1 не поддерживается).",
			EN: "winget requires Windows 10 1809+ (Windows 8/8.1 unsupported).",
		},
	}
}

// readCurrentBuild reads HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion value
// "CurrentBuild" (a REG_SZ such as "22000") and parses it to an int. Any open,
// read, or parse failure surfaces as an error so Check can fail closed.
func readCurrentBuild(_ core.ActionContext) (int, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return 0, err
	}
	defer k.Close()

	s, _, err := k.GetStringValue("CurrentBuild")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

var _ core.Gate = BuildGate{}
