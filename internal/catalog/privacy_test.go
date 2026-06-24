package catalog

import (
	"testing"

	"morgtweaker/internal/action"
)

// TestDiagTrackTelemetryPolicy pins the second action on the DiagTrack tweak: the
// canonical "Allow Telemetry" GPO DWORD. action[0] stays the ServiceStart; the
// RegSet is action[1] (OffAbsent restores the default).
func TestDiagTrackTelemetryPolicy(t *testing.T) {
	tw, ok := Build().Find("privacy.disable_diagtrack")
	if !ok {
		t.Fatal("privacy.disable_diagtrack missing")
	}
	if len(tw.Actions) != 2 {
		t.Fatalf("disable_diagtrack has %d actions, want 2", len(tw.Actions))
	}
	rs, ok := tw.Actions[1].(action.RegSet)
	if !ok {
		t.Fatalf("disable_diagtrack action[1] is %T, want action.RegSet", tw.Actions[1])
	}
	if rs.Path != `SOFTWARE\Policies\Microsoft\Windows\DataCollection` {
		t.Errorf("disable_diagtrack action[1] path = %q want DataCollection policy", rs.Path)
	}
	if rs.Value != "AllowTelemetry" {
		t.Errorf("disable_diagtrack action[1] value = %q want AllowTelemetry", rs.Value)
	}
	if rs.On != uint64(0) {
		t.Errorf("disable_diagtrack action[1] On = %v want uint64(0)", rs.On)
	}
	if !rs.OffAbsent {
		t.Error("disable_diagtrack action[1] must have OffAbsent=true")
	}
}
