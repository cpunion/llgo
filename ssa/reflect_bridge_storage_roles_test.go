package ssa

import (
	"go/build"
	"go/importer"
	"go/token"
	"go/types"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestWasmReflectBridgeSourceStorageRoles(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs)
	before := build.Default
	build.Default.GOOS, build.Default.GOARCH = "wasip1", "wasm"
	build.Default.BuildTags = []string{"llgo", "llgo.wasi_threads"}
	t.Cleanup(func() { build.Default = before })
	runtimePackage, err := importer.For("source", nil).Import(PkgRuntime)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uintptr", "fixed-slice", "variadic", "pointer"} {
		t.Run(name, func(t *testing.T) {
			prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm", WasmProfile: "w32", WasmProvider: "wasi", WasmReflectBridges: true})
			defer prog.Dispose()
			prog.SetRuntime(runtimePackage)
			if prog.GoWordSize() != 8 || prog.PointerSize() != 4 {
				t.Fatal("fixture requires Memory32 physical addresses and eight-byte Go storage")
			}
			var in, out types.Type = types.Typ[types.Uintptr], types.Typ[types.Uintptr]
			if name == "fixed-slice" || name == "variadic" {
				in = types.NewSlice(in)
			} else if name == "pointer" {
				in, out = types.Typ[types.UnsafePointer], types.Typ[types.UnsafePointer]
			}
			sig := types.NewSignatureType(nil, nil, nil,
				types.NewTuple(types.NewParam(token.NoPos, nil, "value", in)),
				types.NewTuple(types.NewParam(token.NoPos, nil, "", out)), name == "variadic")
			pkg := prog.NewPackage("p", "example.com/reflectrole")
			fn := pkg.NewFunc("use", NoArgsNoRet, GoDecl)
			b := fn.MakeBody(1)
			defer b.Dispose()
			// Derive both roles through the real type metadata producer. Native
			// first makes a missing role/storage cache discriminator observable.
			native := b.abiType(sig)
			goValue := b.abiType(prog.Closure(sig).RawType())
			b.Return()
			b.EndBuild()
			ft := prog.rtNamed("functype").Underlying().(*types.Struct)
			bridge := func(value Expr, field string) Function {
				entry := mapMetadataPointer(t, value.impl.Initializer().Operand(structFieldIndex(ft, field)))
				if entry.IsNull() {
					return nil
				}
				return pkg.FuncOf(entry.Name())
			}
			goCall, goMake := bridge(goValue, "Call_"), bridge(goValue, "Make_")
			nativeCall, nativeMake := bridge(native, "Call_"), bridge(native, "Make_")
			if goCall == nil || goMake == nil || nativeCall == nil {
				t.Fatal("actual callable metadata lacks its typed bridge")
			}
			if goMake == nativeMake {
				t.Fatal("Go eight-byte frame reused a native four-byte storage-role bridge")
			}
			for _, role := range []struct {
				name string
				fn   Function
				bits int
			}{{"Go", goCall, 64}, {"native", nativeCall, 32}} {
				count := 0
				for block := role.fn.impl.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
					for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
						call := inst.IsACallInst()
						if call.IsNil() || call.CalledValue() != role.fn.Param(0).impl {
							continue
						}
						count++
						result := call.CalledFunctionType().ReturnType()
						if name == "pointer" {
							if result.TypeKind() != llvm.PointerTypeKind {
								t.Fatal("address-valued result acquired numeric semantics")
							}
						} else if result.TypeKind() != llvm.IntegerTypeKind || result.IntTypeWidth() != role.bits {
							t.Fatalf("%s indirect result is %s, want i%d", role.name, result, role.bits)
						}
					}
				}
				if count != 2 {
					t.Fatalf("%s bridge lost env/no-env entries: %d", role.name, count)
				}
			}
			for block := goMake.impl.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
				for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
					if inst.InstructionOpcode() != llvm.Ret {
						continue
					}
					value := inst.Operand(0)
					if value.InstructionOpcode() == llvm.IntToPtr {
						value = value.Operand(0)
					}
					if value.InstructionOpcode() == llvm.Trunc {
						value = value.Operand(0)
					}
					load := value.IsALoadInst()
					if load.IsNil() || load.Operand(0).IsAAllocaInst().IsNil() || prog.TargetData().TypeAllocSize(load.Operand(0).AllocatedType()) != 8 {
						t.Fatalf("Go MakeFunc result does not load its full eight-byte frame slot:\n%s", goMake.impl)
					}
				}
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWasmReflectNativeVariadicMetadataHasNoMakeEntry(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs)
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm", WasmProfile: "w32", WasmProvider: "wasi", WasmReflectBridges: true})
	defer prog.Dispose()
	setTestRuntime(t, prog)
	pkg := prog.NewPackage("p", "example.com/nativevararg")
	sig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "fixed", types.Typ[types.Uintptr]), VArg()), nil, true)
	bridges := pkg.wasmReflectBridge(sig, CDecl)
	if bridges.call == nil || bridges.make != nil {
		t.Fatal("native variadic metadata must keep its call role without supplying MakeFunc")
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}
