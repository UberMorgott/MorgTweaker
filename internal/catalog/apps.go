package catalog

import (
	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// wingetBuildFloor is winget's OS floor: Windows 10 1809 / CurrentBuild 17763.
// Windows 8/8.1 and Windows 10 < 1809 are unsupported, so every apps tweak
// carries NewBuildGate(wingetBuildFloor) and renders Blocked on older builds.
const wingetBuildFloor = 17763

// appAcceptExit are the winget install exit codes treated as success (confirmed
// against winget-cli returnCodes.md):
//
//	0           success
//	-1978334967 0x8A150109 INSTALL_REBOOT_REQUIRED_TO_FINISH
//	-1978335135 0x8A150061 already installed (no install needed)
//	-1978335189 0x8A15002B update not applicable (already up to date)
var appAcceptExit = []int{0, -1978334967, -1978335135, -1978335189}

// apps returns the single expandable "Программы (winget)" parent. Its children
// are queued in declaration order, so apps.winget (the bootstrap) MUST be first:
// it installs winget before the four winget-driven app installs run.
func apps() []core.Tweak {
	n := func(ru, en string) core.I18n { return core.I18n{RU: ru, EN: en} }
	return []core.Tweak{
		{
			ID: "apps.programs", Category: "apps", Elevation: core.ElevAdmin,
			Name: n("Программы (winget)", "Programs (winget)"),
			Desc: n("Установить winget, затем приложения через winget.",
				"Install winget, then apps via winget."),
			Children: []core.Tweak{
				{
					ID: "apps.winget", Category: "apps", Elevation: core.ElevAdmin,
					Name: n("Установить winget", "Install winget"),
					Gate: action.NewBuildGate(wingetBuildFloor),
					Actions: []core.Action{
						action.WingetBootstrap{Elev: core.ElevAdmin},
					},
				},
				appChild("apps.powershell", n("PowerShell 7", "PowerShell 7"), "Microsoft.PowerShell"),
				appChild("apps.7zip", n("7-Zip", "7-Zip"), "7zip.7zip"),
				appChild("apps.terminal", n("Windows Terminal", "Windows Terminal"), "Microsoft.WindowsTerminal"),
				appChild("apps.vlc", n("VLC", "VLC"), "VideoLAN.VLC"),
			},
		},
	}
}

// appChild builds one winget-install leaf for the given package ID. Every child
// is Admin-elevated, build-gated to the winget floor, and accepts the
// reboot-required / already-installed / not-applicable codes as success.
func appChild(id string, name core.I18n, pkgID string) core.Tweak {
	return core.Tweak{
		ID: id, Category: "apps", Elevation: core.ElevAdmin,
		Name: name,
		Gate: action.NewBuildGate(wingetBuildFloor),
		Actions: []core.Action{
			action.WingetInstall{ID: pkgID, Elev: core.ElevAdmin, AcceptExit: appAcceptExit},
		},
	}
}
