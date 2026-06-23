package engine

import (
	"context"
	"errors"
	"testing"

	"morgtweaker/internal/core"
)

// asyncCmd invokes a tea.Cmd (a func() tea.Msg) and returns the produced Msg.
// Mirrors how Bubble Tea runs a Cmd off the UI goroutine, then asserts the typed
// result Msg the UI will receive.

func TestProbeBatchCmd(t *testing.T) {
	e := newTestEngine(nil, nil)
	tws := []core.Tweak{
		{ID: "a", Actions: []core.Action{&fakeAction{state: core.PointOn}}},
		{ID: "b", Actions: []core.Action{&fakeAction{state: core.PointOff}}},
	}
	cmd := e.ProbeBatchCmd(tws)
	msg, ok := cmd().(BatchStatusMsg)
	if !ok {
		t.Fatalf("ProbeBatchCmd() returned %T, want BatchStatusMsg", cmd())
	}
	if msg.Statuses["a"] != core.StatusOn || msg.Statuses["b"] != core.StatusOff {
		t.Errorf("batch statuses = %v", msg.Statuses)
	}
}

func TestProbeCmd(t *testing.T) {
	e := newTestEngine(nil, nil)
	cmd := e.ProbeCmd(core.Tweak{ID: "x", Actions: []core.Action{&fakeAction{state: core.PointOn}}})
	msg, ok := cmd().(StatusMsg)
	if !ok {
		t.Fatalf("ProbeCmd() returned %T, want StatusMsg", cmd())
	}
	if msg.ID != "x" || msg.Status != core.StatusOn {
		t.Errorf("StatusMsg = %+v", msg)
	}
	if msg.Err != nil {
		t.Errorf("StatusMsg.Err = %v want nil", msg.Err)
	}
}

func TestProbeCmdPropagatesError(t *testing.T) {
	e := newTestEngine(nil, nil)
	cmd := e.ProbeCmd(core.Tweak{ID: "e", Actions: []core.Action{errProbeAction{}}})
	msg, ok := cmd().(StatusMsg)
	if !ok {
		t.Fatalf("ProbeCmd() returned %T, want StatusMsg", cmd())
	}
	if msg.ID != "e" || msg.Err == nil {
		t.Errorf("StatusMsg = %+v want non-nil Err", msg)
	}
}

func TestApplyCmdDone(t *testing.T) {
	e := newTestEngine(nil, nil)
	cmd := e.ApplyCmd(context.Background(),
		core.Tweak{ID: "ap", Actions: []core.Action{&fakeAction{state: core.PointOn}}}, true, nil)
	msg, ok := cmd().(ApplyDoneMsg)
	if !ok {
		t.Fatalf("ApplyCmd() returned %T, want ApplyDoneMsg", cmd())
	}
	if msg.ID != "ap" || msg.Status != core.StatusOn || msg.Err != nil {
		t.Errorf("ApplyDoneMsg = %+v want {ap On nil}", msg)
	}
}

func TestApplyCmdPropagatesError(t *testing.T) {
	e := newTestEngine(nil, nil)
	boom := errors.New("apply boom")
	cmd := e.ApplyCmd(context.Background(),
		core.Tweak{ID: "ax", Actions: []core.Action{&fakeAction{state: core.PointOff, applyErr: boom}}}, true, nil)
	msg, ok := cmd().(ApplyDoneMsg)
	if !ok {
		t.Fatalf("ApplyCmd() returned %T, want ApplyDoneMsg", cmd())
	}
	if msg.ID != "ax" || msg.Err == nil {
		t.Errorf("ApplyDoneMsg = %+v want non-nil Err", msg)
	}
}

func TestApplyCmdWiresProgressSink(t *testing.T) {
	e := newTestEngine(nil, nil)
	var gotPct int
	var gotNote string
	prog := func(pct int, note string, _, _ int64) { gotPct, gotNote = pct, note }
	tw := core.Tweak{ID: "pg", Actions: []core.Action{progressAction{}}}
	cmd := e.ApplyCmd(context.Background(), tw, true, prog)
	if _, ok := cmd().(ApplyDoneMsg); !ok {
		t.Fatalf("ApplyCmd() returned %T, want ApplyDoneMsg", cmd())
	}
	if gotPct != 42 || gotNote != "half" {
		t.Errorf("progress sink got (%d,%q) want (42,\"half\")", gotPct, gotNote)
	}
}

// progressAction reports progress through the ActionContext sink during Apply,
// proving ApplyCmd threads the prog callback into ApplyCtx.
type progressAction struct{}

func (progressAction) Level() core.Elevation { return core.ElevUser }
func (progressAction) Apply(ctx core.ActionContext, _ bool) error {
	ctx.Report(42, "half", 0, 0)
	return nil
}
func (progressAction) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}
func (progressAction) Restore(core.ActionContext, core.Backup) error { return nil }
func (progressAction) Probe(core.ActionContext) (core.PointState, error) {
	return core.PointOn, nil
}
