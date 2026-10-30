package crosscompile

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// Share runtime release provenance and Windows payload pins with release tests.
// The x86 compiler's development SDK remains independently pinned in this file.
//
//go:embed espressif-windows.json
var espWindowsJSON []byte

type espWindowsToolchainPins struct {
	Repository string `json:"repository"`
	Release    string `json:"release"`
	Payloads   map[string]struct {
		Asset  string `json:"asset"`
		SHA256 string `json:"sha256"`
	} `json:"payloads"`
}

var espWindowsPins = mustParseESPWindowsPins(espWindowsJSON)

func mustParseESPWindowsPins(data []byte) (pins espWindowsToolchainPins) {
	if err := json.Unmarshal(data, &pins); err != nil {
		panic(err)
	}
	return
}

func espClangRelease(platform string) string {
	if strings.HasSuffix(platform, "-w64-mingw32") {
		return espWindowsPins.Release
	}
	return espClangVersion
}

func espClangChecksums() map[string]string {
	checksums := map[string]string{
		"aarch64-apple-darwin": "571215bfff95320877c7cbaebe2ae835b3cbe973662a4fff365dfda0e883a157",
		"aarch64-linux-gnu":    "e146d8917e10cdc2513649b793594c03bf0c7081f20b1ff524053e12f222b3d0",
		"x86_64-apple-darwin":  "2adbba1b0fa75ecde7f51a0f32edcb1c010ebc98d975b47277c540552ebf3580",
		"x86_64-linux-gnu":     "255ae7c57da1d8589539ca5e28215db27518c43ceff0427db7d1127557effddb",
	}
	for arch, pin := range espWindowsPins.Payloads {
		checksums[getESPClangPlatform("windows", arch)] = pin.SHA256
	}
	return checksums
}
