//go:build !llgo

package cl

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"runtime"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestGCSafepointClassification(t *testing.T) {
	fn := buildGCRootSSAFunction(t, `package p
func helper()
func classify(p *int, text string, bytes []byte, ch chan int, m map[string]int, value any) {
	_ = *p
	_ = len(bytes)
	_ = text + text
	_ = string(bytes)
	_ = value == value
	helper()
	_ = <-ch
	m[text] = 1
}`)
	seen := make(map[string]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr := instr.(type) {
			case *ssa.UnOp:
				switch instr.Op {
				case token.MUL:
					seen["deref"] = true
					if gcSafepoint(instr) {
						t.Error("pointer dereference classified as a safepoint")
					}
				case token.ARROW:
					seen["receive"] = true
					if !gcSafepoint(instr) {
						t.Error("channel receive not classified as a safepoint")
					}
				}
			case *ssa.BinOp:
				switch basicKind(instr.X.Type()) {
				case types.String:
					seen["string operation"] = true
					if !gcSafepoint(instr) {
						t.Error("string operation not classified as a safepoint")
					}
				default:
					if _, ok := instr.X.Type().Underlying().(*types.Interface); ok {
						seen["interface comparison"] = true
						if !gcSafepoint(instr) {
							t.Error("interface comparison not classified as a safepoint")
						}
					}
				}
			case *ssa.Convert:
				seen["string conversion"] = true
				if !gcSafepoint(instr) {
					t.Error("string conversion not classified as a safepoint")
				}
			case *ssa.Call:
				if builtin, ok := instr.Call.Value.(*ssa.Builtin); ok && builtin.Name() == "len" {
					seen["pure builtin"] = true
					if gcSafepoint(instr) {
						t.Error("len classified as a safepoint")
					}
				} else {
					seen["call"] = true
					if !gcSafepoint(instr) {
						t.Error("call not classified as a safepoint")
					}
				}
			case *ssa.MapUpdate:
				seen["map update"] = true
				if !gcSafepoint(instr) {
					t.Error("map update not classified as a safepoint")
				}
			}
		}
	}
	for _, want := range []string{
		"deref", "receive", "string operation", "string conversion",
		"interface comparison", "pure builtin", "call", "map update",
	} {
		if !seen[want] {
			t.Errorf("%s instruction was not generated", want)
		}
	}
	if !functionHasGCSafepoint(fn) {
		t.Error("function with runtime operations has no GC safepoint")
	}
}

func TestGCSafepointPureInstructions(t *testing.T) {
	for _, instr := range []ssa.Instruction{
		new(ssa.DebugRef),
		new(ssa.Extract),
		new(ssa.Field),
		new(ssa.FieldAddr),
		new(ssa.If),
		new(ssa.Index),
		new(ssa.IndexAddr),
		new(ssa.Jump),
		new(ssa.Phi),
		new(ssa.Return),
		new(ssa.Slice),
		new(ssa.SliceToArrayPointer),
		new(ssa.Store),
		new(ssa.ChangeType),
	} {
		if gcSafepoint(instr) {
			t.Errorf("%T classified as a safepoint", instr)
		}
	}
	if !gcSafepoint(new(ssa.MakeSlice)) {
		t.Error("unknown runtime-lowered instruction must stay conservative")
	}
	if gcConversionSafepoint(types.Typ[types.Int], types.Typ[types.Uint]) {
		t.Error("numeric conversion classified as a safepoint")
	}
	pure := buildGCRootSSAFunction(t, `package p
func classify(p *int) *int { return p }
`)
	if functionHasGCSafepoint(pure) {
		t.Error("pure function has a GC safepoint")
	}
}

func TestGCRootCountUsesConcreteRangeNextType(t *testing.T) {
	fn := buildGCRootSSAFunction(t, `package p
func classify(values map[int]*int, text string, pointer *int) {
	for range values {}
	for range text {}
}`)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	ctx := context{prog: prog}
	if got := ctx.gcRootCount(fn.Params[2]); got != 1 {
		t.Fatalf("pointer parameter root count = %d, want 1", got)
	}

	seenMap, seenString := false, false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			next, ok := instr.(*ssa.Next)
			if !ok {
				continue
			}
			got := ctx.gcRootCount(next)
			if next.IsString {
				seenString = true
				if got != 0 {
					t.Fatalf("string range Next root count = %d, want 0", got)
				}
			} else {
				seenMap = true
				if got != 1 {
					t.Fatalf("map range Next root count = %d, want 1", got)
				}
			}
		}
	}
	if !seenMap || !seenString {
		t.Fatalf("range Next instructions: map=%v string=%v", seenMap, seenString)
	}
}

func TestGCRootCountExcludesFunctionAddressIntrinsic(t *testing.T) {
	fn := buildGCRootSSAFunction(t, `package p
import "unsafe"
func intrinsic(any) unsafe.Pointer
func classify(fn func()) unsafe.Pointer { return intrinsic(fn) }
`)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	prog.SetLinkname("gcroot.intrinsic", "llgo.funcAddr")
	ctx := context{prog: prog, goTyps: fn.Pkg.Pkg}

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			call, ok := instr.(*ssa.Call)
			if !ok {
				continue
			}
			if got := ctx.gcRootCount(call); got != 0 {
				t.Fatalf("function address root count = %d, want 0", got)
			}
			return
		}
	}
	t.Fatal("function address call not found")
}

func buildGCRootSSAFunction(t *testing.T, src string) *ssa.Function {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gcroot.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(
		&types.Config{Importer: importer.Default()},
		fset,
		types.NewPackage("gcroot", "p"),
		[]*ast.File{file},
		ssa.InstantiateGenerics,
	)
	if err != nil {
		t.Fatal(err)
	}
	return pkg.Func("classify")
}
