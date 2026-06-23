// internal/core/types.go
// Package core defines the data model of the tweak engine: a Tweak is data (id,
// inline i18n, elevation, a list of Actions, an optional Gate). Executors live in
// internal/action; orchestration lives in internal/engine. This file holds types
// only — no I/O.
package core

import (
	"context"
	"time"
)

// I18n is an inline RU/EN string pair, carried directly on each tweak/category so
// there is no parallel name/desc map to drift out of sync.
type I18n struct{ RU, EN string }

// Status is the live, verified state of a tweak.
type Status int

const (
	StatusUnknown       Status = iota // not probed yet (async): View shows "…"
	StatusOff                         // OFF / default value in effect
	StatusOn                          // ON value in effect (all points)
	StatusPartial                     // some points ON, some not
	StatusBlocked                     // written then reverted (e.g. Tamper Protection)
	StatusAbsent                      // target service/key/cmdlet does not exist
	StatusRebootPending               // written, effect awaits reboot
	StatusWorking                     // async apply in flight (download/install)
)

func (s Status) IsOn() bool { return s == StatusOn }

func (s Status) String() string {
	switch s {
	case StatusOff:
		return "off"
	case StatusOn:
		return "on"
	case StatusPartial:
		return "partial"
	case StatusBlocked:
		return "blocked"
	case StatusAbsent:
		return "absent"
	case StatusRebootPending:
		return "reboot-pending"
	case StatusWorking:
		return "working"
	default:
		return "unknown"
	}
}

// PointState is one action's probe result; the engine aggregates points into a
// tweak Status (absent points are excluded from the aggregate).
type PointState int

const (
	PointOff    PointState = iota // this action's ON value is not in effect
	PointOn                       // this action's ON value is in effect
	PointAbsent                   // this action's target does not exist (n/a)
)

// Elevation is the privilege level an action/tweak needs.
type Elevation int

const (
	ElevUser Elevation = iota
	ElevAdmin
	ElevSystem
	ElevTrustedInstaller
)

func (e Elevation) NeedsAdmin() bool { return e != ElevUser }

// Backup is the raw pre-change snapshot of a single action's target, taken
// save-once before apply so the change reverts exactly. JSON-compatible with v1.
type Backup struct {
	Existed   bool      `json:"existed"`
	Type      uint32    `json:"type"`
	Value     any       `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// ActionContext threads cancellation and a progress sink into action methods.
type ActionContext struct {
	Ctx      context.Context
	Progress func(pct int, note string) // nil for non-async actions
}

// Report forwards progress if a sink is set (nil-safe).
func (c ActionContext) Report(pct int, note string) {
	if c.Progress != nil {
		c.Progress(pct, note)
	}
}

// Action is one executor kind (reg.set, service.start, run, download_install...).
// Catalog data wires concrete Action values; the engine interprets them uniformly.
type Action interface {
	Apply(ctx ActionContext, on bool) error      // write ON (on=true) or OFF (on=false)
	Snapshot(ctx ActionContext) (Backup, error)  // capture pre-change state
	Restore(ctx ActionContext, b Backup) error   // exact revert from a snapshot
	Probe(ctx ActionContext) (PointState, error) // is the ON state in effect / absent?
	Level() Elevation                            // elevation this action needs
}

// GateAction is an optional deep-link the UI surfaces when a gate blocks a tweak
// (e.g. open Windows Security to turn off Tamper Protection). URL=="" means none.
type GateAction struct {
	Label I18n
	URL   string
}

// Gate is an optional precondition checked before a tweak's actions run. ok=false
// short-circuits apply and the returned Status (e.g. Blocked/Absent) is reported.
type Gate interface {
	Check(ctx ActionContext) (ok bool, st Status, action GateAction)
}

// Tweak is a data row: presentation + a list of actions + an optional gate.
type Tweak struct {
	ID        string
	Category  string
	Name      I18n
	Desc      I18n
	Elevation Elevation
	Reboot    bool
	Tags      []string
	Actions   []Action
	Gate      Gate
}

// NeedsAdmin reports whether applying needs elevation (tweak level or any action).
func (t Tweak) NeedsAdmin() bool {
	if t.Elevation.NeedsAdmin() {
		return true
	}
	for _, a := range t.Actions {
		if a.Level().NeedsAdmin() {
			return true
		}
	}
	return false
}

// Category groups tweaks under a heading.
type Category struct {
	ID     string
	Name   I18n
	Tweaks []Tweak
}

// Catalog is the ordered set of categories shown by the app.
type Catalog []Category

// Find returns the tweak with the given id, ok=false if absent.
func (c Catalog) Find(id string) (Tweak, bool) {
	for _, cat := range c {
		for _, t := range cat.Tweaks {
			if t.ID == id {
				return t, true
			}
		}
	}
	return Tweak{}, false
}
