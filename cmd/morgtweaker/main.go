// Command MorgTweaker is a portable Windows tweaker TUI. The packaged exe
// self-elevates via the embedded manifest (requireAdministrator); for the
// manifest-less `go run` path it self-elevates at runtime (ShellExecute "runas").
// On launch it enables SeDebugPrivilege (best-effort), builds the tweak catalog,
// and runs the Bubble Tea UI.
package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"

	"morgtweaker/internal/backup"
	"morgtweaker/internal/catalog"
	"morgtweaker/internal/elevate"
	"morgtweaker/internal/engine"
	"morgtweaker/internal/ui"
)

//go:generate goversioninfo -platform-specific

func main() {
	// Universal admin guard: the manifest elevates the packaged exe, but a
	// manifest-less `go run` starts unelevated. Every tweak needs admin, so refuse
	// to proceed unelevated — re-launch elevated via ShellExecute "runas" (a clean
	// UAC prompt) and exit this instance. If re-launch is impractical, block with a
	// clear message rather than silently proceeding to fail later.
	if !elevate.IsAdmin() {
		if err := relaunchElevated(); err != nil {
			fmt.Fprintln(os.Stderr, "MorgTweaker must be run as administrator.")
			fmt.Fprintf(os.Stderr, "could not self-elevate (%v) — right-click and 'Run as administrator'.\n", err)
			os.Exit(1)
		}
		os.Exit(0) // elevated instance launched; this unelevated one is done
	}

	// Best-effort: enables opening privileged processes for SYSTEM/TI impersonation
	// used by future tweaks. A failure here is non-fatal — most tweaks don't need it.
	if err := elevate.EnableSeDebugPrivilege(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not enable SeDebugPrivilege: %v\n", err)
	}

	store, err := backup.New()
	if err != nil {
		// Backup disabled (couldn't locate the EXE dir) — the UI still applies tweaks,
		// but rollback won't be available. Surface it, don't abort.
		fmt.Fprintf(os.Stderr, "warning: backup disabled: %v\n", err)
		store = nil
	}

	eng := engine.New(store)
	cat := catalog.Build()

	if err := ui.Run(cat, eng, Version); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// relaunchElevated re-launches this executable with a UAC elevation prompt via
// ShellExecute("runas"), forwarding the original arguments. It returns an error
// if the exe path cannot be resolved or ShellExecute fails (e.g. the user
// declines the UAC prompt), so the caller can fall back to a blocking message.
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	exePtr, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	// Forward the original args (quoted) so a `go run`-built temp exe relaunch keeps
	// any flags the user passed.
	var argsPtr *uint16
	if args := strings.Join(quoteArgs(os.Args[1:]), " "); args != "" {
		if argsPtr, err = windows.UTF16PtrFromString(args); err != nil {
			return err
		}
	}
	return windows.ShellExecute(0, verb, exePtr, argsPtr, nil, windows.SW_NORMAL)
}

// quoteArgs quotes each argument for a Windows command line so it round-trips
// through CommandLineToArgvW (which ShellExecute's parameter string is parsed by).
func quoteArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = quoteArg(a)
	}
	return out
}

// quoteArg implements the standard Windows argv-quoting algorithm (Microsoft's
// ArgvQuote / the inverse of CommandLineToArgvW). An arg with no spaces, tabs or
// quotes is emitted verbatim; otherwise it is wrapped in double quotes with these
// backslash rules: a run of N backslashes immediately before a literal " becomes
// 2N+1 backslashes (so the quote is escaped, not the count); a run of N
// backslashes at the very end (before the closing quote) becomes 2N (so a path
// ending in `\` does not escape the closing quote and merge with the next arg);
// backslashes elsewhere are untouched.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\v\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			backslashes++
		case '"':
			// Escape all pending backslashes (they precede a quote) then the quote.
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			b.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
			b.WriteByte(s[i])
		}
	}
	// Double the trailing backslash run so it does not escape the closing quote.
	b.WriteString(strings.Repeat(`\`, backslashes*2))
	b.WriteByte('"')
	return b.String()
}
