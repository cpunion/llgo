//go:build !llgo

package build

import (
	"runtime"
	"testing"

	"github.com/xgo-dev/llgo/internal/optlevel"
)

func TestFileWrapperLTOSelectionAndOptimization(t *testing.T) {
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
		{"os", true, optlevel.Os},
		{"internal/poll", true, optlevel.Os},
		{"github.com/xgo-dev/llgo/runtime/internal/coro", true, optlevel.O2},
		{"github.com/xgo-dev/llgo/runtime/internal/runtime", true, optlevel.O2},
		{"github.com/xgo-dev/llgo/runtime/internal/coroalloc", true, optlevel.O2},
		{"io", false, optlevel.Os},
		{"net", false, optlevel.Os},
		{"syscall", false, optlevel.Os},
		{"example.com/app", false, optlevel.Os},
		{"example.com/os", false, optlevel.Os},
	} {
		t.Run(test.pkg, func(t *testing.T) {
			if got := runtimeLibraryLTOPackage(&base, test.pkg); got != test.bitcode {
				t.Fatalf("bitcode=%t want%t", got, test.bitcode)
			}
			if got := runtimeLibraryOptLevel(&base, test.pkg); got != test.level {
				t.Fatalf("optimization=%v want%v", got, test.level)
			}
			named := base
			named.Target = "aarch64-apple-darwin"
			if runtimeLibraryLTOPackage(&named, test.pkg) || runtimeLibraryOptLevel(&named, test.pkg) != optlevel.Os {
				t.Fatal("named target changed policy")
			}
			external := base
			external.LinkOptions.ExternalLinker = "clang"
			if runtimeLibraryLTOPackage(&external, test.pkg) || runtimeLibraryOptLevel(&external, test.pkg) != optlevel.Os {
				t.Fatal("custom linker changed policy")
			}
		})
	}
	t.Setenv("LLGO_AR", "/usr/bin/ar")
	for _, pkg := range []string{"os", "internal/poll"} {
		if runtimeLibraryLTOPackage(&base, pkg) {
			t.Fatal("custom archiver selected bitcode")
		}
	}
}

func TestNativeFileWrapperRawBitcodeArchiveRetainsSymbols(t *testing.T) {
	for _, pkg := range []string{"os", "internal/poll"} {
		t.Run(pkg, func(t *testing.T) { testRuntimeLibraryArchiveConsumer(t, true, "", pkg) })
	}
}
