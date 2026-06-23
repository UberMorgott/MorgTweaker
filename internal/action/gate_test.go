package action

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"morgtweaker/internal/core"
)

// gateWith builds a TamperGate backed by a cache whose runner returns the given
// canned output/err. No real PowerShell is ever spawned.
func gateWith(out []byte, err error) TamperGate {
	return NewTamperGate(NewTamperCache(func(string, ...string) ([]byte, error) {
		return out, err
	}, time.Minute))
}

func TestTamperGateBlocksWhenTPOn(t *testing.T) {
	g := gateWith([]byte(`{"AMServiceEnabled":true,"IsTamperProtected":true}`), nil)
	ok, st, act := g.Check(core.ActionContext{})
	if ok || st != core.StatusBlocked {
		t.Errorf("TP on -> Check = %v,%v want false,Blocked", ok, st)
	}
	if act.URL == "" {
		t.Error("blocked gate should offer a deep-link action")
	}
	if act.Label.RU == "" || act.Label.EN == "" {
		t.Error("blocked gate action should be localized RU+EN")
	}
}

// FIX 4: a genuine check ERROR (Tamper state unknown) must FAIL CLOSED → Blocked
// for this durable-disable gate, NOT Absent. The block message must not assert TP
// is on, and the deep-link is still offered so the user can confirm/disable TP.
func TestTamperGateBlocksWhenCheckErrors(t *testing.T) {
	g := gateWith(nil, errors.New("cmdlet failed"))
	ok, st, act := g.Check(core.ActionContext{})
	if ok || st != core.StatusBlocked {
		t.Errorf("check error -> Check = %v,%v want false,Blocked (fail closed)", ok, st)
	}
	if act.URL != "windowsdefender://threatsettings" {
		t.Errorf("check-error block should keep the deep-link, got %q", act.URL)
	}
	if act.Label.RU == "" || act.Label.EN == "" {
		t.Error("check-error block should be localized RU+EN")
	}
}

func TestTamperGateAbsentWhenEmptyOutput(t *testing.T) {
	g := gateWith(nil, nil)
	ok, st, _ := g.Check(core.ActionContext{})
	if ok || st != core.StatusAbsent {
		t.Errorf("empty output -> Check = %v,%v want false,Absent", ok, st)
	}
}

func TestTamperGateAbsentWhenBadJSON(t *testing.T) {
	g := gateWith([]byte("not json"), nil)
	ok, st, _ := g.Check(core.ActionContext{})
	if ok || st != core.StatusAbsent {
		t.Errorf("bad json -> Check = %v,%v want false,Absent", ok, st)
	}
}

func TestTamperGateOpenWhenTPOff(t *testing.T) {
	g := gateWith([]byte(`{"AMServiceEnabled":true,"IsTamperProtected":false}`), nil)
	ok, st, _ := g.Check(core.ActionContext{})
	if !ok || st != core.StatusOff {
		t.Errorf("TP off -> Check = %v,%v want true,Off", ok, st)
	}
}

// FIX 3: tolerate an array-wrapped object (PS may wrap output as a 1-element array).
func TestTamperGateParsesArrayWrappedJSON(t *testing.T) {
	g := gateWith([]byte(`[{"AMServiceEnabled":true,"IsTamperProtected":true}]`), nil)
	ok, st, _ := g.Check(core.ActionContext{})
	if ok || st != core.StatusBlocked {
		t.Errorf("array-wrapped TP on -> Check = %v,%v want false,Blocked", ok, st)
	}
}

// FIX 1: one cache shared by many gates spawns the runner only ONCE per TTL.
func TestTamperCacheSingleSpawnAcrossGates(t *testing.T) {
	var calls int32
	c := NewTamperCache(func(string, ...string) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"IsTamperProtected":true}`), nil
	}, time.Minute)

	gates := []TamperGate{NewTamperGate(c), NewTamperGate(c), NewTamperGate(c)}
	for _, g := range gates {
		if _, st, _ := g.Check(core.ActionContext{}); st != core.StatusBlocked {
			t.Fatalf("gate Check = %v want Blocked", st)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("runner spawned %d times for 3 gates; want 1 (cached)", got)
	}
}

// FIX 1: concurrent Gets within the TTL must not stampede the runner.
func TestTamperCacheNoStampede(t *testing.T) {
	var calls int32
	c := NewTamperCache(func(string, ...string) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond) // widen the race window
		return []byte(`{"IsTamperProtected":false}`), nil
	}, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Get(core.ActionContext{}) }()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("concurrent Gets spawned runner %d times; want 1 (single-flight)", got)
	}
}

// FIX 1: TTL expiry re-probes.
func TestTamperCacheTTLRefresh(t *testing.T) {
	var calls int32
	c := NewTamperCache(func(string, ...string) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"IsTamperProtected":false}`), nil
	}, 1*time.Millisecond)

	c.Get(core.ActionContext{})
	time.Sleep(8 * time.Millisecond)
	c.Get(core.ActionContext{})
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("after TTL expiry runner spawned %d times; want >=2", got)
	}
}

// FIX 1: explicit Invalidate forces a re-probe before TTL.
func TestTamperCacheInvalidate(t *testing.T) {
	var calls int32
	c := NewTamperCache(func(string, ...string) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"IsTamperProtected":false}`), nil
	}, time.Hour)

	c.Get(core.ActionContext{})
	c.Invalidate()
	c.Get(core.ActionContext{})
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("after Invalidate runner spawned %d times; want 2", got)
	}
}

// FIX 2: the runner must observe the caller's ctx (no 4s lingering on quit).
func TestTamperCacheHonorsCtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	var sawCancel bool
	c := NewTamperCacheCtx(func(rc context.Context, _ string, _ ...string) ([]byte, error) {
		sawCancel = rc.Err() != nil
		return []byte(`{"IsTamperProtected":false}`), nil
	}, time.Minute)

	c.Get(core.ActionContext{Ctx: ctx})
	if !sawCancel {
		t.Error("runner did not observe the cancelled ctx from ActionContext.Ctx")
	}
}

// mpDetect is a pure parser over (out, err) → (present, tamperOn, errored);
// mirrors v1 queryDefender semantics. FIX 4: errored distinguishes a genuine check
// error (state unknown → fail closed) from a successful "Defender absent" result.
func TestMpDetect(t *testing.T) {
	present, tamperOn, errored := mpDetect([]byte(`{"AMServiceEnabled":true,"IsTamperProtected":true}`), nil)
	if !present || !tamperOn || errored {
		t.Errorf("mpDetect(TP on) = %v,%v,%v want true,true,false", present, tamperOn, errored)
	}
	// genuine runner error → not present AND errored (state unknown).
	present, _, errored = mpDetect(nil, errors.New("boom"))
	if present || !errored {
		t.Errorf("mpDetect(run error) = present=%v,errored=%v want false,true", present, errored)
	}
	// successful run, empty output → not present but NOT errored (Defender absent).
	present, _, errored = mpDetect(nil, nil)
	if present || errored {
		t.Errorf("mpDetect(empty,no-err) = present=%v,errored=%v want false,false (absent, not error)", present, errored)
	}
	// successful run, unparseable output → absent, not errored.
	present, _, errored = mpDetect([]byte("not json"), nil)
	if present || errored {
		t.Errorf("mpDetect(bad json,no-err) = present=%v,errored=%v want false,false", present, errored)
	}
	present, tamperOn, errored = mpDetect([]byte(`[{"IsTamperProtected":true}]`), nil)
	if !present || !tamperOn || errored {
		t.Errorf("mpDetect(array) = %v,%v,%v want true,true,false", present, tamperOn, errored)
	}
}

// compile-time interface guard mirror.
var _ core.Gate = TamperGate{}
