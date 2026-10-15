package cl

import (
	"fmt"
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// DiscoverCoroProgramPredicates projects feature demand from the same SSAPlan
// used for effect analysis. It is deliberately independent of physical recipes:
// a predicate must be frozen before those recipes and their callable ABI exist.
func DiscoverCoroProgramPredicates(plan *coro.SSAPlan, imported map[*ssa.Function]coro.LibraryEffectFunction) (coro.ProgramCapabilities, error) {
	if plan == nil {
		return 0, fmt.Errorf("program predicate discovery requires an SSA plan")
	}
	capabilities, err := deriveCoroFunctionProgramCapabilities(plan, nil, imported)
	if err != nil {
		return 0, err
	}
	return projectCoroProgramCapabilities(plan, capabilities) & coro.ProgramPredicateMask(), nil
}

// CoroProgramPredicateValues returns planning-time constants, not a second
// codegen-time feature inference. A known false value still requires its mask.
func (c *Compilation) CoroProgramPredicateValues() (coro.ProgramCapabilities, error) {
	if c == nil || c.immutableEmissionUniverse() == nil || c.immutableEmissionUniverse().coroProgramIR == nil {
		return 0, fmt.Errorf("program predicates require a prepared compilation")
	}
	binding := c.immutableEmissionUniverse().coroProgramIR.programPredicates
	if !binding.Valid() || binding.Mask == 0 {
		return 0, fmt.Errorf("program predicates were not frozen before effect analysis")
	}
	return binding.Values, nil
}

// CoroFunctionProgramPredicates exports the exact specialization consumed by
// this callable entry, including transitive calls whose effect/ABI depended on
// a predicate. Unrelated functions remain reusable in either program profile.
func (c *Compilation) CoroFunctionProgramPredicates(function *ssa.Function) (coro.ProgramPredicateRequirements, error) {
	if _, err := c.coroFunctionProgramCapabilities(); err != nil {
		return coro.ProgramPredicateRequirements{}, err
	}
	mask, ok := c.coroPredicateMasksByFunc[function]
	if !ok {
		return coro.ProgramPredicateRequirements{}, fmt.Errorf("function is absent from program predicate closure")
	}
	if mask == 0 {
		return coro.ProgramPredicateRequirements{}, nil
	}
	values, err := c.CoroProgramPredicateValues()
	if err != nil {
		return coro.ProgramPredicateRequirements{}, err
	}
	return coro.ProgramPredicateRequirements{Mask: mask, Values: values & mask}, nil
}

// Freeze the constant-CFG projection while the ordinary frontend context owns
// exact intrinsic identity. Later physical lowering reads this same projection.
func (ir *coroProgramIR) freezeCoroPredicateCFG(ctx *context, fn *ssa.Function) map[*ssa.BasicBlock]bool {
	resolve := func(value ssa.Value) (bool, bool) {
		call, ok := value.(*ssa.Call)
		if !ok || call.Common() == nil || call.Common().IsInvoke() {
			return false, false
		}
		target := call.Common().StaticCallee()
		if target == nil {
			return false, false
		}
		_, name, kind := ctx.funcName(target)
		predicate := coroProgramPredicateCapability(llgoInstrs[name])
		if kind != llgoInstr || predicate == 0 ||
			target.Signature.Params().Len() != 0 || target.Signature.Results().Len() != 1 ||
			!types.Identical(target.Signature.Results().At(0).Type(), types.Typ[types.Bool]) {
			return false, false
		}
		return ir.programPredicates.Values&predicate != 0, ir.programPredicates.Mask&predicate != 0
	}
	reachable := coroPhysicalConstantReachableBlocksWith(fn, resolve)
	for _, block := range fn.Blocks {
		if len(block.Instrs) == 0 {
			continue
		}
		if branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If); ok {
			if value, known := coroPhysicalConstantBoolWith(branch.Cond, resolve); known {
				ir.constantBranches[branch] = value
			}
		}
	}
	ir.reachableBlocks[fn] = reachable
	return reachable
}

func (ir *coroProgramIR) coroSuccessors(block *ssa.BasicBlock) []*ssa.BasicBlock {
	if ir != nil && len(block.Instrs) != 0 && len(block.Succs) == 2 {
		if branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If); ok {
			if value, known := ir.constantBranches[branch]; known {
				if value {
					return block.Succs[:1]
				}
				return block.Succs[1:]
			}
		}
	}
	return block.Succs
}

func (ir *coroProgramIR) coroReachableBlocks(fn *ssa.Function) map[*ssa.BasicBlock]bool {
	if ir != nil {
		if blocks, frozen := ir.reachableBlocks[fn]; frozen {
			return blocks
		}
	}
	return coroPhysicalConstantReachableBlocks(fn)
}

func coroSelectedProgramIR(programs []*coroProgramIR) *coroProgramIR {
	if len(programs) != 0 {
		return programs[0]
	}
	return nil
}

func (ir *coroProgramIR) coroLocalProgramPredicateMasks() map[*ssa.Function]coro.ProgramCapabilities {
	masks := make(map[*ssa.Function]coro.ProgramCapabilities)
	for site, frozen := range ir.callPlans {
		if frozen.plan.Elision == CoroCallElidedIntrinsic && frozen.plan.Intrinsic {
			masks[site.Parent()] |= coroProgramPredicateCapability(frozen.opcode)
		}
	}
	return masks
}

func coroProgramPredicateCapability(opcode int) coro.ProgramCapabilities {
	switch opcode {
	case llgoCoroExecutionDomainEnabled:
		return coro.ExecutionDomainProgramCapability()
	case llgoCoroGCObservationEnabled:
		return coro.GCObservationProgramCapability()
	default:
		return 0
	}
}
