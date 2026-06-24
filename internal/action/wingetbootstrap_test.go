package action

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"morgtweaker/internal/core"
)

// archSubdirForTest mirrors the impl's GOARCH -> deps-zip subdir mapping so the
// test builds a zip whose layout the extractor will accept on this host.
func archSubdirForTest() string {
	switch runtime.GOARCH {
	case "386":
		return "x86"
	case "arm64":
		return "arm64"
	default:
		return "x64"
	}
}

// zeroReader yields an endless stream of zero bytes (paired with io.LimitReader
// to fake a large download body without allocating it).
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// makeDepsZip returns the bytes of a deps zip carrying one dependency .appx
// under the arch subdir, so extractDeps yields exactly one dep file.
func makeDepsZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(archSubdirForTest() + "/Microsoft.VCLibs.140.00_x64.appx")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte("fake-appx-bytes")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// sampleReleaseJSON is a trimmed GitHub "latest release" payload carrying the
// three assets the bootstrap selects from.
const sampleReleaseJSON = `{
  "tag_name": "v1.7.0",
  "assets": [
    {"name": "Microsoft.DesktopAppInstaller_8wekyb3d8bbwe.msixbundle", "browser_download_url": "https://example.test/bundle.msixbundle", "size": 250000000},
    {"name": "DesktopAppInstaller_Dependencies.zip", "browser_download_url": "https://example.test/deps.zip", "size": 4000000},
    {"name": "e53e159d00e04f729cc2180cffd1c02e_License1.xml", "browser_download_url": "https://example.test/license.xml", "size": 1234},
    {"name": "SHA256SUMS", "browser_download_url": "https://example.test/sums", "size": 99}
  ]
}`

func TestSelectAssets(t *testing.T) {
	bundle, deps, license, err := selectAssets([]byte(sampleReleaseJSON))
	if err != nil {
		t.Fatalf("selectAssets err = %v", err)
	}
	if bundle.URL != "https://example.test/bundle.msixbundle" || bundle.Size != 250000000 {
		t.Errorf("bundle = %+v", bundle)
	}
	if deps.URL != "https://example.test/deps.zip" || deps.Size != 4000000 {
		t.Errorf("deps = %+v", deps)
	}
	if license.URL != "https://example.test/license.xml" {
		t.Errorf("license = %+v", license)
	}
}

func TestSelectAssetsMissingBundle(t *testing.T) {
	const noBundle = `{"assets":[{"name":"DesktopAppInstaller_Dependencies.zip","browser_download_url":"u","size":1}]}`
	if _, _, _, err := selectAssets([]byte(noBundle)); err == nil {
		t.Fatal("selectAssets: expected error when no msixbundle present")
	}
}

func TestApplyAlreadyPresentNoOp(t *testing.T) {
	apiCalled, httpCalled := false, false
	b := WingetBootstrap{
		verFn:   func(context.Context) bool { return true },
		apiGet:  func(context.Context, string) ([]byte, error) { apiCalled = true; return nil, nil },
		httpGet: func(context.Context, string) (io.ReadCloser, int64, error) { httpCalled = true; return nil, 0, nil },
	}
	if err := b.Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	if apiCalled || httpCalled {
		t.Errorf("seams called although winget already present (api=%v http=%v)", apiCalled, httpCalled)
	}
}

func TestApplyOffNoOp(t *testing.T) {
	called := false
	b := WingetBootstrap{
		verFn:  func(context.Context) bool { called = true; return false },
		apiGet: func(context.Context, string) ([]byte, error) { called = true; return nil, nil },
	}
	if err := b.Apply(core.ActionContext{Ctx: context.Background()}, false); err != nil {
		t.Fatalf("Apply(off) err = %v", err)
	}
	if called {
		t.Error("Apply(off) touched a seam")
	}
}

func TestApplySizeMismatchRetriesThenError(t *testing.T) {
	apiCalled := false
	httpCalls := 0
	b := WingetBootstrap{
		verFn:  func(context.Context) bool { return false },
		apiGet: func(context.Context, string) ([]byte, error) { apiCalled = true; return []byte(sampleReleaseJSON), nil },
		httpGet: func(_ context.Context, _ string) (io.ReadCloser, int64, error) {
			httpCalls++
			// declare a large size but return a tiny body -> short read mismatch.
			return io.NopCloser(strings.NewReader("short")), 250000000, nil
		},
	}
	err := b.Apply(core.ActionContext{Ctx: context.Background()}, true)
	if err == nil {
		t.Fatal("Apply: expected error on persistent size mismatch")
	}
	if !apiCalled {
		t.Error("apiGet not called")
	}
	if httpCalls < 3 {
		t.Errorf("httpGet called %d times, want >=3 (retries)", httpCalls)
	}
}

func TestBuildAppxScript(t *testing.T) {
	deps := []string{`C:\tmp\a.appx`, `C:\tmp\b.appx`}
	bundle := `C:\tmp\winget.msixbundle`
	s := buildAppxScript(deps, bundle)
	for _, want := range []string{"Add-AppxPackage", deps[0], deps[1], bundle, "-ForceUpdateFromAnyVersion"} {
		if !strings.Contains(s, want) {
			t.Errorf("buildAppxScript output missing %q\n--- script ---\n%s", want, s)
		}
	}
}

func TestEncodePSCommand(t *testing.T) {
	const script = "Add-AppxPackage -Path 'x'"
	enc := encodePSCommand(script)
	if enc == "" {
		t.Fatal("encodePSCommand returned empty")
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	// decode UTF-16LE back to the original string.
	if len(raw)%2 != 0 {
		t.Fatalf("decoded bytes not UTF-16 aligned: %d", len(raw))
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(u16)); got != script {
		t.Errorf("round-trip = %q want %q", got, script)
	}
}

// TestApplyUnsignedDepAborts asserts the fix for the HIGH security gap: every
// extracted dependency package is Authenticode-verified (Valid + Microsoft) BEFORE
// the Add-AppxPackage script runs. Here the bundle verifies clean but the dep
// reports a non-Microsoft signer, so Apply must error AND never run the install.
func TestApplyUnsignedDepAborts(t *testing.T) {
	depsZip := makeDepsZip(t)
	installRan := false

	// release JSON whose deps size matches the real zip bytes (so the byte-count
	// verify passes) and a small bundle size we can stream cheaply.
	const bundleSize = 4096
	relJSON := `{"assets":[` +
		`{"name":"x.msixbundle","browser_download_url":"https://example.test/bundle.msixbundle","size":` + strconv.Itoa(bundleSize) + `},` +
		`{"name":"DesktopAppInstaller_Dependencies.zip","browser_download_url":"https://example.test/deps.zip","size":` + strconv.Itoa(len(depsZip)) + `}]}`

	b := WingetBootstrap{
		verFn:  func(context.Context) bool { return false },
		apiGet: func(context.Context, string) ([]byte, error) { return []byte(relJSON), nil },
		httpGet: func(_ context.Context, url string) (io.ReadCloser, int64, error) {
			if strings.HasSuffix(url, ".zip") {
				return io.NopCloser(bytes.NewReader(depsZip)), int64(len(depsZip)), nil
			}
			// bundle: a zero reader sized to match the declared asset size.
			return io.NopCloser(io.LimitReader(zeroReader{}, bundleSize)), bundleSize, nil
		},
		psRun: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "-EncodedCommand") {
				installRan = true
				return nil, nil
			}
			// Authenticode check: Valid+Microsoft for the bundle, bad signer for the dep.
			if strings.Contains(joined, ".appx") {
				return []byte(`{"Status":"Valid","Subject":"O=Evil Corp, C=US"}`), nil
			}
			return []byte(`{"Status":"Valid","Subject":"O=Microsoft Corporation, C=US"}`), nil
		},
	}
	err := b.Apply(core.ActionContext{Ctx: context.Background()}, true)
	if err == nil {
		t.Fatal("Apply: expected error for non-Microsoft-signed dependency")
	}
	if installRan {
		t.Error("Add-AppxPackage install ran despite an unverified dependency")
	}
}

var _ core.Action = WingetBootstrap{}
