// Package catalog is the tweak data of the v2 engine: one file per category, each
// a []core.Tweak of Go literals with inline RU/EN i18n. Build() assembles them in
// display order. Adding a tweak = one literal block; adding a category = one file
// + one line in Build().
//
// Tamper-cache wiring (anti-lag design): Build() creates exactly ONE shared
// *action.TamperCache and injects it into every Defender-related action/gate. A
// full probe over all Defender tweaks therefore spawns Get-MpComputerStatus at
// most once per TTL, not once per tweak. The Defender suppression action uses it
// only to scope its realtime-off sub-step (it never blocks the whole tweak).
package catalog

import (
	"time"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// Build assembles the catalog in display order. It owns the single TamperCache
// shared by every Defender gate.
func Build() core.Catalog {
	// One shared cache for the whole catalog: every Defender gate reuses it, so a
	// full probe spawns Get-MpComputerStatus once, not per tweak.
	tc := action.NewTamperCache(nil, 5*time.Second)

	return core.Catalog{
		{ID: "prep", Name: core.I18n{RU: "Подготовка", EN: "Prep"}, Tweaks: prep(tc)},
		{ID: "explorer", Name: core.I18n{RU: "Проводник", EN: "Explorer"}, Tweaks: explorer},
		{ID: "privacy", Name: core.I18n{RU: "Приватность", EN: "Privacy"}, Tweaks: privacy},
	}
}
