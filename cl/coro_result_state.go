package cl

import (
	"fmt"
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// This is a projection of an owned result allocation, not a new lifetime or
// invocation protocol. Only ProgramIR can grant access to the private word.
type coroResultStateAccess struct {
	result *types.Struct
	bind   bool
	owned  bool
}

func sameCoroResultStateAccess(a, b coroResultStateAccess) bool {
	if a.result == nil || b.result == nil {
		return a.result == b.result && a.bind == b.bind && a.owned == b.owned
	}
	return a.bind == b.bind && a.owned == b.owned && types.Identical(a.result, b.result)
}

func coroResultStateAllocation(value ssa.Value) *ssa.Alloc {
	for {
		switch converted := value.(type) {
		case *ssa.Convert:
			value = converted.X
		case *ssa.ChangeType:
			value = converted.X
		default:
			allocation, _ := value.(*ssa.Alloc)
			return allocation
		}
	}
}

func parseCoroResultStateAccess(ctx *context, call *ssa.Call, opcode int) (coroResultStateAccess, error) {
	fail := func() (coroResultStateAccess, error) {
		return coroResultStateAccess{}, fmt.Errorf("result state access requires an exact typed result pointer and unsafe.Pointer state")
	}
	if ctx == nil || call == nil || call.Common() == nil || call.Common().IsInvoke() {
		return fail()
	}
	bind := opcode == llgoCoroBindResultState
	sig, ok := ctx.patchType(call.Common().Signature()).(*types.Signature)
	count, results := 1, 1
	if bind {
		count, results = 2, 0
	}
	if !ok || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != count || sig.Results().Len() != results || len(call.Common().Args) != count {
		return fail()
	}
	pointer, ok := types.Unalias(sig.Params().At(0).Type()).(*types.Pointer)
	if !ok {
		return fail()
	}
	tuple, ok := types.Unalias(pointer.Elem()).(*types.Struct)
	if !ok {
		return fail()
	}
	if bind && !types.Identical(sig.Params().At(1).Type(), types.Typ[types.UnsafePointer]) ||
		!bind && !types.Identical(sig.Results().At(0).Type(), types.Typ[types.UnsafePointer]) {
		return fail()
	}
	return coroResultStateAccess{result: tuple, bind: bind}, nil
}

func (builder coroProgramIRBuilder) freezeDeliveryStateAccesses(ir *coroProgramIR) error {
	drops := make(map[*ssa.Function]types.Type)
	for _, recipe := range ir.deliveryProducers {
		if recipe.drop != nil {
			drops[recipe.drop] = recipe.result
		}
	}
	bound := make(map[*ssa.Function]bool)
	readers := make(map[*ssa.Function]bool)
	bindings := make(map[*ssa.Function]int)
	for call, plan := range ir.callPlans {
		if plan.resultState.result != nil && plan.resultState.bind && plan.failure == "" {
			bindings[call.Parent()]++
			if bindings[call.Parent()] > 1 {
				return fmt.Errorf("result state binding is outside its exact owned borrow: repeated binding")
			}
		}
	}
	for instruction, frozen := range ir.callPlans {
		if frozen.resultState.result == nil || frozen.failure != "" {
			continue
		}
		call, direct := instruction.(*ssa.Call)
		if !direct {
			return fmt.Errorf("result state access requires an exact ordinary call")
		}
		valid := false
		if frozen.resultState.bind {
			recipe, present := ir.deliveryProducers[call.Parent()]
			valid = present && recipe.borrow != nil && !bound[call.Parent()] &&
				call.Common().Args[0] == recipe.borrow && call.Block() == recipe.borrow.Block() &&
				instructionPrecedes(recipe.borrow, call) && types.Identical(recipe.result, frozen.resultState.result)
			bound[call.Parent()] = true
			if valid {
				allocation := coroResultStateAllocation(call.Common().Args[1])
				if allocation == nil || allocation.Parent() != call.Parent() || !allocation.Heap {
					return fmt.Errorf("result state binding requires its own escaping managed record allocation")
				}
				pointer, _ := types.Unalias(allocation.Type()).(*types.Pointer)
				if pointer == nil {
					return fmt.Errorf("result state binding has no typed managed record")
				}
				if _, record := types.Unalias(pointer.Elem()).Underlying().(*types.Struct); !record {
					return fmt.Errorf("result state binding requires a managed record")
				}
				for other, plan := range ir.callPlans {
					if other == recipe.borrow || other == call || other.Parent() != call.Parent() || plan.plan.Elision == CoroCallElidedFrontendUnevaluated {
						continue
					}
					if instructionPrecedes(other, call) {
						return fmt.Errorf("result state must bind before source calls, go and defer")
					}
				}
				if refs := recipe.borrow.Referrers(); refs != nil {
					for _, use := range *refs {
						if use == call {
							continue
						}
						if _, debug := use.(*ssa.DebugRef); debug {
							continue
						}
						if instructionPrecedes(use, call) {
							return fmt.Errorf("result state must bind before any use of its borrowed output")
						}
					}
				}
				for _, parameter := range recipe.drop.Params[1:] {
					if refs := parameter.Referrers(); refs != nil {
						for _, ref := range *refs {
							if _, debug := ref.(*ssa.DebugRef); !debug {
								return fmt.Errorf("state-bound drop must consume only its owned result and state")
							}
						}
					}
				}
				recipe.stateBind = call
				ir.deliveryProducers[call.Parent()] = recipe
			}
		} else if result, present := drops[call.Parent()]; present && len(call.Parent().Params) > 0 {
			valid = call.Common().Args[0] == call.Parent().Params[0] && types.Identical(result, frozen.resultState.result)
			object := call.Parent().Object()
			valid = valid && object != nil && !object.Exported()
			readers[call.Parent()] = true
		}
		if !valid {
			return fmt.Errorf("result state access %q is outside its exact owned borrow or drop helper", call.String())
		}
		frozen.resultState.owned = true
		if err := builder.freezeDeliveryConsumerSite(ir, call, frozen); err != nil {
			return err
		}
	}
	for target, uses := range ir.functionOperands {
		helper := builder.canonical.universe.canonicalAlias(target)
		if !readers[helper] {
			continue
		}
		for instruction := range uses {
			call, ordinary := instruction.(*ssa.Call)
			if ordinary {
				recipe, present := ir.deliveryProducers[call.Parent()]
				if present && recipe.borrow == call && recipe.drop == helper && len(call.Common().Args) == 2 && call.Common().Args[1] == target {
					continue
				}
			}
			return fmt.Errorf("result state drop helper %q cannot be invoked or published as an ordinary Go function", helper.String())
		}
	}
	return nil
}

func (ir *coroProgramIR) borrowedResultStateArgument(call *ssa.Call, index int) bool {
	if ir == nil || index != 0 {
		return false
	}
	return ir.callPlans[call].resultState.owned
}

func (p *context) compileCoroResultStateAccess(b llssa.Builder, call *ssa.Call) llssa.Expr {
	access := p.emissionUniverse.coroProgramIR.callPlans[call].resultState
	if !access.owned || access.result == nil {
		panic("result state access has no frozen ownership proof")
	}
	out := p.compileValue(b, call.Common().Args[0])
	layout := p.prog.NewCoroDeliveryStorage(p.prog.Elem(out.Type))
	address := layout.StateAddress(b, out)
	if access.bind {
		b.StoreVolatile(address, p.compileValue(b, call.Common().Args[1]))
		return llssa.Nil
	}
	return b.LoadKnownNonNil(address)
}
