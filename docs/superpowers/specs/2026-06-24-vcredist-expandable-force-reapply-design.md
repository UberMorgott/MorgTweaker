# VC++ Redistributables: expandable group + force-reapply — Design

- Date: 2026-06-24
- Project: MorgTweaker (module `morgtweaker`, `D:\MorgDEV\wintweaker`)
- Status: approved-for-planning (pending user spec review)

## Problem

- Current `prep.install_vcredist` tweak holds two `DownloadInstall` actions (VC++ 2015-2022 x64 + x86) under one checkbox. It greys out when both runtimes are detected installed and offers no way to reinstall or to manage other VC++ versions.
- Two gaps to close:
  1. **Force-reapply**: an already-applied (grey, `StatusOn`) tweak selected with the checkbox is currently filtered out of `[Apply]` (only `Off`/`Partial` are appliable). User wants a checked grey item to be re-applied on `[Apply]` (RegSet rewrites same value; redist re-downloads + reinstalls).
  2. **Legacy VC++ coverage + per-item control**: old games/apps need VC++ 2005-2013. User wants all of 2005, 2008, 2010, 2012, 2013 (x64+x86) plus the existing 2015-2022, surfaced as ONE expandable parent row whose children install individually.

## Decisions (from brainstorming)

- **Reapply UX**: `[Apply]` acts on ALL checked tweaks whose status is `Off`, `Partial`, OR `On`. `[Rollback]` keeps acting on checked `On`/`RebootPending`. A checked grey row is ambiguous only across the two buttons — the pressed button decides direction. Minimal change.
- **Legacy scope**: all of 2005, 2008, 2010, 2012, 2013 — plus existing 2015-2022. Each version × arch.
- **Grouping**: ONE expandable parent tweak "Install VC++ Redistributable" in the `prep` category. Inline expand/collapse (tree, indented children in the same list — NOT a separate screen, NOT a new category). Each child = one version+arch, individually selectable/installable.
- **Verify**: hybrid, chosen per child. 2015-2022 stays `VerifyAuthenticodeMicrosoft` (evergreen permalink). Legacy uses `VerifySHA256` with a pin computed during implementation; any installer whose download is not reliably static falls back to `VerifyAuthenticodeMicrosoft`. No new "combined" verify mode — the per-action `Verify` field already supports this choice.
- **Detect**: full per version+arch. 2010/2012/2013/2022 use their registry `Installed` dword. 2005/2008 have no `Installed` flag → detect MSI `Uninstall\{ProductGUID}` key presence via a new `RegPresent` detect action.

## Architecture

### 1. Force-reapply (`internal/ui/render.go`)

- `statusAppliable` becomes `st == StatusOff || st == StatusPartial || st == StatusOn`.
- Consequence (no other code change): `applySelected` → `selectedByStatus(statusAppliable)` now queues checked `On` tweaks; `dispatchApply(tw, true)` runs `Apply(on=true)` on them.
- `statusRollbackable` and `statusHasAction` unchanged. Grey rows already show a checkbox (rollbackable), so they are already selectable — only the Apply filter widens.
- Behaviour: RegSet `Apply(on=true)` re-writes the same value (idempotent). `DownloadInstall.Apply(on=true)` re-downloads + reinstalls. For an already-present redist the installer returns 1638, which is in `vcredistAcceptExit` → success.

### 2. Parent/child catalog (`internal/core`, `internal/catalog`)

- Extend `core.Tweak` with `Children []core.Tweak` (zero value = leaf, current behaviour unchanged).
- Parent "redist group" tweak: `ID: "prep.vcredist"`, no own `Actions`; its status is the AGGREGATE of its children's statuses (reuse the engine aggregation rule: all-On → On/grey, all-Off → Off, mix → Partial).
- 12 child tweaks (leaf), each one `DownloadInstall` action + its Detect:
  - `prep.vcredist.vc2022_x64`, `…_x86` (split from today's `prep.install_vcredist`)
  - `prep.vcredist.vc2013_x64/x86`, `vc2012_x64/x86`, `vc2010_x64/x86`, `vc2008_x64/x86`, `vc2005_x64/x86`
- Remove the old combined `prep.install_vcredist`; its two actions become the two 2022 children.
- Engine: parent Probe = aggregate over children; parent Apply(on) = apply each child in order; parent Rollback = honest no-op per child (installs have no inverse). Child Probe/Apply unchanged from leaf behaviour.

### 3. Inline-expand UI (`internal/ui`)

- Model: add `expanded map[string]bool` keyed by parent tweak ID (collapsed by default).
- Visible-rows model: the right pane renders a FLATTENED list of visible rows = for each category tweak, the row itself, then (if it has children and is expanded) its children indented one level. Cursor, selection, and checkbox hit-testing all operate on this flattened visible list, not on `curTweaks()` directly.
- Keys: `Enter`/`→` on a parent toggles `expanded`; `←` collapses. Children render with indent + child status marker (`● installed` / `○ not installed`), parent shows a `▸`/`▾` glyph + aggregate marker (e.g. "some missing").
- Selection/apply:
  - Check parent + `[Apply]` → applies all children (via parent Apply = apply-each-child).
  - Expand, check a child + `[Apply]` → installs only that child (no sibling re-download — solves the "re-download everything for one missing" waste).
  - `[Rollback]` unchanged (no-op for installs).
- Status colour rule UNCHANGED: installed/`On` rows stay GREY/dim (`● installed`), not-installed/`Off` stay BRIGHT (`○ not installed`), per the existing `rightBody` switch. Force-reapply widens ONLY the `[Apply]` *filter* (`statusAppliable`), NOT the row colour — a grey `On` row remains visually grey but is now selectable-and-appliable. This keeps "grey = already installed" reading intact while allowing reinstall.

### 4. Verify (hybrid) — `internal/catalog`

- Per child `Verify`:
  - 2022 x64/x86: `VerifyAuthenticodeMicrosoft`, URLs `https://aka.ms/vs/17/release/vc_redist.{x64,x86}.exe` (existing).
  - 2005-2013: `VerifySHA256` + pinned hash (computed in implementation), or `VerifyAuthenticodeMicrosoft` fallback where a static hash can't be relied on.
- `AcceptExit`: reuse `{0, 3010, 1638, 1641}` for all.
- Args: legacy installers differ. 2010+ commonly `/install /quiet /norestart` (or `/q /norestart`); 2005/2008 are MSI-wrapped and use `/q` (exact silent flags GROUNDED per installer in implementation).

### 5. Detect — `internal/action`, `internal/catalog`

- Reuse `RegSet`-as-detect for versions with a clean `Installed` dword:
  - 2022: `VisualStudio\14.0\VC\Runtimes\{x64,x86}` (existing `vcRuntimeDetect`).
  - 2013: `VisualStudio\12.0\VC\Runtimes\{x64,x86}`.
  - 2012: `VisualStudio\11.0\VC\Runtimes\{x64,x86}`.
  - 2010: `VisualStudio\10.0\VC\VCRedist\{x64,x86}` (note: `VCRedist`, not `Runtimes`).
  - x86 always in WOW6432Node view; x64 in default-64 view (existing view logic).
- New `RegPresent` detect action for 2005/2008 (no `Installed` flag): probes existence of `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{ProductGUID}` (x86 GUIDs under WOW6432Node). Key/value present → `PointOn`, absent → `PointOff`. No value comparison (string DisplayNames vary by SP/language and are fragile).
- `RegPresent` implements `core.Action` with Apply/Snapshot/Restore as honest no-ops (detect-only) and a real Probe; or is a thin detect-only type used solely as `DownloadInstall.Detect`.

### 6. Grounding (MANDATORY — no literals from memory)

Before authoring any catalog literal, obtain from a real source and verify:
- Download URLs for 2005/2008/2010/2012/2013 (x64+x86) — Microsoft permalinks (`aka.ms/...`) or `download.microsoft.com` static files.
- SHA256 of each downloaded installer (download → compute → pin). If a download isn't static, mark that child Authenticode-fallback.
- Registry detect keys for 2010/2012/2013 — confirm `Installed` path+view on a real machine.
- Product GUIDs for 2005/2008 x64+x86 `Uninstall` keys — confirm on a real machine (these change by minor/SP version; pin the shipped redist's GUID and accept that a different minor may read not-installed → at worst a harmless re-install).
- Silent-install args per legacy installer.

The spec forbids inventing these; implementation grounds them (download/registry inspection) and records the exact values.

## Testing (TDD)

- `statusAppliable(StatusOn) == true` (render unit).
- `applySelected` queues a checked `On` tweak; `dispatchApply(_, true)` invoked (update unit, fake engine).
- `[Rollback]` on checked `On` still works; not double-handled (update unit).
- Parent Probe aggregates children (all-On→On, mix→Partial, all-Off→Off) (engine unit).
- Parent Apply applies each child once, in order (engine unit, fake actions).
- `RegPresent` Probe: present→On, absent→Off, both views (action unit).
- Per-arch detect view: x86 reads WOW6432Node, x64 reads 64-bit (action unit).
- Flattened visible rows: collapsed shows parent only; expanded shows parent + indented children; cursor/selection index the flattened list (ui unit).
- Expand/collapse key toggles `expanded` (ui unit).
- Integration (live machine, flags already zeroed x64=0/x86=0): reapply a redist child → installer runs → Detect re-probes On (not falsely Blocked); 1638 treated as success.

## Risks / open items

- 2005/2008 GUID fragility: pinned GUID may miss a differently-versioned install → harmless re-install (idempotent). Accepted.
- Verify-after on reapply: if an installer returns 1638 WITHOUT writing `Installed=1` and the flag was zeroed, re-probe reads Off → false Blocked. Mitigation: `/install` rewrites the flag in practice; confirm in integration test. If it fails, set those children `SkipVerifyAfter` via `Detect==nil` is NOT acceptable (we want detect) — instead accept 1638 as "installed" by trusting Apply exit code over re-probe for the redist case (revisit in plan if observed).
- UI cursor/selection refactor to a flattened visible-row list touches existing navigation — cover with units before changing render.

## Out of scope

- Uninstall of redists (no inverse; Rollback stays no-op).
- Other runtime families (.NET, DirectX).
- Per-row inline buttons (interaction stays checkbox + bottom bar, consistent with the rest of the app).
