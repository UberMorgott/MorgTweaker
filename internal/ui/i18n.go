package ui

// ---------------------------------------------------------------------------
// i18n — extensible UI localization.
//
// This file holds ONLY the UI chrome strings (buttons, hints, status words),
// keyed by the kXxx Key enum and resolved via T(lang, key). Catalog CONTENT
// (category/tweak names + descriptions) is NOT here: in v2 it is carried inline
// on each catalog row as a core.I18n{RU, EN} pair and resolved by the pickers in
// catalog_i18n.go (pick/catName/tweakName/tweakDesc) — there are no per-ID maps.
//
// HOW TO ADD A LANGUAGE (e.g. German):
//   1. Add a Lang const below:           LangDE
//   2. Append it to langOrder:           {LangRU, LangEN, LangDE}
//   3. Add ONE inner map to `tr`:        LangDE: { kAppTitle: "...", ... }
//      (the completeness test fails until every Key is present)
//   4. Extend core.I18n to carry the new language and add that field to every
//      catalog tweak/category literal (internal/catalog/*.go), then teach pick()
//      in catalog_i18n.go to select it — the catalog package's validate test
//      fails until every tweak/category has the new translation. (Today core.I18n
//      is RU/EN only, so a 3rd language needs that struct extended first.)
// No cycle logic, render code, or update code needs to change — Next() walks
// langOrder, every chrome string routes through T(), and content routes through
// the inline-I18n pickers.
// ---------------------------------------------------------------------------

// Lang is the active UI language.
type Lang int

const (
	LangRU Lang = iota
	LangEN
)

// defaultLang is Russian — the operator's native language.
const defaultLang = LangRU

// langOrder is the cycle order for Next(). Adding a language here (and to tr /
// the catalog tables) is all that the 'l' hotkey needs — no if-else chain.
var langOrder = []Lang{LangRU, LangEN}

// Next returns the language after l in langOrder, wrapping around. An unknown
// language falls back to the first entry.
func Next(l Lang) Lang {
	for i, x := range langOrder {
		if x == l {
			return langOrder[(i+1)%len(langOrder)]
		}
	}
	return langOrder[0]
}

// Key enumerates every user-facing chrome string. No raw English/Russian text
// is hardcoded in render/update — it all flows through T(lang, key).
type Key int

const (
	kAppTitle Key = iota

	// status-bar pieces
	kAdmin     // "ADMIN"
	kNotAdmin  // "NOT ADMIN"
	kSelectedN // "%d on" — count of tweaks currently in the ON state
	kStatusSep // " │ "

	// keybind hints (status bar)
	kHints

	// tweak state words
	kOn       // "on"
	kOff      // "off"
	kStateErr // "err"

	// markers
	kNeedsAdmin // "(admin)" marker on a tweak needing elevation the user lacks

	// richer status markers (blocked / absent / partial / reboot-pending)
	kStatusBlocked
	kStatusAbsent
	kStatusPartial
	kStatusRebootPending
	kStatusUnknown // "…" placeholder before the async probe resolves
	kStatusWorking // async apply (download/install) in flight
	kProbing       // marker while a tweak's status is being (re)fetched

	// status-aware apply / action messages
	kMsgBlocked        // Tamper-Protection block: carries a gate deep-link ('o')
	kMsgBlockedGeneric // block WITHOUT a gate (verify-after / access-denied): no 'o'
	kMsgRebootPending
	kMsgActionDone
	kMsgNoAction

	// action result messages (format strings)
	kMsgApplied       // "%s → %s"  (name, ON/OFF word)
	kVerbOn           // "ON"
	kVerbOff          // "OFF"
	kMsgNeedsAdmin    // "%s needs administrator"
	kMsgRolledBack    // "%s rolled back"
	kMsgSecRolledBack // "category %s rolled back"
	kMsgFail          // "%s: %s failed: %v" (name, what, err)
	kMsgSecErrors     // "category %s: %d error(s) — %v"

	// "what failed" verbs used in kMsgFail
	kWhatApply    // "apply"
	kWhatRollback // "rollback"

	// empty-state placeholders
	kNoCategories // "(no categories)"
	kNoTweaks     // "(no tweaks in this category)"

	// universal bottom button-bar labels (mouse-clickable footer)
	kBtnApply    // "Apply"    — apply all checked appliable tweaks
	kBtnRollback // "Rollback" — rollback all checked applied tweaks
	kBtnLang     // "RU/EN"    — language toggle
	kBtnQuit     // "Quit"     — exit

	// progress-screen labels (apply/rollback batch view)
	kProgOverall     // "Overall %d/%d" — completed/total tweaks (overall bar)
	kProgApplying    // "applying"      — current-tweak phase during apply
	kProgRollingBack // "rolling back"  — current-tweak phase during rollback
	kProgDownloading // "Downloading"   — situational bar stage label
	kProgInstalling  // "Installing"    — situational bar stage label
	kMsgCancelled    // "cancelled"     — status line after esc aborts a batch
	kMsgBatchSummary // "%d ok, %d failed" — return-to-list summary
)

// tr is the translation table: every Lang must carry every Key (guarded by the
// completeness test in i18n_test.go).
var tr = map[Lang]map[Key]string{
	LangRU: {
		kAppTitle: "MorgTweaker",

		kAdmin:     "АДМИН",
		kNotAdmin:  "НЕ АДМИН",
		kSelectedN: "включено: %d",
		kStatusSep: " │ ",

		kHints: "tab/←→ панель · ↑↓ выбор · пробел переключить · a применить · r откат · R откат категории · o действие · l язык · q выход",

		kOn:       "вкл",
		kOff:      "выкл",
		kStateErr: "ошибка",

		kNeedsAdmin: "(админ)",

		kStatusBlocked:       "заблокировано",
		kStatusAbsent:        "нет",
		kStatusPartial:       "частично",
		kStatusRebootPending: "нужна перезагрузка",
		kStatusUnknown:       "…",
		kStatusWorking:       "выполняется",
		kProbing:             "…",
		kMsgBlocked:          "%s: заблокировано (Tamper Protection). Нажми o — открыть Центр безопасности.",
		kMsgBlockedGeneric:   "%s: заблокировано (изменение не применилось или нет прав).",
		kMsgRebootPending:    "%s: применено, нужна перезагрузка.",
		kMsgActionDone:       "Открываю Центр безопасности Windows…",
		kMsgNoAction:         "Для этого твика нет действия.",

		kMsgApplied:       "%s → %s",
		kVerbOn:           "ВКЛ",
		kVerbOff:          "ВЫКЛ",
		kMsgNeedsAdmin:    "%s требует прав администратора",
		kMsgRolledBack:    "%s откачен",
		kMsgSecRolledBack: "категория %s откачена",
		kMsgFail:          "%s: %s — ошибка: %v",
		kMsgSecErrors:     "категория %s: ошибок %d — %v",

		kWhatApply:    "применение",
		kWhatRollback: "откат",

		kNoCategories: "(нет категорий)",
		kNoTweaks:     "(в этой категории нет твиков)",

		kBtnApply:    "Применить",
		kBtnRollback: "Откатить",
		kBtnLang:     "RU/EN",
		kBtnQuit:     "Выход",

		kProgOverall:     "Общий %d/%d",
		kProgApplying:    "применение",
		kProgRollingBack: "откат",
		kProgDownloading: "Скачивание",
		kProgInstalling:  "Установка",
		kMsgCancelled:    "отменено",
		kMsgBatchSummary: "готово: %d, ошибок: %d",
	},
	LangEN: {
		kAppTitle: "MorgTweaker",

		kAdmin:     "ADMIN",
		kNotAdmin:  "NOT ADMIN",
		kSelectedN: "%d on",
		kStatusSep: " │ ",

		kHints: "tab/←→ pane · ↑↓ move · space toggle · a apply · r rollback · R rollback category · o action · l lang · q quit",

		kOn:       "on",
		kOff:      "off",
		kStateErr: "err",

		kNeedsAdmin: "(admin)",

		kStatusBlocked:       "blocked",
		kStatusAbsent:        "absent",
		kStatusPartial:       "partial",
		kStatusRebootPending: "reboot needed",
		kStatusUnknown:       "…",
		kStatusWorking:       "working",
		kProbing:             "…",
		kMsgBlocked:          "%s: blocked (Tamper Protection). Press o to open Windows Security.",
		kMsgBlockedGeneric:   "%s: blocked (change didn't apply or access denied).",
		kMsgRebootPending:    "%s: applied, reboot required.",
		kMsgActionDone:       "Opening Windows Security…",
		kMsgNoAction:         "No action for this tweak.",

		kMsgApplied:       "%s → %s",
		kVerbOn:           "ON",
		kVerbOff:          "OFF",
		kMsgNeedsAdmin:    "%s needs administrator",
		kMsgRolledBack:    "%s rolled back",
		kMsgSecRolledBack: "category %s rolled back",
		kMsgFail:          "%s: %s failed: %v",
		kMsgSecErrors:     "category %s: %d error(s) — %v",

		kWhatApply:    "apply",
		kWhatRollback: "rollback",

		kNoCategories: "(no categories)",
		kNoTweaks:     "(no tweaks in this category)",

		kBtnApply:    "Apply",
		kBtnRollback: "Rollback",
		kBtnLang:     "RU/EN",
		kBtnQuit:     "Quit",

		kProgOverall:     "Overall %d/%d",
		kProgApplying:    "applying",
		kProgRollingBack: "rolling back",
		kProgDownloading: "Downloading",
		kProgInstalling:  "Installing",
		kMsgCancelled:    "cancelled",
		kMsgBatchSummary: "%d ok, %d failed",
	},
}

// T returns the localized string for k, falling back to LangEN, then to a
// visible "?<key>" placeholder so a missing key is obvious, never silently blank.
func T(lang Lang, k Key) string {
	if m, ok := tr[lang]; ok {
		if s, ok := m[k]; ok {
			return s
		}
	}
	if m, ok := tr[LangEN]; ok {
		if s, ok := m[k]; ok {
			return s
		}
	}
	return "?" + itoa(int(k))
}

// itoa is a tiny dependency-free int→string for the missing-key placeholder.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
