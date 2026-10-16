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
