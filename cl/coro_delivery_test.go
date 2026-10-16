//go:build !llgo

package cl

import (
	"fmt"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

const coroDeliverySourceFixture = `package foo
import "unsafe"
type Result = struct { R0 int; R1 error }
func initFD(out *Result) { out.R0 = -1; out.R1 = nil }
func dropFD(out *Result, _ unsafe.Pointer, _ string) { out.R0 = -1 }
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result, unsafe.Pointer, string)) *Result
func Acquire(path string) (int, error) {
	out := borrow(initFD, dropFD)
	return out.R0, out.R1
}
func Root() { Acquire("fixture") }
`

func TestCoroDeliverySourceFreezesExactTypedHelperIdentities(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, coroDeliverySourceFixture)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	producer := pkg.Func("Acquire")
	recipe, found, err := universe.coroProgramIR.sourceDelivery(producer)
	if err != nil || !found || recipe.producer != producer || recipe.initializer != pkg.Func("initFD") ||
		recipe.drop != pkg.Func("dropFD") || recipe.borrow == nil || recipe.result.NumFields() != 2 {
		t.Fatalf("source recipe=%+v present=%t error=%v", recipe, found, err)
	}
	initializerResult := recipe.initializer.Signature.Params().At(0).Type().(*types.Pointer).Elem()
	if !types.Identical(recipe.result, initializerResult) ||
		universe.emissionTypeKeys.strictABI(recipe.result) != universe.emissionTypeKeys.strictABI(initializerResult) {
		t.Fatal("Go-identical canonical and source-expressible result tuples have different strict ABI keys")
	}
	site, frozen, err := universe.CoroCallSitePlan(recipe.borrow)
	if err != nil || !frozen || !site.Intrinsic || !site.ElidesCall() ||
		site.IntrinsicSemantics != CoroIntrinsicCallInlineWithLoweredCalls {
		t.Fatalf("borrow site=%+v frozen=%t error=%v", site, frozen, err)
	}
	roots, err := universe.intrinsicCallValueRoots(llgoCoroBorrowResult, recipe.borrow.Common().Args, fnNormal)
	if err != nil || len(roots) != 2 || !roots[0].directFunction || !roots[1].directFunction ||
		roots[0].value != recipe.initializer || roots[1].value != recipe.drop {
		t.Fatalf("helper identities became runtime function values: roots=%+v error=%v", roots, err)
	}
	copy := recipe
	copy.drop = pkg.Func("Root")
	if again, _, _ := universe.coroProgramIR.sourceDelivery(producer); again.drop != recipe.drop {
		t.Fatal("source recipe lookup exposed mutable producer state")
	}
	if ordinary, found, err := universe.coroProgramIR.sourceDelivery(pkg.Func("Root")); err != nil || found || ordinary.producer != nil {
		t.Fatal("calling an acquisition inferred resource ownership for an ordinary wrapper")
	}
	if err := universe.coroProgramIR.freezeDeliveryProducer(recipe); err == nil {
		t.Fatal("source recipe remained mutable after source freeze")
	}
	lowered, err := universe.CoroLoweredCalls(producer)
	if err != nil || len(lowered) != 2 ||
		lowered[0].LogicalName != coroDeliveryDropCall || lowered[0].Target != recipe.drop ||
		lowered[1].LogicalName != coroDeliveryInitializeCall || lowered[1].Target != recipe.initializer {
		t.Fatalf("hidden helper calls were not frozen in the ordinary call graph: %+v error=%v", lowered, err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	var plan *coro.SSAPlan
	for _, asyncHelper := range []*ssa.Function{recipe.initializer, recipe.drop} {
		plan, err = coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse: ssaUniverse, FunctionIDs: functionIDs, MaxPlainInstructions: -1,
			OutcomeMode:       coro.OutcomeExplicitStatus,
			ClassifyLocalBody: universe.CoroLocalBodyFacts,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == asyncHelper {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, Exec: coro.NeedsRuntimeContext}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
				site, found, err := universe.CoroCallSitePlan(call)
				return found && site.ElidesCall(), err
			},
			ClassifyStaticFunctionReferenceCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
				return universe.CoroStaticFunctionReferenceCallArgument(call, argument)
			},
			ClassifyLoweredCalls: universe.CoroLoweredCalls,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, function := range []*ssa.Function{producer, pkg.Func("Root")} {
			fact, found := plan.FunctionPlan(function)
			if !found || !fact.Effect.Contains(coro.AwaitStructured) || !fact.Exec.Contains(coro.NeedsRuntimeContext) {
				t.Fatalf("hidden %s did not automatically color %s: %+v present=%t", asyncHelper, function, fact, found)
			}
		}
	}
	for _, function := range []*ssa.Function{recipe.initializer, recipe.drop} {
		fact, found := plan.FunctionPlan(function)
		if !found || fact.FuncRep == coro.Dispatch {
			t.Fatalf("compile-time helper reference required a runtime descriptor for %s: %+v", function, fact)
		}
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	diagnostic := func() (diagnostic string) {
		defer func() {
			if failure := recover(); failure != nil {
				diagnostic = fmt.Sprint(failure)
			}
		}()
		compiled, _, err := NewPackageExWithEmbedOptions(
			prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
		)
		if compiled != nil {
			defer compiled.Module().Dispose()
		}
		if err != nil {
			return err.Error()
		}
		return ""
	}()
	if !strings.Contains(diagnostic, "bound physical resource-delivery guard") {
		t.Fatalf("unprotected result slot was exposed or rejected by another gate: %s", diagnostic)
	}
}

func TestCoroDeliverySourceRejectsUnboundOrInexactShapes(t *testing.T) {
	for _, test := range []struct {
		name, old, replacement, diagnostic string
	}{
		{"defined-result", "type Result = struct", "type Result struct", "exact typed result"},
		{"private-result", "R0", "r0", "exact typed result"},
		{"indirect-init", "borrow(initFD, dropFD)", "borrow(initializer, dropFD)", "exact context-free function"},
		{"captured-init", "borrow(initFD, dropFD)", "borrow(func(out *Result) { out.R0 = len(path) }, dropFD)", "exact context-free function"},
		{"non-entry", "out := borrow", "if path == \"\" { return 0, nil }; out := borrow", "source entry block"},
		{"duplicate", "out := borrow(initFD, dropFD)", "out := borrow(initFD, dropFD); _ = borrow(initFD, dropFD)", "conflicting or repeated"},
		{"foreign-drop", "func dropFD", "//go:linkname dropFD C.llgo_test_delivery_drop\nfunc dropFD", "managed Go helper"},
		{"invented-fd", "return out.R0, out.R1", "return 0, out.R1", "load each exact borrowed result field"},
		{"changed-fd", "return out.R0, out.R1", "return out.R0 + 1, out.R1", "load each exact borrowed result field"},
		{"invented-error", "return out.R0, out.R1", "return out.R0, nil", "load each exact borrowed result field"},
		{"stale-fd", "return out.R0, out.R1", "fd := out.R0; out.R0 = -1; return fd, out.R1", "followed only by the remaining projections"},
		{"await-after-load", "return out.R0, out.R1", "fd := out.R0; dropFD(out, nil, path); return fd, out.R1", "followed only by the remaining projections"},
		{"cross-block-load", "return out.R0, out.R1", "fd := out.R0; if path == \"\" { return fd, out.R1 }; return out.R0, out.R1", "load each exact borrowed result field"},
		{"call-before-borrow", "out := borrow", "initFD(nil); out := borrow", "borrow must precede source calls"},
		{"defer-before-borrow", "out := borrow", "defer initFD(nil); out := borrow", "borrow must precede source calls"},
		{"go-before-borrow", "out := borrow", "go initFD(nil); out := borrow", "borrow must precede source calls"},
		{"cleanup-after-load", "return out.R0, out.R1", "defer dropFD(out, nil, path); return out.R0, out.R1", "exact borrowed result field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := strings.ReplaceAll(coroDeliverySourceFixture, test.old, test.replacement)
			if test.name == "indirect-init" {
				source += "\nvar initializer = initFD\n"
			}
			pkg, _, files := buildGoSSAPkg(t, source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err == nil {
				// Shape diagnostics are frozen on the exact call rather than
				// rediscovered by a later analysis or physical emitter.
				for _, block := range pkg.Func("Acquire").Blocks {
					for _, instruction := range block.Instrs {
						call, ok := instruction.(*ssa.Call)
						if !ok || call.Common().StaticCallee() != pkg.Func("borrow") {
							continue
						}
						_, _, err = universe.CoroCallSitePlan(call)
						if err != nil {
							break
						}
					}
					if err != nil {
						break
					}
				}
			}
			if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf("diagnostic=%v, want %q", err, test.diagnostic)
			}
		})
	}
}

func TestCoroDeliverySourceAcceptsFreshProjectionsAfterOrdinaryWork(t *testing.T) {
	for _, body := range []string{
		"dropFD(out, nil, path); return out.R0, out.R1",
		"if path == \"\" { out.R0 = 0; return out.R0, out.R1 }; out.R0 = 1; return out.R0, out.R1",
	} {
		pkg, _, files := buildGoSSAPkg(t, strings.Replace(coroDeliverySourceFixture, "return out.R0, out.R1", body, 1))
		prog := newLLSSAProg(t)
		universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
		if err == nil {
			_, present, lookupErr := universe.coroProgramIR.sourceDelivery(pkg.Func("Acquire"))
			if lookupErr != nil || !present {
				t.Errorf("fresh borrowed-result projection was not frozen: present=%t error=%v", present, lookupErr)
			}
		} else {
			t.Errorf("ordinary producer work rejected: %v", err)
		}
		prog.Dispose()
	}
}
