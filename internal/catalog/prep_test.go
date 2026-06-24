package catalog

import (
	"testing"

	"morgtweaker/internal/action"
)

// The VC++ redistributable tests formerly here (TestVcRuntimeDetectRegistry,
// TestVcRuntimeDetectView, TestInstallVcredistTweak) were removed in Task 4: the
// 1-arg vcRuntimeDetect and the combined prep.install_vcredist tweak no longer
// exist. Their coverage now lives in redist_test.go (parent + 12 children).

// TestSmartScreenModernPolicy pins the SmartScreen tweak to the modern policy
// control: action[0] is the EnableSmartScreen policy DWORD (OffAbsent restores
// default), and the tweak requires a reboot.
func TestSmartScreenModernPolicy(t *testing.T) {
	tw, ok := Build().Find("prep.disable_smartscreen")
	if !ok {
		t.Fatal("prep.disable_smartscreen missing")
	}
	if len(tw.Actions) != 2 {
		t.Fatalf("disable_smartscreen has %d actions, want 2", len(tw.Actions))
	}
	rs, ok := tw.Actions[0].(action.RegSet)
	if !ok {
		t.Fatalf("disable_smartscreen action[0] is %T, want action.RegSet", tw.Actions[0])
	}
	if rs.Path != `SOFTWARE\Policies\Microsoft\Windows\System` {
		t.Errorf("disable_smartscreen action[0] path = %q want policy System", rs.Path)
	}
	if rs.Value != "EnableSmartScreen" {
		t.Errorf("disable_smartscreen action[0] value = %q want EnableSmartScreen", rs.Value)
	}
	if rs.On != uint64(0) {
		t.Errorf("disable_smartscreen action[0] On = %v want uint64(0)", rs.On)
	}
	if !rs.OffAbsent {
		t.Error("disable_smartscreen action[0] must have OffAbsent=true")
	}
	if !tw.Reboot {
		t.Error("disable_smartscreen must require a reboot (Reboot:true)")
	}
}

// TestSeccenterLegacyWin81Notify pins the three legacy (Win8.1-and-older)
// Security Center notification toggles onto prep.disable_seccenter_notify, while
// confirming the modern Win10/11 policy action is still present. The legacy keys
// live under SOFTWARE\Microsoft\Security Center and are removed on Win10/11, so
// the write-all strategy leaves them inert there.
func TestSeccenterLegacyWin81Notify(t *testing.T) {
	tw, ok := Build().Find("prep.disable_seccenter_notify")
	if !ok {
		t.Fatal("prep.disable_seccenter_notify missing")
	}

	// Collect legacy Security Center RegSet values keyed by Value name.
	const legacyPath = `SOFTWARE\Microsoft\Security Center`
	legacy := map[string]action.RegSet{}
	modernFound := false
	for _, a := range tw.Actions {
		rs, ok := a.(action.RegSet)
		if !ok {
			continue
		}
		if rs.Path == legacyPath {
			legacy[rs.Value] = rs
		}
		if rs.Path == `SOFTWARE\Policies\Microsoft\Windows Defender Security Center\Notifications` && rs.Value == "DisableNotifications" {
			modernFound = true
		}
	}

	for _, name := range []string{"AntiVirusDisableNotify", "FirewallDisableNotify", "UpdatesDisableNotify"} {
		rs, ok := legacy[name]
		if !ok {
			t.Errorf("missing legacy Security Center action for value %q", name)
			continue
		}
		if rs.On != uint64(1) {
			t.Errorf("legacy %q On = %v want uint64(1)", name, rs.On)
		}
		if !rs.OffAbsent {
			t.Errorf("legacy %q must have OffAbsent=true", name)
		}
	}

	if !modernFound {
		t.Error("modern Win10/11 action (Windows Defender Security Center\\Notifications DisableNotifications) must remain present")
	}
}

// TestPauseUpdateName pins the corrected, non-overpromising name.
func TestPauseUpdateName(t *testing.T) {
	tw, ok := Build().Find("prep.pause_update")
	if !ok {
		t.Fatal("prep.pause_update missing")
	}
	if tw.Name.EN != "Disable automatic Windows Update install" {
		t.Errorf("pause_update Name.EN = %q", tw.Name.EN)
	}
}
