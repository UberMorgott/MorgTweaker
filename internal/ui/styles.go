package ui

import "charm.land/lipgloss/v2"

// ---------------------------------------------------------------------------
// Theme — LIME / salad-green palette. Retune the whole UI from these constants.
//
//	accentLime   primary accent (borders, scrollbar thumb, on-glyph, title)
//	accentBright highlight for the selected/active element
//	accentDim    muted green for inactive / scrollbar track / dim borders
//	inkDark      dark text used ON lime-filled highlights (contrast)
//	inkLight     light text used on the normal dark background
//	gray         neutral gray for secondary/disabled chrome
//	warn / err   amber + red, kept for needs-admin / error states
//
// ---------------------------------------------------------------------------
// Theme colors (lipgloss.Color is an interface value, so var not const).
var (
	accentLime   = lipgloss.Color("#A6E22E") // primary lime accent
	accentBright = lipgloss.Color("#C8FF6B") // brighter lime for selection/active
	accentDim    = lipgloss.Color("#5A7A2E") // muted green: inactive, track, dim border
	inkDark      = lipgloss.Color("#10240A") // near-black green, text on lime fills
	inkLight     = lipgloss.Color("#E8F5D0") // light text on dark bg
	gray         = lipgloss.Color("#808080") // neutral gray
	warnAmber    = lipgloss.Color("#E0B000") // needs-admin / caution
	errRed       = lipgloss.Color("#FF5555") // errors
	okGreen      = lipgloss.Color("#A6E22E") // success reuses the lime accent
)

var (
	// Borders + frame: lime accent; dim variant for inactive pane edges/track.
	borderStyle    = lipgloss.NewStyle().Foreground(accentLime)
	borderDimStyle = lipgloss.NewStyle().Foreground(accentDim)

	// Text tiers.
	labelStyle = lipgloss.NewStyle().Foreground(inkLight)
	dimStyle   = lipgloss.NewStyle().Foreground(gray)
	helpStyle  = lipgloss.NewStyle().Foreground(gray)

	// Title bar accent (bold lime on dark).
	titleStyle = lipgloss.NewStyle().Foreground(accentLime).Bold(true)

	// Selected row in the ACTIVE pane: dark ink on bright lime fill (high contrast).
	selActiveStyle = lipgloss.NewStyle().Foreground(inkDark).Background(accentBright).Bold(true)
	// Selected row in an INACTIVE pane: lime text, no fill (still locatable, less loud).
	selInactiveStyle = lipgloss.NewStyle().Foreground(accentLime)

	// Active-pane indicator (the small ▸ marker + active title accent).
	activeMarkStyle = lipgloss.NewStyle().Foreground(accentBright).Bold(true)

	// Tweak status coloring. Color encodes state — no on/off text needed:
	//   APPLIED  tweaks recede (dim gray) — nothing to do.
	//   APPLIABLE tweaks are bright (bold lime) — the call to action.
	appliedStyle   = lipgloss.NewStyle().Foreground(gray)                    // applied → dim
	appliableStyle = lipgloss.NewStyle().Foreground(accentBright).Bold(true) // appliable → bright

	// Retained for compatibility / status bar glyphs.
	onStyle  = lipgloss.NewStyle().Foreground(accentLime).Bold(true)
	offStyle = lipgloss.NewStyle().Foreground(gray)

	// Status / result.
	errStyle = lipgloss.NewStyle().Foreground(errRed)
	okStyle  = lipgloss.NewStyle().Foreground(okGreen)

	// Admin badge.
	adminOnStyle  = lipgloss.NewStyle().Foreground(accentLime).Bold(true)
	adminOffStyle = lipgloss.NewStyle().Foreground(warnAmber).Bold(true)
)

// checkbox glyphs.
const (
	glyphOn  = "▣"
	glyphOff = "☐"
)
