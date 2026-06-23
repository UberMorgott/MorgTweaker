package catalog

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// vcredistX64URL / vcredistX86URL are Microsoft's evergreen redirects to the
// LATEST VC++ 2015-2022 redistributables. Because "latest" is updated by
// Microsoft over time, the exact bytes cannot be pinned to a static SHA256 at
// authoring time. DownloadInstall therefore verifies these in
// VerifyAuthenticodeMicrosoft mode: the downloaded file must carry a Valid
// Authenticode signature by "O=Microsoft Corporation" or it is NOT run
// (fail-closed). No SHA256 pin is used.
const (
	vcredistX64URL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"
	vcredistX86URL = "https://aka.ms/vs/17/release/vc_redist.x86.exe"
)

// vcredistAcceptExit are the installer exit codes treated as success:
//
//	0    success
//	3010 success, reboot required
//	1638 a newer version is already installed (treat as satisfied)
//	1641 success, reboot has been initiated
var vcredistAcceptExit = []int{0, 3010, 1638, 1641}

// vcRuntimeDetect reads the VC++ runtime's registry "Installed" flag (set to 1 by
// the redist installer) for the given runtime subkey (x64 or x86) in the CORRECT
// registry view. The x64 runtime registers under the 64-bit view at
// ...\VC\Runtimes\x64; the x86 runtime registers ONLY under the 32-bit view
// (WOW6432Node) at ...\VC\Runtimes\x86 and is ABSENT from the 64-bit view —
// reading x86 in the 64-bit view falsely reports not-installed. We therefore pin
// each arch to its own view. A missing key reads as Off (the RegSet probe returns
// PointOff when the key/value is absent), so a fresh box is correctly reported as
// not-installed rather than erroring.
func vcRuntimeDetect(arch string) action.RegSet {
	view := action.ViewDefault64 // x64 runtime lives in the 64-bit view
	if arch == "x86" {
		view = action.ViewWow6432 // x86 runtime lives ONLY in the 32-bit (WOW6432Node) view
	}
	return action.RegSet{
		Root:  registry.LOCAL_MACHINE,
		Path:  `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\` + arch,
		Value: "Installed", Kind: action.KindDword, On: uint64(1), Off: uint64(0), Elev: core.ElevUser,
		View: view,
	}
}

// prep builds the "Prep" category. It takes the shared TamperCache so the Defender
// tweak's gate can be wired to it (one cache for the whole catalog).
func prep(tc *action.TamperCache) []core.Tweak {
	return []core.Tweak{
		{
			ID: "prep.disable_uac", Category: "prep",
			Name:      core.I18n{RU: "Отключить UAC", EN: "Disable UAC"},
			Desc:      core.I18n{RU: "EnableLUA=0. Требуется перезагрузка.", EN: "EnableLUA=0. Requires reboot."},
			Elevation: core.ElevAdmin, Reboot: true,
			Actions: []core.Action{action.RegSet{
				Root:  registry.LOCAL_MACHINE,
				Path:  `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`,
				Value: "EnableLUA", Kind: action.KindDword, On: uint64(0), Off: uint64(1), Elev: core.ElevAdmin,
			}},
		},
		{
			ID: "prep.disable_smartscreen", Category: "prep",
			Name:      core.I18n{RU: "Отключить SmartScreen", EN: "Disable SmartScreen"},
			Desc:      core.I18n{RU: "Выключить SmartScreen Проводника.", EN: "Turn off Explorer SmartScreen."},
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
			Name:      core.I18n{RU: "Приостановить авто-обновления", EN: "Pause automatic Windows Update"},
			Desc:      core.I18n{RU: "WindowsUpdate\\AU NoAutoUpdate=1.", EN: "WindowsUpdate\\AU NoAutoUpdate=1."},
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
			Name:      core.I18n{RU: "Отключить уведомления Центра безопасности", EN: "Disable Security Center notifications"},
			Desc:      core.I18n{RU: "Отключить уведомления Центра безопасности Windows.", EN: "Turn off Windows Security Center notifications."},
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
			Name:      core.I18n{RU: "Установить Visual C++ Redistributable (последний)", EN: "Install Visual C++ Redistributable (latest)"},
			Desc:      core.I18n{RU: "Скачать и тихо установить последний VC++ 2015-2022 (x64 и x86) с проверкой подписи Microsoft.", EN: "Download and silently install the latest VC++ 2015-2022 (x64 and x86), verifying the Microsoft signature."},
			Elevation: core.ElevAdmin, Reboot: false,
			// Two installers: x64 then x86. Each downloads the LATEST build from
			// Microsoft's evergreen permalink and is verified by Authenticode
			// (Valid + Microsoft Corporation signer) before running — no SHA256 pin.
			//
			// Each action's Detect is its OWN arch's runtime (x64→x64, x86→x86), NOT
			// the combined both-installed probe. This matters for the engine's
			// per-ACTION verify-after: after the x64 installer runs, x86 may not yet
			// be installed, so a both-installed Detect would re-probe Off and FALSELY
			// flag the successful x64 install as Blocked. A per-arch Detect verifies
			// only what that action actually installed. A missing key reads as Off
			// (RegSet probe), so a fresh box is correctly not-installed (no error).
			Actions: []core.Action{
				action.DownloadInstall{
					URL:        vcredistX64URL,
					Verify:     action.VerifyAuthenticodeMicrosoft,
					Args:       []string{"/install", "/quiet", "/norestart"},
					AcceptExit: vcredistAcceptExit,
					Detect:     vcRuntimeDetect("x64"),
					Elev:       core.ElevAdmin,
				},
				action.DownloadInstall{
					URL:        vcredistX86URL,
					Verify:     action.VerifyAuthenticodeMicrosoft,
					Args:       []string{"/install", "/quiet", "/norestart"},
					AcceptExit: vcredistAcceptExit,
					Detect:     vcRuntimeDetect("x86"),
					Elev:       core.ElevAdmin,
				},
			},
		},
	}
}
