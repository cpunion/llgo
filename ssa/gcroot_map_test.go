//go:build !llgo

package ssa_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llgo/ssa/ssatest"
	"github.com/xgo-dev/llvm"
)

func emitRootMapFunction(pkg ssa.Package, name string, count int) {
	fn := pkg.NewFunc(name, ssa.NoArgsNoRet, ssa.InGo)
	b := fn.MakeBody(1)
	fn.NewGCRoots(count)
	// Keep the published frame observable to an external collector through
	// optimization; a function with no calls can discard its entire frame.
	sink := pkg.FuncOf("rootMapSink")
	if sink == nil {
		sink = pkg.NewFunc("rootMapSink", ssa.NoArgsNoRet, ssa.InGo)
	}
	b.Call(sink.Expr)
	b.Return()
	b.EndBuild()
}

func TestGCRootMapSharing(t *testing.T) {
	for _, target := range []*ssa.Target{
		{GOOS: "js", GOARCH: "wasm"},
		{GOOS: "wasip1", GOARCH: "wasm"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten", WasmABI: "emscripten-memory64"},
		{GOOS: "linux", GOARCH: "amd64"},
	} {
		t.Run(target.GOOS+"/"+target.GOARCH+"/"+target.LLVMTarget, func(t *testing.T) {
			prog := ssatest.NewProgram(t, target)
			pkg := prog.NewPackage("main", "main")
			emitRootMapFunction(pkg, "one", 2)
			emitRootMapFunction(pkg, "two", 2)
			emitRootMapFunction(pkg, "three", 3)
			mod := pkg.Module()
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			if target.GOARCH != "wasm" {
				for _, name := range []string{"one$gcmap", "two$gcmap", "three$gcmap"} {
					g := mod.NamedGlobal(name)
					if g.IsNil() || g.Linkage() != llvm.InternalLinkage {
						t.Fatalf("native map %s lost per-function internal linkage", name)
					}
				}
				if strings.Contains(pkg.String(), "__llgo_wasm_gcmap$") {
					t.Fatal("native module contains WebAssembly root maps")
				}
				return
			}
			for name, count := range map[string]uint64{"__llgo_wasm_gcmap$2": 2, "__llgo_wasm_gcmap$3": 3} {
				g := mod.NamedGlobal(name)
				if g.IsNil() || g.Linkage() != llvm.LinkOnceODRLinkage || !g.IsGlobalConstant() {
					t.Fatalf("map %s lacks immutable ODR storage", name)
				}
				init := g.Initializer()
				if init.Operand(0).ZExtValue() != count || init.Operand(1).ZExtValue() != 0 {
					t.Fatalf("map %s has incorrect count or metadata", name)
				}
			}
			for _, name := range []string{"one$gcmap", "two$gcmap", "three$gcmap"} {
				if !mod.NamedGlobal(name).IsNil() {
					t.Fatalf("per-function map %s survived pooling", name)
				}
			}
			if strings.Count(pkg.String(), "comdat any") != 2 {
				t.Fatalf("expected exactly two root-map COMDAT groups:\n%s", pkg.String())
			}
		})
	}
}

func TestGCRootMapSharingAcrossLinkedPackages(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatalf("clang is required to verify WebAssembly GC-map linking: %v", err)
	}
	linker, err := exec.LookPath("wasm-ld")
	if err != nil {
		t.Fatalf("wasm-ld is required to verify WebAssembly GC-map linking: %v", err)
	}
	dir := t.TempDir()
	var objects []string
	for _, name := range []string{"left", "right"} {
		// Separate Programs reproduce isolated package workers and LLVM contexts.
		prog := ssatest.NewProgram(t, &ssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
		pkg := prog.NewPackage(name, name)
		emitRootMapFunction(pkg, name, 2)
		if name == "right" {
			emitRootMapFunction(pkg, "different", 3)
		}
		mod := pkg.Module()
		mod.SetTarget("wasm32-unknown-unknown")
		mod.SetDataLayout(prog.DataLayout())
		if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
			t.Fatal(err)
		}
		ir := filepath.Join(dir, name+".ll")
		object := filepath.Join(dir, name+".o")
		if err := os.WriteFile(ir, []byte(pkg.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(clang, "--target=wasm32", "-Oz", "-c", ir, "-o", object).CombinedOutput(); err != nil {
			t.Fatalf("compile GC-map package: %v\n%s", err, output)
		}
		objects = append(objects, object)
	}
	linkMap := filepath.Join(dir, "linked.map")
	args := []string{"--no-entry", "--allow-undefined", "--gc-sections", "--export=left", "--export=right", "--export=different", "--Map=" + linkMap, "-o", filepath.Join(dir, "linked.wasm")}
	if output, err := exec.Command(linker, append(args, objects...)...).CombinedOutput(); err != nil {
		t.Fatalf("link GC-map packages: %v\n%s", err, output)
	}
	data, err := os.ReadFile(linkMap)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 4 && strings.HasPrefix(fields[3], "__llgo_wasm_gcmap$") {
			counts[fields[3]]++
		}
	}
	for _, name := range []string{"__llgo_wasm_gcmap$2", "__llgo_wasm_gcmap$3"} {
		if counts[name] != 1 {
			t.Fatalf("linked map %s has %d copies, want exactly one:\n%s", name, counts[name], data)
		}
	}
}
