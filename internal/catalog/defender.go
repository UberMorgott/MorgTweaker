package catalog

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// Defender registry paths (ported from v1 internal/tweak/defender.go).
const (
	// rtPath holds the five Real-Time Protection policy points.
	rtPath = `SOFTWARE\Policies\Microsoft\Windows Defender\Real-Time Protection`
	// asPath holds DisableAntiSpyware (parent key, NOT Real-Time Protection).
	asPath = `SOFTWARE\Policies\Microsoft\Windows Defender`
)

// rtRealtime builds a Real-Time Protection reg.set point: On=1 disables the named
// protection; turning off restores the default by deleting the value (v1 Off=nil).
func rtRealtime(value string) action.RegSet {
	return action.RegSet{
		Root:  registry.LOCAL_MACHINE,
		Path:  rtPath,
		Value: value, Kind: action.KindDword, On: uint64(1), OffAbsent: true, Elev: core.ElevTrustedInstaller,
	}
}

// svcDisabled toggles a Defender service Start value: ON=4 (disabled), OFF=3
// (manual) — matching v1's Defender service points.
func svcDisabled(svc string) action.ServiceStart {
	return action.ServiceStart{Root: registry.LOCAL_MACHINE, Svc: svc, OnStart: 4, OffStart: 3, Elev: core.ElevTrustedInstaller}
}

// defenderTweak is the former v1 DefenderTweak composite expressed as pure data: a
// shared tamper gate + 6 realtime reg.set points + 6 service disables. No special
// Go type — the engine handles it generically. The gate is wired to the catalog's
// single shared TamperCache so a full probe spawns Get-MpComputerStatus once.
func defenderTweak(tc *action.TamperCache) core.Tweak {
	return core.Tweak{
		ID: "prep.disable_defender", Category: "prep",
		Name: core.I18n{RU: "Отключить Defender", EN: "Disable Windows Defender"},
		Desc: core.I18n{
			RU: "Отключить realtime и службы Defender — только если Tamper Protection выключен.",
			EN: "Disable Defender realtime + services — only when Tamper Protection is off.",
		},
		Elevation: core.ElevTrustedInstaller, Reboot: true,
		Tags: []string{"dangerous"},
		Gate: action.NewTamperGate(tc),
		Actions: []core.Action{
			rtRealtime("DisableRealtimeMonitoring"),
			rtRealtime("DisableBehaviorMonitoring"),
			rtRealtime("DisableIOAVProtection"),
			rtRealtime("DisableOnAccessProtection"),
			rtRealtime("DisableScanOnRealtimeEnable"),
			// DisableAntiSpyware lives on the parent key, not Real-Time Protection.
			action.RegSet{
				Root:  registry.LOCAL_MACHINE,
				Path:  asPath,
				Value: "DisableAntiSpyware", Kind: action.KindDword, On: uint64(1), OffAbsent: true, Elev: core.ElevTrustedInstaller,
			},
			svcDisabled("WinDefend"),
			svcDisabled("WdNisSvc"),
			svcDisabled("Sense"),
			svcDisabled("SecurityHealthService"),
			svcDisabled("SgrmBroker"),
			svcDisabled("wscsvc"),
		},
	}
}
