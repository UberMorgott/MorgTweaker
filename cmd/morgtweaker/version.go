package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// versioninfoJSON is the single source of truth for the product version. The same
// file is consumed by goversioninfo (//go:generate) to stamp the Windows EXE
// resource, so embedding+parsing it here keeps `go run` and the built exe in sync:
// bumping ProductVersion in versioninfo.json auto-updates the in-app title.
//
//go:embed versioninfo.json
var versioninfoJSON []byte

// Version is the product version shown in the UI title (e.g. "1.0.0"). It is
// resolved at startup from the embedded versioninfo.json; falls back to "dev" if
// the file is missing or unparsable.
var Version = resolveVersion()

func resolveVersion() string {
	const fallback = "dev"

	var info struct {
		StringFileInfo struct {
			ProductVersion string `json:"ProductVersion"`
		} `json:"StringFileInfo"`
	}
	if err := json.Unmarshal(versioninfoJSON, &info); err != nil {
		return fallback
	}

	v := strings.TrimSpace(info.StringFileInfo.ProductVersion)
	if v == "" {
		return fallback
	}
	// versioninfo carries a 4-part version (e.g. "1.0.0.0"); trim a trailing
	// ".0" build segment so the title reads "1.0.0" rather than "1.0.0.0".
	if parts := strings.Split(v, "."); len(parts) == 4 && parts[3] == "0" {
		v = strings.Join(parts[:3], ".")
	}
	return v
}
