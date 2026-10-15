//go:build !llgo

package cl

import (
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPromotedMethodWrapperSSAProjectionShape(t *testing.T) {
	const source = `package foo
type Inner struct{ Value int }
func (inner *Inner) Child(delta int) int { return inner.Value + delta }
type Outer struct {
	Prefix int
	*Inner
}
type Child interface { Child(int) int }
func Parent(value Outer, pointer *Outer) int {
	var first Child = value
	var second Child = pointer
	return first.Child(1) + second.Child(2)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var valueWrapper, pointerWrapper, target *ssa.Function
	for _, function := range universe.Functions() {
		if function == nil || !strings.HasPrefix(function.Synthetic, "wrapper for ") || function.Name() != "Child" {
			continue
		}
		receiver := function.Signature.Recv().Type()
		if _, pointer := types.Unalias(receiver).Underlying().(*types.Pointer); pointer {
			pointerWrapper = function
		} else {
			valueWrapper = function
		}
		for _, instruction := range function.Blocks[0].Instrs {
			call, isCall := instruction.(*ssa.Call)
			if isCall && call.Common() != nil && call.Common().StaticCallee() != nil {
				target = call.Common().StaticCallee()
			}
		}
	}
	if valueWrapper == nil || pointerWrapper == nil || target == nil {
		t.Fatalf("promoted wrappers = value:%v pointer:%v target:%v", valueWrapper, pointerWrapper, target)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: ssaPkg.Func("Parent"), Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == target {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, NeedsDispatch: true}, nil
			}
			if function == valueWrapper || function == pointerWrapper {
				return coro.SSAFunctionPolicy{NeedsDispatch: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()

	valuePhysical, err := universe.coroProgramIR.physicalFunctionPlan(
		valueWrapper, universe.ownerOf(valueWrapper),
	)
	if err != nil {
		t.Fatal(err)
	}
	pointerPhysical, err := universe.coroProgramIR.physicalFunctionPlan(
		pointerWrapper, universe.ownerOf(pointerWrapper),
	)
	if err != nil {
		t.Fatal(err)
	}
	if valuePhysical.tailForward == nil || valuePhysical.tailForward.target != target ||
		len(valuePhysical.tailForward.args) == 0 ||
		len(valuePhysical.tailForward.args[0].fieldPath) != 1 ||
		valuePhysical.tailForward.args[0].fieldPath[0] != 1 {
		t.Fatalf("value promoted wrapper tail-forward plan = %+v", valuePhysical.tailForward)
	}
	if pointerPhysical.tailForward != nil {
		t.Fatalf("nullable pointer promoted wrapper escaped into frame-free projection: %+v", pointerPhysical.tailForward)
	}
	physicalName := universe.physicalNames[emissionFunctionOwnerKey{
		function: valueWrapper,
		owner:    universe.ownerOf(valueWrapper),
	}]
	valueIR := module.NamedFunction(physicalName + "$coro").String()
	if strings.Contains(valueIR, "llvm.coro.") || strings.Contains(valueIR, coroFrameAllocHookV1) ||
		!strings.Contains(valueIR, "extractvalue") || strings.Count(valueIR, "call ptr") != 1 {
		t.Fatalf("value promoted wrapper was not reduced to one projected tail-forward ramp:\n%s", valueIR)
	}
	runCoroABITestPipeline(t, prog, module)
	for _, suffix := range []string{".resume", ".destroy"} {
		if !module.NamedFunction(physicalName + "$coro" + suffix).IsNil() {
			t.Fatalf("frame-free promoted wrapper acquired split %s entry:\n%s", suffix, module.String())
		}
	}
}
