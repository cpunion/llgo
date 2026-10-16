//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// Use real ProgramIR constant-CFG pruning and the SSA effect/call closure in
// every round, not a synthetic list of capability bits. The build driver owns
// advancement and rejects a decreasing sequence; this gate proves the source
// semantics which that convergence layer must consume.
func TestCoroProgramPredicateFixedPointSourceSemantics(t *testing.T) {
	gc, domain := coro.GCObservationProgramCapability(), coro.ExecutionDomainProgramCapability()
	for _, test := range []struct {
		name, body string
		values     []coro.ProgramCapabilities
		decreasing bool
	}{
		{"self-gated-registration-stays-disabled", "if GCEnabled() { EnableGCObserver() }", []coro.ProgramCapabilities{0, 0}, false},
		{"domain-seed-enables-gc-next-round", "synctest.Run(); if DomainEnabled() { EnableGCObserver() }", []coro.ProgramCapabilities{0, domain, domain | gc, domain | gc}, false},
		{"negative-guard-is-not-monotone", "if !GCEnabled() { EnableGCObserver() }", []coro.ProgramCapabilities{0, gc, 0}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := newEmissionTestProgram()
			program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			synctest := program.addPackage(t, "internal/synctest", `package synctest; func Run() {}`)
			runtime := program.addPackage(t, "github.com/xgo-dev/llgo/runtime/internal/runtime", `package runtime
import (
	_ "unsafe"
	"internal/synctest"
)
//go:linkname GCEnabled llgo.coroGCObservationEnabled
func GCEnabled() bool
//go:linkname DomainEnabled llgo.coroExecutionDomainEnabled
func DomainEnabled() bool
func EnableGCObserver() {}
func dead() { synctest.Run() }
func Root() { `+test.body+` }
`)
			program.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			inputs := []EmissionPackage{
				{SSA: synctest.ssa, Files: []*ast.File{synctest.file}},
				{SSA: runtime.ssa, Files: []*ast.File{runtime.file}},
			}
			for round, previous := range test.values[:len(test.values)-1] {
				universe, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{
					CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask(), Values: previous},
				})
				if err != nil {
					t.Fatal(err)
				}
				emission, err := coro.NewSSAEmissionUniverse(program.ssa, universe.Functions())
				if err != nil {
					t.Fatal(err)
				}
				plan, err := coro.AnalyzeSSA(program.ssa, coro.Roots{{Function: runtime.ssa.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
					EmissionUniverse: emission, FunctionIDs: universe.FunctionIDConfig(),
					ClassifyLocalBody: universe.CoroLocalBodyFacts,
					ClassifyElidedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
						frozen, _, err := universe.CoroCallSitePlan(site)
						return frozen.Elision != CoroCallElidedFrontendUnevaluated && frozen.ElidesCall(), err
					},
					ClassifyUnevaluatedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
						frozen, _, err := universe.CoroCallSitePlan(site)
						return frozen.Elision == CoroCallElidedFrontendUnevaluated, err
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				discovered, err := DiscoverCoroProgramPredicates(plan, nil)
				if err != nil || discovered != test.values[round+1] {
					t.Fatalf("round %d: %#x -> %#x, %v; want %#x", round, previous, discovered, err, test.values[round+1])
				}
				lost := previous &^ discovered
				if (lost != 0) != (test.decreasing && round == len(test.values)-2) {
					t.Fatalf("round %d: unexpected monotonicity result %#x", round, lost)
				}
			}
		})
	}
}
