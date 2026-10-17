package ssa

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestCoroDeliveryStoragePreservesPhysicalResultAndState(t *testing.T) {
	for _, target := range []struct {
		name string
		arch *Target
	}{{name: "native"}, {name: "wasm32", arch: &Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			f := newCoroDynamicDispatchTestFixture(t, target.arch)
			callback := coroPlainDispatchTestSignature(nil, nil)
			for _, test := range []struct {
				name   string
				result Type
			}{
				{"empty", f.prog.Struct()},
				{"word", f.prog.Struct(f.prog.Int32())},
				{"aligned", f.prog.Struct(f.prog.Float64())},
				{"large_function_field", f.prog.Type(types.NewStruct([]*types.Var{
					types.NewField(token.NoPos, nil, "R0", callback, false),
					types.NewField(token.NoPos, nil, "R1", types.NewArray(types.Typ[types.Byte], 131073), false),
				}, nil), InGo)},
			} {
				t.Run(test.name, func(t *testing.T) {
					layout := f.prog.NewCoroDeliveryStorage(test.result)
					if offset := layout.ResultOffset(); offset < uint64(f.prog.PointerSize()) || offset%f.prog.AlignOf(test.result) != 0 {
						t.Fatalf("result offset=%d, pointer=%d, alignment=%d", offset, f.prog.PointerSize(), f.prog.AlignOf(test.result))
					}
					if layout.ResultOffset() >= f.prog.SizeOf(layout.Type()) {
						t.Fatal("public result pointer cannot retain its containing allocation")
					}
					if f.prog.LocalGoTypeExceedsNativeStack(CoroDeliveryStorageGoType(test.result.RawType())) != f.prog.LocalAllocExceedsNativeStack(layout.Type()) {
						t.Fatal("source helper planning disagrees with physical guard allocation")
					}
					if field := f.prog.Field(layout.Type(), 1); field.ll.C != test.result.ll.C || !types.Identical(field.RawType(), test.result.RawType()) {
						t.Fatal("storage changed the exact physically lowered result")
					}
					sig := coroPlainDispatchTestSignature([]types.Type{types.Typ[types.UnsafePointer]}, []types.Type{types.Typ[types.UnsafePointer]})
					fn := f.pkg.NewFunc("state_roundtrip_"+test.name, sig, InGo)
					b := fn.MakeBody(1)
					storage := b.AllocaT(layout.Type())
					result := layout.ResultAddress(b, storage)
					b.StoreKnownNonNil(layout.StateAddress(b, result), fn.PhysicalParam(0))
					b.Return(b.LoadKnownNonNil(b.FieldAddrKnownNonNil(storage, 0)))
					b.EndBuild()
					b.Dispose()
				})
			}
			if err := llvm.VerifyModule(f.pkg.mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			options := llvm.NewPassBuilderOptions()
			defer options.Dispose()
			if err := f.pkg.mod.RunPasses("function(sroa,instcombine)", f.prog.TargetMachine(), options); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"empty", "word", "aligned", "large_function_field"} {
				body := f.pkg.mod.NamedFunction("state_roundtrip_" + name).String()
				if strings.Contains(body, "alloca ") || strings.Contains(body, "getelementptr ") || !strings.Contains(body, "ret ptr %0") {
					t.Fatalf("private state round trip did not return the original pointer:\n%s", body)
				}
			}
		})
	}
}

func TestCoroDeliveryStorageRejectsOtherLayouts(t *testing.T) {
	f := newCoroDynamicDispatchTestFixture(t)
	coroPlainDispatchMustPanicContains(t, "exact result tuple", func() {
		f.prog.NewCoroDeliveryStorage(f.prog.Int())
	})
	layout := f.prog.NewCoroDeliveryStorage(f.result)
	fn := f.pkg.NewFunc("reject_wrong_storage", coroPlainDispatchTestSignature(nil, nil), InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	wrong := b.AllocaT(f.prog.Struct(f.prog.Int32(), f.result))
	coroPlainDispatchMustPanicContains(t, "exact owned storage pointer", func() {
		layout.ResultAddress(b, wrong)
	})
	coroPlainDispatchMustPanicContains(t, "exact owned result pointer", func() {
		layout.StateAddress(b, b.AllocaT(f.prog.Struct(f.prog.Int64())))
	})
	b.Return()
	b.EndBuild()
	if err := llvm.VerifyModule(f.pkg.mod, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}
