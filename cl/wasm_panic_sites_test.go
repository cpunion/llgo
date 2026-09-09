//go:build !llgo

package cl

import (
	"go/constant"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
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
`
	for _, target := range []*llssa.Target{
		{GOOS: "wasip1", GOARCH: "wasm"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "linux", GOARCH: "amd64"},
	} {
		t.Run(target.GOOS+"/"+target.LLVMTarget, func(t *testing.T) {
			ssaPkg, files := buildCallerFrameSSAPackage(t, "example.com/sites", source)
			prog := newLLSSAProgForTarget(t, target)
			defer prog.Dispose()
			// Match the physical pointer layout selected by build.effectiveTypeSizes,
			// not cmd/compile's eight-byte Go wasm word layout.
			if target.GOARCH == "wasm" && prog.PointerSize() == 4 {
				prog.TypeSizes(types.SizesFor("gc", "386"))
			}
			pkg, _, err := NewPackageExWithEmbedMetaOptions(prog, nil, nil, nil, ssaPkg, files, nil, false, Options{ShadowStack: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"Global", "Field", "Constant", "Allocated", "Pointer", "PointerField", "PointerArray", "Dynamic", "Slice"} {
				body := pkg.Module().NamedFunction("example.com/sites." + name).String()
				want := target.GOARCH != "wasm" || name == "Pointer" || name == "PointerField" || name == "PointerArray" || name == "Dynamic" || name == "Slice"
				if got := strings.Contains(body, "RecordPanicLocation"); got != want {
					t.Errorf("%s records panic=%v, want %v:\n%s", name, got, want, body)
				}
				if !strings.Contains(body, "RecordCallerLocation") {
					t.Errorf("%s lost its actual runtime.Caller source location", name)
				}
			}
		})
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
