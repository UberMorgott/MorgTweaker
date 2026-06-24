package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"morgtweaker/internal/core"
)

// VerifyMode selects how a downloaded installer is trusted before it is allowed
// to run. The zero value is VerifySHA256 so existing callers (pinned downloads)
// keep their fail-closed behaviour without changing any literal.
type VerifyMode int

const (
	// VerifySHA256 (default) requires the downloaded bytes to hash exactly to the
	// pinned SHA256. Used for installers we ship against a known, fixed build.
	VerifySHA256 VerifyMode = iota
	// VerifyAuthenticodeMicrosoft requires the downloaded file to carry a Valid
	// Authenticode signature whose signer subject contains "O=Microsoft
	// Corporation". Used for evergreen "always latest" Microsoft downloads (e.g.
	// the VC++ redistributable) whose SHA256 cannot be pinned at authoring time.
	VerifyAuthenticodeMicrosoft
)

// DownloadInstall fetches an installer over HTTP, verifies its authenticity, and
// — only on a pass — runs it (silently, elevated). It is the "download_install"
// executor kind.
//
// Security invariant: the installer is NEVER executed unless the downloaded
// bytes pass the configured Verify check. A failure returns an error and the
// temp file is discarded — we never run an unverified binary (fail-closed).
//
//   - Verify == VerifySHA256: the bytes must hash exactly to SHA256 (a pinned
//     lowercase-hex 64-char digest). A malformed/placeholder pin is rejected
//     BEFORE the network is touched.
//   - Verify == VerifyAuthenticodeMicrosoft: after download, the file's digital
//     signature is checked via Get-AuthenticodeSignature (through the injectable
//     psRun seam). It passes only when Status == 'Valid' AND the signer subject
//     contains 'O=Microsoft Corporation'. No SHA256 is required in this mode.
//
// On a successful verify the installer runs and its process exit code is mapped
// to success/failure via AcceptExit (default {0}). 3010 (reboot required), 1638
// (newer already installed) and 1641 (reboot initiated) are commonly added so a
// reboot-pending or already-satisfied install is not reported as a failure.
//
// Apply(on=true) streams the download to a temp file, reporting progress via
// ctx.Report using Content-Length as the denominator, and honours ctx.Ctx
// cancellation (the HTTP request is bound to the context and the copy loop
// aborts on cancel). Apply(on=false) is an honest no-op: an install has no exact
// inverse (uninstall is a separate, manual step). Snapshot/Restore are likewise
// honest no-ops. Probe delegates to Detect (a cheap, network-free check such as
// a RegSet reading the component's "Installed" flag): installed→On, else Off.
type DownloadInstall struct {
	URL        string
	Verify     VerifyMode  // how to trust the download (default VerifySHA256)
	SHA256     string      // pinned lowercase hex sha256 (VerifySHA256 mode only)
	Args       []string    // silent-install args, e.g. /install /quiet /norestart
	AcceptExit []int       // installer exit codes treated as success (nil/empty -> {0})
	Detect     core.Action // cheap installed-detector reused for Probe (may be nil)
	Elev       core.Elevation
	httpGet    func(ctx context.Context, url string) (io.ReadCloser, int64, error) // injectable
	run        func(ctx context.Context, path string, args ...string) error        // injectable (legacy: nil exit code)
	runCode    func(ctx context.Context, path string, args ...string) (int, error) // injectable (exit-code aware)
	psRun      psCtxRunner                                                         // injectable PowerShell runner (Authenticode mode)
}

func (a DownloadInstall) Level() core.Elevation { return a.Elev }

func (a DownloadInstall) Apply(ctx core.ActionContext, on bool) error {
	if !on {
		return nil // revert is a manual uninstall — honest no-op, never pretend
	}
	// SECURITY (fail-closed): in SHA256 mode, refuse before touching the network if
	// the pin is not a well-formed sha256 (exactly 64 hex chars). A TODO placeholder
	// pin therefore fails here, never installing. Authenticode mode does not use a
	// pin — the signature is the trust anchor — so skip this gate for it.
	if a.Verify == VerifySHA256 && !isSHA256Hex(a.SHA256) {
		return fmt.Errorf("download_install: invalid pinned sha256 %q (need 64 hex chars) — installer not run", a.SHA256)
	}
	c := ctx.Ctx
	if c == nil {
		c = context.Background()
	}

	get := a.httpGet
	if get == nil {
		get = httpGetDefault
	}
	body, size, err := get(c, a.URL)
	if err != nil {
		return err
	}
	defer body.Close()

	tmp, err := os.CreateTemp("", "morgtweaker-*.exe")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	h := sha256.New()
	mw := io.MultiWriter(tmp, h)
	if err := streamWithProgress(ctx, mw, body, size); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Verify (fail-closed): never run an unverified binary.
	if err := a.verify(c, tmpName, h); err != nil {
		return err
	}

	ctx.Report(100, "installing", 0, 0)
	code, err := a.invoke(c, tmpName)
	if err != nil {
		return err
	}
	if !a.exitAccepted(code) {
		return fmt.Errorf("download_install: installer exited with code %d (not an accepted success code) — install failed", code)
	}
	if code == 3010 || code == 1641 {
		ctx.Report(100, "installed (reboot recommended)", 0, 0)
	}
	return nil
}

// verify enforces the configured trust check on the downloaded temp file. It
// returns nil only when the file is trusted; any error means "do not run".
func (a DownloadInstall) verify(ctx context.Context, path string, h interface{ Sum([]byte) []byte }) error {
	switch a.Verify {
	case VerifyAuthenticodeMicrosoft:
		return a.verifyAuthenticodeMicrosoft(ctx, path)
	default: // VerifySHA256
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, a.SHA256) {
			return fmt.Errorf("download_install: sha256 mismatch (got %s, want %s) — installer not run", got, a.SHA256)
		}
		return nil
	}
}

// verifyAuthenticodeMicrosoft runs Get-AuthenticodeSignature on the file and
// passes only when the signature Status is 'Valid' AND the signer subject
// contains 'O=Microsoft Corporation'. Any other outcome (Invalid, unsigned,
// non-Microsoft signer, runner error, unparseable output) FAILS CLOSED.
func (a DownloadInstall) verifyAuthenticodeMicrosoft(ctx context.Context, path string) error {
	run := a.psRun
	if run == nil {
		run = realPSRunner
	}
	base := ctx
	if base == nil {
		base = context.Background()
	}
	cctx, cancel := context.WithTimeout(base, 30*time.Second)
	defer cancel()

	out, err := run(cctx, "powershell", psCommandArgs(authenticodeCmd(path))...)
	if err != nil {
		return fmt.Errorf("download_install: Authenticode check failed to run (%v) — installer not run", err)
	}
	status, subject := parseAuthenticode(out)
	if !strings.EqualFold(status, "Valid") {
		return fmt.Errorf("download_install: Authenticode status %q (need Valid) — installer not run", status)
	}
	if !strings.Contains(subject, "O=Microsoft Corporation") {
		return fmt.Errorf("download_install: Authenticode signer %q is not Microsoft Corporation — installer not run", subject)
	}
	return nil
}

// authenticodeCmd builds the PowerShell that emits the signature Status and
// signer subject as compact JSON for the given file.
func authenticodeCmd(path string) string {
	return "$s = Get-AuthenticodeSignature -LiteralPath " + psQuote(path) +
		"; [pscustomobject]@{Status=$s.Status.ToString();Subject=$s.SignerCertificate.Subject} | ConvertTo-Json -Compress"
}

// parseAuthenticode extracts (status, signerSubject) from authenticodeCmd's JSON.
// On any parse failure it returns empty strings, which the caller treats as a
// fail-closed result.
func parseAuthenticode(out []byte) (status, subject string) {
	if len(strings.TrimSpace(string(out))) == 0 {
		return "", ""
	}
	type raw struct {
		Status  string `json:"Status"`
		Subject string `json:"Subject"`
	}
	var r raw
	if err := json.Unmarshal(out, &r); err != nil {
		return "", ""
	}
	return r.Status, r.Subject
}

// invoke runs the verified installer and returns its process exit code. It
// prefers the exit-code-aware seam; falls back to the legacy error-only seam
// (mapping nil->0, non-nil->the real code if available else 1); and defaults to
// the real ctx-bound exec when neither is injected.
func (a DownloadInstall) invoke(ctx context.Context, path string) (int, error) {
	if a.runCode != nil {
		return a.runCode(ctx, path, a.Args...)
	}
	if a.run != nil {
		if err := a.run(ctx, path, a.Args...); err != nil {
			return exitCodeOf(err), err
		}
		return 0, nil
	}
	return execRunCode(ctx, path, a.Args...)
}

// exitAccepted reports whether code is in the configured accepted set (default
// {0} when AcceptExit is empty).
func (a DownloadInstall) exitAccepted(code int) bool {
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

func (a DownloadInstall) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}

func (a DownloadInstall) Restore(core.ActionContext, core.Backup) error { return nil }

func (a DownloadInstall) Probe(ctx core.ActionContext) (core.PointState, error) {
	if a.Detect == nil {
		return core.PointOff, nil
	}
	return a.Detect.Probe(ctx)
}

// SkipVerifyAfter reports whether the engine's per-action verify-after must SKIP
// this action. For a DownloadInstall it is ALWAYS true: an install is a one-shot
// whose success is decided by the installer's exit code (mapped via AcceptExit),
// NOT by re-probing Detect. Re-probing after a successful install is unreliable —
// e.g. a redist whose runtime is already present (newer) exits 1638 WITHOUT
// rewriting its "Installed" flag, so a Detect re-probe reads Off and would FALSELY
// flag the successful install as reverted/Blocked. Detect is still used for the
// LIST status (installed/not in the catalog view) via Probe; it just must not gate
// the apply result. So the engine trusts Apply's accepted exit code here.
func (a DownloadInstall) SkipVerifyAfter() bool { return true }

// streamWithProgress copies src→dst in chunks, reporting percent complete via
// ctx.Report (using total as the denominator) and aborting on ctx cancellation.
func streamWithProgress(ctx core.ActionContext, dst io.Writer, src io.Reader, total int64) error {
	buf := make([]byte, 32*1024)
	var done int64
	for {
		if ctx.Ctx != nil {
			if err := ctx.Ctx.Err(); err != nil {
				return err // cancelled
			}
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if total > 0 {
				pct := int(done * 100 / total)
				if pct > 100 {
					pct = 100
				}
				ctx.Report(pct, "downloading", done, total)
			} else {
				ctx.Report(0, "downloading", done, 0) // unknown length: still signal activity + bytes so far
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// httpGetDefault performs the real network GET, bound to ctx so cancellation
// aborts an in-flight download.
func httpGetDefault(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("download_install: GET %s → %s", url, resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

// execRunCode runs the installer bound to ctx (cancellation kills the process)
// and returns its exit code. A clean exit yields (0, nil). A non-zero exit yields
// (code, nil) so the caller can map it against AcceptExit — only a failure to
// start / signal-kill returns a non-nil error.
func execRunCode(ctx context.Context, path string, args ...string) (int, error) {
	err := exec.CommandContext(ctx, path, args...).Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, err // failed to start / killed by signal / ctx cancel
}

// exitCodeOf extracts a process exit code from an error returned by the legacy
// run seam, defaulting to 1 when the error is not an *exec.ExitError.
func exitCodeOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

// isSHA256Hex reports whether s is exactly 64 hexadecimal digits (a valid
// sha256 pin). Case-insensitive.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

var (
	_ core.Action = DownloadInstall{}
	// compile-time check that DownloadInstall exposes the verify-after-skip
	// capability the engine looks for (an install with a non-informative probe
	// must not be falsely flagged Blocked by per-action verify-after).
	_ interface{ SkipVerifyAfter() bool } = DownloadInstall{}
)
