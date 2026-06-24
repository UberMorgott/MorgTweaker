package catalog

import (
	"morgtweaker/internal/action"
	"morgtweaker/internal/core"

	"golang.org/x/sys/windows/registry"
)

// privacy is the "Privacy" category.
var privacy = []core.Tweak{
	{
		ID: "privacy.disable_diagtrack", Category: "privacy",
		Name: core.I18n{RU: "Отключить телеметрию (DiagTrack)", EN: "Disable telemetry service (DiagTrack)"},
		Desc: core.I18n{RU: "Перевести службу телеметрии Windows в состояние «отключена».", EN: "Set the Connected User Experiences and Telemetry service to disabled."},
		// v1 (internal/tweak/catalog.go) wrote DiagTrack's Start as a plain admin
		// RegistryTweak — HKLM\SYSTEM\...\Services\DiagTrack\Start is admin-writable,
		// no SYSTEM impersonation needed. Use ElevAdmin to match (ElevSystem would
		// force a needless winlogon impersonation).
		Elevation: core.ElevAdmin,
		// v1: Start ON=4 (disabled), OFF=2 (automatic).
		Actions: []core.Action{
			action.ServiceStart{
				Root: registry.LOCAL_MACHINE, Svc: "DiagTrack", OnStart: 4, OffStart: 2, Elev: core.ElevAdmin,
			},
			// Canonical "Allow Telemetry" GPO: 0 = Security/off. OffAbsent removes
			// the policy value to restore the OS default.
			action.RegSet{
				Root:      registry.LOCAL_MACHINE,
				Path:      `SOFTWARE\Policies\Microsoft\Windows\DataCollection`,
				Value:     "AllowTelemetry",
				Kind:      action.KindDword,
				On:        uint64(0),
				OffAbsent: true,
				Elev:      core.ElevAdmin,
			},
		},
	},
}
