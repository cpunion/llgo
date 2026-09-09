//go:build !llgo

package cl

import (
	"fmt"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llgo/ssa/abi"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestWasmScalarLeafAnalysis(t *testing.T) {
	pkg, _ := buildCallerFrameSSAPackage(t, "example.com/scalar", `package scalar
var global uint
type Word uint
func empty() {}
func add(x, y, c uint) (uint, uint) { z := x + y + c; return z, ((x & y) | ((x | y) &^ z)) >> 63 }
func pair(x uint) uint { a, b := add(x, 1, 0); return a + b }
func choose(x Word) Word { if x > 3 { return x - 1 }; return x + 1 }
func floating(x, y float64) float64 { return -x / y }
func truth(x bool) bool { return !x }
func convert(x uint32) uint64 { return uint64(x) }
func unsignedShift(x, y uint) uint { return x << y }
func constantShift(x uint) uint { return x >> 3 }
func signedShift(x uint, y int) uint { return x << y }
func divide(x, y uint) uint { return x / y }
func divideConstant(x uint) uint { return x / 7 }
func remainder(x uint) uint { return x % 7 }
func pointer(p *uint) uint { return *p }
func store(p *uint) { *p = 1 }
func readGlobal() uint { return global }
func text(s string) int { return len(s) }
func complexValue(x complex128) complex128 { return x * x }
func allocation() *uint { return new(uint) }
func loop(x uint) uint { for x > 0 { x-- }; return x }
func recurse(x uint) uint { if x != 0 { return recurse(x - 1) }; return x }
func mutualA(x uint) uint { return mutualB(x) }
func mutualB(x uint) uint { return mutualA(x) }
func missing(uint) uint
func external(x uint) uint { return missing(x) }
func dynamic(f func(uint) uint, x uint) uint { return f(x) }
func panics(x uint) uint { if x == 0 { panic("zero") }; return x }
func deferred(x uint) uint { defer empty(); return x }
func spawn() { go empty() }
func closure(x uint) func() uint { return func() uint { return x } }
//go:linkname linked replacement
func linked(x uint) uint { return x }
func (x Word) method() Word { return x }
func stringPhi(x uint) bool { s := "a"; if x != 0 { s = "b" }; return s == "c" }
func stringify(x uint) int { return len(string(rune(x))) }
`)
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	p := &context{prog: prog}
	for _, name := range []string{"empty", "add", "pair", "choose", "floating", "truth", "convert", "unsignedShift", "constantShift", "divideConstant", "remainder"} {
		if !p.isWasmScalarLeaf(pkg.Func(name)) {
			t.Errorf("%s should be a bounded non-panicking scalar leaf", name)
		}
	}
	for _, name := range []string{"signedShift", "divide", "pointer", "store", "readGlobal", "text", "complexValue", "allocation", "loop", "recurse", "mutualA", "mutualB", "missing", "external", "dynamic", "panics", "deferred", "spawn", "closure", "linked", "stringPhi", "stringify"} {
		if p.isWasmScalarLeaf(pkg.Func(name)) {
			t.Errorf("%s lost required instrumentation", name)
		}
	}
	if p.isWasmScalarLeaf(nil) || p.isWasmScalarLeaf(pkg.Func("closure").AnonFuncs[0]) {
		t.Fatal("missing function or capturing closure accepted")
	}
	word := pkg.Pkg.Scope().Lookup("Word").Type().(*types.Named)
	if p.isWasmScalarLeaf(pkg.Prog.FuncValue(word.Method(0))) {
		t.Fatal("method accepted as a scalar function")
	}
	if got := p.wasmScalarLeafCost(nil); got != -1 {
		t.Fatalf("missing body cost=%d, want rejection", got)
	}
	native := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "linux", GOARCH: "amd64"})
	defer native.Dispose()
	if (&context{prog: native}).isWasmScalarLeaf(pkg.Func("add")) {
		t.Fatal("native instrumentation changed")
	}
	prog.SetLinkname("example.com/scalar.empty", "C.hidden")
	prog.SetWasmImport("example.com/scalar.add", "host", "hidden")
	redirected := &context{prog: prog}
	if redirected.isWasmScalarLeaf(pkg.Func("empty")) || redirected.isWasmScalarLeaf(pkg.Func("add")) {
		t.Fatal("source proof accepted a redirected function")
	}
}

func TestWasmScalarLeafExpandedBudget(t *testing.T) {
	var source strings.Builder
	source.WriteString("package scalar\nfunc f0(x uint) uint { return x + 1 }\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&source, "func f%d(x uint) uint { return f%d(f%d(x)) }\n", i, i-1, i-1)
	}
	pkg, _ := buildCallerFrameSSAPackage(t, "example.com/scalar", source.String())
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	for _, reverse := range []bool{false, true} {
		p := &context{prog: prog}
		if reverse {
			p.isWasmScalarLeaf(pkg.Func("f9"))
		}
		if !p.isWasmScalarLeaf(pkg.Func("f2")) || p.isWasmScalarLeaf(pkg.Func("f9")) {
			t.Fatalf("expanded work budget not respected (reverse=%v)", reverse)
		}
	}
}

func TestWasmScalarLeafPatchedPackage(t *testing.T) {
	dep, root := buildCallerFrameSSAProgram(t,
		"example.com/dep", `package dep
func Add(x, y uint) uint { return x + y }
`, "example.com/root", `package root
import "example.com/dep"
func Scalar(x, y uint) uint { return dep.Add(x, y) }
`)
	alt, _ := buildCallerFrameSSAPackage(t, abi.PatchPathPrefix+"example.com/dep", `package dep
func Add(x, y uint) uint { for y != 0 { x++; y-- }; return x }
func Pure(x uint) uint { return x + 1 }
`)
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	patches := Patches{dep.Pkg.Path(): {Alt: alt, Types: alt.Pkg}}
	p := &context{prog: prog, patches: patches}
	for _, fn := range []*ssa.Function{dep.Func("Add"), root.Func("Scalar"), alt.Func("Pure")} {
		if p.isWasmScalarLeaf(fn) {
			t.Errorf("%s accepted despite a package replacement", fn)
		}
	}
	// The caller still refers to the original Go SSA function, whose scalar
	// body says nothing about the replacement that will execute at link time.
	prog.EnableCooperativeSafepoints(true)
	pkg, _, err := NewPackageExWithEmbedMetaOptions(prog, nil, patches, nil, root, nil, nil, false, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	body := pkg.Module().NamedFunction("example.com/root.Scalar").String()
	if strings.Count(body, "CooperativeSafepoint") != 1 {
		t.Errorf("patched call tree lost its entry poll:\n%s", body)
	}
}

func TestWasmScalarLeafForeignPackage(t *testing.T) {
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	for _, kind := range []string{"decl", "decl: intrinsics", "link", "link: -lexternal", "py.foreign"} {
		t.Run(kind, func(t *testing.T) {
			pkg, _ := buildCallerFrameSSAPackage(t, "example.com/foreign", fmt.Sprintf(`package foreign
const LLGoPackage = %q
func Stub(x uint) uint { return x }
`, kind))
			if (&context{prog: prog}).isWasmScalarLeaf(pkg.Func("Stub")) {
				t.Fatal("foreign declaration's Go placeholder accepted as its implementation")
			}
		})
	}
	pkg, _ := buildCallerFrameSSAPackage(t, "example.com/cgo", `package cgo
func _Cfunc_hidden(x uint) uint { return x }
`)
	if (&context{prog: prog}).isWasmScalarLeaf(pkg.Func("_Cfunc_hidden")) {
		t.Fatal("Cgo entry accepted as a Go scalar function")
	}
}

func TestWasmScalarLeafCrossPackageAndInstrumentation(t *testing.T) {
	dep, root := buildCallerFrameSSAProgram(t,
		"example.com/dep", `package dep
//go:noinline
func Add(x, y uint) uint { return x + y }
`, "example.com/root", `package root
import "example.com/dep"
import "runtime"
//go:noinline
func Scalar(x, y uint) uint { return dep.Add(x, y) }
func Owner(x uint) uint { runtime.Caller(0); return Scalar(x, 1) }
func Loop(x uint) uint { for x != 0 { x = Scalar(x, 0) - 1 }; return x }
func Recursive(x uint) uint { if x != 0 { return Recursive(x - 1) }; return x }
`)
	for _, target := range []*llssa.Target{
		{GOOS: "wasip1", GOARCH: "wasm"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "linux", GOARCH: "amd64"},
	} {
		t.Run(target.GOOS+"/"+target.LLVMTarget, func(t *testing.T) {
			prog := newLLSSAProgForTarget(t, target)
			defer prog.Dispose()
			if target.GOARCH == "wasm" && prog.PointerSize() == 4 {
				prog.TypeSizes(types.SizesFor("gc", "386"))
			}
			prog.EnableCooperativeSafepoints(true)
			for _, sp := range []*ssa.Package{dep, root} {
				pkg, _, err := NewPackageExWithEmbedMetaOptions(prog, nil, nil, nil, sp, nil, nil, false, Options{ShadowStack: true})
				if err != nil {
					t.Fatal(err)
				}
				if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
				for name, wantPolls := range map[string]int{"Add": 1, "Scalar": 1, "Owner": 1, "Loop": 2, "Recursive": 1} {
					fn := pkg.Module().NamedFunction(sp.Pkg.Path() + "." + name)
					if fn.IsNil() || fn.FirstBasicBlock().IsNil() {
						continue
					}
					body := fn.String()
					if target.GOARCH == "wasm" && (name == "Add" || name == "Scalar") {
						wantPolls = 0
						if strings.Contains(body, "CallerLocationFrame") || strings.Contains(body, "RecordPanicLocation") {
							t.Errorf("scalar leaf %s still contains suspendable attribution:\n%s", name, body)
						}
					}
					if got := strings.Count(body, "CooperativeSafepoint"); got != wantPolls {
						t.Errorf("%s polls=%d, want %d:\n%s", name, got, wantPolls, body)
					}
					if name == "Owner" && target.GOARCH == "wasm" {
						if strings.Contains(body, "RecordPanicLocation") || !strings.Contains(body, "RecordCallerLocation") {
							t.Errorf("scalar call removal affected actual Caller lookup:\n%s", body)
						}
					}
				}
			}
		})
	}
}
