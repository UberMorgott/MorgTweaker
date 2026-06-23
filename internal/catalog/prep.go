package catalog

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// vcredistSHA256 is the pinned sha256 of the SPECIFIC vc_redist.x64.exe served
// at vcredistURL. It MUST be replaced with the real lowercase-hex sha256 of the
// exact binary you ship against: the file behind aka.ms/vs/17/release is updated
// by Microsoft over time, so this cannot be known at authoring time. Pin it by
// downloading that exact build once and hashing it
// (PowerShell: Get-FileHash -Algorithm SHA256 vc_redist.x64.exe).
// DownloadInstall refuses to run the installer unless the download hashes to
// this value, so leaving the placeholder safely fails closed (never installs an
// unverified binary) rather than silently running something unchecked.
const vcredistSHA256 = "TODO-pin-real-sha256-of-shipped-vc_redist.x64.exe"

// vcredistURL is Microsoft's evergreen redirect to the latest VC++ 2015-2022
// x64 redistributable.
const vcredistURL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"

// prep builds the "Prep" category. It takes the shared TamperCache so the Defender
// tweak's gate can be wired to it (one cache for the whole catalog).
func prep(tc *action.TamperCache) []core.Tweak {
	return []core.Tweak{
		{
			ID: "prep.disable_uac", Category: "prep",
			Name: core.I18n{RU: "Отключить UAC", EN: "Disable UAC"},
			Desc: core.I18n{RU: "EnableLUA=0. Требуется перезагрузка.", EN: "EnableLUA=0. Requires reboot."},
			Elevation: core.ElevAdmin, Reboot: true,
			Actions: []core.Action{action.RegSet{
				Root:  registry.LOCAL_MACHINE,
				Path:  `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`,
				Value: "EnableLUA", Kind: action.KindDword, On: uint64(0), Off: uint64(1), Elev: core.ElevAdmin,
			}},
		},
		{
			ID: "prep.disable_smartscreen", Category: "prep",
			Name: core.I18n{RU: "Отключить SmartScreen", EN: "Disable SmartScreen"},
			Desc: core.I18n{RU: "Выключить SmartScreen Проводника.", EN: "Turn off Explorer SmartScreen."},
			Elevation: core.ElevAdmin,
			Actions: []core.Action{action.RegSet{
				Root:  registry.LOCAL_MACHINE,
				Path:  `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer`,
				Value: "SmartScreenEnabled", Kind: action.KindString, On: "Off", Off: "On", Elev: core.ElevAdmin,
			}},
		},
		defenderTweak(tc),
		{
			ID: "prep.pause_update", Category: "prep",
			Name: core.I18n{RU: "Приостановить авто-обновления", EN: "Pause automatic Windows Update"},
			Desc: core.I18n{RU: "WindowsUpdate\\AU NoAutoUpdate=1.", EN: "WindowsUpdate\\AU NoAutoUpdate=1."},
			Elevation: core.ElevAdmin, Reboot: true,
			// v1: Off=nil -> OffAbsent (turning off restores the default by deleting the value).
			Actions: []core.Action{action.RegSet{
				Root:  registry.LOCAL_MACHINE,
				Path:  `SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU`,
				Value: "NoAutoUpdate", Kind: action.KindDword, On: uint64(1), OffAbsent: true, Elev: core.ElevAdmin,
			}},
		},
		{
			ID: "prep.disable_seccenter_notify", Category: "prep",
			Name: core.I18n{RU: "Отключить уведомления Центра безопасности", EN: "Disable Security Center notifications"},
			Desc: core.I18n{RU: "Отключить уведомления Центра безопасности Windows.", EN: "Turn off Windows Security Center notifications."},
			Elevation: core.ElevAdmin, Reboot: true,
			// v1: Off=nil -> OffAbsent.
			Actions: []core.Action{action.RegSet{
				Root:  registry.LOCAL_MACHINE,
				Path:  `SOFTWARE\Policies\Microsoft\Windows Defender Security Center\Notifications`,
				Value: "DisableNotifications", Kind: action.KindDword, On: uint64(1), OffAbsent: true, Elev: core.ElevAdmin,
			}},
		},
		{
			ID: "prep.install_vcredist", Category: "prep",
			Name: core.I18n{RU: "Установить VC++ Redistributable", EN: "Install VC++ Redistributable"},
			Desc: core.I18n{RU: "Скачать и тихо установить VC++ 2015-2022 x64.", EN: "Download and silently install VC++ 2015-2022 x64."},
			Elevation: core.ElevAdmin,
			Actions: []core.Action{action.DownloadInstall{
				URL:    vcredistURL,
				SHA256: vcredistSHA256,
				Args:   []string{"/quiet", "/norestart"},
				// Probe via the VC++ runtime's registry "Installed" flag (set by the
				// redist installer). On a fresh box the key is absent → reads as Off.
				Detect: action.RegSet{
					Root:  registry.LOCAL_MACHINE,
					Path:  `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64`,
					Value: "Installed", Kind: action.KindDword, On: uint64(1), Off: uint64(0), Elev: core.ElevUser,
				},
				Elev: core.ElevAdmin,
			}},
		},
	}
}
