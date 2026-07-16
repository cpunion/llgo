//go:build !llgo

package crosscompile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/clang"
	"github.com/goplus/llgo/internal/lto"
	"github.com/goplus/llgo/internal/optlevel"
)

// TestFreestandingWasmTargetToolchainSmoke is an opt-in integration test. It
// exercises the exact named-target setup, compiles a real wasm object, links
// it with the target's wasmbuiltins/compiler-rt archives, and audits both link
// configuration and final symbols. CI enables it explicitly; ordinary unit
// tests do not download target toolchains or source archives.
func TestFreestandingWasmTargetToolchainSmoke(t *testing.T) {
	if os.Getenv("LLGO_WASM_TARGET_SMOKE") != "1" {
		t.Skip("set LLGO_WASM_TARGET_SMOKE=1 to compile and link named wasm targets")
	}

	for _, targetName := range []string{"wasip2", "wasm-unknown"} {
		t.Run(targetName, func(t *testing.T) {
			// Use is the path taken by the llgo driver's -target flag. Host
			// GOOS/GOARCH are intentionally supplied here to prove the named
			// target, rather than ambient GOOS/tags, owns backend selection.
			export, err := Use("host-os", "host-arch", targetName, false, true, optlevel.Oz, lto.Off, false)
			if err != nil {
				t.Fatalf("setup -target=%s: %v", targetName, err)
			}
			wantTriple := map[string]string{
				"wasip2":       "wasm32-unknown-wasi",
				"wasm-unknown": "wasm32-unknown-unknown",
			}[targetName]
			if export.LLVMTarget != wantTriple {
				t.Fatalf("-target=%s LLVM triple = %q, want %q", targetName, export.LLVMTarget, wantTriple)
			}
			if export.GOOS != "linux" || export.GOARCH != "arm" {
				t.Fatalf("-target=%s frontend = %s/%s, want the explicit 32-bit linux/arm frontend",
					targetName, export.GOOS, export.GOARCH)
			}
			if export.Libc != "wasmbuiltins" {
				t.Fatalf("-target=%s libc = %q, want freestanding wasmbuiltins", targetName, export.Libc)
			}
			assertNoConservativeGCLinkInputs(t, export)

			dir := t.TempDir()
			source := filepath.Join(dir, "smoke.c")
			object := filepath.Join(dir, "smoke.o")
			module := filepath.Join(dir, targetName+".wasm")
			const smokeSource = `
typedef __SIZE_TYPE__ size_t;
extern void *memcpy(void *, const void *, size_t);
extern double exp(double);
extern void *malloc(size_t);
extern void free(void *);
__attribute__((visibility("default")))
int llgo_wasm_target_smoke(void) {
  const unsigned char src[4] = {1, 2, 3, 4};
  unsigned char *dst = (unsigned char *)malloc(64);
  if (dst == (void *)0) {
    return 10;
  }
  memcpy(dst, src, 4);
  double value = exp((double)dst[0]);
  int result = dst[3] == 4 && value > 2.0 ? 0 : 20;
  free(dst);
  return result;
}
`
			if err := os.WriteFile(source, []byte(smokeSource), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg := clang.NewConfig(export.CC, export.CCFLAGS, export.CFLAGS, export.LDFLAGS, export.Linker)
			compiler := clang.NewCompiler(cfg)
			if err := compiler.Compile("-fno-builtin", "-x", "c", "-c", source, "-o", object); err != nil {
				t.Fatalf("compile -target=%s smoke object: %v", targetName, err)
			}
			linker := clang.NewLinker(cfg)
			if err := linker.Link("--export=llgo_wasm_target_smoke", "-o", module, object); err != nil {
				t.Fatalf("link -target=%s smoke module: %v", targetName, err)
			}

			contents, err := os.ReadFile(module)
			if err != nil {
				t.Fatal(err)
			}
			if len(contents) < 8 || !bytes.Equal(contents[:4], []byte{'\x00', 'a', 's', 'm'}) {
				t.Fatalf("-target=%s output is not a WebAssembly module", targetName)
			}
			assertClosedWasmSymbols(t, export, module)
			if wasmtime, lookErr := exec.LookPath("wasmtime"); lookErr == nil {
				cmd := exec.Command(wasmtime, "run", "--invoke", "llgo_wasm_target_smoke", module)
				if output, runErr := cmd.CombinedOutput(); runErr != nil {
					t.Fatalf("execute -target=%s allocator smoke: %v\n%s", targetName, runErr, output)
				}
				t.Logf("executed -target=%s malloc/write/read/free smoke with %s", targetName, wasmtime)
			} else {
				t.Logf("wasmtime unavailable; compile/link/symbol closure for -target=%s is verified, execution skipped", targetName)
			}
		})
	}
}

func assertNoConservativeGCLinkInputs(t *testing.T, export Export) {
	t.Helper()
	for _, flag := range export.LDFLAGS {
		lower := strings.ToLower(flag)
		if flag == "-lgc" || strings.Contains(lower, "libgc") || strings.Contains(lower, "bdwgc") ||
			strings.Contains(lower, "rpath") {
			t.Fatalf("WebAssembly link flags contain forbidden conservative-GC/runtime path %q: %v", flag, export.LDFLAGS)
		}
	}
	if !slices.ContainsFunc(export.LDFLAGS, func(flag string) bool {
		return strings.Contains(flag, "wasmbuiltins-wasm32")
	}) {
		t.Fatalf("WebAssembly link flags do not contain a triple-scoped wasmbuiltins archive: %v", export.LDFLAGS)
	}
}

func assertClosedWasmSymbols(t *testing.T, export Export, module string) {
	t.Helper()
	nm := filepath.Join(filepath.Dir(export.CC), "llvm-nm")
	if _, err := os.Stat(nm); err != nil {
		if path, lookErr := exec.LookPath("llvm-nm"); lookErr == nil {
			nm = path
		} else {
			t.Fatalf("WebAssembly toolchain capability missing: llvm-nm next to %q and on PATH", export.CC)
		}
	}
	cmd := exec.Command(nm, "--defined-only", "--format=just-symbols", module)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s symbols: %v\n%s", module, err, output)
	}
	definedSymbols := strings.Fields(string(output))
	for _, symbol := range definedSymbols {
		if strings.HasPrefix(symbol, "GC_") {
			t.Fatalf("final WebAssembly module contains BDWGC symbol %s:\n%s", symbol, output)
		}
	}
	for _, required := range []string{"llgo_wasm_target_smoke", "malloc", "free", "sbrk"} {
		if !slices.Contains(definedSymbols, required) {
			t.Fatalf("final WebAssembly module is missing required allocator symbol %s:\n%s", required, output)
		}
	}
	undefined := exec.Command(nm, "--undefined-only", "--format=just-symbols", module)
	undefinedOutput, err := undefined.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s undefined symbols: %v\n%s", module, err, undefinedOutput)
	}
	if len(bytes.TrimSpace(undefinedOutput)) != 0 {
		t.Fatalf("final WebAssembly module has unresolved symbols:\n%s", undefinedOutput)
	}
	t.Logf("linked %s with %s (%d bytes)", filepath.Base(module), export.Linker, fileSize(t, module))
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
