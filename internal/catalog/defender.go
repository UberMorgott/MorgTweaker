package catalog

import (
	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// defenderTweak is the DURABLE, reversible "Defender off until re-enabled" toggle.
// Turning it ON keeps Defender off across reboots and Windows maintenance timers
// until the user turns it back off in the tweaker.
//
// It layers two actions:
//  1. DefenderSuppress (ElevAdmin) — IMMEDIATE same-session effect: adds Defender
//     exclusions for our process + work dir and (Tamper off) turns realtime off.
//  2. DefenderServiceDisable (ElevTrustedInstaller) — DURABLE effect: sets the
//     Defender services' Start to 4 (disabled) so they do not start next boot, and
//     disables Defender's scheduled tasks. Hardened keys (WdFilter/WdBoot) that
//     reject even TI get the take-ownership fallback; the original owner/DACL and
//     Start are snapshotted so Restore reverts everything exactly.
//
// A TamperGate (shared TamperCache) short-circuits the WHOLE tweak to StatusBlocked
// (with a deep-link to Windows Security) while Tamper Protection is ON, because the
// WdFilter minifilter reverts the durable service-key writes while Tamper is up and
// Tamper cannot be disabled programmatically. The user turns Tamper off, then
// retries — the gate guarantees the actions run only with Tamper OFF.
//
// Reboot is true: the durable layer takes full effect after one reboot (the UI
// surfaces RebootPending); we never auto-reboot.
func defenderTweak(tc *action.TamperCache) core.Tweak {
	return core.Tweak{
		ID: "prep.disable_defender", Category: "prep",
		Name: core.I18n{RU: "Отключить Defender (до ручного включения)", EN: "Disable Defender (until re-enabled)"},
		Desc: core.I18n{
			RU: "Отключить Defender (до ручного включения)",
			EN: "Disable Defender (until re-enabled)",
		},
		Elevation: core.ElevTrustedInstaller,
		Reboot:    true,
		Tags:      []string{"dangerous"},
		Gate:      action.NewTamperGate(tc),
		Actions: []core.Action{
			action.NewDefenderSuppress(tc, core.ElevAdmin),
			action.NewDefenderServiceDisable(core.ElevTrustedInstaller),
		},
	}
}

// legacyDefenderRemovalActions is the FORMER v1 aggressive composite — Start=4 on
// the Defender services + the Real-Time Protection policy points + DisableAntiSpyware,
// all under TrustedInstaller. It is RETAINED FOR REFERENCE ONLY and intentionally
// NOT wired into the catalog: it is not reversible without a reboot and amounts to
// full Defender removal, which is a future, separate, opt-in feature — NOT the
// session-scoped suppression above. Do not add this to a Tweak's Actions as-is.
//
// To revive a removal feature later, restore the rtRealtime/svcDisabled helpers
// (see git history) and gate it behind an explicit, clearly-labeled tweak.
//
//	rtRealtime("DisableRealtimeMonitoring"), rtRealtime("DisableBehaviorMonitoring"),
//	rtRealtime("DisableIOAVProtection"), rtRealtime("DisableOnAccessProtection"),
//	rtRealtime("DisableScanOnRealtimeEnable"),
//	RegSet{Path: `SOFTWARE\Policies\Microsoft\Windows Defender`, Value: "DisableAntiSpyware", On: 1, OffAbsent: true, Elev: ElevTrustedInstaller},
//	svcDisabled("WinDefend"), svcDisabled("WdNisSvc"), svcDisabled("Sense"),
//	svcDisabled("SecurityHealthService"), svcDisabled("SgrmBroker"), svcDisabled("wscsvc"),
//	// where svcDisabled => ServiceStart{OnStart:4, OffStart:3, Elev:ElevTrustedInstaller}
//	// and rtRealtime => RegSet{Path: ...\Real-Time Protection, On:1, OffAbsent:true, Elev:ElevTrustedInstaller}
