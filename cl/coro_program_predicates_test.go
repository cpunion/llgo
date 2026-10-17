//go:build !llgo

package cl

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroProgramPredicateRemovesGuardedEffectsBeforeAnalysis(t *testing.T) {
	t.Run("ExecutionDomain", func(t *testing.T) {
		testCoroProgramPredicateRemovesGuardedEffects(t, "coroExecutionDomainEnabled", coro.ExecutionDomainProgramCapability())
	})
	t.Run("GCObservation", func(t *testing.T) {
		testCoroProgramPredicateRemovesGuardedEffects(t, "coroGCObservationEnabled", coro.GCObservationProgramCapability())
	})
}

func testCoroProgramPredicateRemovesGuardedEffects(t *testing.T, intrinsic string, bit coro.ProgramCapabilities) {
	for _, body := range []string{
		"if Enabled() { Block(ch) }",
		"if !Enabled() { return }; Block(ch)",
		"if Enabled() == true { Block(ch) }",
		"if Enabled() { for { Sink++ } }",
		"for Enabled() {}",
		"if Enabled() { fn() }",
		"if Enabled() { go Block(ch) }",
		"if Enabled() { defer Block(ch) }",
		"if Enabled() { iface.Call() }",
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(body+map[bool]string{false: "/false", true: "/true"}[enabled], func(t *testing.T) {
				ssaPkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"
//go:linkname Enabled llgo.`+intrinsic+`
func Enabled() bool
var Sink int
type I interface { Call() }
func Block(ch chan int) { <-ch }
func Guarded(ch chan int, fn func(), iface I) { `+body+` }
func Root(ch chan int, fn func(), iface I) { Guarded(ch, fn, iface) }
func Independent() int { return 1 }
`)
				prog := newLLSSAProg(t)
				defer prog.Dispose()
				binding := coro.ProgramPredicateRequirements{Mask: bit}
				if enabled {
					binding.Values = binding.Mask
				}
				universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{CoroProgramPredicates: binding})
				if err != nil {
					t.Fatal(err)
				}
				ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
				if err != nil {
					t.Fatal(err)
				}
				config := coro.SSAConfig{
					EmissionUniverse: ssaUniverse, FunctionIDs: universe.FunctionIDConfig(),
					ClassifyLocalBody: universe.CoroLocalBodyFacts,
					ClassifyElidedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
						frozen, _, err := universe.CoroCallSitePlan(site)
						return frozen.Elision != CoroCallElidedFrontendUnevaluated && frozen.ElidesCall(), err
					},
					ClassifyUnevaluatedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
						frozen, _, err := universe.CoroCallSitePlan(site)
						return frozen.Elision == CoroCallElidedFrontendUnevaluated, err
					},
				}
				plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: ssaPkg.Func("Root"), Demand: coro.AsyncDemand}}, config)
				if err != nil {
					t.Fatal(err)
				}
				guarded, _ := plan.FunctionPlan(ssaPkg.Func("Guarded"))
				if !enabled && (guarded.Effect != coro.NoSuspend || guarded.Emission != coro.EmitPlain || guarded.Exec.Contains(coro.NeedsPreempt)) {
					t.Fatalf("disabled guard retained effect/ABI: %+v", guarded)
				}
				if enabled && body != "if Enabled() { go Block(ch) }" && guarded.Effect == coro.NoSuspend {
					t.Fatalf("enabled guard lost its suspension: %+v", guarded)
				}
				if body == "if Enabled() { go Block(ch) }" {
					child, _ := plan.FunctionPlan(ssaPkg.Func("Block"))
					if (child.Emission != coro.EmitNone) != enabled {
						t.Fatalf("spawn demand disagrees with predicate: %+v", child)
					}
					if !enabled {
						if err := validateCoroPhysicalConsumersCapabilities(plan, universe, true, true, true); err != nil {
							t.Fatalf("physical consumers revived an unevaluated spawn: %v", err)
						}
					}
					err := validateCoroPhysicalConsumersCapabilities(plan, universe, true, false, true)
					if !enabled && err != nil {
						t.Fatalf("unevaluated spawn required scheduler capability: %v", err)
					}
					if enabled && err == nil {
						t.Fatal("live spawn lost its scheduler capability gate")
					}
				}
				masks := universe.coroProgramIR.coroLocalProgramPredicateMasks()
				if _, err := deriveCoroFunctionProgramCapabilities(plan, nil, nil, masks); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"Guarded", "Root"} {
					if masks[ssaPkg.Func(name)] != binding.Mask {
						t.Fatalf("%s lost transitive predicate dependency: %v", name, masks)
					}
				}
				if masks[ssaPkg.Func("Independent")] != 0 {
					t.Fatal("unrelated function acquired a specialization constraint")
				}
			})
		}
	}
}

func TestCoroProgramPredicateDiscoveryAndFreezeAreSeparate(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"
//go:linkname Enabled llgo.coroExecutionDomainEnabled
func Enabled() bool
func Root() { if Enabled() { for {} } }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	inputs := []EmissionPackage{{SSA: ssaPkg, Files: files}}
	unknown, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := unknown.CoroLocalBodyFacts(ssaPkg.Func("Root"))
	if err != nil || !facts.HasCycle {
		t.Fatalf("discovery must remain conservative: %+v, %v", facts, err)
	}
	if _, err := (&Compilation{EmissionUniverse: unknown}).CoroProgramPredicateValues(); err == nil {
		t.Fatal("unknown predicate was treated as false")
	}
	// The real build driver repeats preparation on these same SSA/package
	// identities, without cloning or mutating source bodies.
	frozen, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{
		CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ExecutionDomainProgramCapability()},
	})
	if err != nil {
		t.Fatal(err)
	}
	facts, err = frozen.CoroLocalBodyFacts(ssaPkg.Func("Root"))
	if err != nil || facts.HasCycle {
		t.Fatalf("frozen false predicate retained loop: %+v, %v", facts, err)
	}
	if values, err := (&Compilation{EmissionUniverse: frozen}).CoroProgramPredicateValues(); err != nil || values != 0 {
		t.Fatalf("frozen predicate = %v, %v", values, err)
	}
}
