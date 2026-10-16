package cl

import (
	"fmt"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// ValidateCoroLibraryProgramPredicates must run after the final reachable plan
// and predicate values are frozen, but before publishing any consumer code.
// An incompatible producer needs recompilation; silently recoloring its
// bodyless declaration cannot repair an already-specialized physical entry.
func (c *Compilation) ValidateCoroLibraryProgramPredicates() error {
	if c == nil {
		return fmt.Errorf("library predicate validation requires a compilation")
	}
	plan := c.immutablePlan()
	if len(c.CoroLibraryEffects) == 0 && (plan == nil || len(plan.ImportedFunctions()) == 0) {
		return nil
	}
	values, err := c.CoroProgramPredicateValues()
	if err != nil {
		return err
	}
	return validateCoroLibraryProgramPredicates(plan, c.CoroLibraryEffects, values)
}

func validateCoroLibraryProgramPredicates(plan *coro.SSAPlan, effects map[*ssa.Function]coro.LibraryEffectFunction, values coro.ProgramCapabilities) error {
	if plan == nil || !values.Valid() {
		return fmt.Errorf("library predicate validation requires a final plan and valid predicate values")
	}
	for function, fact := range effects {
		entry, planned := plan.FunctionPlan(function)
		if !planned || entry.Emission == coro.EmitNone {
			continue
		}
		if !fact.ProgramPredicates.Compatible(values) {
			return fmt.Errorf("library function %q requires program predicates mask=%#x values=%#x, consumer has %#x; recompile the library for this program",
				fact.ID, fact.ProgramPredicates.Mask, fact.ProgramPredicates.Values, values)
		}
	}
	for _, entry := range plan.ImportedFunctions() {
		fact := entry.Fact
		if entry.Plan.Emission != coro.EmitNone && !fact.ProgramPredicates.Compatible(values) {
			return fmt.Errorf("library helper %q requires program predicates mask=%#x values=%#x, consumer has %#x; recompile the library for this program",
				fact.ID, fact.ProgramPredicates.Mask, fact.ProgramPredicates.Values, values)
		}
	}
	return nil
}
