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
