//go:build !llgo

package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestLeakingWebAssemblyProfilesExcludeBDWGC(t *testing.T) {
	targets := []struct {
		name   string
		goos   string
		goarch string
		tags   string
	}{
		{name: "js-wasm", goos: "js", goarch: "wasm", tags: "llgo,tinygo.wasm,nogc"},
		{name: "wasip1", goos: "wasip1", goarch: "wasm", tags: "llgo,tinygo.wasm,nogc"},
		{name: "wasip2", goos: "linux", goarch: "arm", tags: "llgo,tinygo.wasm,wasip2,nogc"},
		{name: "wasm-unknown", goos: "linux", goarch: "arm", tags: "llgo,tinygo.wasm,wasm_unknown,nogc"},
	}
	moduleRoot, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", "-json", "-tags="+target.tags,
				"./internal/runtime", "./internal/lib/runtime", "./internal/clite/pthread", "./internal/clite/tls")
			cmd.Dir = moduleRoot
			cmd.Env = append(os.Environ(), "GOOS="+target.goos, "GOARCH="+target.goarch, "CGO_ENABLED=0")
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list leaking target packages: %v", err)
			}
			decoder := json.NewDecoder(bytes.NewReader(output))
			packages := make(map[string]struct {
				GoFiles []string
			})
			for {
				var pkg struct {
					ImportPath string
					GoFiles    []string
				}
				if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					t.Fatalf("decode go list stream: %v", err)
				}
				if pkg.ImportPath == "github.com/goplus/llgo/runtime/internal/clite/bdwgc" {
					t.Fatal("leaking target dependency graph retained BDWGC")
				}
				packages[pkg.ImportPath] = struct{ GoFiles []string }{GoFiles: pkg.GoFiles}
			}
			assertFiles := func(path string, required, forbidden []string) {
				t.Helper()
				pkg, ok := packages[path]
				if !ok {
					t.Fatalf("go list stream is missing %s", path)
				}
				for _, file := range required {
					if !slices.Contains(pkg.GoFiles, file) {
						t.Fatalf("%s GoFiles = %v, want %s", path, pkg.GoFiles, file)
					}
				}
				for _, file := range forbidden {
					if slices.Contains(pkg.GoFiles, file) {
						t.Fatalf("%s GoFiles = %v, unexpectedly selected %s", path, pkg.GoFiles, file)
					}
				}
			}
			assertFiles("github.com/goplus/llgo/runtime/internal/runtime", []string{"z_nogc.go"}, []string{"z_gc.go"})
			if target.name == "wasip1" {
				assertFiles("github.com/goplus/llgo/runtime/internal/runtime",
					[]string{"coro_abort_freestanding_webassembly.go"},
					[]string{"coro_abort_libc.go"})
			}
			assertFiles("github.com/goplus/llgo/runtime/internal/lib/runtime", []string{"runtime_nogc.go", "mfinal_nogc.go"}, []string{"runtime_gc.go", "mfinal.go"})
			assertFiles("github.com/goplus/llgo/runtime/internal/clite/pthread", []string{"pthread_nogc.go"}, []string{"pthread_gc.go"})
			assertFiles("github.com/goplus/llgo/runtime/internal/clite/tls", []string{"tls_webassembly.go"}, []string{"tls_common.go", "tls_gc.go", "tls_nogc.go"})
		})
	}
}

func TestFreestandingWebAssemblyProfilesDoNotSelectHostedRuntimeLeaves(t *testing.T) {
	targets := []struct {
		name   string
		goos   string
		goarch string
		tags   string
	}{
		{name: "wasip1", goos: "wasip1", goarch: "wasm", tags: "llgo,llgo_coro,nogc"},
		{name: "wasip2", goos: "linux", goarch: "arm", tags: "llgo,llgo_coro,tinygo.wasm,wasip2,nogc"},
		{name: "wasm-unknown", goos: "linux", goarch: "arm", tags: "llgo,llgo_coro,tinygo.wasm,wasm_unknown,nogc"},
	}
	moduleRoot, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", "-json", "-tags="+target.tags,
				"./internal/runtime", "./internal/lib/runtime", "./internal/clite/ffi", "./internal/clite/tls")
			cmd.Dir = moduleRoot
			cmd.Env = append(os.Environ(), "GOOS="+target.goos, "GOARCH="+target.goarch, "CGO_ENABLED=0")
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list freestanding WebAssembly packages: %v", err)
			}
			type packageFiles struct {
				GoFiles []string
				Imports []string
			}
			packages := make(map[string]packageFiles)
			decoder := json.NewDecoder(bytes.NewReader(output))
			for {
				var pkg struct {
					ImportPath string
					GoFiles    []string
					Imports    []string
				}
				if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					t.Fatalf("decode go list stream: %v", err)
				}
				packages[pkg.ImportPath] = packageFiles{GoFiles: pkg.GoFiles, Imports: pkg.Imports}
			}
			assertFiles := func(path string, required, forbidden []string) {
				t.Helper()
				pkg, ok := packages[path]
				if !ok {
					t.Fatalf("go list stream is missing %s", path)
				}
				for _, file := range required {
					if !slices.Contains(pkg.GoFiles, file) {
						t.Errorf("%s GoFiles = %v, want %s", path, pkg.GoFiles, file)
					}
				}
				for _, file := range forbidden {
					if slices.Contains(pkg.GoFiles, file) {
						t.Errorf("%s GoFiles = %v, unexpectedly selected %s", path, pkg.GoFiles, file)
					}
				}
			}
			assertFiles("github.com/goplus/llgo/runtime/internal/runtime", []string{
				"coro_abort_freestanding_webassembly.go",
				"panic_slot_single.go",
				"panic_traceback_freestanding_webassembly.go",
				"random_freestanding_webassembly.go",
				"z_freestanding_webassembly.go",
				"z_print_freestanding_webassembly.go",
			}, []string{
				"coro_abort_libc.go",
				"panic_slot_pthread.go",
				"panic_traceback_coro.go",
				"random_libc.go",
				"z_print.go",
				"z_print_format.go",
				"z_thread.go",
				"z_webassembly.go",
			})
			assertFiles("github.com/goplus/llgo/runtime/internal/lib/runtime",
				[]string{"rand_freestanding_webassembly.go", "runtime_webassembly.go", "zgoarch_webassembly.go"},
				[]string{"rand.go", "runtime_default.go", "time_debug_coro.go", "zgoarch_wasm.go"})
			for _, forbidden := range []string{
				"github.com/goplus/llgo/runtime/internal/clite/libuv",
				"github.com/goplus/llgo/runtime/internal/clite/os",
				"github.com/goplus/llgo/runtime/internal/clite/pthread",
			} {
				if imports := packages["github.com/goplus/llgo/runtime/internal/lib/runtime"].Imports; slices.Contains(imports, forbidden) {
					t.Errorf("freestanding patched runtime imports %s: %v", forbidden, imports)
				}
			}
			assertFiles("github.com/goplus/llgo/runtime/internal/clite/ffi",
				[]string{"abi_webassembly.go"}, []string{"abi.go", "abi_wasm.go"})
			assertFiles("github.com/goplus/llgo/runtime/internal/clite/tls",
				[]string{"tls_webassembly.go"}, []string{"tls_common.go", "tls_gc.go", "tls_nogc.go"})
			if imports := packages["github.com/goplus/llgo/runtime/internal/clite/tls"].Imports; slices.Contains(imports, "github.com/goplus/llgo/runtime/internal/clite/pthread") {
				t.Errorf("logical WebAssembly TLS imports pthread: %v", imports)
			}
		})
	}
}
