package catalog

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

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
		redistParent(),
	}
}
