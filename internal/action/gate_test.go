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

func TestTamperGateAbsentWhenCmdletFails(t *testing.T) {
	g := gateWith(nil, errors.New("cmdlet failed"))
	ok, st, _ := g.Check(core.ActionContext{})
	if ok || st != core.StatusAbsent {
		t.Errorf("cmdlet fail -> Check = %v,%v want false,Absent", ok, st)
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

// mpDetect is now a pure parser over (out, err); mirrors v1 queryDefender semantics.
func TestMpDetect(t *testing.T) {
	present, tamperOn := mpDetect([]byte(`{"AMServiceEnabled":true,"IsTamperProtected":true}`), nil)
	if !present || !tamperOn {
		t.Errorf("mpDetect(TP on) = %v,%v want true,true", present, tamperOn)
	}
	present, _ = mpDetect(nil, errors.New("boom"))
	if present {
		t.Error("mpDetect on run error should report not present")
	}
	present, tamperOn = mpDetect([]byte(`[{"IsTamperProtected":true}]`), nil)
	if !present || !tamperOn {
		t.Errorf("mpDetect(array) = %v,%v want true,true", present, tamperOn)
	}
}

// compile-time interface guard mirror.
var _ core.Gate = TamperGate{}
