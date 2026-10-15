/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cl

import (
	"fmt"
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

type coroManagedGoLinknameCallFacade struct {
	source *types.Signature
	target *types.Signature
}

// managedGoLinknameFacadeShape validates the one safe asymmetric Go linkname
// ABI supported by the managed compiler: a declaration may expose additional
// trailing parameters which are evaluated by the caller and deliberately not
// forwarded to a definition with a shorter compatible prefix. A definition
// may never require an argument absent from the declaration. Results remain
// exact in arity and transport. Variadic signatures are admitted only for the
// existing equal-arity facade; changing their fixed boundary would be
// ambiguous after x/tools packs the final slice.
func managedGoLinknameFacadeShape(
	source, target *ssa.Function,
	physicalSignature func(*ssa.Function) (*types.Signature, error),
	transportKey func(types.Type) string,
) (coroManagedGoLinknameCallFacade, bool, error) {
	if physicalSignature == nil || transportKey == nil || source == nil || target == nil || source == target ||
		source.Signature == nil || target.Signature == nil {
		return coroManagedGoLinknameCallFacade{}, false, nil
	}
	sourceSignature, err := physicalSignature(source)
	if err != nil {
		return coroManagedGoLinknameCallFacade{}, false, fmt.Errorf(
			"derive managed go:linkname source facade %q: %w", source.Name(), err,
		)
	}
	targetSignature, err := physicalSignature(target)
	if err != nil {
		return coroManagedGoLinknameCallFacade{}, false, fmt.Errorf(
			"derive managed go:linkname target facade %q: %w", target.Name(), err,
		)
	}
	if sourceSignature.Params().Len() < targetSignature.Params().Len() ||
		sourceSignature.Results().Len() != targetSignature.Results().Len() {
		return coroManagedGoLinknameCallFacade{}, false, nil
	}
	if sourceSignature.Params().Len() != targetSignature.Params().Len() &&
		(source.Signature.Variadic() || target.Signature.Variadic()) {
		return coroManagedGoLinknameCallFacade{}, false, nil
	}
	validateTuplePrefix := func(sourceTuple, targetTuple *types.Tuple) bool {
		if sourceTuple == nil || targetTuple == nil || sourceTuple.Len() < targetTuple.Len() {
			return sourceTuple == targetTuple
		}
		for index := 0; index < targetTuple.Len(); index++ {
			if transportKey(sourceTuple.At(index).Type()) != transportKey(targetTuple.At(index).Type()) {
				return false
			}
		}
		return true
	}
	if !validateTuplePrefix(sourceSignature.Params(), targetSignature.Params()) ||
		!validateTuplePrefix(sourceSignature.Results(), targetSignature.Results()) {
		return coroManagedGoLinknameCallFacade{}, false, nil
	}
	return coroManagedGoLinknameCallFacade{
		source: sourceSignature,
		target: targetSignature,
	}, true, nil
}

// managedGoLinknameCallFacade returns the two source-level views of one exact
// managed go:linkname operation. Pairing already proves the complete
// structural linkname ABI; this projection additionally checks the concrete
// coroutine transport before codegen retags private mirror values at the call
// boundary. It is not a general same-layout conversion capability.
func (u *EmissionUniverse) managedGoLinknameCallFacade(
	source, target *ssa.Function,
) (coroManagedGoLinknameCallFacade, bool, error) {
	if u == nil || source == nil || target == nil || source == target {
		return coroManagedGoLinknameCallFacade{}, false, nil
	}
	pair, paired := u.goLinknameDefinitions[source]
	if !paired || pair.definition == nil || pair.key == "" ||
		u.canonicalAlias(source) != target ||
		u.canonicalAlias(pair.definition) != target ||
		!u.managedGoLinknameDefinitionHasKey(target, pair.targetKey()) {
		return coroManagedGoLinknameCallFacade{}, false, nil
	}
	facade, compatible, err := managedGoLinknameFacadeShape(
		source, target, u.coroPhysicalSourceSignature,
		func(typ types.Type) string { return coroPhysicalTransportTypeKey(u, typ) },
	)
	if err != nil {
		return coroManagedGoLinknameCallFacade{}, true, err
	}
	if !compatible {
		return coroManagedGoLinknameCallFacade{}, true, fmt.Errorf(
			"managed go:linkname facade %q -> %q has no safe parameter-prefix/result transport",
			source.Name(), target.Name(),
		)
	}
	return facade, true, nil
}

func (p *context) compileManagedGoLinknameCallArguments(
	b llssa.Builder,
	source, target *ssa.Function,
	args []llssa.Expr,
) []llssa.Expr {
	if p == nil || p.emissionUniverse == nil {
		return args
	}
	facade, active, err := p.emissionUniverse.managedGoLinknameCallFacade(source, target)
	if err != nil {
		panic(err)
	}
	if !active {
		return args
	}
	if len(args) != facade.source.Params().Len() {
		panic(fmt.Errorf(
			"managed go:linkname facade %q -> %q arguments=%d, want %d",
			source.Name(), target.Name(), len(args), facade.source.Params().Len(),
		))
	}
	converted := append([]llssa.Expr(nil), args[:facade.target.Params().Len()]...)
	for index := range converted {
		targetType := facade.target.Params().At(index).Type()
		if !types.Identical(converted[index].RawType(), targetType) {
			converted[index] = b.ChangeType(p.prog.Type(targetType, llssa.InGo), converted[index])
		}
	}
	return converted
}

func (p *context) compileManagedGoLinknameCallResult(
	b llssa.Builder,
	source, target *ssa.Function,
	result llssa.Expr,
) (llssa.Expr, bool) {
	if p == nil || p.emissionUniverse == nil {
		return result, false
	}
	facade, active, err := p.emissionUniverse.managedGoLinknameCallFacade(source, target)
	if err != nil {
		panic(err)
	}
	if !active || facade.source.Results().Len() == 0 {
		return result, false
	}
	changed := false
	for index := 0; index < facade.source.Results().Len(); index++ {
		if !types.Identical(
			facade.source.Results().At(index).Type(),
			facade.target.Results().At(index).Type(),
		) {
			changed = true
			break
		}
	}
	if !changed {
		return result, false
	}
	if facade.source.Results().Len() == 1 {
		targetType := facade.source.Results().At(0).Type()
		return b.ChangeType(p.prog.Type(targetType, llssa.InGo), result), true
	}
	fields := make([]llssa.Expr, facade.source.Results().Len())
	for index := range fields {
		field := b.Field(result, index)
		targetType := facade.source.Results().At(index).Type()
		if !types.Identical(field.RawType(), targetType) {
			field = b.ChangeType(p.prog.Type(targetType, llssa.InGo), field)
		}
		fields[index] = field
	}
	return b.Aggregate(p.prog.Type(facade.source.Results(), llssa.InGo), fields...), true
}
