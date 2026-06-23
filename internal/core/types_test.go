// internal/core/types_test.go
package core

import (
	"testing"
	"time"
)

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusUnknown:       "unknown",
		StatusOff:           "off",
		StatusOn:            "on",
		StatusPartial:       "partial",
		StatusBlocked:       "blocked",
		StatusAbsent:        "absent",
		StatusRebootPending: "reboot-pending",
		StatusWorking:       "working",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestElevationNeedsAdmin(t *testing.T) {
	if ElevUser.NeedsAdmin() {
		t.Error("ElevUser should not need admin")
	}
	for _, e := range []Elevation{ElevAdmin, ElevSystem, ElevTrustedInstaller} {
		if !e.NeedsAdmin() {
			t.Errorf("%v should need admin", e)
		}
	}
}

func TestTweakNeedsAdmin(t *testing.T) {
	user := Tweak{Elevation: ElevUser}
	if user.NeedsAdmin() {
		t.Error("user-elevation tweak with no actions should not need admin")
	}
	admin := Tweak{Elevation: ElevAdmin}
	if !admin.NeedsAdmin() {
		t.Error("admin-elevation tweak should need admin")
	}
}

func TestActionContextReportNilSafe(t *testing.T) {
	ActionContext{}.Report(50, "half", 0, 0) // must not panic with nil Progress
}

func TestCatalogFind(t *testing.T) {
	c := Catalog{{ID: "prep", Tweaks: []Tweak{{ID: "prep.x"}}}}
	if _, ok := c.Find("prep.x"); !ok {
		t.Error("Find(prep.x) should succeed")
	}
	if _, ok := c.Find("nope"); ok {
		t.Error("Find(nope) should fail")
	}
}

func TestBackupRoundTripFields(t *testing.T) {
	b := Backup{Existed: true, Type: 4, Value: uint64(1), Timestamp: time.Now()}
	if !b.Existed || b.Type != 4 {
		t.Error("Backup fields not preserved")
	}
}
