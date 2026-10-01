package build

import (
	"reflect"
	"testing"

	llruntime "github.com/xgo-dev/llgo/runtime"
	extplan9asm "github.com/xgo-dev/plan9asm"
)

func TestPlan9AsmTranslateOptions(t *testing.T) {
	tests := []struct {
		name string
		conf Config
		want extplan9asm.X87Mode
	}{
		{name: "386 default", conf: Config{Goarch: "386"}, want: extplan9asm.X87Auto},
		{name: "386 sse2", conf: Config{Goarch: "386", GO386: "sse2"}, want: extplan9asm.X87Auto},
		{name: "386 softfloat", conf: Config{Goarch: "386", GO386: "softfloat"}, want: extplan9asm.X87Software},
		{name: "other architecture", conf: Config{Goarch: "amd64", GO386: "softfloat"}, want: extplan9asm.X87Auto},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := plan9asmTranslateOptions(&test.conf)
			if got.GOARM != test.conf.GOARM {
				t.Fatalf("GOARM = %q, want %q", got.GOARM, test.conf.GOARM)
			}
			if got.X87Mode != test.want {
				t.Fatalf("X87Mode = %v, want %v", got.X87Mode, test.want)
			}
		})
	}
}

func TestPlan9AsmTranslateOptionsBindResolvedSourceInputs(t *testing.T) {
	t.Setenv("GOAMD64", "v1")
	conf := &Config{Goos: "linux", Goarch: "amd64", GOAMD64: "v3", sourceGoRoot: "selected-source-toolchain"}
	got := plan9asmTranslateOptions(conf)
	if got.SourceGOROOT != conf.sourceGoRoot || !reflect.DeepEqual(got.AssemblyDefines, []string{"GOOS_linux", "GOARCH_amd64", "GOAMD64_v3"}) {
		t.Fatalf("assembly selected inputs use process/build toolchain instead of resolved source: %+v", got)
	}
}

func TestInternalRuntimeSysUsesPlan9AsmWithoutAltPkg(t *testing.T) {
	conf := &Config{Goarch: "arm64"}
	if !plan9asmEnabledByDefault(conf, "internal/runtime/sys") {
		t.Fatal("plan9asm should be enabled by default for internal/runtime/sys on arm64")
	}
	if hasAltPkgForTarget(conf, "internal/runtime/sys") {
		t.Fatal("internal/runtime/sys should use its source patch instead of an alt package")
	}
}

func TestPlan9AsmDefaultsSupport386(t *testing.T) {
	conf := &Config{Goarch: "386"}
	for _, pkgPath := range []string{
		"internal/bytealg",
		"internal/chacha8rand",
		"internal/cpu",
		"internal/runtime/atomic",
		"internal/runtime/syscall/windows",
		"math",
	} {
		if !plan9asmEnabledByDefault(conf, pkgPath) {
			t.Errorf("plan9asm should be enabled by default for %s on 386", pkgPath)
		}
		if llruntime.SourcePatchReplacesAsmForGOARCH(pkgPath, "386") {
			t.Errorf("%s should retain the Go 386 assembly implementation", pkgPath)
		}
	}
	if !hasAltPkgForTarget(conf, "runtime") {
		t.Fatal("runtime should keep using the LLGo alternate package")
	}
}

func TestPlan9AsmDefaultsSupportWasm(t *testing.T) {
	conf := &Config{Goarch: "wasm"}
	for _, pkgPath := range []string{
		"crypto/internal/boring/sig",
		"internal/bytealg",
		"internal/chacha8rand",
		"internal/runtime/atomic",
		"math",
	} {
		if !plan9asmEnabledByDefault(conf, pkgPath) {
			t.Errorf("plan9asm should be enabled by default for %s on wasm", pkgPath)
		}
		if llruntime.SourcePatchReplacesAsmForGOARCH(pkgPath, "wasm") {
			t.Errorf("%s should retain the Go wasm assembly implementation", pkgPath)
		}
	}
}

func TestInternalRuntimeAtomicUsesSourcePatchOnArm(t *testing.T) {
	conf := &Config{Goarch: "arm"}
	if hasAltPkgForTarget(conf, "internal/runtime/atomic") {
		t.Fatal("internal/runtime/atomic should use its source patch on arm")
	}

	conf = &Config{Goarch: "arm64"}
	if hasAltPkgForTarget(conf, "internal/runtime/atomic") {
		t.Fatal("internal/runtime/atomic should keep plan9asm/std paths on arm64")
	}
}
