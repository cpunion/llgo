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
)

// CoroDynamicImplements evaluates restricted CHA against the same effective
// patched type graph used by code generation. Raw Go SSA retains the original
// invoke interface while a replacement package contributes method receivers
// from its alternate types package; comparing those raw graphs would silently
// produce an empty closed-world target set.
func (u *EmissionUniverse) CoroDynamicImplements(candidate types.Type, iface *types.Interface) (bool, error) {
	if u == nil {
		return false, fmt.Errorf("coroutine dynamic implementation relation: nil emission universe")
	}
	if candidate == nil || iface == nil {
		return false, fmt.Errorf("coroutine dynamic implementation relation requires candidate and interface types")
	}
	owner := u.coroDynamicTypeOwner(candidate)
	if owner == nil {
		owner = u.coroDynamicTypeOwner(iface)
	}
	if owner == nil {
		// Types with no package-owned named edge cannot participate in package
		// replacement. Preserve the ordinary exact go/types relation.
		return types.Implements(candidate, iface), nil
	}
	effectiveCandidate := u.effectiveType(owner, nil, candidate)
	effectiveInterfaceType := types.Type(iface)
	if namedInterface, found, err := u.coroExactNamedInterface(owner, iface); err != nil {
		return false, err
	} else if found {
		effectiveInterfaceType = namedInterface
	} else {
		effectiveInterfaceType = u.effectiveType(owner, nil, iface)
	}
	effectiveInterface, ok := types.Unalias(effectiveInterfaceType).Underlying().(*types.Interface)
	if !ok {
		return false, fmt.Errorf("coroutine dynamic implementation relation: effective invoke type is %T, not an interface", effectiveInterfaceType)
	}
	effectiveInterface.Complete()
	return types.Implements(effectiveCandidate, effectiveInterface), nil
}

// coroExactNamedInterface recovers the package-level named interface whose
// exact raw Underlying pointer was placed in an SSA invoke. Recovering the
// named edge matters for unexported methods: rebuilding only the anonymous
// interface shape would retain the original method package identity, while
// the replacement receiver correctly carries the alternate package identity.
func (u *EmissionUniverse) coroExactNamedInterface(owner *preparedEmissionPackage, iface *types.Interface) (types.Type, bool, error) {
	if u == nil || owner == nil || owner.oldTypes == nil || iface == nil || owner.oldTypes.Scope() == nil {
		return nil, false, nil
	}
	var replacement types.Type
	for _, name := range owner.oldTypes.Scope().Names() {
		object, ok := owner.oldTypes.Scope().Lookup(name).(*types.TypeName)
		if !ok || types.Unalias(object.Type()).Underlying() != iface {
			continue
		}
		candidate := u.effectiveType(owner, nil, object.Type())
		if _, ok := types.Unalias(candidate).Underlying().(*types.Interface); !ok {
			return nil, false, fmt.Errorf("coroutine dynamic implementation relation: effective named invoke type %q is %T, not an interface", name, candidate)
		}
		if replacement != nil && !types.Identical(replacement, candidate) {
			return nil, false, fmt.Errorf("coroutine dynamic implementation relation: raw interface has conflicting effective named owners")
		}
		replacement = candidate
	}
	return replacement, replacement != nil, nil
}

func (u *EmissionUniverse) coroDynamicTypeOwner(typ types.Type) *preparedEmissionPackage {
	if u == nil || typ == nil {
		return nil
	}
	switch typ := types.Unalias(typ).(type) {
	case *types.Pointer:
		return u.coroDynamicTypeOwner(typ.Elem())
	case *types.Named:
		if object := typ.Obj(); object != nil {
			return u.ownerOfTypes(object.Pkg())
		}
	case *types.Interface:
		typ.Complete()
		for index := 0; index < typ.NumExplicitMethods(); index++ {
			if method := typ.ExplicitMethod(index); method != nil {
				if owner := u.ownerOfTypes(method.Pkg()); owner != nil {
					return owner
				}
			}
		}
		for index := 0; index < typ.NumEmbeddeds(); index++ {
			if owner := u.coroDynamicTypeOwner(typ.EmbeddedType(index)); owner != nil {
				return owner
			}
		}
	}
	return nil
}
