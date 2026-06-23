# MorgTweaker

A portable, single-binary Windows tweaker with a terminal UI (Bubble Tea v2).

The engine is **data-driven**: each tweak is a declarative `core.Tweak` value built
from small, reusable action executors, with asynchronous probing, automatic
backup/rollback, privilege brokering, and verify-after-apply.

## Highlights

- **Data-driven catalog** — a tweak is a `core.Tweak` literal: a name/description
  (RU + EN), a list of actions, and an optional precondition gate. Adding a tweak
  means adding a literal to `internal/catalog` — no new types.
- **Reusable action executors** — registry set/delete, service start, run command,
  and download + install (SHA-256 gated). Tweaks compose them.
- **Async, lag-free UI** — probing and applying run as Bubble Tea commands; the
  view never blocks on I/O.
- **Backup & rollback** — every action snapshots its prior state before writing, so
  a single tweak or an entire category can be rolled back to its exact prior state
  (including removing a value that was created).
- **Verify-after-apply** — each action is re-probed after writing; if a protected
  setting (e.g. Defender Tamper Protection) silently reverts, the tweak is reported
  as *blocked* rather than falsely *applied*.
- **Privilege broker** — self-elevating manifest (requireAdministrator);
  SeDebugPrivilege plus SYSTEM / TrustedInstaller impersonation for actions that
  need more than admin.
- **Bilingual TUI** — RU / EN, switchable at runtime.

## Catalog

Three categories, seven tweaks:

| Category | Tweaks |
|----------|--------|
| Prep     | disable UAC, disable SmartScreen, disable Defender (composite, multi-point), pause Windows Update, disable Security Center notifications |
| Explorer | show file extensions |
| Privacy  | disable telemetry (DiagTrack) |

Plus an optional Visual C++ Redistributable installer action (download +
SHA-256 verification).

## Layout

```
cmd/morgtweaker/      entrypoint: privilege setup, build catalog, run TUI
internal/core/        data model: Tweak, Category, Action/Gate ifaces, Status, I18n, Backup
internal/action/      action executors: RegSet/RegDelete, ServiceStart, Run, DownloadInstall, TamperGate
internal/catalog/     Build() the catalog — one file per category
internal/engine/      probe aggregation, Apply (atomic, verify-after, brokered), async commands
internal/backup/      keyed JSON snapshot store (per tweak#action)
internal/elevate/     admin check, SeDebugPrivilege, SYSTEM / TrustedInstaller impersonation
internal/ui/          Bubble Tea v2 model (ui/render/update split), inline RU/EN i18n
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
self-elevate (no embedded manifest) — run it from an elevated console.

## Test

```sh
go test ./...
go vet ./...
```

## Run

Double-click `MorgTweaker.exe` (UAC prompt appears) or run it from a terminal.

| Key | Action |
|-----|--------|
| `tab` / `←` `→` | switch pane |
| `↑` `↓` (or `k` / `j`) | move |
| `space` / `enter` | toggle |
| `a` | apply the focused tweak |
| `r` | roll back the focused tweak |
| `R` | roll back the whole category |
| `o` | run the focused tweak's action |
| `esc` | cancel an in-flight apply |
| `l` | toggle language (RU / EN) |
| `q` | quit |

## Install (one-liner)

After publishing a release with a `MorgTweaker.exe` asset:

```powershell
irm https://raw.githubusercontent.com/UberMorgott/MorgTweaker/main/install.ps1 | iex
```

It downloads the latest release's `MorgTweaker.exe` to `%TEMP%` and launches it
(the EXE self-elevates via its manifest).
