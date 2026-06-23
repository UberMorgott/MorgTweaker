package ui

import (
	"testing"

	"morgtweaker/internal/catalog"
	"morgtweaker/internal/core"
)

// allKeys lists every Key. Keep in sync with the Key enum: the test below also
// cross-checks that LangEN (the reference language) is fully populated, so a new
// key added to the enum but not to this list still gets caught via the EN map
// length check.
var allKeys = []Key{
	kAppTitle,
	kAdmin, kNotAdmin, kSelectedN, kStatusSep,
	kHints,
	kOn, kOff, kStateErr,
	kNeedsAdmin,
	kStatusBlocked, kStatusAbsent, kStatusPartial, kStatusRebootPending,
	kStatusUnknown, kStatusWorking, kProbing,
	kMsgBlocked, kMsgBlockedGeneric, kMsgRebootPending, kMsgActionDone, kMsgNoAction,
	kMsgApplied, kVerbOn, kVerbOff, kMsgNeedsAdmin, kMsgRolledBack,
	kMsgSecRolledBack, kMsgFail, kMsgSecErrors,
	kWhatApply, kWhatRollback,
	kNoCategories, kNoTweaks,
	kBtnApply, kBtnRollback, kBtnLang, kBtnQuit,
	kProgOverall, kProgApplying, kProgRollingBack,
	kProgDownloading, kProgInstalling,
	kMsgCancelled, kMsgBatchSummary,
}

// TestEveryLangHasEveryKey: every Lang in langOrder must define every Key in tr.
// This is the guard that a half-added language cannot ship.
func TestEveryLangHasEveryKey(t *testing.T) {
	for _, lang := range langOrder {
		m, ok := tr[lang]
		if !ok {
			t.Errorf("lang %d missing entirely from tr", lang)
			continue
		}
		for _, k := range allKeys {
			if _, ok := m[k]; !ok {
				t.Errorf("lang %d missing translation for key %d", lang, k)
			}
		}
		// Also guard against EXTRA keys / drift: count must match allKeys.
		if len(m) != len(allKeys) {
			t.Errorf("lang %d has %d keys, allKeys lists %d — update allKeys or tr", lang, len(m), len(allKeys))
		}
	}
}

// TestAllKeysListMatchesEN ensures allKeys (used above) covers exactly the keys
// the reference EN map defines — so the enum, allKeys and tr stay in lockstep.
func TestAllKeysListMatchesEN(t *testing.T) {
	en := tr[LangEN]
	if len(en) != len(allKeys) {
		t.Fatalf("LangEN has %d keys but allKeys lists %d", len(en), len(allKeys))
	}
	seen := map[Key]bool{}
	for _, k := range allKeys {
		if seen[k] {
			t.Errorf("key %d listed twice in allKeys", k)
		}
		seen[k] = true
		if _, ok := en[k]; !ok {
			t.Errorf("allKeys lists key %d not present in LangEN", k)
		}
	}
}

// TestEveryCatalogIDLocalized: every category and tweak in the real catalog must
// resolve to a non-empty inline name (and tweaks a non-empty desc) in EVERY
// language via the i18n pickers. Guards against a catalog entry added without
// inline RU/EN translations.
func TestEveryCatalogIDLocalized(t *testing.T) {
	cat := catalog.Build()
	for _, lang := range langOrder {
		for _, c := range cat {
			if catName(lang, c) == "" {
				t.Errorf("lang %d empty category name for %q", lang, c.ID)
			}
			for _, tw := range c.Tweaks {
				if tweakName(lang, tw) == "" {
					t.Errorf("lang %d empty tweak name for %q", lang, tw.ID)
				}
				if tweakDesc(lang, tw) == "" {
					t.Errorf("lang %d empty tweak desc for %q", lang, tw.ID)
				}
			}
		}
	}
}

// TestInlineI18nLookup verifies the inline RU/EN pickers select per language.
func TestInlineI18nLookup(t *testing.T) {
	tw := core.Tweak{Name: core.I18n{RU: "Имя", EN: "Name"}, Desc: core.I18n{RU: "Описание", EN: "Desc"}}
	if tweakName(LangRU, tw) != "Имя" || tweakName(LangEN, tw) != "Name" {
		t.Errorf("tweakName RU/EN = %q/%q", tweakName(LangRU, tw), tweakName(LangEN, tw))
	}
	if tweakDesc(LangRU, tw) != "Описание" || tweakDesc(LangEN, tw) != "Desc" {
		t.Errorf("tweakDesc RU/EN = %q/%q", tweakDesc(LangRU, tw), tweakDesc(LangEN, tw))
	}
	c := core.Category{Name: core.I18n{RU: "Кат", EN: "Cat"}}
	if catName(LangRU, c) != "Кат" || catName(LangEN, c) != "Cat" {
		t.Errorf("catName RU/EN = %q/%q", catName(LangRU, c), catName(LangEN, c))
	}
}

// TestNextCyclesLanguages verifies Next walks langOrder and wraps.
func TestNextCyclesLanguages(t *testing.T) {
	if len(langOrder) < 2 {
		t.Skip("need at least two languages to test cycling")
	}
	start := langOrder[0]
	cur := start
	for range langOrder {
		cur = Next(cur)
	}
	if cur != start {
		t.Errorf("Next did not cycle back to start after %d steps: got %d, want %d", len(langOrder), cur, start)
	}
}

// TestTFallback verifies the missing-key placeholder is visible, not blank.
func TestTFallback(t *testing.T) {
	// A key far outside the defined range must yield a "?<n>" placeholder.
	got := T(LangEN, Key(99999))
	if got == "" {
		t.Fatal("T returned empty for a missing key; expected a visible placeholder")
	}
	if got[0] != '?' {
		t.Errorf("missing-key placeholder = %q, want it to start with '?'", got)
	}
}
