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

package ssa

import (
	"go/token"
	"go/types"

	"github.com/xgo-dev/llvm"
)

func controlFunctionSignature(parameters []Type, result Type) *types.Signature {
	params := make([]*types.Var, len(parameters))
	for index, parameter := range parameters {
		params[index] = types.NewParam(token.NoPos, nil, "", parameter.RawType())
	}
	var results *types.Tuple
	if result != nil {
		results = types.NewTuple(types.NewParam(token.NoPos, nil, "", result.RawType()))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), results, false)
}

// ControlExit emits the target C process-exit leaf. The caller owns the
// explicit unreachable terminator so LLSSA's lower-level builder remains
// usable in tests which deliberately inspect multiple control declarations.
func (b Builder) ControlExit(status Expr) {
	fn := b.Pkg.cFunc(
		"exit",
		controlFunctionSignature([]Type{b.Prog.CInt()}, nil),
	)
	b.addNoReturnAttr(fn)
	b.Call(fn, status)
}

// ControlTrap emits the target-neutral LLVM trap intrinsic. As with
// ControlExit, the source lowering emits the unreachable terminator.
func (b Builder) ControlTrap() {
	id := llvm.LookupIntrinsicID("llvm.trap")
	if id == 0 {
		panic("ssa: LLVM has no llvm.trap intrinsic")
	}
	b.impl.CreateIntrinsic(b.Prog.Void().ll, id, nil, "")
}

// ControlFork emits the exact current-thread process fork leaf.
func (b Builder) ControlFork() Expr {
	fn := b.Pkg.cFunc(
		"fork",
		controlFunctionSignature(nil, b.Prog.CInt()),
	)
	return b.Call(fn)
}

// ControlExecve emits the exact typed execve leaf. Successful exec does not
// return, while a failed call returns -1, so the declaration itself is not
// marked noreturn.
func (b Builder) ControlExecve(path, argv, envp Expr) Expr {
	cstr := b.Prog.CStr()
	cstrs := b.Prog.Pointer(cstr)
	fn := b.Pkg.cFunc(
		"execve",
		controlFunctionSignature([]Type{cstr, cstrs, cstrs}, b.Prog.CInt()),
	)
	return b.Call(fn, path, argv, envp)
}
