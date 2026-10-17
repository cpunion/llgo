package cl

import (
	"fmt"
	"go/token"
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroDeliveryStateAllocZCall = "$llgo.coro.delivery-state.allocz"

func clearCoroDeliveryState(b llssa.Builder, result llssa.Expr) {
	layout := b.Prog.NewCoroDeliveryStorage(b.Prog.Elem(result.Type))
	b.StoreVolatile(layout.StateAddress(b, result), b.Prog.Nil(b.Prog.VoidPtr()))
}

func (p *context) coroDeliveryStateBound(b llssa.Builder, result llssa.Expr) llssa.Expr {
	layout := p.prog.NewCoroDeliveryStorage(p.prog.Elem(result.Type))
	return b.BinOp(token.NEQ, b.LoadKnownNonNil(layout.StateAddress(b, result)), p.prog.Nil(p.prog.VoidPtr()))
}

func (builder coroProgramIRBuilder) freezeDeliverySnapshotAllocator(function *ssa.Function) error {
	u := builder.canonical.universe
	runtime := u.byPath[llssa.PkgRuntime]
	if runtime == nil || u.pathDup[llssa.PkgRuntime] {
		if u.completeRuntimeABI {
			return fmt.Errorf("receiver snapshot requires its exact runtime allocator")
		}
		return nil
	}
	allocator := u.canonicalAlias(runtime.ssa.Func("AllocZ"))
	if allocator == nil {
		if u.completeRuntimeABI {
			return fmt.Errorf("receiver snapshot has no runtime allocator")
		}
		return nil
	}
	if _, present := u.required[allocator]; !present {
		return fmt.Errorf("receiver snapshot allocator is outside the frozen source inventory")
	}
	return u.recordCoroLoweredCall(function, coroDeliveryStateAllocZCall, allocator)
}

func (p *context) coroForwardedDeliveryArguments(b llssa.Builder, recipe coroSourceDeliveryRecipe, args []llssa.Expr) (coroSourceDeliveryRecipe, []llssa.Expr) {
	if recipe.forward == nil || !recipe.requiresState() {
		return p.coroProjectDeliveryArguments(b, recipe, args)
	}
	base := p.emissionUniverse.coroProgramIR.deliveryProducers[recipe.forwardBase]
	forwarded := []llssa.Expr{args[0], p.prog.Nil(p.prog.VoidPtr())}
	var state llssa.Expr
	if recipe.snapshot != nil {
		layout := p.prog.NewCoroDeliveryStorage(p.prog.Type(recipe.result, llssa.InGo))
		state = b.Convert(p.prog.Pointer(p.prog.Type(recipe.snapshot.box, llssa.InGo)),
			b.LoadKnownNonNil(layout.StateAddress(b, args[0])))
	}
	for index := 0; index < base.signature.Params().Len(); index++ {
		typ := p.prog.Type(base.signature.Params().At(index).Type(), llssa.InGo)
		value := p.prog.Zero(typ)
		if !state.IsNil() {
			value = b.LoadKnownNonNil(b.FieldAddrKnownNonNil(state, index))
			if !types.Identical(value.RawType(), typ.RawType()) {
				value = b.ChangeType(typ, value)
			}
		}
		forwarded = append(forwarded, value)
	}
	return base, forwarded
}

func (p *context) bindCoroDeliverySnapshot(b llssa.Builder, recipe coroSourceDeliveryRecipe, result llssa.Expr) {
	if recipe.snapshot == nil || recipe.snapshot.owner != p.goFn {
		return
	}
	base := p.coroEmissionSourceParamBase()
	env := p.prog.Nil(p.prog.VoidPtr())
	if base == 3 {
		env = b.Convert(p.prog.VoidPtr(), p.fn.PhysicalParam(2))
	}
	args := []llssa.Expr{result, env}
	for index := 0; index < recipe.signature.Params().Len(); index++ {
		args = append(args, p.fn.PhysicalParam(base+index))
	}
	_, values := p.coroProjectDeliveryArguments(b, recipe, args)
	state := b.AllocZAs(p.prog.Type(recipe.snapshot.box, llssa.InGo), coroDeliveryStateAllocZCall)
	for index, value := range values[2:] {
		b.StoreKnownNonNil(b.FieldAddrKnownNonNil(state, index), value)
	}
	layout := p.prog.NewCoroDeliveryStorage(p.prog.Type(recipe.result, llssa.InGo))
	b.StoreKnownNonNil(layout.StateAddress(b, result), b.Convert(p.prog.VoidPtr(), state))
}
