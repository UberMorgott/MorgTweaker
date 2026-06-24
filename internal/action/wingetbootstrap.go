package action

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"morgtweaker/internal/core"
)

// WingetBootstrap installs winget (the App Installer / DesktopAppInstaller
// package) via the robust, Store-independent manual path: it pulls the latest
// microsoft/winget-cli GitHub release, downloads the .msixbundle and its
// dependency zip, Authenticode-verifies the bundle is Microsoft-signed, extracts
// the arch-matched dependency packages, and installs them + the bundle through
// Windows PowerShell 5.1's Add-AppxPackage (PS7's Appx module is broken with
// 0x80131539). Every external touchpoint is an injectable seam so the action is
// fully unit-testable with no real network / PowerShell.
//
// Security invariant (fail-closed): the bundle is NEVER handed to Add-AppxPackage
// unless its Authenticode signature is Valid AND signed by Microsoft Corporation.
type WingetBootstrap struct {
	Elev    core.Elevation
	apiGet  func(ctx context.Context, url string) ([]byte, error)                  // GitHub API JSON; injectable
	httpGet func(ctx context.Context, url string) (io.ReadCloser, int64, error)    // asset download; injectable (reuse httpGetDefault)
	psRun   func(ctx context.Context, name string, args ...string) ([]byte, error) // PS 5.1 runner; injectable (reuse realPSRunner)
	verFn   func(ctx context.Context) bool                                         // winget --version probe; injectable
}

const (
	wingetReleaseAPI = "https://api.github.com/repos/microsoft/winget-cli/releases/latest"
	wingetUserAgent  = "morgtweaker"
	maxDownloadTries = 3
)

func (a WingetBootstrap) Level() core.Elevation { return a.Elev }

// present reports whether winget is already installed, using the injected probe
// or the default `winget --version` exit-code check.
func (a WingetBootstrap) present(ctx context.Context) bool {
	if a.verFn != nil {
		return a.verFn(ctx)
	}
	code, err := execRunCode(ctx, "winget", "--version")
	return err == nil && code == 0
}

func (a WingetBootstrap) Probe(ctx core.ActionContext) (core.PointState, error) {
	c := ctx.Ctx
	if c == nil {
		c = context.Background()
	}
	if a.present(c) {
		return core.PointOn, nil
	}
	return core.PointOff, nil
}

func (a WingetBootstrap) Apply(ctx core.ActionContext, on bool) error {
	if !on {
		return nil // installing winget has no exact inverse — honest no-op
	}
	c := ctx.Ctx
	if c == nil {
		c = context.Background()
	}
	// 1. Idempotent: if winget already present, nothing to do.
	if a.present(c) {
		return nil
	}

	// 2. Resolve the latest release assets.
	apiGet := a.apiGet
	if apiGet == nil {
		apiGet = githubAPIGet
	}
	js, err := apiGet(c, wingetReleaseAPI)
	if err != nil {
		return fmt.Errorf("winget_bootstrap: release lookup failed: %w", err)
	}
	bundle, deps, _, err := selectAssets(js)
	if err != nil {
		return fmt.Errorf("winget_bootstrap: %w", err)
	}

	// 3. Download bundle + deps to temp files, verifying byte count vs asset size.
	bundlePath, err := a.download(ctx, c, bundle, "*.msixbundle")
	if err != nil {
		return err
	}
	defer os.Remove(bundlePath)
	depsPath, err := a.download(ctx, c, deps, "*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(depsPath)

	// 4. Authenticode-verify the bundle is Microsoft-signed (reuse DownloadInstall's
	//    fail-closed check via the injectable psRun seam).
	checker := DownloadInstall{Verify: VerifyAuthenticodeMicrosoft, psRun: a.psRun}
	if err := checker.verifyAuthenticodeMicrosoft(c, bundlePath); err != nil {
		return err
	}

	// 5. Extract the deps zip and pick the arch-matched dependency packages.
	depFiles, cleanup, err := extractDeps(depsPath)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return fmt.Errorf("winget_bootstrap: %w", err)
	}

	// 5b. SECURITY (fail-closed): Authenticode-verify EVERY dependency package too,
	//     not just the bundle. buildAppxScript installs the deps FIRST, so a tampered
	//     VCLibs/UI.Xaml would execute before the verified bundle — never hand an
	//     unverified package to Add-AppxPackage. Abort on the first dep that is not
	//     Valid + Microsoft-signed; the install script then never runs.
	for _, dep := range depFiles {
		if err := checker.verifyAuthenticodeMicrosoft(c, dep); err != nil {
			return err
		}
	}

	// 6. Install deps then the bundle via Windows PowerShell 5.1 Add-AppxPackage.
	ctx.Report(100, "installing", 0, 0)
	script := buildAppxScript(depFiles, bundlePath)
	psRun := a.psRun
	if psRun == nil {
		psRun = realPSRunner
	}
	if _, err := psRun(c, "powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePSCommand(script)); err != nil {
		return fmt.Errorf("winget_bootstrap: Add-AppxPackage failed: %w", err)
	}

	// 7. Re-check: winget must now be present.
	if !a.present(c) {
		return fmt.Errorf("winget_bootstrap: winget still absent after install")
	}
	return nil
}

// download fetches one asset to a temp file (named with the given pattern),
// streaming with progress and verifying the written byte count equals the
// asset's declared size. A short read / mismatch retries up to maxDownloadTries
// before returning an error (the ~200MB bundle is prone to transport truncation).
// On success it returns the temp file path (caller removes it).
func (a WingetBootstrap) download(actx core.ActionContext, c context.Context, asset assetInfo, pattern string) (string, error) {
	get := a.httpGet
	if get == nil {
		get = httpGetDefault
	}
	var lastErr error
	for try := 0; try < maxDownloadTries; try++ {
		path, n, err := a.downloadOnce(actx, c, get, asset.URL, pattern)
		if err != nil {
			lastErr = err
			if path != "" {
				os.Remove(path)
			}
			continue
		}
		if asset.Size > 0 && n != asset.Size {
			os.Remove(path)
			lastErr = fmt.Errorf("winget_bootstrap: %s size mismatch (got %d, want %d)", asset.URL, n, asset.Size)
			continue
		}
		return path, nil
	}
	return "", lastErr
}

// downloadOnce performs a single download attempt and returns the temp path and
// bytes written.
func (a WingetBootstrap) downloadOnce(actx core.ActionContext, c context.Context, get func(context.Context, string) (io.ReadCloser, int64, error), url, pattern string) (string, int64, error) {
	body, size, err := get(c, url)
	if err != nil {
		return "", 0, err
	}
	defer body.Close()
	tmp, err := os.CreateTemp("", "morgtweaker-"+strings.TrimPrefix(pattern, "*"))
	if err != nil {
		return "", 0, err
	}
	path := tmp.Name()
	cw := &countWriter{w: tmp}
	if err := streamWithProgress(actx, cw, body, size); err != nil {
		tmp.Close()
		return path, cw.n, err
	}
	if err := tmp.Close(); err != nil {
		return path, cw.n, err
	}
	return path, cw.n, nil
}

// countWriter counts bytes written through to the wrapped writer.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// assetInfo is the slice of a GitHub release asset we care about.
type assetInfo struct {
	Name string
	URL  string
	Size int64
}

// selectAssets parses a GitHub "latest release" JSON payload and returns the
// msixbundle, the dependencies zip, and the (optional) license xml. A missing
// bundle or deps zip is an error; a missing license is not (license is empty).
func selectAssets(jsonBytes []byte) (bundle, deps, license assetInfo, err error) {
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if jerr := json.Unmarshal(jsonBytes, &rel); jerr != nil {
		return assetInfo{}, assetInfo{}, assetInfo{}, fmt.Errorf("parse release json: %w", jerr)
	}
	for _, as := range rel.Assets {
		ai := assetInfo{Name: as.Name, URL: as.URL, Size: as.Size}
		switch {
		case strings.HasSuffix(as.Name, ".msixbundle"):
			bundle = ai
		case as.Name == "DesktopAppInstaller_Dependencies.zip":
			deps = ai
		case strings.Contains(as.Name, "License") && strings.HasSuffix(as.Name, ".xml"):
			license = ai
		}
	}
	if bundle.URL == "" {
		return bundle, deps, license, fmt.Errorf("no .msixbundle asset in release")
	}
	if deps.URL == "" {
		return bundle, deps, license, fmt.Errorf("no DesktopAppInstaller_Dependencies.zip asset in release")
	}
	return bundle, deps, license, nil
}

// extractDeps unzips the dependency package into a temp dir, picks the arch
// subdir matching runtime.GOARCH, and returns the *.appx (fallback *.msix)
// dependency files plus a cleanup func that removes the temp dir.
func extractDeps(zipPath string) (files []string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "morgtweaker-deps-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, cleanup, err
	}
	defer zr.Close()

	sub := archSubdir()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// only files under the matching arch subdir
		norm := strings.ReplaceAll(f.Name, "\\", "/")
		if !strings.Contains(norm, "/"+sub+"/") && !strings.HasPrefix(norm, sub+"/") {
			continue
		}
		lower := strings.ToLower(norm)
		if !strings.HasSuffix(lower, ".appx") && !strings.HasSuffix(lower, ".msix") {
			continue
		}
		out := filepath.Join(dir, filepath.Base(norm))
		if werr := extractOne(f, out); werr != nil {
			return nil, cleanup, werr
		}
		files = append(files, out)
	}
	// Prefer .appx; if both .appx and .msix were collected, keep .appx only.
	if appx := filterExt(files, ".appx"); len(appx) > 0 {
		files = appx
	}
	if len(files) == 0 {
		return nil, cleanup, fmt.Errorf("no .appx/.msix dependency for arch %q in zip", sub)
	}
	return files, cleanup, nil
}

func extractOne(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func filterExt(files []string, ext string) []string {
	var out []string
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f), ext) {
			out = append(out, f)
		}
	}
	return out
}

// archSubdir maps runtime.GOARCH to the dependency zip's arch subdir name.
func archSubdir() string {
	switch runtime.GOARCH {
	case "386":
		return "x86"
	case "arm64":
		return "arm64"
	default: // amd64 and anything else -> x64
		return "x64"
	}
}

// buildAppxScript builds the PowerShell that installs each dependency package
// first, then the bundle with all deps as its DependencyPath and
// -ForceUpdateFromAnyVersion (so an older provisioned copy does not block it).
func buildAppxScript(deps []string, bundle string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	for _, d := range deps {
		b.WriteString("Add-AppxPackage -Path " + psQuote(d) + "\n")
	}
	b.WriteString("Add-AppxPackage -Path " + psQuote(bundle))
	if len(deps) > 0 {
		quoted := make([]string, len(deps))
		for i, d := range deps {
			quoted[i] = psQuote(d)
		}
		b.WriteString(" -DependencyPath " + strings.Join(quoted, ","))
	}
	b.WriteString(" -ForceUpdateFromAnyVersion\n")
	return b.String()
}

// encodePSCommand encodes a PowerShell script as a base64 UTF-16LE string for
// powershell.exe -EncodedCommand (avoids all shell-quoting pitfalls of -Command).
func encodePSCommand(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// githubAPIGet performs the default GitHub API GET with the required User-Agent
// header (the API rejects requests without one), bound to ctx.
func githubAPIGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", wingetUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func (a WingetBootstrap) Snapshot(core.ActionContext) (core.Backup, error) {
	return core.Backup{Existed: false}, nil
}

func (a WingetBootstrap) Restore(core.ActionContext, core.Backup) error { return nil }

// SkipVerifyAfter is true: a bootstrap install is a one-shot whose success is
// decided by the post-install presence re-check inside Apply, not by re-probing.
func (a WingetBootstrap) SkipVerifyAfter() bool { return true }

var (
	_ core.Action                         = WingetBootstrap{}
	_ interface{ SkipVerifyAfter() bool } = WingetBootstrap{}
)
