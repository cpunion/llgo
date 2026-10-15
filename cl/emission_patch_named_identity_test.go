//go:build !llgo

package cl

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/typepatch"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llgo/ssa/abi"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestEmissionPatchNamedIdentityPrecedesPhysicalConversion(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact-replacement", true: "unrelated-same-name-layout"}[mismatch], func(t *testing.T) {
			testProg := newEmissionTestProgram()
			const path = "example.com/emission/namedidentity"
			original := testProg.addPackage(t, path, `package namedidentity
type Value struct { Callback func() }
func Target() Value { return Value{} }
func (Value) Method() int { return 42 }
type Box[T any] struct { Callback func(T) T }
func (b Box[T]) Echo(v Value) (Box[T], Value) { return b, v }
`)
			unrelated := testProg.addPackage(t, "example.com/emission/unrelatedidentity", `package unrelatedidentity
type Value struct { Callback func() }
`)
			body := `func Target() Value { return Value{} }`
			imports := ""
			if mismatch {
				imports = `import other "example.com/emission/unrelatedidentity"`
				body = `func Target() other.Value { return other.Value{} }`
			}
			alternate := testProg.addPackage(t, abi.PatchPathPrefix+path, `package namedidentity
`+imports+`
//llgo:skipall
type PatchControl struct{}
type Value struct { Callback func() }
func (Value) Method() int { return 42 }
type Box[T any] struct { Callback func(T) T }
func (b Box[T]) Echo(v Value) (Box[T], Value) { return b, v }
`+body)
			caller := testProg.addPackage(t, "example.com/emission/namedidentitycaller", `package namedidentitycaller
import target "example.com/emission/namedidentity"
func Call() { value := target.Target(); _ = value.Method(); var box target.Box[int]; _, _ = box.Echo(value) }
`)
			testProg.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			// Force the exact case which used to create a detached physical
			// TypeName in only the original declaration's managed-symbol key.
			prog.PhysicalType(alternate.types.Scope().Lookup("Value").Type(), llssa.InGo)
			universe, err := PrepareEmissionUniverse(prog, Patches{
				path: {Alt: alternate.ssa, Types: typepatch.Clone(alternate.types)},
			}, []EmissionPackage{
				{SSA: original.ssa, Files: []*ast.File{original.file, alternate.file}},
				{SSA: unrelated.ssa, Files: []*ast.File{unrelated.file}},
				{SSA: caller.ssa, Files: []*ast.File{caller.file}},
			})
			if mismatch {
				if err == nil || !strings.Contains(err.Error(), "excluded original") {
					t.Fatalf("unrelated named replacement error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := universe.Resolve(original.ssa.Func("Target")); !ok || got != alternate.ssa.Func("Target") {
				t.Fatalf("original resolved to %v (%v), want exact patch", got, ok)
			}
			methods := 0
			for _, block := range caller.ssa.Func("Call").Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if !ok || call.Common().StaticCallee() == nil || call.Common().StaticCallee().Signature.Recv() == nil {
						continue
					}
					target, ok := universe.Resolve(call.Common().StaticCallee())
					if !ok {
						t.Fatal("method has no exact patch replacement")
					}
					if err := validateCoroStaticMethodCallOperands(call, target, universe); err != nil {
						t.Fatal(err)
					}
					methods++
				}
			}
			if methods != 2 {
				t.Fatalf("validated %d patched methods, want concrete and generic", methods)
			}
		})
	}
}

func TestCoroDispatchEnvironmentUsesPhysicalNamedLayout(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "source-to-physical", true: "unrelated-layout"}[mismatch], func(t *testing.T) {
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			pkg := prog.NewPackage("environment", "example.com/environment")
			goPkg := types.NewPackage("example.com/environment", "environment")
			logical := types.NewSignatureType(nil, nil, nil, nil, nil, false)
			named := types.NewNamed(types.NewTypeName(0, goPkg, "Context", nil), types.NewStruct([]*types.Var{
				types.NewField(0, nil, "Callback", logical, false),
			}, nil), nil)
			source := types.NewPointer(named)
			physical := prog.PhysicalType(source, llssa.InGo)
			if types.Identical(source, physical) {
				t.Fatal("fixture did not separate source from physical identity")
			}
			signature := types.NewSignatureType(nil, nil, nil, types.NewTuple(
				types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
				types.NewParam(token.NoPos, nil, "out", types.Typ[types.UnsafePointer]),
				types.NewParam(token.NoPos, nil, "env", physical),
			), types.NewTuple(types.NewParam(0, nil, "handle", types.Typ[types.UnsafePointer])), false)
			target := pkg.NewFunc("target$coro", signature, llssa.InC)
			body := target.MakeBody(1)
			body.Return(prog.Nil(prog.VoidPtr()))
			body.EndBuild()
			body.Dispose()
			var environment types.Type = source
			if mismatch {
				environment = types.NewPointer(types.NewStruct([]*types.Var{types.NewField(0, nil, "Wrong", types.Typ[types.Uint64], false)}, nil))
				defer func() {
					if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "target closure context") {
						t.Fatalf("unrelated closure environment rejection = %v", recovered)
					}
				}()
			}
			ctx := &context{prog: prog, pkg: pkg}
			ctx.newCoroDynamicDispatchEntryThunk("thunk", target.Expr, coroPlainDispatchABI{signature: logical}, coro.EmitCoroutine, environment)
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}
