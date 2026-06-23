package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"morgtweaker/internal/core"
)

// payload is the fake installer body the test server serves. Its sha256 is
// computed in-test (payloadSum) so the happy path verifies against a real hash
// rather than a hand-written constant.
var payload = []byte("morgtweaker-test-installer-payload-bytes-0123456789")

func payloadSum() string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}

// serveBytes spins up an httptest server that returns the given bytes with a
// correct Content-Length so progress reporting has a denominator.
func serveBytes(t *testing.T, b []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestDownloadInstallHappyPath: a correct sha256 downloads, verifies, reports
// monotonically non-decreasing progress, and invokes the injected installer
// runner exactly once with the configured args.
func TestDownloadInstallHappyPath(t *testing.T) {
	srv := serveBytes(t, payload)

	var ranPath string
	var ranArgs []string
	ranCount := 0
	a := DownloadInstall{
		URL:    srv.URL,
		SHA256: payloadSum(),
		Args:   []string{"/quiet", "/norestart"},
		Elev:   core.ElevAdmin,
		run: func(_ context.Context, p string, args ...string) error {
			ranCount++
			ranPath, ranArgs = p, args
			return nil
		},
	}

	var pcts []int
	ctx := core.ActionContext{
		Ctx:      context.Background(),
		Progress: func(p int, _ string) { pcts = append(pcts, p) },
	}
	if err := a.Apply(ctx, true); err != nil {
		t.Fatalf("Apply(on) on good hash should succeed, got %v", err)
	}
	if ranCount != 1 {
		t.Fatalf("installer runner called %d times, want 1", ranCount)
	}
	if ranPath == "" {
		t.Error("installer runner got empty path")
	}
	if len(ranArgs) != 2 || ranArgs[0] != "/quiet" || ranArgs[1] != "/norestart" {
		t.Errorf("installer runner args = %v want [/quiet /norestart]", ranArgs)
	}
	// progress reported and increasing toward 100
	if len(pcts) == 0 {
		t.Fatal("no progress reported during download")
	}
	last := -1
	for _, p := range pcts {
		if p < last {
			t.Errorf("progress went backwards: %v", pcts)
			break
		}
		last = p
	}
	if pcts[len(pcts)-1] < 100 {
		t.Errorf("final progress = %d, want 100 (install step)", pcts[len(pcts)-1])
	}
}

// TestDownloadInstallRejectsBadHash is the SECURITY test: when the downloaded
// bytes do not match the pinned sha256, Apply returns an error AND never invokes
// the installer runner.
func TestDownloadInstallRejectsBadHash(t *testing.T) {
	srv := serveBytes(t, payload)

	a := DownloadInstall{
		URL:    srv.URL,
		SHA256: "deadbeef00000000000000000000000000000000000000000000000000000000",
		Elev:   core.ElevAdmin,
		run: func(context.Context, string, ...string) error {
			t.Fatal("SECURITY: installer must NOT run on sha256 mismatch")
			return nil
		},
	}
	var lastPct int
	ctx := core.ActionContext{
		Ctx:      context.Background(),
		Progress: func(p int, _ string) { lastPct = p },
	}
	if err := a.Apply(ctx, true); err == nil {
		t.Error("Apply should fail on sha256 mismatch")
	}
	// progress is still reported during the (rejected) download
	if lastPct == 0 {
		t.Error("expected progress to be reported during download before the hash check")
	}
}

// TestDownloadInstallCancel: a cancelled context aborts the download and the
// installer never runs.
func TestDownloadInstallCancel(t *testing.T) {
	// Server that drips bytes slowly so cancellation lands mid-stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		chunk := make([]byte, 1000)
		for i := 0; i < 100; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	a := DownloadInstall{
		URL:    srv.URL,
		SHA256: payloadSum(),
		Elev:   core.ElevAdmin,
		run: func(context.Context, string, ...string) error {
			t.Fatal("installer must NOT run when download is cancelled")
			return nil
		},
	}
	actx := core.ActionContext{
		Ctx: ctx,
		Progress: func(int, string) {
			cancel() // cancel as soon as the first chunk lands
		},
	}
	err := a.Apply(actx, true)
	if err == nil {
		t.Error("Apply should return an error when the download is cancelled")
	}
}

// TestDownloadInstallProbeDelegates: Probe forwards to the Detect action.
func TestDownloadInstallProbeDelegates(t *testing.T) {
	on := DownloadInstall{Detect: stubDetect{core.PointOn}}
	if ps, _ := on.Probe(core.ActionContext{}); ps != core.PointOn {
		t.Error("Probe should delegate to Detect (PointOn)")
	}
	off := DownloadInstall{Detect: stubDetect{core.PointOff}}
	if ps, _ := off.Probe(core.ActionContext{}); ps != core.PointOff {
		t.Error("Probe should delegate to Detect (PointOff)")
	}
	// nil Detect → PointOff (not installable info, treat as not-on)
	none := DownloadInstall{}
	if ps, _ := none.Probe(core.ActionContext{}); ps != core.PointOff {
		t.Error("nil Detect should probe PointOff")
	}
}

// TestDownloadInstallNoopOff: Apply(on=false), Snapshot, Restore are honest
// no-ops and never touch the network or runner.
func TestDownloadInstallNoopOff(t *testing.T) {
	a := DownloadInstall{
		URL: "http://127.0.0.1:0/nope",
		run: func(context.Context, string, ...string) error {
			t.Fatal("runner must not run for Apply(off)")
			return nil
		},
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, false); err != nil {
		t.Errorf("Apply(off) should be a no-op, got %v", err)
	}
	b, err := a.Snapshot(core.ActionContext{})
	if err != nil || b.Existed {
		t.Errorf("Snapshot = %+v,%v want empty,nil", b, err)
	}
	if err := a.Restore(core.ActionContext{}, b); err != nil {
		t.Errorf("Restore should be a no-op, got %v", err)
	}
}

func TestDownloadInstallLevel(t *testing.T) {
	if (DownloadInstall{Elev: core.ElevAdmin}).Level() != core.ElevAdmin {
		t.Error("Level() should echo Elev")
	}
}

// TestDownloadInstallRejectsMalformedPin (FIX B, fail-closed): a pin that is not
// exactly 64 hex chars is rejected BEFORE any network call, and the installer
// never runs. Covers empty / placeholder / short / non-hex / wrong-length cases.
func TestDownloadInstallRejectsMalformedPin(t *testing.T) {
	pins := map[string]string{
		"empty":   "",
		"todo":    "TODO-pin-real-sha256-of-shipped-vc_redist.x64.exe",
		"short":   "abc123",
		"non-hex": "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", // 64 chars but not hex
		"65 hex":  "00000000000000000000000000000000000000000000000000000000000000000",
	}
	for name, pin := range pins {
		t.Run(name, func(t *testing.T) {
			a := DownloadInstall{
				URL:    "http://127.0.0.1:0/should-never-be-fetched",
				SHA256: pin,
				Elev:   core.ElevAdmin,
				httpGet: func(context.Context, string) (io.ReadCloser, int64, error) {
					t.Fatal("network must NOT be touched when the pin is malformed")
					return nil, 0, nil
				},
				run: func(context.Context, string, ...string) error {
					t.Fatal("installer must NOT run when the pin is malformed")
					return nil
				},
			}
			if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err == nil {
				t.Errorf("Apply with malformed pin %q should error", pin)
			}
		})
	}
}

// stubDetect is a fake core.Action used to drive DownloadInstall.Probe.
type stubDetect struct{ ps core.PointState }

func (s stubDetect) Apply(core.ActionContext, bool) error              { return nil }
func (s stubDetect) Snapshot(core.ActionContext) (core.Backup, error)  { return core.Backup{}, nil }
func (s stubDetect) Restore(core.ActionContext, core.Backup) error     { return nil }
func (s stubDetect) Probe(core.ActionContext) (core.PointState, error) { return s.ps, nil }
func (s stubDetect) Level() core.Elevation                             { return core.ElevUser }

var _ io.Reader // keep io import even if helpers change
