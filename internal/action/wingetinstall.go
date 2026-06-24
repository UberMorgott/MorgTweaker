package action

import (
	"context"
	"fmt"

	"morgtweaker/internal/core"
)

// WingetInstall installs a single package via the Windows Package Manager
// (winget) as its ON action. winget must already be present (the apps category
// installs it first via WingetBootstrap); if it is absent, Apply's runner errors
// and Probe reads Off.
//
// Apply(on=true) runs `winget install --id <ID> -e --source winget --scope
// machine --silent --accept-package-agreements --accept-source-agreements` and
// maps the process exit code through AcceptExit. The default accepted set is {0};
// the catalog widens it with the codes that mean "succeeded but needs attention"
// or "already satisfied" — e.g. 0x8A150109 / -1978334967
// (INSTALL_REBOOT_REQUIRED_TO_FINISH) and 0x8A150061 / -1978335135
// (PACKAGE_ALREADY_INSTALLED) — so a reboot-pending or already-installed package
// is not reported as a failure. Apply(on=false) is an honest no-op: an install
// has no exact inverse (uninstall is a separate, manual step).
//
// Probe runs `winget list --id <ID> -e --source winget`; exit 0 -> PointOn, any
// other exit OR a runner error (e.g. winget absent) -> PointOff. Snapshot/Restore
// are honest no-ops. SkipVerifyAfter is true: success is decided by the install
// exit code, not by a re-probe (mirrors DownloadInstall).
type WingetInstall struct {
	ID         string // winget package id, e.g. "7zip.7zip"
	AcceptExit []int  // exit codes treated as success (nil/empty -> {0})
	Elev       core.Elevation
	runCode    func(ctx context.Context, name string, args ...string) (int, error) // injectable (default execRunCode)
}

func (a WingetInstall) Level() core.Elevation { return a.Elev }

func (a WingetInstall) Apply(ctx core.ActionContext, on bool) error {
	if !on {
		return nil // revert is a manual uninstall — honest no-op, never pretend
	}
	c := ctx.Ctx
	if c == nil {
		c = context.Background()
	}
	args := []string{
		"install", "--id", a.ID, "-e",
		"--source", "winget",
		"--scope", "machine",
		"--silent",
		"--accept-package-agreements",
		"--accept-source-agreements",
	}
	code, err := a.run(c, "winget", args...)
	if err != nil {
		return err
	}
	if !a.exitAccepted(code) {
		return fmt.Errorf("winget_install: %q exited with code %d (not an accepted success code) — install failed", a.ID, code)
	}
	return nil
}

func (a WingetInstall) Probe(ctx core.ActionContext) (core.PointState, error) {
	c := ctx.Ctx
	if c == nil {
		c = context.Background()
	}
	code, err := a.run(c, "winget", "list", "--id", a.ID, "-e", "--source", "winget")
	if err != nil || code != 0 {
		return core.PointOff, nil // not installed, or winget absent/errored
	}
	return core.PointOn, nil
}

func (a WingetInstall) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}

func (a WingetInstall) Restore(core.ActionContext, core.Backup) error { return nil }

// SkipVerifyAfter is always true: an install is a one-shot whose success is
// decided by winget's exit code, NOT by re-probing. See DownloadInstall for the
// full rationale.
func (a WingetInstall) SkipVerifyAfter() bool { return true }

// run resolves the runner seam, defaulting to the exit-code-aware execRunCode.
func (a WingetInstall) run(ctx context.Context, name string, args ...string) (int, error) {
	r := a.runCode
	if r == nil {
		r = execRunCode
	}
	return r(ctx, name, args...)
}

// exitAccepted reports whether code is in the configured accepted set (default
// {0} when AcceptExit is empty).
func (a WingetInstall) exitAccepted(code int) bool {
	if len(a.AcceptExit) == 0 {
		return code == 0
	}
	for _, c := range a.AcceptExit {
		if c == code {
			return true
		}
	}
	return false
}

var (
	_ core.Action                         = WingetInstall{}
	_ interface{ SkipVerifyAfter() bool } = WingetInstall{}
)
