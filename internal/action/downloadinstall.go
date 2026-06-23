package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"morgtweaker/internal/core"
)

// DownloadInstall fetches an installer over HTTP, verifies its sha256 against a
// PINNED hash, and — only on a match — runs it (silently, elevated). It is the
// "download_install" executor kind.
//
// Security invariant: the installer is NEVER executed unless the downloaded
// bytes hash exactly to SHA256. A mismatch returns an error and the temp file is
// discarded — we never run an unverified binary.
//
// Apply(on=true) streams the download to a temp file, reporting progress via
// ctx.Report using Content-Length as the denominator, and honours ctx.Ctx
// cancellation (the HTTP request is bound to the context and the copy loop
// aborts on cancel). Apply(on=false) is an honest no-op: an install has no exact
// inverse (uninstall is a separate, manual step). Snapshot/Restore are likewise
// honest no-ops. Probe delegates to Detect (a cheap, network-free check such as
// a RegSet reading the component's "Installed" flag): installed→On, else Off.
type DownloadInstall struct {
	URL     string
	SHA256  string      // pinned lowercase hex sha256 of the exact shipped binary
	Args    []string    // silent-install args, e.g. /quiet /norestart
	Detect  core.Action // cheap installed-detector reused for Probe (may be nil)
	Elev    core.Elevation
	httpGet func(ctx context.Context, url string) (io.ReadCloser, int64, error) // injectable
	run     func(ctx context.Context, path string, args ...string) error        // injectable
}

func (a DownloadInstall) Level() core.Elevation { return a.Elev }

func (a DownloadInstall) Apply(ctx core.ActionContext, on bool) error {
	if !on {
		return nil // revert is a manual uninstall — honest no-op, never pretend
	}
	// SECURITY (fail-closed): refuse before touching the network if the pin is not
	// a well-formed sha256 (exactly 64 hex chars). This makes the invariant
	// self-evident rather than relying on the incidental fact that the computed
	// hash is always 64 hex (so an empty/placeholder pin merely "happens" never to
	// match). A TODO placeholder pin therefore fails here, never installing.
	if !isSHA256Hex(a.SHA256) {
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

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, a.SHA256) {
		// SECURITY: do not run an unverified binary.
		return fmt.Errorf("download_install: sha256 mismatch (got %s, want %s) — installer not run", got, a.SHA256)
	}

	ctx.Report(100, "installing")
	run := a.run
	if run == nil {
		run = execRun
	}
	return run(c, tmpName, a.Args...)
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
				ctx.Report(pct, "downloading")
			} else {
				ctx.Report(0, "downloading") // unknown length: still signal activity
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

var _ core.Action = DownloadInstall{}
