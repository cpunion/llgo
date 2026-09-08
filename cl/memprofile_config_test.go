//go:build !llgo

package cl

import (
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestCompileWasmMemProfileConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, path, arch     string
		roots, enabled, want bool
	}{
		{"enabled", llssa.PkgRuntime, "wasm", true, true, true},
		{"no consumer", llssa.PkgRuntime, "wasm", true, false, false},
		{"no linear GC", llssa.PkgRuntime, "wasm", false, true, false},
		{"native", llssa.PkgRuntime, "amd64", true, true, false},
		{"same name ordinary", "user", "wasm", true, true, false},
		{"same name public", "runtime", "wasm", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg, files := buildCallerFrameSSAPackage(t, tc.path, `package runtime
var wasmMemProfileEnabled bool
func Enabled() bool { return wasmMemProfileEnabled }
`)
			os := "js"
			if tc.arch != "wasm" {
				os = "linux"
			}
			prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: os, GOARCH: tc.arch})
			defer prog.Dispose()
			prog.EnableGCRoots(tc.roots)
			prog.EnableWasmMemoryProfiling(tc.enabled)
			compiled, err := NewPackage(prog, pkg, files)
			if err != nil {
				t.Fatal(err)
			}
			mod := compiled.Module()
			g := mod.NamedGlobal(tc.path + ".wasmMemProfileEnabled")
			if g.IsNil() || g.IsGlobalConstant() != (tc.path == llssa.PkgRuntime) {
				t.Fatalf("wrong configuration global: %s", mod.String())
			}
			if got := g.Initializer().ZExtValue() != 0; got != tc.want {
				t.Fatalf("enabled=%v,want%v", got, tc.want)
			}
			if tc.path == llssa.PkgRuntime {
				options := llvm.NewPassBuilderOptions()
				defer options.Dispose()
				if err := mod.RunPasses("function(instcombine)", prog.TargetMachine(), options); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(mod.NamedFunction(tc.path+".Enabled").String(), "load ") {
					t.Fatal("configuration did not fold")
				}
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWasmMemProfileProviderCallElision(t *testing.T) {
	for _, path := range []string{llssa.PkgRuntime, llssa.PkgRuntime + "/tinygogc", "user"} {
		for _, arch := range []string{"wasm", "amd64"} {
			for _, enabled := range []bool{false, true} {
				source := `package runtime
var Effects int
func argument() uintptr { Effects++; return 42 }
func recordMemProfileAlloc(uintptr) {}
func recordWasmMemProfileAlloc(uintptr) {}
func memProfileFree(uintptr) {}
func other(uintptr) {}
func Calls(){ recordMemProfileAlloc(argument()); recordWasmMemProfileAlloc(argument()); memProfileFree(argument()); other(argument()) }
`
				pkg, files := buildCallerFrameSSAPackage(t, path, source)
				os := "js"
				if arch != "wasm" {
					os = "linux"
				}
				prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: os, GOARCH: arch})
				prog.EnableGCRoots(true)
				prog.EnableLogicalGoroutineLocality(true)
				prog.EnableWasmMemoryProfiling(enabled)
				compiled, err := NewPackage(prog, pkg, files)
				if err != nil {
					prog.Dispose()
					t.Fatal(err)
				}
				body := compiled.Module().NamedFunction(path + ".Calls").String()
				for _, name := range []string{"recordMemProfileAlloc", "recordWasmMemProfileAlloc", "memProfileFree", "other"} {
					omit := arch == "wasm" && !enabled && (path == llssa.PkgRuntime && (name == "recordMemProfileAlloc" || name == "recordWasmMemProfileAlloc") || path == llssa.PkgRuntime+"/tinygogc" && name == "memProfileFree")
					if got := strings.Contains(body, path+"."+name); got == omit {
						t.Errorf("%s/%s/%v %s omit=%v:\n%s", path, arch, enabled, name, omit, body)
					}
				}
				if got := strings.Count(body, "call i"); got != 4 {
					t.Errorf("argument side effects lost: %d\n%s", got, body)
				}
				prog.Dispose()
			}
		}
	}
}

func TestWasmMemProfilePreservesPthreadProvider(t *testing.T) {
	pkg, files := buildCallerFrameSSAPackage(t, llssa.PkgRuntime, `package runtime
func recordMemProfileAlloc(uintptr) {}
func Alloc() { recordMemProfileAlloc(32) }
`)
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	prog.EnableGCRoots(true)
	prog.EnableLogicalGoroutineLocality(false)
	compiled, err := NewPackage(prog, pkg, files)
	if err != nil {
		t.Fatal(err)
	}
	body := compiled.Module().NamedFunction(llssa.PkgRuntime + ".Alloc").String()
	if !strings.Contains(body, ".recordMemProfileAlloc") {
		t.Fatalf("single-worker optimization removed the pthreads profile provider:\n%s", body)
	}
}
