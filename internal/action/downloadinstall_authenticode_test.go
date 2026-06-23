package action

import (
	"context"
	"strings"
	"testing"

	"morgtweaker/internal/core"
)

// msSubject is a realistic Microsoft signer subject as emitted by
// SignerCertificate.Subject.
const msSubject = `CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US`

// authJSON builds the compact JSON the Authenticode PowerShell command emits.
func authJSON(status, subject string) []byte {
	return []byte(`{"Status":"` + status + `","Subject":"` + subject + `"}`)
}

// TestAuthenticodeValidMicrosoftRuns: a Valid signature by Microsoft Corporation
// passes verification, so the installer IS run (exit 0 → success).
func TestAuthenticodeValidMicrosoftRuns(t *testing.T) {
	srv := serveBytes(t, payload)

	var psCmd string
	ranCount := 0
	a := DownloadInstall{
		URL:        srv.URL,
		Verify:     VerifyAuthenticodeMicrosoft,
		Args:       []string{"/install", "/quiet", "/norestart"},
		AcceptExit: []int{0, 3010, 1638, 1641},
		Elev:       core.ElevAdmin,
		psRun: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			psCmd = strings.Join(args, " ")
			return authJSON("Valid", msSubject), nil
		},
		runCode: func(_ context.Context, _ string, _ ...string) (int, error) {
			ranCount++
			return 0, nil
		},
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Fatalf("valid Microsoft signature should pass, got %v", err)
	}
	if ranCount != 1 {
		t.Fatalf("installer runner called %d times, want 1", ranCount)
	}
	// Command construction: must call Get-AuthenticodeSignature on the temp file.
	if !strings.Contains(psCmd, "Get-AuthenticodeSignature") {
		t.Errorf("Authenticode command did not call Get-AuthenticodeSignature: %q", psCmd)
	}
	if !strings.Contains(psCmd, "SignerCertificate.Subject") {
		t.Errorf("Authenticode command did not read SignerCertificate.Subject: %q", psCmd)
	}
}

// TestAuthenticodeInvalidFailsClosed: a non-Valid status (e.g. NotSigned /
// HashMismatch) must FAIL CLOSED — the installer is NOT run.
func TestAuthenticodeInvalidFailsClosed(t *testing.T) {
	srv := serveBytes(t, payload)

	for _, status := range []string{"NotSigned", "HashMismatch", "UnknownError", ""} {
		t.Run("status="+status, func(t *testing.T) {
			a := DownloadInstall{
				URL:    srv.URL,
				Verify: VerifyAuthenticodeMicrosoft,
				Elev:   core.ElevAdmin,
				psRun: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
					return authJSON(status, msSubject), nil
				},
				runCode: func(context.Context, string, ...string) (int, error) {
					t.Fatal("SECURITY: installer must NOT run on a non-Valid signature")
					return 0, nil
				},
			}
			if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err == nil {
				t.Errorf("status %q should fail verification", status)
			}
		})
	}
}

// TestAuthenticodeNonMicrosoftSignerFailsClosed: a Valid signature whose signer
// is NOT Microsoft Corporation must FAIL CLOSED — the installer is NOT run.
func TestAuthenticodeNonMicrosoftSignerFailsClosed(t *testing.T) {
	srv := serveBytes(t, payload)

	a := DownloadInstall{
		URL:    srv.URL,
		Verify: VerifyAuthenticodeMicrosoft,
		Elev:   core.ElevAdmin,
		psRun: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return authJSON("Valid", `CN=Evil Corp, O=Evil Corp, C=US`), nil
		},
		runCode: func(context.Context, string, ...string) (int, error) {
			t.Fatal("SECURITY: installer must NOT run when signer is not Microsoft")
			return 0, nil
		},
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err == nil {
		t.Error("a non-Microsoft Valid signature must fail closed")
	}
}

// TestAuthenticodeRunnerErrorFailsClosed: if Get-AuthenticodeSignature fails to
// run, verification fails closed — the installer is NOT run.
func TestAuthenticodeRunnerErrorFailsClosed(t *testing.T) {
	srv := serveBytes(t, payload)

	a := DownloadInstall{
		URL:    srv.URL,
		Verify: VerifyAuthenticodeMicrosoft,
		Elev:   core.ElevAdmin,
		psRun: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
		runCode: func(context.Context, string, ...string) (int, error) {
			t.Fatal("SECURITY: installer must NOT run when the signature check errors")
			return 0, nil
		},
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err == nil {
		t.Error("a failing Authenticode runner must fail closed")
	}
}

// TestAuthenticodeSkipsSHA256Pin: Authenticode mode must NOT require a SHA256 pin
// — an empty SHA256 is fine because the signature is the trust anchor.
func TestAuthenticodeSkipsSHA256Pin(t *testing.T) {
	srv := serveBytes(t, payload)

	a := DownloadInstall{
		URL:    srv.URL,
		Verify: VerifyAuthenticodeMicrosoft,
		// SHA256 intentionally left empty.
		Elev: core.ElevAdmin,
		psRun: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return authJSON("Valid", msSubject), nil
		},
		runCode: func(context.Context, string, ...string) (int, error) { return 0, nil },
	}
	if err := a.Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Errorf("Authenticode mode should not require a SHA256 pin, got %v", err)
	}
}

// TestExitCodeMapping: with the VC++ accepted set, exit codes {0,3010,1638,1641}
// map to success and any other code (1, 2, 5) maps to failure. The verify step is
// satisfied by a stub Valid-Microsoft signature so the test isolates exit mapping.
func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		code int
		ok   bool
	}{
		{0, true}, {3010, true}, {1638, true}, {1641, true},
		{1, false}, {2, false}, {5, false}, {-1, false},
	}
	for _, c := range cases {
		t.Run(itoa(codeForName(c.code)), func(t *testing.T) {
			srv := serveBytes(t, payload)
			a := DownloadInstall{
				URL:        srv.URL,
				Verify:     VerifyAuthenticodeMicrosoft,
				AcceptExit: []int{0, 3010, 1638, 1641},
				Elev:       core.ElevAdmin,
				psRun: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
					return authJSON("Valid", msSubject), nil
				},
				runCode: func(context.Context, string, ...string) (int, error) {
					return c.code, nil
				},
			}
			err := a.Apply(core.ActionContext{Ctx: context.Background()}, true)
			if c.ok && err != nil {
				t.Errorf("exit code %d should be success, got %v", c.code, err)
			}
			if !c.ok && err == nil {
				t.Errorf("exit code %d should be failure, got nil error", c.code)
			}
		})
	}
}

// codeForName turns a possibly-negative exit code into a non-negative key so
// itoa (which only handles non-negatives) can name the subtest.
func codeForName(code int) int {
	if code < 0 {
		return 9999
	}
	return code
}

// TestExitCodeDefaultAcceptsOnlyZero: with no AcceptExit set, only 0 is success
// (3010 is a failure under the default policy).
func TestExitCodeDefaultAcceptsOnlyZero(t *testing.T) {
	srv := serveBytes(t, payload)
	mk := func(code int) DownloadInstall {
		return DownloadInstall{
			URL:    srv.URL,
			Verify: VerifyAuthenticodeMicrosoft,
			Elev:   core.ElevAdmin,
			psRun: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				return authJSON("Valid", msSubject), nil
			},
			runCode: func(context.Context, string, ...string) (int, error) { return code, nil },
		}
	}
	if err := mk(0).Apply(core.ActionContext{Ctx: context.Background()}, true); err != nil {
		t.Errorf("default policy: exit 0 should succeed, got %v", err)
	}
	if err := mk(3010).Apply(core.ActionContext{Ctx: context.Background()}, true); err == nil {
		t.Error("default policy: exit 3010 should fail (not in default accepted set)")
	}
}

// TestParseAuthenticode covers the JSON parse + fail-closed empties directly.
func TestParseAuthenticode(t *testing.T) {
	status, subject := parseAuthenticode(authJSON("Valid", msSubject))
	if status != "Valid" || !strings.Contains(subject, "O=Microsoft Corporation") {
		t.Errorf("parse = (%q,%q), want Valid + Microsoft subject", status, subject)
	}
	if s, sub := parseAuthenticode(nil); s != "" || sub != "" {
		t.Errorf("empty input should parse to empties, got (%q,%q)", s, sub)
	}
	if s, sub := parseAuthenticode([]byte("not json")); s != "" || sub != "" {
		t.Errorf("garbage input should parse to empties (fail closed), got (%q,%q)", s, sub)
	}
}
