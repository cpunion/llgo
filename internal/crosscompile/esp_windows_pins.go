package crosscompile

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// Share the Windows payload pins with the release compiler's x86 SDK recipe.
//
//go:embed espressif-windows.json
var espWindowsJSON []byte

var espWindowsPins = func() (pins struct {
	Release  string `json:"release"`
	Payloads map[string]struct {
		Asset  string `json:"asset"`
		SHA256 string `json:"sha256"`
	} `json:"payloads"`
}) {
	if err := json.Unmarshal(espWindowsJSON, &pins); err != nil {
		panic(err)
	}
	return
}()

func espClangRelease(platform string) string {
	if strings.HasSuffix(platform, "-w64-mingw32") {
		return espWindowsPins.Release
	}
	return espClangVersion
}

func espClangChecksums() map[string]string {
	checksums := map[string]string{
		"aarch64-apple-darwin": "31cb1b87c84531bd777a4d7493a5a35070e6dd860e4c600b63937f5b449c3064",
		"aarch64-linux-gnu":    "4a88902abe7977c0cbdecb239ac1a4d24185557e451a3e1f59aebbd56371124d",
		"x86_64-apple-darwin":  "c31d127230d87bc468e6ea5e709db20d0729ecf6407222ef33f2378e2f847416",
		"x86_64-linux-gnu":     "735f9d343863e5693179908acd946a0e85468463246f11210f2aa690b47784ab",
	}
	for arch, pin := range espWindowsPins.Payloads {
		checksums[getESPClangPlatform("windows", arch)] = pin.SHA256
	}
	return checksums
}
