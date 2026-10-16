//go:build !llgo

package cl

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroLibraryPredicatesValidateOnlyReachableEntries(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
func Imported()
func Unused()
func Root() { Imported() }
`)
	plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function != pkg.Func("Imported") && function != pkg.Func("Unused") {
				return coro.SSAFunctionPolicy{}, nil
			}
			return coro.SSAFunctionPolicy{IgnoreBody: true, External: coro.ExternalKnown, OverrideExternal: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bit := coro.ExecutionDomainProgramCapability()
	for _, producer := range []coro.ProgramCapabilities{0, bit} {
		for _, consumer := range []coro.ProgramCapabilities{0, bit} {
			facts := map[*ssa.Function]coro.LibraryEffectFunction{
				pkg.Func("Imported"): {ID: "imported", ProgramPredicates: coro.ProgramPredicateRequirements{Mask: bit, Values: producer}},
				// The unreachable entry deliberately conflicts with this consumer.
				pkg.Func("Unused"): {ID: "unused", ProgramPredicates: coro.ProgramPredicateRequirements{Mask: bit, Values: consumer ^ bit}},
			}
			err := validateCoroLibraryProgramPredicates(plan, facts, consumer)
			if producer == consumer {
				if err != nil {
					t.Fatalf("matching specialization (%#x) rejected: %v", producer, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "recompile the library") || !strings.Contains(err.Error(), "imported") {
				t.Fatalf("producer %#x / consumer %#x mismatch = %v", producer, consumer, err)
			}
		}
	}
}

func TestCoroLibraryPredicatesRequireFinalPlan(t *testing.T) {
	if err := validateCoroLibraryProgramPredicates(nil, nil, 0); err == nil {
		t.Fatal("missing final plan accepted")
	}
	if err := (*Compilation)(nil).ValidateCoroLibraryProgramPredicates(); err == nil {
		t.Fatal("missing compilation accepted")
	}
}

func TestCoroArchiveOnlyHelpersPropagateCapabilitiesAndPredicates(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
func Owner() {}
func Root() { Owner() }
func Dead() {}
`)
	bit := coro.ExecutionDomainProgramCapability()
	service := coro.GCObservationProgramCapability()
	for _, producer := range []coro.ProgramCapabilities{0, bit} {
		facts := []coro.LibraryEffectFunction{
			{ID: coro.FunctionID(coro.FunctionIDSchema + ":" + strings.Repeat("a", 64)), ABIHash: strings.Repeat("1", 64),
				FuncRep: coro.DirectPlain, Primary: coro.PrimaryPlain, ManagedEntry: coro.ManagedEntryPlain,
				PrimarySymbol: "archive.privateInitialize", ProgramCapabilities: service,
				ProgramPredicates: coro.ProgramPredicateRequirements{Mask: bit, Values: producer}},
			{ID: coro.FunctionID(coro.FunctionIDSchema + ":" + strings.Repeat("b", 64)), ABIHash: strings.Repeat("2", 64),
				FuncRep: coro.DirectPlain, Primary: coro.PrimaryPlain, ManagedEntry: coro.ManagedEntryPlain,
				PrimarySymbol: "archive.dead", ProgramCapabilities: bit,
				ProgramPredicates: coro.ProgramPredicateRequirements{Mask: bit, Values: producer ^ bit}},
		}
		plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
			ImportedFunctions: facts,
			ClassifyLoweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
				switch fn {
				case pkg.Func("Owner"):
					return []coro.SSALoweredCall{{LogicalName: "delivery.initialize", ImportedTarget: facts[0].ID}}, nil
				case pkg.Func("Dead"):
					return []coro.SSALoweredCall{{LogicalName: "delivery.dead", ImportedTarget: facts[1].ID}}, nil
				}
				return nil, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		masks := make(map[*ssa.Function]coro.ProgramCapabilities)
		capabilities, err := deriveCoroFunctionProgramCapabilities(plan, nil, nil, masks)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range []*ssa.Function{pkg.Func("Root"), pkg.Func("Owner")} {
			if capabilities[fn] != service || masks[fn] != bit {
				t.Fatalf("%s: service=%#x mask=%#x, want exact archive facts", fn.Name(), capabilities[fn], masks[fn])
			}
		}
		if capabilities[pkg.Func("Dead")] != 0 || masks[pkg.Func("Dead")] != 0 {
			t.Fatal("unreachable archive leaf retained optional services")
		}
		if discovered, err := DiscoverCoroProgramPredicates(plan, nil); err != nil || discovered != service&coro.ProgramPredicateMask() {
			t.Fatalf("archive invocation retained unrelated program services: %#x: %v", discovered, err)
		}
		for _, consumer := range []coro.ProgramCapabilities{0, bit} {
			err := validateCoroLibraryProgramPredicates(plan, nil, consumer)
			if producer == consumer && err != nil {
				t.Fatalf("matching archive predicate rejected: %v", err)
			}
			if producer != consumer && (err == nil || !strings.Contains(err.Error(), "recompile the library")) {
				t.Fatalf("archive false/true predicate mismatch was ignored: %v", err)
			}
		}
	}
}
