# VC++ Redistributable grounding — authoritative values

- Date grounded: 2026-06-24
- Provenance of URLs + silent args: user's own working utility https://github.com/UberMorgott/ItQoL (`Online Install ALL Visual C++ Redistributable.bat`) — official Microsoft download.microsoft.com / aka.ms links.
- SHA256: computed locally by downloading each static installer (Get-FileHash SHA256); every file's Authenticode signature verified Status=Valid (Microsoft).
- Registry detect keys + 2005/2008 product GUIDs: read directly from the user's machine (all redists already installed there).

## Verify strategy (hybrid, per child)

- 2015-2022 + 2013 → `VerifyAuthenticodeMicrosoft` (aka.ms evergreen permalinks; cannot pin a static SHA). No SHA256.
- 2005 / 2008 / 2010 / 2012 → `VerifySHA256` with the pins below (static download.microsoft.com files).

## URLs + SHA256 pins (lowercase hex) + silent args

| Child ID | URL | SHA256 | Args |
|---|---|---|---|
| vc2005_x86 | https://download.microsoft.com/download/8/B/4/8B42259F-5D70-43F4-AC2E-4B208FD8D66A/vcredist_x86.exe | `8648c5fc29c44b9112fe52f9a33f80e7fc42d10f3b5b42b2121542a13e44adfd` | `/Q` |
| vc2005_x64 | https://download.microsoft.com/download/8/B/4/8B42259F-5D70-43F4-AC2E-4B208FD8D66A/vcredist_x64.exe | `4487570bd86e2e1aac29db2a1d0a91eb63361fcaac570808eb327cd4e0e2240d` | `/Q` |
| vc2008_x86 | https://download.microsoft.com/download/5/D/8/5D8C65CB-C849-4025-8E95-C3966CAFD8AE/vcredist_x86.exe | `8742bcbf24ef328a72d2a27b693cc7071e38d3bb4b9b44dec42aa3d2c8d61d92` | `/q` |
| vc2008_x64 | https://download.microsoft.com/download/5/D/8/5D8C65CB-C849-4025-8E95-C3966CAFD8AE/vcredist_x64.exe | `c5e273a4a16ab4d5471e91c7477719a2f45ddadb76c7f98a38fa5074a6838654` | `/q` |
| vc2010_x86 | https://download.microsoft.com/download/1/6/5/165255E7-1014-4D0A-B094-B6A430A6BFFC/vcredist_x86.exe | `99dce3c841cc6028560830f7866c9ce2928c98cf3256892ef8e6cf755147b0d8` | `/quiet /norestart` |
| vc2010_x64 | https://download.microsoft.com/download/1/6/5/165255E7-1014-4D0A-B094-B6A430A6BFFC/vcredist_x64.exe | `f3b7a76d84d23f91957aa18456a14b4e90609e4ce8194c5653384ed38dada6f3` | `/quiet /norestart` |
| vc2012_x86 | https://download.microsoft.com/download/1/6/B/16B06F60-3B20-4FF2-B699-5E9B7962F9AE/VSU_4/vcredist_x86.exe | `b924ad8062eaf4e70437c8be50fa612162795ff0839479546ce907ffa8d6e386` | `/quiet /norestart` |
| vc2012_x64 | https://download.microsoft.com/download/1/6/B/16B06F60-3B20-4FF2-B699-5E9B7962F9AE/VSU_4/vcredist_x64.exe | `681be3e5ba9fd3da02c09d7e565adfa078640ed66a0d58583efad2c1e3cc4064` | `/quiet /norestart` |
| vc2013_x86 | https://aka.ms/highdpimfc2013x86enu | (Authenticode — no pin) | `/quiet /norestart` |
| vc2013_x64 | https://aka.ms/highdpimfc2013x64enu | (Authenticode — no pin) | `/quiet /norestart` |
| vc2022_x86 | https://aka.ms/vs/17/release/vc_redist.x86.exe | (Authenticode — no pin) | `/install /quiet /norestart` |
| vc2022_x64 | https://aka.ms/vs/17/release/vc_redist.x64.exe | (Authenticode — no pin) | `/install /quiet /norestart` |

## Detection (grounded on this machine — IMPORTANT view correction)

KEY FINDING: on 64-bit Windows the 2010/2012/2013 runtime keys live under **WOW6432Node for BOTH arches** (the 64-bit-view path `...\1X.0\VC\Runtimes` has no children). Only 2015-2022 (14.0) x64 lives in the native 64-bit view. So legacy runtime detect must use `ViewWow6432` for x64 too — NOT `ViewDefault64`.

| Child | Detect type | Path | Value | View |
|---|---|---|---|---|
| vc2022_x64 | RegSet | `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64` | Installed (dword=1) | ViewDefault64 |
| vc2022_x86 | RegSet | `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x86` | Installed | ViewWow6432 |
| vc2013_x64 | RegSet | `SOFTWARE\Microsoft\VisualStudio\12.0\VC\Runtimes\x64` | Installed | **ViewWow6432** |
| vc2013_x86 | RegSet | `SOFTWARE\Microsoft\VisualStudio\12.0\VC\Runtimes\x86` | Installed | ViewWow6432 |
| vc2012_x64 | RegSet | `SOFTWARE\Microsoft\VisualStudio\11.0\VC\Runtimes\x64` | Installed | **ViewWow6432** |
| vc2012_x86 | RegSet | `SOFTWARE\Microsoft\VisualStudio\11.0\VC\Runtimes\x86` | Installed | ViewWow6432 |
| vc2010_x64 | RegSet | `SOFTWARE\Microsoft\VisualStudio\10.0\VC\VCRedist\x64` | Installed | **ViewWow6432** |
| vc2010_x86 | RegSet | `SOFTWARE\Microsoft\VisualStudio\10.0\VC\VCRedist\x86` | Installed | ViewWow6432 |
| vc2008_x64 | RegPresent | `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{5FCE6D76-F5DC-37AB-B2B8-22AB8CEDB1D4}` | DisplayName | ViewDefault64 |
| vc2008_x86 | RegPresent | `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{9BE518E6-ECC6-35A9-88E4-87755C07200F}` | DisplayName | ViewWow6432 |
| vc2005_x64 | RegPresent | `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{ad8a2fa1-06e7-4b0d-927d-6e54b3d31028}` | DisplayName | ViewDefault64 |
| vc2005_x86 | RegPresent | `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{710f4c1c-cc18-4c49-8cbf-51240c89a1a2}` | DisplayName | ViewWow6432 |

Notes:
- 2005/2008 DisplayName on this machine: "Microsoft Visual C++ 2005 Redistributable" / "... (x64)"; "Microsoft Visual C++ 2008 Redistributable - x86/x64 9.0.30729.6161". GUIDs are SP/minor-version specific; a different minor would read not-installed → harmless idempotent re-install (force-reapply covers it).
- AcceptExit for all children: `{0, 3010, 1638, 1641}`.
