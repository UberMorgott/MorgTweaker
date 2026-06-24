package ui

import (
	"testing"

	"morgtweaker/internal/core"
)

func TestStatusAppliableIncludesOn(t *testing.T) {
	cases := map[core.Status]bool{
		core.StatusOff:           true,
		core.StatusPartial:       true,
		core.StatusOn:            true, // force-reapply: applied rows are re-appliable
		core.StatusBlocked:       false,
		core.StatusAbsent:        false,
		core.StatusRebootPending: false,
		core.StatusUnknown:       false,
		core.StatusWorking:       false,
	}
	for st, want := range cases {
		if got := statusAppliable(st); got != want {
			t.Errorf("statusAppliable(%v) = %v, want %v", st, got, want)
		}
	}
}

func TestStatusRollbackableUnchanged(t *testing.T) {
	if !statusRollbackable(core.StatusOn) || !statusRollbackable(core.StatusRebootPending) {
		t.Fatal("rollbackable must still include On and RebootPending")
	}
	if statusRollbackable(core.StatusOff) || statusRollbackable(core.StatusPartial) {
		t.Fatal("rollbackable must not include Off/Partial")
	}
}
