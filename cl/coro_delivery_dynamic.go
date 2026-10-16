/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/go/ssa"
)

// freezeDeliveryHelperCall retains an ordinary managed invocation with no
// fabricated SSA target. The existing value-flow pass resolves its exact
// source condition before computing effects; physical emission must consume
// that active lowered record, never the mutable builder map.
func (builder coroProgramIRBuilder) freezeDeliveryHelperCall(owner *ssa.Function, name string, source ssa.CallInstruction) error {
	u := builder.canonical.universe
	if u == nil || owner == nil || u.canonicalAlias(owner) != owner ||
		source == nil || source.Parent() != owner || source.Block() == nil || source.Common() == nil ||
		source.Common().StaticCallee() != nil || source.Common().IsInvoke() ||
		source.Common().Signature() == nil || source.Common().Signature().Results().Len() == 0 ||
		name == "" || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("conditional delivery helper requires an exact owner-local typed dynamic result call")
	}
	if _, present := u.required[owner]; !present {
		return fmt.Errorf("conditional delivery helper owner is outside the emission universe")
	}
	if _, builtin := source.Common().Value.(*ssa.Builtin); builtin {
		return fmt.Errorf("conditional delivery helper cannot own a builtin invocation")
	}
	switch call := source.(type) {
	case *ssa.Call:
	case *ssa.Defer:
		if call.DeferStack != nil {
			return fmt.Errorf("conditional delivery helper requires a bound external defer owner")
		}
	default:
		return fmt.Errorf("conditional delivery helper cannot own a spawned invocation")
	}
	return builder.recordLoweredCallTarget(owner, name, coroLoweredCallTarget{whenResultDeliveryOf: source})
}
