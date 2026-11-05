package ssa

import (
	"go/build"
	"go/importer"
	"go/token"
	"go/types"
	"testing"

	rtabi "github.com/xgo-dev/llgo/runtime/abi"
	"github.com/xgo-dev/llvm"
)

func TestWasmReflectMethodExpressionGoRoleRegistration(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs)
	before := build.Default
	build.Default.GOOS, build.Default.GOARCH = "wasip1", "wasm"
	build.Default.BuildTags = []string{"llgo", "llgo.wasi_threads"}
	t.Cleanup(func() { build.Default = before })
	runtimePackage, err := importer.For("source", nil).Import(PkgRuntime)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zero-sized", "value", "pointer", "variadic"} {
		t.Run(name, func(t *testing.T) {
			prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm", WasmProfile: "w32", WasmProvider: "wasi", WasmReflectBridges: true})
			defer prog.Dispose()
			prog.SetRuntime(runtimePackage)
			if prog.GoWordSize() != 8 || prog.PointerSize() != 4 {
				t.Fatal("fixture must retain Memory32 addresses and eight-byte Go words")
			}
			goPackage := types.NewPackage("example.com/methodrole", "methodrole")
			var underlying types.Type = types.Typ[types.Int]
			if name == "zero-sized" {
				underlying = types.NewStruct(nil, nil)
			}
			named := types.NewNamed(types.NewTypeName(token.NoPos, goPackage, "Receiver", nil), underlying, nil)
			var receiver types.Type = named
			if name == "pointer" {
				receiver = types.NewPointer(named)
			}
			var parameter types.Type = types.Typ[types.Int]
			if name == "variadic" {
				parameter = types.NewSlice(parameter)
			}
			method := types.NewSignatureType(types.NewVar(token.NoPos, goPackage, "r", receiver), nil, nil,
				types.NewTuple(types.NewParam(token.NoPos, goPackage, "values", parameter)),
				types.NewTuple(types.NewParam(token.NoPos, goPackage, "", types.Typ[types.Int])), name == "variadic")
			named.AddMethod(types.NewFunc(token.NoPos, goPackage, "Apply", method))
			pkg := prog.NewPackageEx("methodrole", goPackage.Path(), true)
			fn := pkg.NewFunc("use", NoArgsNoRet, GoDecl)
			b := fn.MakeBody(1)
			defer b.Dispose()
			b.abiType(receiver)
			b.Return()
			b.EndBuild()

			// reflect.Type.Method constructs FuncOf(receiver, parameters...).
			// FuncOf's result is a Go funcval, not a native C code pointer.
			expression := methodExprSignature(method)
			goRole := prog.Closure(expression).RawType()
			goName, _ := prog.abi.TypeName(goRole)
			descriptor := pkg.VarOf(goName)
			if descriptor == nil || descriptor.impl.Initializer().IsNil() {
				t.Fatalf("method expression has no registered Go-role descriptor %s", expression)
			}
			initial := descriptor.impl.Initializer()
			common := prog.rtNamed("_type").Underlying().(*types.Struct)
			flags := initial.Operand(0).Operand(structFieldIndex(common, "TFlag")).ZExtValue()
			if flags&uint64(rtabi.TFlagClosure) == 0 {
				t.Fatal("reflected method expression descriptor lost its Go callable role")
			}
			bridges := pkg.wasmReflectBridge(expression)
			functionType := prog.rtNamed("functype").Underlying().(*types.Struct)
			for _, field := range []struct {
				name string
				want llvm.Value
			}{{"Call_", bridges.call.impl}, {"Make_", bridges.make.impl}} {
				actual := mapMetadataPointer(t, initial.Operand(structFieldIndex(functionType, field.name)))
				if actual.IsNull() || actual != field.want {
					t.Fatalf("Go method expression %s has an incorrect %s bridge", expression, field.name)
				}
			}
			// Explicit native metadata remains a separate role even when the
			// source parameter/result types are otherwise identical.
			nativeName, _ := prog.abi.TypeName(expression)
			if nativeName == goName || prog.abi.TFlag(expression)&rtabi.TFlagClosure != 0 {
				t.Fatal("native signature was relabeled as a Go function")
			}
			_, entry := b.abiMethodFuncs(receiver, types.NewMethodSet(receiver).At(0))
			if err := pkg.FinishMetaCollection(); err != nil {
				t.Fatal(err)
			}
			defer pkg.Meta.Close()
			if !hasOrdinaryEdge(pkg.Meta, entry.Name(), goName) {
				t.Fatal("method entry does not retain its exact Go-role descriptor through DCE")
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}
