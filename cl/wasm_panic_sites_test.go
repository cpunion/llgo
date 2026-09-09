//go:build !llgo

package cl

import (
	"go/constant"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llgo/ssa/ssatest"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestWasmSafePanicSites(t *testing.T) {
	const source = `package sites
import "runtime"
var scalar float64
var object struct{ value int }
var array [4]int
func Global() float64 { runtime.Caller(0); return -scalar }
func Field() int { runtime.Caller(0); return object.value }
func Constant() int { runtime.Caller(0); return array[2] }
func Allocated() int { runtime.Caller(0); p := new([4]int); p[2] = 7; return p[2] }
func Pointer(p *int) int { runtime.Caller(0); return *p }
func PointerField(p *struct{ value int }) int { runtime.Caller(0); return p.value }
func PointerArray(p *[4]int) int { runtime.Caller(0); return p[2] }
func Dynamic(i int) int { runtime.Caller(0); return array[i] }
func Slice(s []int) int { runtime.Caller(0); return s[2] }
func Wide(s []int, i uint64) int { runtime.Caller(0); return s[i] }
func String(s string, i int) byte { runtime.Caller(0); return s[i] }
func Store(p *int, v int) { runtime.Caller(0); *p = v }
func StoreField(p *struct{ value int }, v int) { runtime.Caller(0); p.value = v }
func UnusedField(p *struct{ value int }) { runtime.Caller(0); _ = p.value }
func Empty(p *[0]int) [0]int { runtime.Caller(0); return *p }
func Loop(s []int) { runtime.Caller(0); for i := range s { s[i] = s[i] + 1 } }
func BuiltinComplex(x, y float64) complex128 { runtime.Caller(0); return complex(x, y) }
func BuiltinReal(x complex128) float64 { runtime.Caller(0); return real(x) }
func BuiltinImag(x complex128) float64 { runtime.Caller(0); return imag(x) }
func BuiltinComplex32(x, y float32) complex64 { runtime.Caller(0); return complex(x, y) }
func BuiltinReal32(x complex64) float32 { runtime.Caller(0); return real(x) }
func BuiltinImag32(x complex64) float32 { runtime.Caller(0); return imag(x) }
func PointerComplex(p *float64) complex128 { runtime.Caller(0); return complex(*p, 1) }
func Close(ch chan int) { runtime.Caller(0); close(ch) }
func ShadowedComplex(x, y float64) complex128 {
  complex := func(x, y float64) complex128 { runtime.Caller(0); return 0 }
  runtime.Caller(0)
  return complex(x, y)
}
`
	for _, target := range []*llssa.Target{
		{GOOS: "wasip1", GOARCH: "wasm"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "linux", GOARCH: "amd64"},
	} {
		t.Run(target.GOOS+"/"+target.LLVMTarget, func(t *testing.T) {
			ssaPkg, files := buildCallerFrameSSAPackage(t, "example.com/sites", source)
			prog := ssatest.NewProgram(t, target)
			defer prog.Dispose()
			// Match the physical pointer layout selected by build.effectiveTypeSizes,
			// not cmd/compile's eight-byte Go wasm word layout.
			if target.GOARCH == "wasm" && prog.PointerSize() == 4 {
				prog.TypeSizes(types.SizesFor("gc", "386"))
				// Import the 32-bit runtime declarations (including the wide
				// index helpers), independent of this test process's host arch.
				t.Setenv("GOOS", "windows")
				t.Setenv("GOARCH", "386")
			}
			pkg, _, err := NewPackageExWithEmbedMetaOptions(prog, nil, nil, nil, ssaPkg, files, nil, false, Options{ShadowStack: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"Global", "Field", "Constant", "Allocated", "Pointer", "PointerField", "PointerArray", "Dynamic", "Slice", "Wide", "String", "Store", "StoreField", "UnusedField", "Empty", "Loop", "BuiltinComplex", "BuiltinReal", "BuiltinImag", "BuiltinComplex32", "BuiltinReal32", "BuiltinImag32", "PointerComplex", "Close", "ShadowedComplex"} {
				fn := pkg.Module().NamedFunction("example.com/sites." + name)
				body := fn.String()
				want := name != "Global" && name != "Field" && name != "Constant" && name != "Allocated" && !strings.HasPrefix(name, "Builtin")
				if target.GOARCH != "wasm" {
					want = name != "Store"
				}
				if got := strings.Contains(body, "RecordPanicLocation"); got != want {
					t.Errorf("%s records panic=%v, want %v:\n%s", name, got, want, body)
				}
				if !strings.Contains(body, "RecordCallerLocation") {
					t.Errorf("%s lost its actual runtime.Caller source location", name)
				}
				// Close and the shadowing Go function retain ordinary call
				// attribution, not just failure-block guard attribution.
				if target.GOARCH == "wasm" && name != "Close" && name != "ShadowedComplex" {
					assertWasmGuardLocationsAreCold(t, fn)
				}
			}
		})
	}
}

// Each record must precede a real panic helper in the same isolated failure
// block. A record merely following a conditional branch is not sufficient:
// accidentally inserting it in the successful successor would retain the hot
// loop cost and could leave an incorrect source location for the next guard.
func assertWasmGuardLocationsAreCold(t *testing.T, fn llvm.Value) {
	t.Helper()
	for block := fn.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
		for instr := block.FirstInstruction(); !instr.IsNil(); instr = llvm.NextInstruction(instr) {
			if instr.IsACallInst().IsNil() || !strings.HasSuffix(instr.CalledValue().Name(), ".RecordPanicLocationWasm") {
				continue
			}
			found := false
			for next := llvm.NextInstruction(instr); !next.IsNil(); next = llvm.NextInstruction(next) {
				if !next.IsACallInst().IsNil() {
					name := next.CalledValue().Name()
					found = strings.Contains(name, ".PanicIndex") || strings.Contains(name, ".PanicExtendIndex") || strings.HasSuffix(name, ".AssertNilDeref")
					break
				}
			}
			if !found {
				t.Errorf("%s records a guard outside its panic path:\n%s", fn.Name(), fn.String())
			}
			last := block.LastInstruction()
			if last.InstructionOpcode() != llvm.Br || last.SuccessorsCount() != 1 || last.Successor(0) != block {
				t.Errorf("%s panic-location block can escape into the successful continuation:\n%s", fn.Name(), fn.String())
			}
		}
	}
}

func TestKnownSafeArrayIndexAddrBounds(t *testing.T) {
	ssaPkg, _ := buildCallerFrameSSAPackage(t, "example.com/index", `package index
var array [4]int
var slice []int
func use(p *[4]int, s []int, index int) { _ = p[index]; _ = s[index] }
`)
	fn := ssaPkg.Func("use")
	for _, tc := range []struct {
		name  string
		base  ssa.Value
		index ssa.Value
		want  bool
	}{
		{"first", ssaPkg.Var("array"), ssa.NewConst(constant.MakeInt64(0), types.Typ[types.Int]), true},
		{"last", ssaPkg.Var("array"), ssa.NewConst(constant.MakeInt64(3), types.Typ[types.Int]), true},
		{"negative", ssaPkg.Var("array"), ssa.NewConst(constant.MakeInt64(-1), types.Typ[types.Int]), false},
		{"end", ssaPkg.Var("array"), ssa.NewConst(constant.MakeInt64(4), types.Typ[types.Int]), false},
		{"unsigned", ssaPkg.Var("array"), ssa.NewConst(constant.MakeUint64(^uint64(0)), types.Typ[types.Uint64]), false},
		{"dynamic", ssaPkg.Var("array"), fn.Params[2], false},
		{"nullable", fn.Params[0], ssa.NewConst(constant.MakeInt64(0), types.Typ[types.Int]), false},
		{"slice", fn.Params[1], ssa.NewConst(constant.MakeInt64(0), types.Typ[types.Int]), false},
		{"slice address", ssaPkg.Var("slice"), ssa.NewConst(constant.MakeInt64(0), types.Typ[types.Int]), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isKnownSafeArrayIndexAddr(&ssa.IndexAddr{X: tc.base, Index: tc.index}); got != tc.want {
				t.Fatalf("safe=%v, want %v", got, tc.want)
			}
		})
	}
}
