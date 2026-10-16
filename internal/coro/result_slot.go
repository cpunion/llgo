/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import (
	"fmt"
	"go/token"
	"go/types"
)

// ResultSlotType constructs the typed tuple shared by coroutine, outcome and
// dynamic-dispatch ABIs. The caller supplies normalized physical result types.
// Exported field names let a compiler-owned Go adapter declare an identical
// anonymous struct alias; private fields would add its package to type identity.
func ResultSlotType(results *types.Tuple) *types.Struct {
	fields := make([]*types.Var, results.Len())
	for index := range fields {
		fields[index] = types.NewField(token.NoPos, nil, fmt.Sprintf("R%d", index), results.At(index).Type(), false)
	}
	return types.NewStruct(fields, nil)
}
