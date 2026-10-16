package cl

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// CallableSignature gives both sides of scalar CHA lookup the same frozen
// source replacement identity. Raw SSA signatures may refer to an original
// package on one side and its runtime replacement on the other. Physical
// closure lowering is deliberately absent: it must not make distinct Go
// callable types match merely because their machine layouts happen to agree.
func (view CoroPlanningMetadataView) CallableSignature(fn *ssa.Function, signature *types.Signature) (*types.Signature, error) {
	u := view.index.universe
	if u == nil || fn == nil || signature == nil {
		return nil, fmt.Errorf("coroutine dynamic signature requires an emission universe, owner, and source signature")
	}
	owner := u.ownerOf(fn)
	if owner == nil {
		return nil, fmt.Errorf("coroutine dynamic signature: owner %q is outside the frozen emission universe", fn.String())
	}
	effective, ok := u.effectiveType(owner, fn, signature).(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("coroutine dynamic signature: owner %q source replacement is not a signature", fn.String())
	}
	return effective, nil
}

func coroDynamicCallableSignature(signature *types.Signature, err error) (*types.Signature, error) {
	if err != nil {
		return nil, err
	}
	return coroConcreteManagedCallableSignature(signature)
}
