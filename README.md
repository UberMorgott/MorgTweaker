# MorgTweaker

A portable, single-binary Windows tweaker with a terminal UI (Bubble Tea v2).

The engine is **data-driven**: each tweak is a declarative `core.Tweak` value built
from small, reusable action executors, with asynchronous probing, automatic
backup/rollback, privilege brokering, and verify-after-apply.

## Highlights

- **Data-driven catalog** — a tweak is a `core.Tweak` literal: a name/description
  (RU + EN), a list of actions, and an optional precondition gate. Adding a tweak
  means adding a literal to `internal/catalog` — no new types.
- **Reusable action executors** — registry set/delete (WOW64 view-aware), service
  start, run command, and download + install (signature/hash verified). Tweaks
  compose them.
- **Select-then-act UI** — check the tweaks you want, then use the bottom button
  bar: **Apply** runs every checked appliable tweak in sequence, **Rollback** reverts
  every checked applied tweak. Clicking only selects; nothing is applied by accident.
- **Live progress screen** — while applying or rolling back, the list is replaced by
  stacked progress bars: overall (when more than one tweak), the current tweak, and a
  situational download/install bar for tweaks that fetch files.
- **Backup & rollback** — every action snapshots its prior state before writing, so a
  single tweak or an entire category can be rolled back to its exact prior state.
- **Verify-after-apply** — each action is re-probed after writing; if a protected
  setting silently reverts, the tweak is reported as *blocked* with a clear reason
  rather than a raw OS error.
- **Privilege broker** — self-elevating manifest (requireAdministrator); the app
  re-launches elevated if started without admin; SeDebugPrivilege plus
  SYSTEM / TrustedInstaller impersonation and registry-key take-ownership for actions
  that need more than admin.
- **Bilingual TUI** — RU / EN, switchable at runtime, mouse-driven.

## Catalog

Three categories:

| Category | Tweaks |
|----------|--------|
| Prep     | disable UAC, disable SmartScreen, **disable Defender (durable, until re-enabled)**, pause Windows Update, disable Security Center notifications, **install latest Visual C++ Redistributable** |
| Explorer | show file extensions |
| Privacy  | disable telemetry (DiagTrack) |

- **Disable Defender** is a reversible toggle: it suppresses Defender for the session
  (process/folder exclusions, real-time off) and durably disables the Defender services
  so it stays off across reboots until you re-enable it from the tweaker. It is gated on
  Tamper Protection — if Tamper Protection is on, the tweak deep-links you to the
  Windows Security toggle (Tamper Protection cannot be changed programmatically).
- **Install Visual C++ Redistributable** downloads the latest x64 + x86 build straight
  from Microsoft (`aka.ms` permalinks), verifies the Authenticode signature is a valid
  Microsoft signature before running it, and installs silently.

## Layout

```
cmd/morgtweaker/      entrypoint: admin check + self-elevation, build catalog, run TUI
internal/core/        data model: Tweak, Category, Action/Gate ifaces, Status, I18n, Backup
internal/action/      executors: RegSet/RegDelete, ServiceStart, Run, DownloadInstall,
                      DefenderSuppress, DefenderServiceDisable, registry take-ownership, TamperGate
internal/catalog/     Build() the catalog — one file per category
internal/engine/      probe aggregation, Apply (atomic, verify-after, brokered), async commands
internal/backup/      keyed JSON snapshot store (per tweak#action)
internal/elevate/     admin check, SeDebugPrivilege, SYSTEM / TrustedInstaller impersonation
internal/ui/          Bubble Tea v2 model (ui/render/update split), progress screen, RU/EN i18n
app.manifest          requireAdministrator
build.bat             release build (go generate + stripped build -> dist/)
install.ps1           one-liner release installer
```

## Build

Requires Go 1.26.4+. Target is `windows/amd64`, CGO disabled.

Easiest — run `build.bat`. It embeds the manifest + version resource via
`goversioninfo`, then builds `dist\MorgTweaker.exe` (stripped):

```bat
build.bat
```

`goversioninfo` is needed once:

```sh
go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
```

(make sure `%GOPATH%\bin` is on your `PATH`).

Manual equivalent:

```powershell
go generate ./...        # embeds app.manifest -> UAC + version info (.syso)
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -trimpath -ldflags "-s -w" -o dist\MorgTweaker.exe .\cmd\morgtweaker
```

Without the generated `.syso`, the binary still builds but will **not**
self-elevate via the manifest — the app then re-launches itself elevated at startup.

## Test

```sh
go test ./...
go vet ./...
```

## Run

Double-click `MorgTweaker.exe` (UAC prompt appears) or run it from a terminal.

| Key / click | Action |
|-------------|--------|
| `tab` / `←` `→` | switch pane |
| `↑` `↓` (or `k` / `j`) | move |
| `space` / `enter` / click | toggle selection (does not apply) |
| **Apply** button | apply every checked appliable tweak, in sequence |
| **Rollback** button | roll back every checked applied tweak |
| `esc` | cancel an in-flight apply/rollback |
| **RU/EN** button (`l`) | toggle language |
| **Quit** button (`q`) | quit |

Bright rows are appliable; grey rows are already applied or unavailable. Checkboxes
appear only on rows that have an available action.

## Install (one-liner)

After publishing a release with a `MorgTweaker.exe` asset:

```powershell
irm https://raw.githubusercontent.com/UberMorgott/MorgTweaker/main/install.ps1 | iex
```

It downloads the latest release's `MorgTweaker.exe` to `%TEMP%` and launches it
(the EXE self-elevates via its manifest).
