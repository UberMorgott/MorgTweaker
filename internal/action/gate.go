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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && time.Since(c.fetchedAt) < c.ttl {
		return c.present, c.tamperOn
	}
	base := ctx.Ctx
	if base == nil {
		base = context.Background()
	}
	cctx, cancel := context.WithTimeout(base, 4*time.Second)
	defer cancel()
	out, err := c.run(cctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-MpComputerStatus | Select-Object AMServiceEnabled,IsTamperProtected | ConvertTo-Json -Compress")
	c.present, c.tamperOn = mpDetect(out, err)
	c.valid = true
	c.fetchedAt = time.Now()
	return c.present, c.tamperOn
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
// when the Defender cmdlet is unavailable (gutted build / non-Defender host),
// Blocked when Tamper Protection is ON (with a deep-link to Windows Security so
// the user can turn it off), and clears (ok=true, Off) only when TP is off. All
// gates sharing a *TamperCache cause only one Get-MpComputerStatus per refresh.
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
	present, tamperOn := c.Get(ctx)
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

// mpDetect parses Get-MpComputerStatus output into (present, tamperOn). It never
// errors: any failure (cmdlet missing, empty output, parse error) means "not
// present", so callers treat Defender as absent rather than guessing. Ported from
// v1 queryDefender, plus tolerance for a 1-element array wrapper (PS sometimes
// emits an array for a single object).
func mpDetect(out []byte, err error) (present, tamperOn bool) {
	if err != nil || len(out) == 0 {
		return false, false
	}
	type mpRaw struct {
		AMServiceEnabled  bool `json:"AMServiceEnabled"`
		IsTamperProtected bool `json:"IsTamperProtected"`
	}
	var obj mpRaw
	if jerr := json.Unmarshal(out, &obj); jerr == nil {
		return true, obj.IsTamperProtected
	}
	var arr []mpRaw
	if jerr := json.Unmarshal(out, &arr); jerr == nil && len(arr) > 0 {
		return true, arr[0].IsTamperProtected
	}
	return false, false
}

// realPSRunner invokes PowerShell bound to ctx so caller cancellation kills it;
// the cache additionally caps it with a 4s timeout.
func realPSRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

var _ core.Gate = TamperGate{}
