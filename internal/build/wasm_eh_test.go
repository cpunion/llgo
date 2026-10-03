package build

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/crosscompile"
)

func TestEmscriptenEHFunctionFeature(t *testing.T) {
	for _, profile := range []crosscompile.WasmProfile{
		crosscompile.WasmProfileJ32, crosscompile.WasmProfileJ64,
		crosscompile.WasmProfileW32, crosscompile.WasmProfileNone,
	} {
		t.Run(string(profile), func(t *testing.T) {
			mod := parseWasmAggregateIR(t, `
declare void @external()
define void @plain() { ret void }
define void @vector() "target-features"="+simd128,-atomics,-exception-handling" { ret void }
`)
			ctx := &context{crossCompile: crosscompile.Export{WasmProfile: profile}}
			before := mod.String()
			applyEmscriptenEHFeature(ctx, mod)
			if profile == crosscompile.WasmProfileW32 || profile == crosscompile.WasmProfileNone {
				if mod.String() != before {
					t.Fatal("changed a non-Emscripten module")
				}
				return
			}
			for _, name := range []string{"plain", "vector"} {
				found := false
				for _, attr := range mod.NamedFunction(name).GetFunctionAttributes() {
					if attr.IsString() && attr.GetStringKind() == "target-features" {
						found = strings.Contains(attr.GetStringValue(), "+exception-handling")
					}
				}
				if !found {
					t.Fatalf("%s lacks the native EH capability", name)
				}
			}
			if !strings.Contains(mod.String(), "+simd128,-atomics") || strings.Contains(mod.String(), "-exception-handling") {
				t.Fatal("changed unrelated features or retained conflicting EH feature")
			}
			if len(mod.NamedFunction("external").GetFunctionAttributes()) != 0 {
				t.Fatal("changed an external declaration")
			}
			first := mod.String()
			applyEmscriptenEHFeature(ctx, mod)
			if mod.String() != first {
				t.Fatal("EH feature application is not idempotent")
			}
		})
	}
}

func TestEmscriptenFlagsSeparatePackageFingerprints(t *testing.T) {
	for _, target := range []struct{ goos, goarch string }{
		{"js", "wasm"}, {"wasip1", "wasm"}, {"linux", "amd64"},
	} {
		t.Run(target.goos, func(t *testing.T) {
			ctx := &context{buildConf: &Config{Goos: target.goos, Goarch: target.goarch}, llvmVersion: "test"}
			fingerprint := func(flags string) string {
				t.Setenv("EMCC_CFLAGS", flags)
				manifest := newManifestBuilder()
				ctx.collectEnvInputs(manifest)
				return manifest.Fingerprint()
			}
			plain := fingerprint("")
			nativeEH := fingerprint("-fwasm-exceptions -sSUPPORT_LONGJMP=wasm")
			if (plain != nativeEH) != (target.goos == "js") {
				t.Fatal("EMCC_CFLAGS cache isolation does not match the selected compiler")
			}
			if fingerprint("") != plain {
				t.Fatal("restoring default EH did not restore its cache key")
			}
		})
	}
}
