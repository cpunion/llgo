//go:build !llgo

package cl

import (
	"slices"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroInterfaceCallFormsFreezeReceiverHelperAtSource(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/invocations", `package invocations
type I interface { M() }
func Plain() {}
func Use(value I) {
	value.M(); go value.M(); defer value.M()
	Plain(); go Plain(); defer Plain()
}
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe.prog = prog
	fn := pkg.ssa.Func("Use")
	ctx, err := universe.functionABIContext(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	var invokes, direct int
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			helpers := universe.loweredRuntimeHelpers(ctx, instruction)
			if got, want := slices.Contains(helpers, "IfacePtrData"), call.Common().IsInvoke(); got != want {
				t.Fatalf("%T %s receiver helper = %t, want %t; frozen helpers=%v", instruction, instruction, got, want, helpers)
			}
			if call.Common().IsInvoke() {
				invokes++
				for _, dynamic := range []bool{false, true} {
					shape := coroEmissionFunctionShape{dynamicCleanup: dynamic}
					if shape.helperPlacement(instruction, "IfacePtrData") != coroRuntimeHelperAtSource {
						t.Fatalf("%T moved receiver evaluation away from its source site", instruction)
					}
				}
			} else {
				direct++
			}
		}
	}
	if invokes != 3 || direct != 3 {
		t.Fatalf("call forms = %d invokes/%d direct, want call/go/defer each", invokes, direct)
	}
}
