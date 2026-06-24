package action

import (
	"context"
	"errors"
	"testing"

	"morgtweaker/internal/core"
)

func TestBuildGate_Check(t *testing.T) {
	ctx := core.ActionContext{Ctx: context.Background()}

	cases := []struct {
		name     string
		buildFn  func(core.ActionContext) (int, error)
		wantOK   bool
		wantStat core.Status
	}{
		{
			name:     "below min is blocked",
			buildFn:  func(core.ActionContext) (int, error) { return 17762, nil },
			wantOK:   false,
			wantStat: core.StatusBlocked,
		},
		{
			name:     "exactly min is ok",
			buildFn:  func(core.ActionContext) (int, error) { return 17763, nil },
			wantOK:   true,
			wantStat: core.StatusOff,
		},
		{
			name:     "well above min is ok",
			buildFn:  func(core.ActionContext) (int, error) { return 22000, nil },
			wantOK:   true,
			wantStat: core.StatusOff,
		},
		{
			name:     "read error fails closed",
			buildFn:  func(core.ActionContext) (int, error) { return 0, errors.New("boom") },
			wantOK:   false,
			wantStat: core.StatusBlocked,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewBuildGate(17763)
			g.buildFn = tc.buildFn

			ok, st, action := g.Check(ctx)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if st != tc.wantStat {
				t.Errorf("status = %v, want %v", st, tc.wantStat)
			}
			if !tc.wantOK {
				if action.Label.RU == "" || action.Label.EN == "" {
					t.Errorf("blocked gate must carry a non-empty RU/EN label, got %+v", action.Label)
				}
				if action.URL != "" {
					t.Errorf("BuildGate must not carry a deep-link URL, got %q", action.URL)
				}
			}
		})
	}
}

func TestBuildGate_ReadErrorLabel(t *testing.T) {
	g := NewBuildGate(17763)
	g.buildFn = func(core.ActionContext) (int, error) { return 0, errors.New("boom") }

	_, _, action := g.Check(core.ActionContext{})
	const wantRU = "Не удалось определить версию Windows."
	const wantEN = "Could not determine the Windows version."
	if action.Label.RU != wantRU {
		t.Errorf("RU label = %q, want %q", action.Label.RU, wantRU)
	}
	if action.Label.EN != wantEN {
		t.Errorf("EN label = %q, want %q", action.Label.EN, wantEN)
	}
}
