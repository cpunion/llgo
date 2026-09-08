//go:build !llgo

package build

import (
	"go/token"
	"go/types"
	"testing"

	"github.com/xgo-dev/llgo/cl"
	"github.com/xgo-dev/llgo/internal/packages"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestWasmMemProfileBuildConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, os, arch                  string
		mode                            BuildMode
		linear, consumer, threads, want bool
	}{
		{"consumer", "js", "wasm", BuildModeExe, true, true, false, true},
		{"no consumer", "js", "wasm", BuildModeExe, true, false, false, false},
		{"no linear GC", "js", "wasm", BuildModeExe, false, true, false, false},
		{"native", "linux", "amd64", BuildModeExe, true, true, false, false},
		{"wasi", "wasip1", "wasm", BuildModeExe, true, true, false, true},
		{"wasi threads", "wasip1", "wasm", BuildModeExe, true, true, true, false},
		{"archive unknown consumer", "js", "wasm", BuildModeCArchive, true, false, false, true},
		{"shared unknown consumer", "wasip1", "wasm", BuildModeCShared, true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			threads := "0"
			if tc.threads {
				threads = "1"
			}
			t.Setenv("LLGO_WASI_THREADS", threads)
			prog := llssa.NewProgram(&llssa.Target{GOOS: tc.os, GOARCH: tc.arch})
			defer prog.Dispose()
			prog.EnableGCRoots(tc.linear)
			ssaProg := ssa.NewProgram(token.NewFileSet(), 0)
			if tc.consumer {
				ssaProg.CreatePackage(types.NewPackage("runtime/pprof", "pprof"), nil, nil, true)
			}
			ctx := &context{prog: prog, progSSA: ssaProg, callerTracking: cl.NewCallerTracking(), buildConf: &Config{Goos: tc.os, Goarch: tc.arch, BuildMode: tc.mode}}
			ctx.configureWasmMemoryProfiling(tc.linear)
			if got := prog.WasmMemoryProfilingEnabled(); got != tc.want {
				t.Fatalf("enabled=%v,want%v", got, tc.want)
			}
		})
	}
	for _, tags := range []string{"llgo.wasi_threads", "other,llgo.wasm.workers"} {
		t.Run(tags, func(t *testing.T) {
			prog := llssa.NewProgram(&llssa.Target{GOOS: "js", GOARCH: "wasm"})
			defer prog.Dispose()
			prog.EnableGCRoots(true)
			ctx := &context{prog: prog, callerTracking: cl.NewCallerTracking(), buildConf: &Config{Goos: "js", Goarch: "wasm", BuildMode: BuildModeCArchive, Tags: tags}}
			ctx.configureWasmMemoryProfiling(true)
			if prog.WasmMemoryProfilingEnabled() {
				t.Fatal("threaded runtime enabled single-worker sampler")
			}
		})
	}
}

func TestWasmMemProfilePackageFingerprint(t *testing.T) {
	for _, path := range []string{llssa.PkgRuntime, "user"} {
		fingerprints := make(map[bool]string)
		for _, enabled := range []bool{false, true} {
			prog := llssa.NewProgram(&llssa.Target{GOOS: "js", GOARCH: "wasm"})
			prog.EnableGCRoots(true)
			prog.EnableWasmMemoryProfiling(enabled)
			ctx := &context{prog: prog, conf: &packages.Config{}, buildConf: &Config{}, llvmVersion: "test"}
			pkg := &aPackage{Package: &packages.Package{ID: path, PkgPath: path}}
			if err := ctx.collectFingerprint(pkg); err != nil {
				t.Fatal(err)
			}
			data, err := decodeManifest(pkg.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			if data.Package.WasmMemoryProfiling != enabled {
				t.Fatalf("missing profile mode:\n%s", pkg.Manifest)
			}
			fingerprints[enabled] = pkg.Fingerprint
			prog.Dispose()
		}
		if fingerprints[false] == fingerprints[true] {
			t.Fatalf("%s cache ignores profiling mode", path)
		}
	}
	if (&packageSection{WasmMemoryProfiling: true}).empty() {
		t.Fatal("profile setting treated as empty")
	}
}
