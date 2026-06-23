package ui

// Catalog content localization. In v2 every tweak/category carries its display
// name + description inline as a core.I18n{RU, EN} pair, so there is no parallel
// name/desc map to drift out of sync. These helpers just pick the RU or EN field
// for the active language; the completeness of the inline strings is enforced by
// the catalog package's startup-validation test (internal/catalog/validate_test.go).
//
// Category labels MUST be single-word per language (left-pane requirement) — kept
// short directly in the catalog data.

import "morgtweaker/internal/core"

// pick returns the RU or EN field of an I18n pair for the active language.
// Unknown languages fall back to RU (the default/operator language); an empty
// chosen field falls back to the other so a missing translation is never blank.
func pick(lang Lang, s core.I18n) string {
	chosen := s.RU
	if lang == LangEN {
		chosen = s.EN
	}
	if chosen != "" {
		return chosen
	}
	if s.RU != "" {
		return s.RU
	}
	return s.EN
}

// catName resolves a category label for the active language.
func catName(lang Lang, c core.Category) string { return pick(lang, c.Name) }

// tweakName resolves a tweak display name for the active language.
func tweakName(lang Lang, t core.Tweak) string { return pick(lang, t.Name) }

// tweakDesc resolves a tweak description for the active language.
func tweakDesc(lang Lang, t core.Tweak) string { return pick(lang, t.Desc) }
