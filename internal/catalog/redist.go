package catalog

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// vcredistX64URL / vcredistX86URL are Microsoft's evergreen redirects to the
// LATEST VC++ 2015-2022 redistributables. Because "latest" is updated by
// Microsoft over time, the exact bytes cannot be pinned to a static SHA256 at
// authoring time. DownloadInstall therefore verifies these in
// VerifyAuthenticodeMicrosoft mode: the downloaded file must carry a Valid
// Authenticode signature by "O=Microsoft Corporation" or it is NOT run
// (fail-closed). No SHA256 pin is used.
const (
	vcredistX64URL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"
	vcredistX86URL = "https://aka.ms/vs/17/release/vc_redist.x86.exe"
)

// vc2013X64URL / vc2013X86URL are the aka.ms evergreen permalinks to the 2013
// runtime (highdpimfc2013*enu). Like the 2015-2022 redirects they cannot be
// pinned to a static SHA256, so they are Authenticode-verified (no pin).
const (
	vc2013X64URL = "https://aka.ms/highdpimfc2013x64enu"
	vc2013X86URL = "https://aka.ms/highdpimfc2013x86enu"
)

// Static download.microsoft.com installers for 2005/2008/2010/2012 — these are
// immutable files, so each carries a grounded lowercase-hex SHA256 pin
// (VerifySHA256). URLs + hashes from docs/superpowers/refs/vcredist-grounding.md.
const (
	vc2012X64URL = "https://download.microsoft.com/download/1/6/B/16B06F60-3B20-4FF2-B699-5E9B7962F9AE/VSU_4/vcredist_x64.exe"
	vc2012X86URL = "https://download.microsoft.com/download/1/6/B/16B06F60-3B20-4FF2-B699-5E9B7962F9AE/VSU_4/vcredist_x86.exe"
	vc2010X64URL = "https://download.microsoft.com/download/1/6/5/165255E7-1014-4D0A-B094-B6A430A6BFFC/vcredist_x64.exe"
	vc2010X86URL = "https://download.microsoft.com/download/1/6/5/165255E7-1014-4D0A-B094-B6A430A6BFFC/vcredist_x86.exe"
	vc2008X64URL = "https://download.microsoft.com/download/5/D/8/5D8C65CB-C849-4025-8E95-C3966CAFD8AE/vcredist_x64.exe"
	vc2008X86URL = "https://download.microsoft.com/download/5/D/8/5D8C65CB-C849-4025-8E95-C3966CAFD8AE/vcredist_x86.exe"
	vc2005X64URL = "https://download.microsoft.com/download/8/B/4/8B42259F-5D70-43F4-AC2E-4B208FD8D66A/vcredist_x64.exe"
	vc2005X86URL = "https://download.microsoft.com/download/8/B/4/8B42259F-5D70-43F4-AC2E-4B208FD8D66A/vcredist_x86.exe"

	vc2012X64SHA = "681be3e5ba9fd3da02c09d7e565adfa078640ed66a0d58583efad2c1e3cc4064"
	vc2012X86SHA = "b924ad8062eaf4e70437c8be50fa612162795ff0839479546ce907ffa8d6e386"
	vc2010X64SHA = "f3b7a76d84d23f91957aa18456a14b4e90609e4ce8194c5653384ed38dada6f3"
	vc2010X86SHA = "99dce3c841cc6028560830f7866c9ce2928c98cf3256892ef8e6cf755147b0d8"
	vc2008X64SHA = "c5e273a4a16ab4d5471e91c7477719a2f45ddadb76c7f98a38fa5074a6838654"
	vc2008X86SHA = "8742bcbf24ef328a72d2a27b693cc7071e38d3bb4b9b44dec42aa3d2c8d61d92"
	vc2005X64SHA = "4487570bd86e2e1aac29db2a1d0a91eb63361fcaac570808eb327cd4e0e2240d"
	vc2005X86SHA = "8648c5fc29c44b9112fe52f9a33f80e7fc42d10f3b5b42b2121542a13e44adfd"
)

// Per-version silent install args (grounded). The legacy installers do NOT all
// take the modern "/install /quiet /norestart" form:
//
//	2005      -> /Q
//	2008      -> /q
//	2010/12/13 -> /quiet /norestart
//	2015-2022 -> /install /quiet /norestart
var (
	vcArgs2005  = []string{"/Q"}
	vcArgs2008  = []string{"/q"}
	vcArgsQuiet = []string{"/quiet", "/norestart"}             // 2010/2012/2013
	vcArgs2022  = []string{"/install", "/quiet", "/norestart"} // 2015-2022
)

// vcredistAcceptExit are the installer exit codes treated as success:
//
//	0    success
//	3010 success, reboot required
//	1638 a newer version is already installed (treat as satisfied)
//	1641 success, reboot has been initiated
var vcredistAcceptExit = []int{0, 3010, 1638, 1641}

// vcRuntimeDetect reads a version's runtime "Installed" dword in the given view.
// keyVer is the VisualStudio key version ("14.0","12.0","11.0","10.0"); sub is
// the runtime subkey family ("VC\\Runtimes" for 2012/2013/2022, "VC\\VCRedist"
// for 2010). View is EXPLICIT per child: on 64-bit Windows the 2010/2012/2013
// runtime keys register under WOW6432Node for BOTH arches (the native 64-bit
// path has no children), so those x64 children must read ViewWow6432 — only
// 2015-2022 (14.0) x64 lives in the native 64-bit view. A missing key reads as
// Off (the RegSet probe returns PointOff when the key/value is absent), so a
// fresh box is correctly reported as not-installed rather than erroring.
func vcRuntimeDetect(keyVer, sub, arch string, view action.RegView) action.RegSet {
	return action.RegSet{
		Root:  registry.LOCAL_MACHINE,
		Path:  `SOFTWARE\Microsoft\VisualStudio\` + keyVer + `\` + sub + `\` + arch,
		Value: "Installed", Kind: action.KindDword, On: uint64(1), Off: uint64(0),
		Elev: core.ElevUser, View: view,
	}
}

// vcUninstallDetect detects 2005/2008 by the existence of their MSI Uninstall
// key's DisplayName value. guid is the product GUID (with literal braces). The
// view is derived from arch: x64 GUIDs live in the native 64-bit view, x86 GUIDs
// under WOW6432Node on 64-bit Windows.
func vcUninstallDetect(guid, arch string) action.RegPresent {
	view := action.ViewDefault64
	if arch == "x86" {
		view = action.ViewWow6432
	}
	return action.RegPresent{
		Root:  registry.LOCAL_MACHINE,
		Path:  `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + guid,
		Value: "DisplayName", Elev: core.ElevUser, View: view,
	}
}

// redistChild builds one version+arch install leaf.
func redistChild(id string, name core.I18n, url string, verify action.VerifyMode, sha string, args []string, detect core.Action) core.Tweak {
	return core.Tweak{
		ID: id, Category: "prep", Name: name, Elevation: core.ElevAdmin,
		Actions: []core.Action{action.DownloadInstall{
			URL:        url,
			Verify:     verify,
			SHA256:     sha,
			Args:       args,
			AcceptExit: vcredistAcceptExit,
			Detect:     detect,
			Elev:       core.ElevAdmin,
		}},
	}
}

// redistParent is the expandable group surfaced in the prep category.
func redistParent() core.Tweak {
	n := func(ru, en string) core.I18n { return core.I18n{RU: ru, EN: en} }
	return core.Tweak{
		ID: "prep.vcredist", Category: "prep", Elevation: core.ElevAdmin,
		Name: n("Visual C++ Redistributable (все версии)", "Visual C++ Redistributable (all versions)"),
		Desc: n("Скачать и тихо установить VC++ 2005-2022 (x64/x86) с проверкой подписи Microsoft.",
			"Download and silently install VC++ 2005-2022 (x64/x86), verifying the Microsoft signature."),
		Children: []core.Tweak{
			// 2015-2022 (14.0) — evergreen aka.ms, Authenticode-verified, no pin.
			redistChild("prep.vcredist.vc2022_x64", n("VC++ 2015-2022 x64", "VC++ 2015-2022 x64"), vcredistX64URL, action.VerifyAuthenticodeMicrosoft, "", vcArgs2022, vcRuntimeDetect("14.0", `VC\Runtimes`, "x64", action.ViewDefault64)),
			redistChild("prep.vcredist.vc2022_x86", n("VC++ 2015-2022 x86", "VC++ 2015-2022 x86"), vcredistX86URL, action.VerifyAuthenticodeMicrosoft, "", vcArgs2022, vcRuntimeDetect("14.0", `VC\Runtimes`, "x86", action.ViewWow6432)),
			// 2013 (12.0) — VC\Runtimes; aka.ms evergreen permalinks, Authenticode (no pin).
			// Runtime keys live under WOW6432Node for BOTH arches on 64-bit Windows.
			redistChild("prep.vcredist.vc2013_x64", n("VC++ 2013 x64", "VC++ 2013 x64"), vc2013X64URL, action.VerifyAuthenticodeMicrosoft, "", vcArgsQuiet, vcRuntimeDetect("12.0", `VC\Runtimes`, "x64", action.ViewWow6432)),
			redistChild("prep.vcredist.vc2013_x86", n("VC++ 2013 x86", "VC++ 2013 x86"), vc2013X86URL, action.VerifyAuthenticodeMicrosoft, "", vcArgsQuiet, vcRuntimeDetect("12.0", `VC\Runtimes`, "x86", action.ViewWow6432)),
			// 2012 (11.0) — VC\Runtimes; static file, SHA256-pinned. Both arches WOW6432Node.
			redistChild("prep.vcredist.vc2012_x64", n("VC++ 2012 x64", "VC++ 2012 x64"), vc2012X64URL, action.VerifySHA256, vc2012X64SHA, vcArgsQuiet, vcRuntimeDetect("11.0", `VC\Runtimes`, "x64", action.ViewWow6432)),
			redistChild("prep.vcredist.vc2012_x86", n("VC++ 2012 x86", "VC++ 2012 x86"), vc2012X86URL, action.VerifySHA256, vc2012X86SHA, vcArgsQuiet, vcRuntimeDetect("11.0", `VC\Runtimes`, "x86", action.ViewWow6432)),
			// 2010 (10.0) — VC\VCRedist (NOT Runtimes); static file, SHA256-pinned. Both arches WOW6432Node.
			redistChild("prep.vcredist.vc2010_x64", n("VC++ 2010 x64", "VC++ 2010 x64"), vc2010X64URL, action.VerifySHA256, vc2010X64SHA, vcArgsQuiet, vcRuntimeDetect("10.0", `VC\VCRedist`, "x64", action.ViewWow6432)),
			redistChild("prep.vcredist.vc2010_x86", n("VC++ 2010 x86", "VC++ 2010 x86"), vc2010X86URL, action.VerifySHA256, vc2010X86SHA, vcArgsQuiet, vcRuntimeDetect("10.0", `VC\VCRedist`, "x86", action.ViewWow6432)),
			// 2008 (9.0) — MSI; detect via Uninstall\{GUID}; static file, SHA256-pinned.
			redistChild("prep.vcredist.vc2008_x64", n("VC++ 2008 x64", "VC++ 2008 x64"), vc2008X64URL, action.VerifySHA256, vc2008X64SHA, vcArgs2008, vcUninstallDetect("{5FCE6D76-F5DC-37AB-B2B8-22AB8CEDB1D4}", "x64")),
			redistChild("prep.vcredist.vc2008_x86", n("VC++ 2008 x86", "VC++ 2008 x86"), vc2008X86URL, action.VerifySHA256, vc2008X86SHA, vcArgs2008, vcUninstallDetect("{9BE518E6-ECC6-35A9-88E4-87755C07200F}", "x86")),
			// 2005 (8.0) — MSI; detect via Uninstall\{GUID}; static file, SHA256-pinned.
			redistChild("prep.vcredist.vc2005_x64", n("VC++ 2005 x64", "VC++ 2005 x64"), vc2005X64URL, action.VerifySHA256, vc2005X64SHA, vcArgs2005, vcUninstallDetect("{ad8a2fa1-06e7-4b0d-927d-6e54b3d31028}", "x64")),
			redistChild("prep.vcredist.vc2005_x86", n("VC++ 2005 x86", "VC++ 2005 x86"), vc2005X86URL, action.VerifySHA256, vc2005X86SHA, vcArgs2005, vcUninstallDetect("{710f4c1c-cc18-4c49-8cbf-51240c89a1a2}", "x86")),
		},
	}
}
