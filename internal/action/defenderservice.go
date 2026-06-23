package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"morgtweaker/internal/core"
)

// DefenderServiceDisable is the DURABLE half of the Defender-off toggle: it sets
// the Defender services' Start type to 4 (Disabled) so they do not start on the
// next boot, and disables Defender's scheduled tasks so Windows timers cannot
// silently re-arm protection. Combined with DefenderSuppress (which gives the
// IMMEDIATE same-session effect), the result stays OFF across reboots and Windows
// maintenance windows until the user turns the tweak back off in the tweaker.
//
// Elevation is ElevTrustedInstaller: the engine raises to a TrustedInstaller token
// (internal/elevate.RunAs) for the whole group, because the Defender service keys
// under HKLM\SYSTEM\CurrentControlSet\Services deny write access to a plain admin.
// A few keys (WdFilter, WdBoot) are hardened further and reject even TI; for those
// Apply FALLS BACK to the take-ownership dance (seize ownership as Administrators +
// grant FullControl, via regOwnership) before writing Start=4, recording the
// original owner+DACL (SDDL) in the snapshot so Restore reverts the ACL exactly.
//
// Tamper Protection is NOT handled here — the tweak carries a TamperGate that
// short-circuits to StatusBlocked (with a deep-link) while Tamper is on, because
// the WdFilter minifilter reverts these very writes while Tamper is up. The gate
// guarantees Apply only runs with Tamper OFF.
//
// All three side-effecting subsystems are injected (reg, own, runTask) so unit
// tests exercise every branch against fakes without touching HKLM, the live ACLs,
// or schtasks. nil fields default to the real Windows implementations.
type DefenderServiceDisable struct {
	Elev core.Elevation

	// Services is the ordered set of Defender service short names whose Start DWORD
	// is set to 4. A service whose key is absent is skipped (3rd-party AV / gutted
	// build) — never fabricated.
	Services []string
	// Tasks is the set of Defender scheduled-task paths disabled for durability.
	Tasks []string

	reg     regWriter    // Start-DWORD read/write seam (nil -> realRegWriter)
	own     regOwnership // take-ownership/ACL seam (nil -> realRegOwnership)
	runTask taskRunner   // scheduled-task enable/disable seam (nil -> realTaskRunner)
}

// defaultDefenderServices are the service keys whose Start we flip to disabled.
// Order is stable so snapshots/restores are deterministic.
var defaultDefenderServices = []string{
	"WinDefend", "WdNisSvc", "WdFilter", "WdBoot", "WdDevFlt", "Sense", "SgrmBroker",
}

// defaultDefenderTasks are the scheduled tasks disabled for durability. Paths are
// the canonical \Microsoft\Windows\Windows Defender\* task tree.
var defaultDefenderTasks = []string{
	`\Microsoft\Windows\Windows Defender\Windows Defender Cache Maintenance`,
	`\Microsoft\Windows\Windows Defender\Windows Defender Cleanup`,
	`\Microsoft\Windows\Windows Defender\Windows Defender Scheduled Scan`,
	`\Microsoft\Windows\Windows Defender\Windows Defender Verification`,
}

const startDisabled uint64 = 4 // SERVICE_DISABLED

// NewDefenderServiceDisable builds the durable action over the default Defender
// service + task sets, wired to the real Windows side-effect implementations.
func NewDefenderServiceDisable(elev core.Elevation) DefenderServiceDisable {
	return DefenderServiceDisable{
		Elev:     elev,
		Services: append([]string(nil), defaultDefenderServices...),
		Tasks:    append([]string(nil), defaultDefenderTasks...),
	}
}

func (a DefenderServiceDisable) Level() core.Elevation { return a.Elev }

func (a DefenderServiceDisable) regw() regWriter {
	if a.reg != nil {
		return a.reg
	}
	return realRegWriter{}
}

func (a DefenderServiceDisable) owner() regOwnership {
	if a.own != nil {
		return a.own
	}
	return realRegOwnership{}
}

func (a DefenderServiceDisable) tasks() taskRunner {
	if a.runTask != nil {
		return a.runTask
	}
	return realTaskRunner{}
}

// svcKeyPath returns the registry subkey path holding a service's Start value.
func svcKeyPath(svc string) string {
	return `SYSTEM\CurrentControlSet\Services\` + svc
}

// svcSnap records one service's pre-change state. OwnerSDDL is the key's ORIGINAL
// owner+DACL captured at snapshot time (before any take-ownership), so Restore can
// revert the ACL to exactly what it was even if Apply seized the key. It is "" only
// when the security read was unavailable; Restore then leaves the ACL untouched
// (an admin-owned + FullControl key is strictly more permissive, never a lock-out).
type svcSnap struct {
	Present   bool   `json:"present"`
	Start     uint64 `json:"start"`
	OwnerSDDL string `json:"ownerSDDL,omitempty"`
}

// taskSnap records one scheduled task's pre-change enabled state.
type taskSnap struct {
	Present bool `json:"present"`
	Enabled bool `json:"enabled"`
}

// serviceSnap is the whole action's pre-change snapshot, stored in Backup.Value.
// It survives a JSON round-trip through the backup store (decodeServiceSnap
// re-parses whether Value is the struct or a map[string]any).
type serviceSnap struct {
	Services map[string]svcSnap  `json:"services"`
	Tasks    map[string]taskSnap `json:"tasks"`
}

func (a DefenderServiceDisable) Snapshot(_ core.ActionContext) (core.Backup, error) {
	snap := serviceSnap{
		Services: map[string]svcSnap{},
		Tasks:    map[string]taskSnap{},
	}
	allDisabled := true // becomes Existed: "durably disabled state already present"
	anyPresent := false
	for _, svc := range a.Services {
		present, start, err := a.regw().readStart(svcKeyPath(svc))
		if err != nil {
			// A read failure for one key is graceful: treat it as absent rather than
			// aborting the whole snapshot (3rd-party AV may ACL-block the read).
			snap.Services[svc] = svcSnap{Present: false}
			continue
		}
		ss := svcSnap{Present: present, Start: start}
		if present {
			anyPresent = true
			if start != startDisabled {
				allDisabled = false
			}
			// Capture the ORIGINAL owner+DACL so Restore reverts the ACL exactly even
			// if Apply later seizes the key. Best-effort: a failed read leaves OwnerSDDL
			// empty (Restore then leaves the ACL alone).
			if sddl, serr := a.owner().snapshotSecurity(svcKeyPath(svc)); serr == nil {
				ss.OwnerSDDL = sddl
			}
		}
		snap.Services[svc] = ss
	}
	for _, task := range a.Tasks {
		present, enabled, err := a.tasks().readState(task)
		if err != nil {
			snap.Tasks[task] = taskSnap{Present: false}
			continue
		}
		snap.Tasks[task] = taskSnap{Present: present, Enabled: enabled}
	}
	existed := anyPresent && allDisabled
	return core.Backup{Existed: existed, Value: snap, Timestamp: time.Now()}, nil
}

// decodeServiceSnap recovers a serviceSnap from a Backup.Value that may be the
// struct (in-memory) or a map[string]any (after a JSON round-trip through the
// store). Mirrors decodeSnap in defendersuppress.go.
func decodeServiceSnap(v any) serviceSnap {
	if s, ok := v.(serviceSnap); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return serviceSnap{}
	}
	var s serviceSnap
	_ = json.Unmarshal(b, &s)
	if s.Services == nil {
		s.Services = map[string]svcSnap{}
	}
	if s.Tasks == nil {
		s.Tasks = map[string]taskSnap{}
	}
	return s
}

func (a DefenderServiceDisable) Apply(ctx core.ActionContext, on bool) error {
	if !on {
		// OFF for a plain Apply (no snapshot in hand) re-enables the scheduled tasks
		// — the part that is durably reversible WITHOUT knowing prior state. Exact
		// service-Start + ACL reversal needs the snapshot and is routed SOLELY through
		// Restore (the engine's rollback entrypoint), so a snapshot-less Apply(off)
		// never guesses a Start value or paints a false-clean state.
		var firstErr error
		for _, task := range a.Tasks {
			if err := a.tasks().setState(ctx, task, true); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	// ON == durably disable. For each present service set Start=4; on ACCESS_DENIED
	// fall back to take-ownership (seize as Administrators + grant FullControl), then
	// retry the write. The ORIGINAL owner+DACL was already captured by Snapshot, so
	// Apply records nothing — Restore reverts both Start and ACL from that snapshot.
	for _, svc := range a.Services {
		if err := a.disableService(svc); err != nil {
			return err
		}
	}
	for _, task := range a.Tasks {
		if err := a.tasks().setState(ctx, task, false); err != nil {
			return err
		}
	}
	return nil
}

// disableService sets Start=4 on one present service key, taking ownership on a
// hard ACCESS_DENIED. An absent key is a no-op (never fabricated). The original
// owner+DACL was captured by Snapshot, so the take-ownership path records nothing.
func (a DefenderServiceDisable) disableService(svc string) error {
	path := svcKeyPath(svc)
	present, _, err := a.regw().readStart(path)
	if err != nil {
		// Read blocked (likely ACL): try the take-ownership path before giving up.
		return a.takeOwnershipAndWrite(path)
	}
	if !present {
		return nil // absent service key: skip, never fabricate
	}
	if werr := a.regw().writeStart(path, startDisabled); werr != nil {
		if isAccessDeniedErr(werr) {
			return a.takeOwnershipAndWrite(path)
		}
		return werr
	}
	return nil
}

// takeOwnershipAndWrite seizes ownership of the service key, grants Administrators
// FullControl, then writes Start=4.
//
// SECURITY / idempotency (FIX 2): we NEVER seize a key whose original owner+DACL we
// cannot capture. Before touching ownership we re-read the security; if that read
// fails we HARD-FAIL the key's disable WITHOUT seizing. Otherwise Restore (guarded
// on a non-empty snapshot SDDL) would silently skip the ACL revert and leave the
// key permanently Administrators-owned/FullControl after rollback. The snapshot
// captured the same restorable SDDL before Apply, so a readable key round-trips
// exactly on Restore; an unreadable key is left untouched rather than seized.
func (a DefenderServiceDisable) takeOwnershipAndWrite(path string) error {
	if _, serr := a.owner().snapshotSecurity(path); serr != nil {
		return fmt.Errorf("defenderservice: refusing to seize %s — original security not capturable (no restorable snapshot): %w", path, serr)
	}
	if terr := a.owner().takeOwnership(path); terr != nil {
		return terr
	}
	return a.regw().writeStart(path, startDisabled)
}

func (a DefenderServiceDisable) Restore(ctx core.ActionContext, b core.Backup) error {
	snap := decodeServiceSnap(b.Value)
	var errs []error

	// 1. Restore each service's Start exactly from the snapshot, then revert its
	// owner+DACL to the snapshotted SDDL (captured before any take-ownership, so this
	// lands the key in EXACTLY its original security — idempotent when we never
	// seized it). Order matters: write Start while we still own the key, THEN hand
	// ownership/DACL back (restoring the original, more-restrictive DACL first could
	// strip our own write access).
	//
	// FIX 3: the Start write and the ACL revert are INDEPENDENT. We attempt BOTH and
	// collect both errors — a failed Start write must NOT skip (or mask) the ACL
	// revert, and vice-versa, so a partial restore still maximally reverts. Both
	// errors are combined into the returned error rather than the first masking later.
	for _, svc := range a.Services {
		ss, ok := snap.Services[svc]
		if !ok || !ss.Present {
			continue // nothing snapshotted / key was absent: leave it alone
		}
		if werr := a.regw().writeStart(svcKeyPath(svc), ss.Start); werr != nil {
			errs = append(errs, werr)
		}
		if ss.OwnerSDDL != "" {
			if rerr := a.owner().restoreSecurity(svcKeyPath(svc), ss.OwnerSDDL); rerr != nil {
				errs = append(errs, rerr)
			}
		}
	}

	// 2. Re-enable each scheduled task we disabled (only those enabled before).
	for _, task := range a.Tasks {
		ts, ok := snap.Tasks[task]
		if !ok || !ts.Present {
			continue
		}
		if ts.Enabled {
			if terr := a.tasks().setState(ctx, task, true); terr != nil {
				errs = append(errs, terr)
			}
		}
	}
	return errors.Join(errs...)
}

func (a DefenderServiceDisable) Probe(_ core.ActionContext) (core.PointState, error) {
	anyPresent := false
	allDisabled := true
	for _, svc := range a.Services {
		present, start, err := a.regw().readStart(svcKeyPath(svc))
		if err != nil {
			// Graceful: a read failure for one key (ACL/absent) is treated as n/a, not
			// a crash. Keep scanning the rest.
			continue
		}
		if !present {
			continue
		}
		anyPresent = true
		if start != startDisabled {
			allDisabled = false
		}
	}
	if !anyPresent {
		return core.PointAbsent, nil // Defender services absent (3rd-party AV / gutted build)
	}
	if allDisabled {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}

// isAccessDeniedErr reports whether err is (or wraps) ERROR_ACCESS_DENIED (5),
// the signal to fall back to take-ownership. Mirrors the engine's isAccessDenied
// but kept local so the action does not import the engine.
func isAccessDeniedErr(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(5)
	}
	return false
}

// --- scheduled-task seam ------------------------------------------------------

// taskRunner enables/disables and reads Defender scheduled tasks. Injected so
// tests assert the exact command construction without spawning schtasks.
type taskRunner interface {
	// setState enables (enable=true) or disables a task by full path.
	setState(ctx core.ActionContext, taskPath string, enable bool) error
	// readState reports whether the task exists and is currently enabled.
	readState(taskPath string) (present, enabled bool, err error)
}

// realTaskRunner drives schtasks.exe. setState uses /Change /Enable|/Disable;
// readState parses /Query /FO LIST for the "Scheduled Task State:" line.
type realTaskRunner struct{}

// taskChangeArgs builds the schtasks /Change argument vector (exported shape used
// by tests to pin command construction).
func taskChangeArgs(taskPath string, enable bool) []string {
	flag := "/Disable"
	if enable {
		flag = "/Enable"
	}
	return []string{"/Change", "/TN", taskPath, flag}
}

func (realTaskRunner) setState(ctx core.ActionContext, taskPath string, enable bool) error {
	base := ctx.Ctx
	if base == nil {
		base = context.Background()
	}
	cctx, cancel := context.WithTimeout(base, 8*time.Second)
	defer cancel()
	args := taskChangeArgs(taskPath, enable)
	out, err := exec.CommandContext(cctx, "schtasks", args...).CombinedOutput()
	if err == nil {
		return nil
	}
	// An ABSENT task (schtasks "ERROR: The system cannot find the path specified."
	// / exit 1) is NOT a failure: there is nothing to enable/disable, so the desired
	// state is already satisfied. Only genuine, unexpected errors propagate — and
	// when they do, surface the command + schtasks stderr so the user sees the real
	// cause instead of a bare "exit status 1".
	if isTaskNotFound(out) {
		return nil
	}
	return fmt.Errorf("schtasks %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
}

// isTaskNotFound reports whether schtasks output signals the task does not exist
// (the path/task could not be found). Matched case-insensitively against the
// known "cannot find" wordings so an absent Defender task is treated as already
// satisfied rather than a hard error.
func isTaskNotFound(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "cannot find") ||
		strings.Contains(s, "does not exist") ||
		strings.Contains(s, "the specified task name") // schtasks "...does not exist..."
}

func (realTaskRunner) readState(taskPath string) (bool, bool, error) {
	out, err := exec.Command("schtasks", "/Query", "/TN", taskPath, "/FO", "LIST").Output()
	if err != nil {
		// schtasks exits non-zero for an absent task: report absent, not an error.
		return false, false, nil
	}
	return true, parseTaskEnabled(out), nil
}

// parseTaskEnabled reads the "Scheduled Task State:" line of schtasks /Query LIST
// output; "Disabled" => not enabled, anything else (Ready/Running) => enabled.
func parseTaskEnabled(out []byte) bool {
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, ":"); i >= 0 {
			key := strings.TrimSpace(line[:i])
			if strings.EqualFold(key, "Scheduled Task State") || strings.EqualFold(key, "Status") {
				val := strings.TrimSpace(line[i+1:])
				return !strings.EqualFold(val, "Disabled")
			}
		}
	}
	return true // no state line found: assume enabled (conservative)
}

// ensure DefenderServiceDisable satisfies core.Action.
var _ core.Action = DefenderServiceDisable{}
