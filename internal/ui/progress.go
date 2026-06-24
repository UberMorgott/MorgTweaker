package ui

// Progress screen — the apply/rollback batch view. While a batch runs, this view
// REPLACES the two-pane list (m.screen == screenProgress), inside the same rubber
// border + title, showing up to THREE stacked determinate bars:
//
//	1) OVERALL   — completed/total tweaks; shown only when the batch has >1 tweak.
//	2) CURRENT   — the in-flight tweak's name + its progress (determinate when a
//	               percent streams, e.g. a download_install; else a phase label).
//	3) DOWNLOAD  — bytes / total + MB + speed + stage; shown only while the current
//	               action is a download/install (its progress carries byte counts).
//
// Every value is read from cached model state fed by messages (no I/O); the View
// stays pure. The whole region is sized from the live window dimensions so it
// always fits the border and never overflows.

import (
	"fmt"
	"strings"

	"morgtweaker/internal/engine"
)

// barFraction clamps a 0..1 fraction and maps it to a filled-cell count over an
// inner width of w cells (rounded to nearest). Returns 0 for w<=0. The clamp makes
// 0% → 0 cells and 100% (or any overshoot) → exactly w cells.
func barFraction(frac float64, w int) int {
	if w <= 0 {
		return 0
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(w) + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > w {
		filled = w
	}
	return filled
}

// renderBar draws a determinate bar of exactly w cells: a bright filled run sized
// to frac, the remainder a dim track. w<=0 yields an empty string.
func renderBar(frac float64, w int) string {
	if w <= 0 {
		return ""
	}
	filled := barFraction(frac, w)
	return barFillStyle.Render(strings.Repeat(barFillRune, filled)) +
		barTrackStyle.Render(strings.Repeat(barTrackRune, w-filled))
}

// progressRegion builds the progress-screen rows: a centered stack of the visible
// bars, padded to exactly regionH rows and `total` cells wide. It never returns
// more than regionH rows (truncates on a tiny terminal) and never less (blank
// pad), so the surrounding frame stays flush and nothing clips.
func (m model) progressRegion(total, regionH int) []string {
	if regionH < 1 {
		regionH = 1
	}
	var rows []string
	for _, b := range m.progressBlocks(total) {
		rows = append(rows, b...)
		rows = append(rows, "") // one blank line between blocks
	}
	// Drop the trailing separator blank, if any.
	if n := len(rows); n > 0 && rows[n-1] == "" {
		rows = rows[:n-1]
	}

	// Vertically center within regionH (pad top+bottom); truncate if too tall.
	if len(rows) > regionH {
		rows = rows[:regionH]
	}
	pad := regionH - len(rows)
	top := pad / 2
	out := make([]string, 0, regionH)
	for i := 0; i < top; i++ {
		out = append(out, padRight("", total))
	}
	for _, r := range rows {
		out = append(out, padRight(r, total))
	}
	for len(out) < regionH {
		out = append(out, padRight("", total))
	}
	return out
}

// progressBlocks returns the visible bar blocks (each a slice of caption+bar
// lines), top→bottom, applying the visibility rules for the three bars.
func (m model) progressBlocks(total int) [][]string {
	var blocks [][]string

	// Bar width leaves a 1-cell indent on each side so the bar reads inside the
	// frame; captions use the full width.
	barW := maxi(total-2, 1)
	indent := " "

	// 1) OVERALL — only when the batch has more than one tweak.
	if m.batchTotal > 1 {
		caption := fmt.Sprintf(T(m.lang, kProgOverall), m.batchDone, m.batchTotal)
		frac := float64(m.batchDone) / float64(m.batchTotal)
		blocks = append(blocks, []string{
			barCaptionStyle.Render(truncDisplay(caption, total)),
			indent + renderBar(frac, barW),
		})
	}

	// 2) CURRENT TWEAK. When a download/install is streaming, its own bar (block 3)
	// already shows the in-flight progress for this tweak, so a separate generic
	// CURRENT bar would be a redundant duplicate — show ONLY the download bar (it
	// carries the tweak name). Otherwise (e.g. a registry tweak) show the CURRENT bar.
	if blk, ok := m.downloadBlock(total, barW, indent); ok {
		blocks = append(blocks, blk)
	} else if m.currentID != "" {
		blocks = append(blocks, m.currentBlock(total, barW, indent))
	}

	return blocks
}

// currentBlock renders the CURRENT-TWEAK bar: the tweak's display name + a phase
// label, and a determinate bar when a percent streams (download_install), else an
// empty (indeterminate) track that still shows the phase.
func (m model) currentBlock(total, barW int, indent string) []string {
	name := m.currentID
	if tw, ok := m.catalog.Find(m.currentID); ok {
		name = tweakName(m.lang, tw)
	}
	phase := T(m.lang, kProgApplying)
	if m.batchKind == batchRollback {
		phase = T(m.lang, kProgRollingBack)
	}

	// Determinate only when real percent flows (a streamed download carries Total>0).
	frac := 0.0
	determinate := false
	if p, ok := m.progress[m.currentID]; ok && p.Total > 0 {
		frac = float64(p.Pct) / 100
		determinate = true
	}

	caption := name + "  " + barDetailStyle.Render(phase)
	if determinate {
		caption += barDetailStyle.Render(fmt.Sprintf("  %d%%", clampPct(m.progress[m.currentID].Pct)))
	}
	return []string{
		barCaptionStyle.Render(truncDisplay(caption, total)),
		indent + renderBar(frac, barW),
	}
}

// downloadBlock renders the situational download/install bar. ok=false (block
// hidden) unless the current tweak's latest progress is a download/install stage.
// A download stage shows a determinate bytes bar + MB done/total + speed; the
// install stage shows a full bar + the install label (no byte counters).
func (m model) downloadBlock(total, barW int, indent string) (block []string, ok bool) {
	if m.currentID == "" {
		return nil, false
	}
	p, has := m.progress[m.currentID]
	if !has {
		return nil, false
	}
	stage, isDL, known := m.downloadStage(p)
	if !known {
		return nil, false // not a download/install report
	}

	// Prefix the tweak's display name — this bar is the single in-flight indicator
	// for the current tweak (it replaces the generic CURRENT bar during a stream).
	name := m.currentID
	if tw, ok := m.catalog.Find(m.currentID); ok {
		name = tweakName(m.lang, tw)
	}

	// Install phase (or unknown-length download): no determinate byte counters.
	if !isDL || p.Total <= 0 {
		frac := 0.0
		if !isDL {
			frac = 1.0 // installing: download finished, show a full bar
		}
		caption := name + "  " + barDetailStyle.Render(stage)
		return []string{barCaptionStyle.Render(truncDisplay(caption, total)), indent + renderBar(frac, barW)}, true
	}

	frac := float64(p.Done) / float64(p.Total)
	detail := fmt.Sprintf("%s  %d%%  %.1f / %.1f MB  %.1f MB/s",
		stage, clampPct(p.Pct), bytesToMB(p.Done), bytesToMB(p.Total), m.dlSpeed/(1<<20))
	caption := name + "  " + barDetailStyle.Render(detail)
	return []string{
		barCaptionStyle.Render(truncDisplay(caption, total)),
		indent + renderBar(frac, barW),
	}, true
}

// downloadStage maps a download_install progress note to its localized stage
// label. isDL is true for the download phase, false for the install phase; known
// is false when the note is neither (so the situational bar stays hidden). The
// download_install action emits Note=="downloading" while streaming bytes and a
// note containing "install" ("installing" / "installed (reboot recommended)")
// once it runs the verified installer.
func (m model) downloadStage(p engine.ApplyProgressMsg) (stage string, isDL, known bool) {
	switch {
	case p.Note == "downloading":
		return T(m.lang, kProgDownloading), true, true
	case strings.Contains(p.Note, "install"):
		return T(m.lang, kProgInstalling), false, true
	}
	return "", false, false
}

// bytesToMB converts bytes to mebibytes for display.
func bytesToMB(b int64) float64 { return float64(b) / (1 << 20) }

// clampPct bounds a percent to [0,100] for display.
func clampPct(p int) int {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
