package ssa

import (
	"go/types"
	"testing"
)

func TestPreLLVMQueriesUsePhysicalTupleAndClosureLayout(t *testing.T) {
	Initialize(InitAll)
	for _, target := range []*Target{nil, {GOOS: "wasip1", GOARCH: "wasm"}} {
		prog := NewProgram(target)
		if target != nil && target.GOARCH == "wasm" {
			// Match build.llgoTargetTypeSizes: LLGo wasm uses 32-bit words
			// and 8-byte i64 alignment; gc/wasm uses 64-bit source words.
			prog.TypeSizes(&types.StdSizes{WordSize: 4, MaxAlign: 8})
		}
		prog.SetRuntime(RuntimeTypesForTest(t))
		signature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
		callbackFields := []*types.Var{
			types.NewField(0, nil, "Callbacks", types.NewArray(signature, 3), false),
			types.NewField(0, nil, "Last", types.Typ[types.Int32], false),
		}
		callbackStruct := types.NewStruct(callbackFields, nil)
		callbackTuple := types.NewTuple(callbackFields...)
		largeFields := []*types.Var{
			types.NewField(0, nil, "FD", types.Typ[types.Int32], false),
			types.NewField(0, nil, "Data", types.NewArray(types.Typ[types.Byte], 131073), false),
			types.NewField(0, nil, "Owner", types.NewPointer(types.NewArray(types.Typ[types.Byte], 128)), false),
		}
		largeStruct := types.NewStruct(largeFields, nil)
		largeTuple := types.NewTuple(largeFields...)
		for _, test := range []struct {
			source   types.Type
			physical *types.Struct
		}{
			{callbackStruct, callbackStruct}, {callbackTuple, callbackStruct}, {largeTuple, largeStruct},
		} {
			// Query first: these APIs must work before LLVM materialization.
			size := prog.PhysicalSizeOfGoType(test.source)
			align := prog.PhysicalAlignOfGoType(test.source)
			physical := prog.Type(test.physical, InGo)
			if size != int64(prog.SizeOf(physical)) || align != int64(prog.AlignOf(physical)) {
				t.Fatalf("%v: pre-LLVM size/alignment %d/%d, physical %d/%d", target, size, align, prog.SizeOf(physical), prog.AlignOf(physical))
			}
		}
		if !prog.LocalGoTypeExceedsNativeStack(largeTuple) {
			t.Fatal("large delivery tuple lost its managed-slot allocation")
		}
		prog.Dispose()
	}
}
