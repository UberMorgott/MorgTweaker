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

// vcredistAcceptExit are the installer exit codes treated as success:
//
//	0    success
//	3010 success, reboot required
//	1638 a newer version is already installed (treat as satisfied)
//	1641 success, reboot has been initiated
var vcredistAcceptExit = []int{0, 3010, 1638, 1641}

// sha256TODO is a PLACEHOLDER legacy value — REPLACED in Task 7 by a grounded
// URL+SHA256. A non-hex SHA256 here makes DownloadInstall.Apply refuse before the
// network (fail-closed, see downloadinstall.go), so a forgotten value can never
// install. DO NOT ship with these.
const sha256TODO = "TODO_GROUND_THIS_SHA256_IN_TASK_7"

// vcRuntimeDetect reads a version's runtime "Installed" dword in the correct view.
// keyVer is the VisualStudio key version ("14.0","12.0","11.0","10.0"); sub is the
// runtime subkey family ("VC\\Runtimes" for 2012/2013/2022, "VC\\VCRedist" for
// 2010). The x64 runtime registers under the 64-bit view; the x86 runtime
// registers ONLY under the 32-bit (WOW6432Node) view and is ABSENT from the
// 64-bit view, so each arch is pinned to its own view (reading x86 in the 64-bit
// view would falsely report not-installed). A missing key reads as Off (the RegSet
// probe returns PointOff when the key/value is absent), so a fresh box is
// correctly reported as not-installed rather than erroring.
func vcRuntimeDetect(keyVer, sub, arch string) action.RegSet {
	view := action.ViewDefault64 // x64 runtime lives in the 64-bit view
	if arch == "x86" {
		view = action.ViewWow6432 // x86 runtime lives ONLY in the 32-bit (WOW6432Node) view
	}
	return action.RegSet{
		Root:  registry.LOCAL_MACHINE,
		Path:  `SOFTWARE\Microsoft\VisualStudio\` + keyVer + `\` + sub + `\` + arch,
		Value: "Installed", Kind: action.KindDword, On: uint64(1), Off: uint64(0),
		Elev: core.ElevUser, View: view,
	}
}

// vcUninstallDetect detects 2005/2008 by the existence of their MSI Uninstall key's
// DisplayName value. guid is the product GUID (grounded in Task 7). x86 GUIDs live
// under the WOW6432Node view on 64-bit Windows.
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
func redistChild(id string, name core.I18n, url string, verify action.VerifyMode, sha string, detect core.Action) core.Tweak {
	return core.Tweak{
		ID: id, Category: "prep", Name: name, Elevation: core.ElevAdmin,
		Actions: []core.Action{action.DownloadInstall{
			URL:        url,
			Verify:     verify,
			SHA256:     sha,
			Args:       []string{"/install", "/quiet", "/norestart"}, // 2010+; legacy MSI args grounded in Task 7
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
			// 2015-2022: evergreen, Authenticode-verified (existing behaviour).
			redistChild("prep.vcredist.vc2022_x64", n("VC++ 2015-2022 x64", "VC++ 2015-2022 x64"), vcredistX64URL, action.VerifyAuthenticodeMicrosoft, "", vcRuntimeDetect("14.0", `VC\Runtimes`, "x64")),
			redistChild("prep.vcredist.vc2022_x86", n("VC++ 2015-2022 x86", "VC++ 2015-2022 x86"), vcredistX86URL, action.VerifyAuthenticodeMicrosoft, "", vcRuntimeDetect("14.0", `VC\Runtimes`, "x86")),
			// 2013 (12.0) — VC\Runtimes. URL+SHA grounded in Task 7.
			redistChild("prep.vcredist.vc2013_x64", n("VC++ 2013 x64", "VC++ 2013 x64"), "TODO_URL_2013_X64", action.VerifySHA256, sha256TODO, vcRuntimeDetect("12.0", `VC\Runtimes`, "x64")),
			redistChild("prep.vcredist.vc2013_x86", n("VC++ 2013 x86", "VC++ 2013 x86"), "TODO_URL_2013_X86", action.VerifySHA256, sha256TODO, vcRuntimeDetect("12.0", `VC\Runtimes`, "x86")),
			// 2012 (11.0) — VC\Runtimes.
			redistChild("prep.vcredist.vc2012_x64", n("VC++ 2012 x64", "VC++ 2012 x64"), "TODO_URL_2012_X64", action.VerifySHA256, sha256TODO, vcRuntimeDetect("11.0", `VC\Runtimes`, "x64")),
			redistChild("prep.vcredist.vc2012_x86", n("VC++ 2012 x86", "VC++ 2012 x86"), "TODO_URL_2012_X86", action.VerifySHA256, sha256TODO, vcRuntimeDetect("11.0", `VC\Runtimes`, "x86")),
			// 2010 (10.0) — VC\VCRedist (NOT Runtimes).
			redistChild("prep.vcredist.vc2010_x64", n("VC++ 2010 x64", "VC++ 2010 x64"), "TODO_URL_2010_X64", action.VerifySHA256, sha256TODO, vcRuntimeDetect("10.0", `VC\VCRedist`, "x64")),
			redistChild("prep.vcredist.vc2010_x86", n("VC++ 2010 x86", "VC++ 2010 x86"), "TODO_URL_2010_X86", action.VerifySHA256, sha256TODO, vcRuntimeDetect("10.0", `VC\VCRedist`, "x86")),
			// 2008 (9.0) — MSI; detect via Uninstall\{GUID}. GUID+URL+SHA grounded in Task 7.
			redistChild("prep.vcredist.vc2008_x64", n("VC++ 2008 x64", "VC++ 2008 x64"), "TODO_URL_2008_X64", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2008_X64", "x64")),
			redistChild("prep.vcredist.vc2008_x86", n("VC++ 2008 x86", "VC++ 2008 x86"), "TODO_URL_2008_X86", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2008_X86", "x86")),
			// 2005 (8.0) — MSI; detect via Uninstall\{GUID}.
			redistChild("prep.vcredist.vc2005_x64", n("VC++ 2005 x64", "VC++ 2005 x64"), "TODO_URL_2005_X64", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2005_X64", "x64")),
			redistChild("prep.vcredist.vc2005_x86", n("VC++ 2005 x86", "VC++ 2005 x86"), "TODO_URL_2005_X86", action.VerifySHA256, sha256TODO, vcUninstallDetect("TODO_GUID_2005_X86", "x86")),
		},
	}
}
