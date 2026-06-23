package action

import (
	"context"
	"os/exec"

	"morgtweaker/internal/core"
)

// Run executes a one-off command (sc/schtasks/dism/cmd) as its ON action. Such a
// command has no exact inverse, so Snapshot/Restore are HONEST no-ops — we never
// fabricate a backup or pretend to revert. Apply(on=false) is likewise a no-op.
// Probe delegates to ProbeFn (a cheap check such as "is the scheduled task
// absent?"); a nil ProbeFn means the command is stateless and always re-runnable
// (PointOff = appliable). The command is run via exec.CommandContext using
// ctx.Ctx, so engine/UI cancellation aborts a long-running command.
type Run struct {
	Path    string
	Args    []string
	ProbeFn func() (core.PointState, error)
	Elev    core.Elevation
	runner  func(ctx context.Context, path string, args ...string) error // injectable for tests
}

func (a Run) Level() core.Elevation { return a.Elev }

func (a Run) Apply(ctx core.ActionContext, on bool) error {
	if !on {
		return nil // OFF for a one-off command is a no-op (no inverse)
	}
	run := a.runner
	if run == nil {
		run = execRun
	}
	c := ctx.Ctx
	if c == nil {
		c = context.Background()
	}
	return run(c, a.Path, a.Args...)
}

func (a Run) Snapshot(_ core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}

func (a Run) Restore(_ core.ActionContext, _ core.Backup) error { return nil }

func (a Run) Probe(_ core.ActionContext) (core.PointState, error) {
	if a.ProbeFn == nil {
		return core.PointOff, nil
	}
	return a.ProbeFn()
}

// execRun runs the command bound to ctx so cancellation kills the process.
func execRun(ctx context.Context, path string, args ...string) error {
	return exec.CommandContext(ctx, path, args...).Run()
}

var _ core.Action = Run{}
