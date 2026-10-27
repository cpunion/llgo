//go:build !llgo

package build

import (
	"runtime"
	"testing"

	"github.com/xgo-dev/llgo/internal/optlevel"
)

func TestPublicRuntimeLTOSelectionAndOptimization(t *testing.T) {
	t.Setenv("LLGO_AR", "")
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native Darwin arm64 library profile")
	}
	base := Config{Mode: ModeBuild, BuildMode: BuildModeExe, Goos: "darwin", Goarch: "arm64", OptLevel: optlevel.Os}
	for _, test := range []struct {
		pkg     string
		bitcode bool
		level   optlevel.Level
	}{
		{"runtime", true, optlevel.Os},
		{"github.com/xgo-dev/llgo/runtime/internal/coro", true, optlevel.O2},
		{"github.com/xgo-dev/llgo/runtime/internal/runtime", true, optlevel.O2},
		{"github.com/xgo-dev/llgo/runtime/internal/coroalloc", true, optlevel.O2},
		{"os", false, optlevel.Os},
		{"internal/poll", false, optlevel.Os},
		{"net", false, optlevel.Os},
		{"example.com/runtime", false, optlevel.Os},
	} {
		t.Run(test.pkg, func(t *testing.T) {
			if got := runtimeLibraryLTOPackage(&base, test.pkg); got != test.bitcode {
				t.Fatalf("bitcode=%t want %t", got, test.bitcode)
			}
			if got := runtimeLibraryOptLevel(&base, test.pkg); got != test.level {
				t.Fatalf("optimization=%v want %v", got, test.level)
			}
		})
	}
	for name, change := range map[string]func(*Config){
		"named-target":    func(c *Config) { c.Target = "aarch64-apple-darwin" },
		"custom-linker":   func(c *Config) { c.LinkOptions.ExternalLinker = "clang" },
		"debug":           func(c *Config) { c.LinkOptions.DWARF = DWARFPreserve },
		"cross-arch":      func(c *Config) { c.Goarch = "amd64" },
		"generation":      func(c *Config) { c.Mode = ModeGen },
		"package-compile": func(c *Config) { c.PackageCompileOnly = true },
		"llvm-text":       func(c *Config) { c.GenLL = true },
	} {
		t.Run(name, func(t *testing.T) {
			conf := base
			change(&conf)
			for _, pkg := range []string{"runtime", "github.com/xgo-dev/llgo/runtime/internal/runtime"} {
				if runtimeLibraryLTOPackage(&conf, pkg) || runtimeLibraryOptLevel(&conf, pkg) != optlevel.Os {
					t.Fatalf("fallback changed for %s", pkg)
				}
			}
		})
	}
	t.Setenv("LLGO_AR", "/usr/bin/ar")
	if runtimeLibraryLTOPackage(&base, "runtime") || runtimeLibraryOptLevel(&base, "runtime") != optlevel.Os {
		t.Fatal("custom archiver changed public runtime policy")
	}
}

func TestNativePublicRuntimeRawBitcodeArchiveRetainsSymbols(t *testing.T) {
	testRuntimeLibraryArchiveConsumer(t, true, "", "runtime")
}

func TestNativePublicRuntimeCustomArchiverKeepsNativeObjects(t *testing.T) {
	testRuntimeLibraryArchiveConsumer(t, false, "/usr/bin/ar", "runtime")
}
