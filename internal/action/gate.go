package action

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"time"

	"morgtweaker/internal/core"
)

// psRunner runs a command and returns its stdout. Convenience (ctx-unaware) form,
// used by tests and NewTamperCache; the cache adapts it to the ctx-aware runner.
type psRunner func(name string, args ...string) ([]byte, error)

// psCtxRunner is the ctx-aware runner the cache calls internally so caller
// cancellation (UI quit / apply-cancel) propagates to the spawned PowerShell.
type psCtxRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// TamperCache reads Defender's Tamper Protection state AT MOST ONCE per TTL and
// shares the result across every gate that holds it. Without this, a single
// ProbeBatch over N Defender tweaks would spawn N Get-MpComputerStatus processes
// (~seconds each), reintroducing the UI lag the v2 rewrite exists to remove. The
// catalog creates one *TamperCache and injects it into every Defender tweak's
// gate, so one refresh => one PowerShell spawn.
//
// The mutex is held across the fetch, so concurrent Gets within the window do not
// stampede: the first does the work, the rest reuse its cached result.
type TamperCache struct {
	run psCtxRunner
	ttl time.Duration

	mu        sync.Mutex
	valid     bool
	fetchedAt time.Time
	present   bool
	tamperOn  bool
	errored   bool // the LAST fetch's check genuinely errored (runner/cmdlet error)
}

// NewTamperCache builds a cache from a ctx-unaware runner (the common case for
// tests and simple wiring). nil run -> the real timeout-bounded PowerShell runner.
func NewTamperCache(run psRunner, ttl time.Duration) *TamperCache {
	var cr psCtxRunner
	if run != nil {
		cr = func(_ context.Context, name string, args ...string) ([]byte, error) {
			return run(name, args...)
		}
	}
	return NewTamperCacheCtx(cr, ttl)
}

// NewTamperCacheCtx builds a cache from a ctx-aware runner. nil run -> the real
// timeout-bounded PowerShell runner.
func NewTamperCacheCtx(run psCtxRunner, ttl time.Duration) *TamperCache {
	if run == nil {
		run = realPSRunner
	}
	return &TamperCache{run: run, ttl: ttl}
}

// Get returns (present, tamperOn), fetching at most once per TTL. The first
// caller in a stale window pays the PowerShell cost; concurrent callers block on
// the mutex and reuse the freshly cached value (single-flight via the mutex).
func (c *TamperCache) Get(ctx core.ActionContext) (present, tamperOn bool) {
	present, tamperOn, _ = c.GetState(ctx)
	return present, tamperOn
}

// GetState is Get plus the errored bit: true when the LAST check genuinely errored
// (the runner/cmdlet returned an error), as distinct from a successful check that
// simply reports Defender absent. The TamperGate uses this to fail CLOSED on a real
// check error (Tamper state unknown) while still reporting Absent for a clean
// not-installed result. Same single-flight/TTL semantics as Get.
func (c *TamperCache) GetState(ctx core.ActionContext) (present, tamperOn, errored bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && time.Since(c.fetchedAt) < c.ttl {
		return c.present, c.tamperOn, c.errored
	}
	base := ctx.Ctx
	if base == nil {
		base = context.Background()
	}
	cctx, cancel := context.WithTimeout(base, 4*time.Second)
	defer cancel()
	out, err := c.run(cctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-MpComputerStatus | Select-Object AMServiceEnabled,IsTamperProtected | ConvertTo-Json -Compress")
	c.present, c.tamperOn, c.errored = mpDetect(out, err)
	c.valid = true
	c.fetchedAt = time.Now()
	return c.present, c.tamperOn, c.errored
}

// Invalidate drops the cached value so the next Get re-probes (e.g. after an
// apply that toggled Defender, or on an explicit user refresh).
func (c *TamperCache) Invalidate() {
	c.mu.Lock()
	c.valid = false
	c.mu.Unlock()
}

// TamperGate is the Defender precondition, reusable on any tweak whose registry
// writes Defender's Tamper Protection would silently revert. It reports Absent
// when the check SUCCEEDS but Defender is not present (gutted build / non-Defender
// host), Blocked when Tamper Protection is ON (with a deep-link to Windows Security
// so the user can turn it off), Blocked when the check ERRORS (FIX 4: TP state
// unknown → fail closed for this durable-disable gate, with a non-Tamper-asserting
// message), and clears (ok=true, Off) only when TP is confirmed off. All gates
// sharing a *TamperCache cause only one Get-MpComputerStatus per refresh.
//
// Detection reuses the v1 approach (internal/tweak/defender_status.go): the
// Get-MpComputerStatus cmdlet's IsTamperProtected field. There is no reliable,
// readable registry value for TP state — HKLM\...\Features\TamperProtection is
// tamper-protected itself and ACL-blocked even for SYSTEM — so the cmdlet is the
// real source of truth and is what v1 used.
type TamperGate struct {
	cache *TamperCache
}

// NewTamperGate wires a gate to a shared cache. The catalog builds one cache and
// passes it to every Defender tweak's gate.
func NewTamperGate(c *TamperCache) TamperGate { return TamperGate{cache: c} }

func (g TamperGate) Check(ctx core.ActionContext) (bool, core.Status, core.GateAction) {
	c := g.cache
	if c == nil {
		c = NewTamperCache(nil, 5*time.Second)
	}
	present, tamperOn, errored := c.GetState(ctx)
	if errored {
		// FAIL CLOSED (FIX 4): the Tamper check genuinely errored, so TP state is
		// UNKNOWN. For a durable-disable gate an unknown TP could silently revert our
		// writes, so block — but do NOT assert TP is on (we don't know). The deep-link
		// still points the user to where they can confirm/disable TP.
		return false, core.StatusBlocked, core.GateAction{
			Label: core.I18n{
				RU: "Не удалось проверить состояние Tamper Protection — убедитесь, что оно отключено, в Безопасности Windows.",
				EN: "Could not verify Tamper Protection state — make sure it is turned off in Windows Security.",
			},
			URL: "windowsdefender://threatsettings",
		}
	}
	if !present {
		return false, core.StatusAbsent, core.GateAction{}
	}
	if tamperOn {
		return false, core.StatusBlocked, core.GateAction{
			Label: core.I18n{
				RU: "Защита от подделки Windows Defender блокирует эту настройку. Откройте Безопасность Windows и отключите её.",
				EN: "Windows Defender Tamper Protection blocks this tweak. Open Windows Security and turn it off.",
			},
			URL: "windowsdefender://threatsettings",
		}
	}
	return true, core.StatusOff, core.GateAction{}
}

// mpDetect parses Get-MpComputerStatus output into (present, tamperOn, errored).
// errored is true ONLY when the runner returned a genuine error (the cmdlet failed
// to run / is missing) — the case where Tamper state is truly UNKNOWN and a durable
// gate must fail closed. A successful run that yields empty/unparseable output (no
// runner error) is NOT errored: it reports not-present, so the caller treats
// Defender as absent rather than guessing. Tolerates a 1-element array wrapper (PS
// sometimes emits an array for a single object). Ported from v1 queryDefender.
func mpDetect(out []byte, err error) (present, tamperOn, errored bool) {
	if err != nil {
		return false, false, true // genuine check error → state unknown
	}
	if len(out) == 0 {
		return false, false, false // ran OK but no data → Defender absent
	}
	type mpRaw struct {
		AMServiceEnabled  bool `json:"AMServiceEnabled"`
		IsTamperProtected bool `json:"IsTamperProtected"`
	}
	var obj mpRaw
	if jerr := json.Unmarshal(out, &obj); jerr == nil {
		return true, obj.IsTamperProtected, false
	}
	var arr []mpRaw
	if jerr := json.Unmarshal(out, &arr); jerr == nil && len(arr) > 0 {
		return true, arr[0].IsTamperProtected, false
	}
	return false, false, false // ran OK but unparseable → treat as absent, not error
}

// realPSRunner invokes PowerShell bound to ctx so caller cancellation kills it;
// the cache additionally caps it with a 4s timeout.
func realPSRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

var _ core.Gate = TamperGate{}
