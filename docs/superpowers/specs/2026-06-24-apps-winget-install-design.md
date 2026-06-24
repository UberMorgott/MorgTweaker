# Apps category — winget bootstrap + app installs — design

- Date: 2026-06-24
- Scope: new catalog category + two native Go actions + one gate. Reuses the
  existing expandable parent/child + tri-state UI (NO new UI code).

## Goal

- A new "Программы" (apps) menu category that installs winget, then four apps via
  winget: PowerShell 7, 7-Zip, Windows Terminal, VLC.
- Everything is a native Go tweak (Go-orchestrated `os/exec`; no remote
  `irm|iex`). Appx steps unavoidably call `powershell.exe` (no Go API for
  `Add-AppxPackage`), exactly as `DownloadInstall` calls PowerShell for the
  Authenticode check.
- winget is unsupported on Windows 8/8.1 and Windows 10 < 1809 (build 17763): a
  build gate marks the tweaks Blocked there with a clear message.

## Research grounding (verified)

- winget OS floor: Windows 10 1809 / build 17763. Win8/8.1 unsupported. (MS Learn)
- Manual/robust bootstrap (Store-independent): GitHub release of
  microsoft/winget-cli → `.msixbundle` + `DesktopAppInstaller_Dependencies.zip`
  (+ `*_License*.xml`), deps installed BEFORE the bundle, via Windows PowerShell
  5.1 (`Add-AppxPackage`). PS7's Appx module fails (0x80131539) → must use
  `powershell.exe` (5.1) or `Import-Module Appx -UseWindowsPowerShell`.
- Verified package IDs: `Microsoft.PowerShell` (PS7), `7zip.7zip`,
  `Microsoft.WindowsTerminal`, `VideoLAN.VLC`.
- `winget install` silent/unattended flags: `--id <ID> -e --source winget
  --scope machine --silent --accept-package-agreements --accept-source-agreements`.
  `--scope machine` is best-effort (some MSIX packages are per-user).
- Exit codes (returnCodes.md): 0 success; 0x8A150109 (-1978334967)
  INSTALL_REBOOT_REQUIRED_TO_FINISH; 0x8A150101 (-1978334975) PACKAGE_IN_USE.
  The implementer MUST confirm the "already installed / no upgrade applicable"
  code from returnCodes.md before pinning AcceptExit (do not guess).

## Components

### 1. BuildGate (`internal/action/buildgate.go`)

A `core.Gate` that blocks tweaks on an unsupported Windows build.

```go
type BuildGate struct {
    Min     int                              // minimum CurrentBuild, e.g. 17763
    buildFn func(core.ActionContext) (int, error) // injectable; nil -> registry read
}
func NewBuildGate(min int) BuildGate
func (g BuildGate) Check(ctx core.ActionContext) (bool, core.Status, core.GateAction)
```

- Default build source: read `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`
  value `CurrentBuild` (REG_SZ) via `golang.org/x/sys/windows/registry`, atoi.
- `Check`: build >= Min -> `(true, core.StatusOff, core.GateAction{})`. build < Min
  -> `(false, core.StatusBlocked, GateAction{Label: RU/EN "winget требует Windows
  10 1809+ (Win 8/8.1 не поддерживается)"})` (no deep-link URL).
- Read error -> FAIL CLOSED: `(false, core.StatusBlocked, ...)` with a "could not
  determine Windows version" label.
- Tests: injected buildFn for 17762 (blocked), 17763 (ok), 22000 (ok), error
  (blocked).

### 2. WingetInstall (`internal/action/wingetinstall.go`)

Installs one package via winget; exit-code aware; Probe via `winget list`.

```go
type WingetInstall struct {
    ID         string         // e.g. "7zip.7zip"
    AcceptExit []int          // success exit codes (nil/empty -> {0})
    Elev       core.Elevation
    runCode    func(ctx context.Context, name string, args ...string) (int, error) // injectable
}
```

- `Apply(on=true)`: args = `install --id <ID> -e --source winget --scope machine
  --silent --accept-package-agreements --accept-source-agreements`; run `winget`;
  map exit code via AcceptExit (default {0}, plus reboot code). Non-accepted ->
  error. `Apply(on=false)`: honest no-op (uninstall is separate).
- `Probe`: run `winget list --id <ID> -e --source winget`; exit 0 -> `PointOn`,
  else `PointOff` (also covers winget-absent -> the runner errors -> PointOff).
- `Snapshot`/`Restore`: honest no-op. `SkipVerifyAfter() bool { return true }`
  (install is one-shot, decided by exit code, like DownloadInstall).
- `Level()` returns Elev.
- Reuse `execRunCode` from downloadinstall.go for the default runner.
- Tests (injected runCode): args contain the ID + silent flags; accepted vs
  rejected exit codes; Probe maps exit 0 -> On, non-zero/err -> Off.

### 3. WingetBootstrap (`internal/action/wingetbootstrap.go`)

Native Go orchestration of the robust manual bootstrap.

```go
type WingetBootstrap struct {
    Elev    core.Elevation
    apiGet  func(ctx context.Context, url string) ([]byte, error)                       // GitHub API JSON; injectable
    httpGet func(ctx context.Context, url string) (io.ReadCloser, int64, error)         // asset download; injectable (reuse httpGetDefault)
    psRun   func(ctx context.Context, name string, args ...string) ([]byte, error)      // PS 5.1 runner; injectable (reuse realPSRunner)
    verFn   func(ctx context.Context) (present bool)                                     // winget --version probe; injectable
}
```

- `Probe`: `verFn` (default: `winget --version`, exit 0 -> present) -> On/Off.
- `Apply(on=true)`:
  1. If winget already present (`verFn`) -> nil (idempotent).
  2. `apiGet` `https://api.github.com/repos/microsoft/winget-cli/releases/latest`
     (default: GET with `User-Agent` header — GitHub API requires it). Parse JSON
     `assets[]` (name, browser_download_url, size). Select the `*.msixbundle`, the
     `DesktopAppInstaller_Dependencies.zip`, and the `*License*.xml` (xml optional).
  3. Download each via `httpGet` to temp files, ctx-bound, progress via
     `ctx.Report`, verifying the byte count against the asset `size` (retry up to
     3x on mismatch/short read — the bundle is ~200MB and old transports truncate).
  4. Authenticode-verify the `.msixbundle` is Microsoft-signed (reuse the
     `verifyAuthenticodeMicrosoft` seam / its helpers from downloadinstall.go).
  5. Extract the deps zip; pick the arch subdir (x64/x86/arm64 from
     `runtime.GOARCH` or `PROCESSOR_ARCHITECTURE`); collect `*.appx` (fallback
     `*.msix`).
  6. Via `psRun` -> `powershell.exe` (Windows PowerShell 5.1, NOT pwsh) with an
     `-EncodedCommand`: `Add-AppxPackage` each dependency, then the bundle with
     `-DependencyPath <deps> -ForceUpdateFromAnyVersion`. (All-users
     `Add-AppxProvisionedPackage` is OUT OF SCOPE for v1 — per-user Add-AppxPackage
     is the reliable path on broken builds; note as a future option.)
  7. Re-check `verFn`; return an error if winget is still absent.
- `Snapshot`/`Restore`: honest no-op. `SkipVerifyAfter`: true.
- Tests (injected apiGet/httpGet/psRun/verFn): asset selection from a fixture
  JSON; size-mismatch triggers retry then error; already-present short-circuits to
  no-op; the PS command string includes Add-AppxPackage + the bundle path +
  -ForceUpdateFromAnyVersion. NO real network/PowerShell in tests.

### 4. Catalog (`internal/catalog/apps.go` + one line in `Build()`)

```go
func apps() []core.Tweak  // returns the single parent with five children
```

- Parent `apps.programs` (`Category:"apps"`, `Elevation: core.ElevAdmin`),
  `Name`: RU "Программы (winget)", EN "Programs (winget)". No Actions (it is a
  parent). `Children` in order:
  1. `apps.winget` — Name RU "Установить winget" / EN "Install winget";
     Actions `[WingetBootstrap{Elev: ElevAdmin}]`; `Gate: NewBuildGate(17763)`;
     `Elevation: ElevAdmin`.
  2. `apps.powershell` — "PowerShell 7"; `WingetInstall{ID:"Microsoft.PowerShell"}`.
  3. `apps.7zip` — "7-Zip"; `WingetInstall{ID:"7zip.7zip"}`.
  4. `apps.terminal` — "Windows Terminal"; `WingetInstall{ID:"Microsoft.WindowsTerminal"}`.
  5. `apps.vlc` — "VLC"; `WingetInstall{ID:"VideoLAN.VLC"}`.
  - Every child: `Elevation: ElevAdmin`, `Gate: NewBuildGate(17763)`,
    `AcceptExit` including the reboot code (and the confirmed already-installed
    code).
- `Build()`: add `{ID:"apps", Name: I18n{RU:"Программы", EN:"Programs"}, Tweaks: apps()}`
  after privacy.
- Order matters: the sequential apply batch runs `apps.winget` first (children are
  queued in declaration order), so winget exists before the four installs run.
- Tests (`apps_test.go`): category present; parent has 5 children in the right
  order with winget first; each child's ID and package ID correct; each child has a
  BuildGate; the parent is `IsParent()`.

## UI reuse (no changes)

The parent/child expandable list + tri-state checkbox + `(*)` marker (shipped in
`2026-06-24-vcredist-parent-checkbox-design.md`) is generic over any `IsParent`
tweak. The new parent renders and behaves identically with zero UI edits. A
BuildGate-Blocked child renders via the existing Blocked status path; the parent's
aggregate status shows "…/Unknown" when children are gated (acceptable for v1).

## Error handling

- Fail-closed everywhere a trust/precondition is unknown: bad build read ->
  Blocked; unsigned/altered bundle -> Authenticode failure -> abort; size mismatch
  -> retry then error.
- winget-absent during an app Probe -> the app reads Off (appliable), not an error.
- All long-running steps are ctx-bound so UI cancel/quit aborts them.

## Out of scope (v1)

- All-users `Add-AppxProvisionedPackage` provisioning (fragile on broken builds).
- arm64-only edge cases beyond arch-subdir selection.
- Uninstall / rollback of installed apps (installs are one-shot, no inverse).

## Files

- `internal/action/buildgate.go` (+ `_test.go`)
- `internal/action/wingetinstall.go` (+ `_test.go`)
- `internal/action/wingetbootstrap.go` (+ `_test.go`)
- `internal/catalog/apps.go` (+ `_test.go`), `internal/catalog/catalog.go` (one line)
