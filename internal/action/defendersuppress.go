package action

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"morgtweaker/internal/core"
)

// DefenderSuppress makes Windows Defender stand aside for the duration of a
// tweak session WITHOUT removing or permanently disabling it — a reliable,
// reversible "suppress for tweaking" toggle that works on any home/standalone PC
// with no reboot.
//
// Mechanism (all elevated-admin, no TrustedInstaller, no reboot):
//   - Apply adds two Defender exclusions: our own process (ExclusionProcess) and
//     the download/work dir the tweaker uses (ExclusionPath = os.TempDir(), the
//     same directory DownloadInstall streams installers into via os.CreateTemp).
//     Exclusions can be added by an elevated admin on a standalone PC EVEN when
//     Tamper Protection is ON (Tamper only locks exclusions on Intune/MDE-managed
//     devices), so this never hard-blocks on Tamper.
//   - If Tamper Protection is OFF (probed via the shared TamperCache), Apply also
//     turns realtime monitoring off (Set-MpPreference -DisableRealtimeMonitoring
//     $true). When Tamper is ON this sub-step is skipped (it would fail 0x800106ba)
//     — the exclusions alone still let the tweaker operate unimpeded.
//
// Rollback removes ONLY the exclusions we added (never a pre-existing one) and,
// if Apply had disabled realtime, re-enables it. It is idempotent and safe to
// run twice.
//
// Probe reports On iff OUR ExclusionProcess entry is present; Absent when
// Get-MpPreference fails (third-party AV / Defender service stripped) so the
// engine degrades to a clear status instead of crashing.
//
// The PowerShell runner is injected (run field) so tests exercise all command
// construction / parsing / snapshot logic against a fake without touching the
// live system. nil run -> the package's real timeout-bounded runner.
type DefenderSuppress struct {
	tamper  *TamperCache           // shared cache; gates only the realtime-off sub-step (may be nil)
	Elev    core.Elevation         //
	run     psCtxRunner            // injectable PowerShell runner (nil -> realPSRunner)
	exe     func() (string, error) // injectable os.Executable (nil -> os.Executable)
	workdir func() string          // injectable work-dir resolver (nil -> os.TempDir)
}

// NewDefenderSuppress builds the action wired to the catalog's shared TamperCache.
func NewDefenderSuppress(tc *TamperCache, elev core.Elevation) DefenderSuppress {
	return DefenderSuppress{tamper: tc, Elev: elev}
}

func (a DefenderSuppress) Level() core.Elevation { return a.Elev }

// suppressSnap is the pre-change snapshot, stored in Backup.Value so Rollback is
// exact and idempotent. It survives a JSON round-trip through the backup store
// (decodeSnap re-parses whether Value is this struct or a map[string]any).
type suppressSnap struct {
	ProcAlreadyExcluded bool `json:"procAlreadyExcluded"` // our exe was already an ExclusionProcess
	PathAlreadyExcluded bool `json:"pathAlreadyExcluded"` // our workdir was already an ExclusionPath
	RealtimeWasOff      bool `json:"realtimeWasOff"`      // realtime was ALREADY disabled before Apply
}

// mpPref is the parsed, relevant subset of Get-MpPreference.
type mpPref struct {
	procs            []string
	paths            []string
	realtimeDisabled bool
	ok               bool // false => Get-MpPreference unavailable (Defender absent)
}

// runner returns the effective PowerShell runner (real one if none injected).
func (a DefenderSuppress) runner() psCtxRunner {
	if a.run != nil {
		return a.run
	}
	return realPSRunner
}

// selfExe resolves our own executable path (injectable for tests).
func (a DefenderSuppress) selfExe() (string, error) {
	if a.exe != nil {
		return a.exe()
	}
	return os.Executable()
}

// workDir resolves the download/work dir to exclude. It is os.TempDir() — the
// exact directory DownloadInstall streams installers into (os.CreateTemp("")).
func (a DefenderSuppress) workDir() string {
	if a.workdir != nil {
		return a.workdir()
	}
	return os.TempDir()
}

// runPS runs a single PowerShell -Command, bounded by a short timeout so a hung
// cmdlet cannot stall apply/rollback.
func (a DefenderSuppress) runPS(ctx core.ActionContext, command string) ([]byte, error) {
	base := ctx.Ctx
	if base == nil {
		base = context.Background()
	}
	cctx, cancel := context.WithTimeout(base, 8*time.Second)
	defer cancel()
	return a.runner()(cctx, "powershell", psCommandArgs(command)...)
}

// psCommandArgs wraps a -Command string in the standard non-interactive flags.
func psCommandArgs(command string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-Command", command}
}

// psQuote single-quotes s for PowerShell, doubling embedded single quotes so a
// path with a quote cannot break out of the literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// addExclusionsCmd builds the Add-MpPreference command adding our exe + workdir.
func addExclusionsCmd(exe, dir string) string {
	return "Add-MpPreference -ExclusionProcess " + psQuote(exe) + " -ExclusionPath " + psQuote(dir)
}

// removeExclusionsCmd builds the Remove-MpPreference command for the selected
// exclusions. dropProc/dropPath choose which to remove (skip a pre-existing one).
// Returns "" when neither is selected (caller skips the call).
func removeExclusionsCmd(exe, dir string, dropProc, dropPath bool) string {
	if !dropProc && !dropPath {
		return ""
	}
	parts := []string{"Remove-MpPreference"}
	if dropProc {
		parts = append(parts, "-ExclusionProcess "+psQuote(exe))
	}
	if dropPath {
		parts = append(parts, "-ExclusionPath "+psQuote(dir))
	}
	return strings.Join(parts, " ")
}

// realtimeCmd builds the Set-MpPreference realtime toggle ($true disables).
func realtimeCmd(disable bool) string {
	v := "$false"
	if disable {
		v = "$true"
	}
	return "Set-MpPreference -DisableRealtimeMonitoring " + v
}

// getPrefCmd is the read used by Probe/Snapshot: the exclusion lists + realtime
// flag as compact JSON.
const getPrefCmd = "Get-MpPreference | Select-Object ExclusionProcess,ExclusionPath,DisableRealtimeMonitoring | ConvertTo-Json -Compress"

// readPref runs getPrefCmd and parses it.
func (a DefenderSuppress) readPref(ctx core.ActionContext) mpPref {
	out, err := a.runPS(ctx, getPrefCmd)
	return parseMpPref(out, err)
}

// parseMpPref parses Get-MpPreference JSON into the relevant subset. It never
// panics: any error / empty / unparseable output yields ok=false ("Defender
// unavailable"), so callers treat Defender as absent rather than guessing. The
// ExclusionProcess/ExclusionPath fields may be a JSON string, an array, or null.
func parseMpPref(out []byte, err error) mpPref {
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return mpPref{ok: false}
	}
	type raw struct {
		ExclusionProcess          json.RawMessage `json:"ExclusionProcess"`
		ExclusionPath             json.RawMessage `json:"ExclusionPath"`
		DisableRealtimeMonitoring bool            `json:"DisableRealtimeMonitoring"`
	}
	var r raw
	if jerr := json.Unmarshal(out, &r); jerr != nil {
		// PS may wrap a single object in a 1-element array.
		var arr []raw
		if aerr := json.Unmarshal(out, &arr); aerr != nil || len(arr) == 0 {
			return mpPref{ok: false}
		}
		r = arr[0]
	}
	return mpPref{
		procs:            decodeStringField(r.ExclusionProcess),
		paths:            decodeStringField(r.ExclusionPath),
		realtimeDisabled: r.DisableRealtimeMonitoring,
		ok:               true,
	}
}

// decodeStringField tolerantly decodes a field that may be a JSON array of
// strings, a single string, or null/absent.
func decodeStringField(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil && one != "" {
		return []string{one}
	}
	return nil
}

// containsFold reports whether list contains s (case-insensitive; Windows paths
// are case-insensitive).
func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func (a DefenderSuppress) Snapshot(ctx core.ActionContext) (core.Backup, error) {
	exe, err := a.selfExe()
	if err != nil {
		return core.Backup{}, err
	}
	pref := a.readPref(ctx)
	snap := suppressSnap{
		ProcAlreadyExcluded: pref.ok && containsFold(pref.procs, exe),
		PathAlreadyExcluded: pref.ok && containsFold(pref.paths, a.workDir()),
		RealtimeWasOff:      pref.ok && pref.realtimeDisabled,
	}
	// Existed == "suppression (our process exclusion) already active".
	return core.Backup{Existed: snap.ProcAlreadyExcluded, Value: snap, Timestamp: time.Now()}, nil
}

// decodeSnap recovers a suppressSnap from a Backup.Value that may be the struct
// (in-memory) or a map[string]any (after a JSON round-trip through the store).
func decodeSnap(v any) suppressSnap {
	if s, ok := v.(suppressSnap); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return suppressSnap{}
	}
	var s suppressSnap
	_ = json.Unmarshal(b, &s)
	return s
}

// wouldDisableRealtime reports whether Apply's realtime-off sub-step runs given
// the snapshot and current Tamper state: only when realtime was on before AND
// Tamper is off (so Set-MpPreference is permitted). Rollback uses the same
// predicate to decide whether to re-enable realtime — no post-Apply snapshot
// mutation needed.
func (a DefenderSuppress) wouldDisableRealtime(ctx core.ActionContext, realtimeWasOff bool) bool {
	if realtimeWasOff || a.tamper == nil {
		return false
	}
	present, tamperOn := a.tamper.Get(ctx)
	return present && !tamperOn
}

func (a DefenderSuppress) Apply(ctx core.ActionContext, on bool) error {
	exe, err := a.selfExe()
	if err != nil {
		return err
	}
	dir := a.workDir()

	if !on {
		// OFF == un-suppress. Remove our exclusions (Remove-MpPreference on an absent
		// exclusion is a harmless no-op). Deliberately DO NOT touch realtime here: a
		// plain Apply(off) has no snapshot, so it cannot know whether WE disabled
		// realtime or it was already off. Re-enabling unconditionally would wrongly
		// turn protection ON when the user had it off. Realtime restoration is routed
		// SOLELY through the snapshot-aware Restore (the engine's rollback entrypoint),
		// which re-enables only what this action disabled.
		//
		// Best-effort: if Defender is not active the cmdlet fails (0x800106ba "service
		// not running"); there is then no exclusion to remove, so the un-suppress is
		// already satisfied — never fail rollback on a dead Defender.
		_, _ = a.runPS(ctx, removeExclusionsCmd(exe, dir, true, true))
		return nil
	}

	// ON == suppress. Best-effort: session-suppression is a courtesy, not a hard
	// requirement. If Defender is not active, Add-MpPreference fails (0x800106ba
	// "service not running") and the scheduled tasks/cmdlets are dead — there is
	// simply NOTHING to suppress, so we treat the failure as success ("Defender not
	// active") rather than aborting the whole atomic Defender apply (which would skip
	// the durable Start=4 layer entirely). Exclusions work even with Tamper ON, so a
	// non-nil error here genuinely means Defender is gone, not merely tamper-locked.
	_, _ = a.runPS(ctx, addExclusionsCmd(exe, dir))

	// Realtime-off only when Tamper is OFF (Set-MpPreference is blocked under Tamper).
	// realtimeWasOff is unknown here without a read; pass false so the predicate
	// consults Tamper. The result is the same one Rollback recomputes from the snap.
	if a.wouldDisableRealtime(ctx, false) {
		_, _ = a.runPS(ctx, realtimeCmd(true)) // best-effort; Windows may re-enable on idle
	}
	return nil
}

func (a DefenderSuppress) Restore(ctx core.ActionContext, b core.Backup) error {
	exe, err := a.selfExe()
	if err != nil {
		return err
	}
	dir := a.workDir()
	snap := decodeSnap(b.Value)

	// Remove only the exclusions WE added (skip any that pre-existed). If the
	// snapshot is empty (e.g. store round-trip lost it), default to removing both —
	// safe because Remove-MpPreference on an absent exclusion is a no-op.
	//
	// Best-effort throughout: like Apply, if Defender is not active the cmdlets fail
	// (0x800106ba) — there is then nothing to un-exclude or re-enable, so rollback is
	// already satisfied. Never hard-fail a rollback because Defender is gone.
	dropProc := !snap.ProcAlreadyExcluded
	dropPath := !snap.PathAlreadyExcluded
	if cmd := removeExclusionsCmd(exe, dir, dropProc, dropPath); cmd != "" {
		_, _ = a.runPS(ctx, cmd)
	}
	// Re-enable realtime iff Apply disabled it (realtime was on before AND Tamper
	// off). Same predicate Apply used, recomputed from the snapshot.
	if a.wouldDisableRealtime(ctx, snap.RealtimeWasOff) {
		_, _ = a.runPS(ctx, realtimeCmd(false))
	}
	return nil
}

func (a DefenderSuppress) Probe(ctx core.ActionContext) (core.PointState, error) {
	exe, err := a.selfExe()
	if err != nil {
		return core.PointOff, err
	}
	pref := a.readPref(ctx)
	if !pref.ok {
		// Defender unavailable (3rd-party AV / service stripped): n/a, not a crash.
		return core.PointAbsent, nil
	}
	if containsFold(pref.procs, exe) {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}

// ensure DefenderSuppress satisfies core.Action.
var _ core.Action = DefenderSuppress{}
