// Command MorgTweaker is a portable Windows tweaker TUI. It self-elevates via the
// embedded manifest (requireAdministrator); on launch it enables SeDebugPrivilege
// (best-effort), builds the tweak catalog, and runs the Bubble Tea UI.
package main

import (
	"fmt"
	"os"

	"morgtweaker/internal/backup"
	"morgtweaker/internal/catalog"
	"morgtweaker/internal/elevate"
	"morgtweaker/internal/engine"
	"morgtweaker/internal/ui"
)

//go:generate goversioninfo -platform-specific

func main() {
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

	if err := ui.Run(cat, eng); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
