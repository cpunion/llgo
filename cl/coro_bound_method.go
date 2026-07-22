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
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// validateCoroExactBoundMethodWrapper recognizes only x/tools/ssa's canonical
// method-value closure body. The wrapper has one captured receiver and a
// tail-call to that exact method; this makes it an ordinary captured function
// producer for the universal {descriptor,env} ABI. Other synthetic functions
// remain outside descriptor transport.
func validateCoroExactBoundMethodWrapper(fn *ssa.Function) error {
	if fn == nil || fn.Pkg != nil || fn.Parent() != nil || fn.Syntax() != nil {
		return fmt.Errorf("requires one top-level syntax-free generated wrapper")
	}
	object, ok := fn.Object().(*types.Func)
	if !ok || object == nil {
		return fmt.Errorf("has no exact method object")
	}
	method, ok := object.Type().(*types.Signature)
	if !ok || method.Recv() == nil || fn.Signature == nil || fn.Signature.Recv() != nil {
		return fmt.Errorf("does not drop exactly one declared method receiver")
	}
	if fn.Name() != object.Name()+"$bound" || fn.Synthetic != fmt.Sprintf("bound method wrapper for %s", object) {
		return fmt.Errorf("has non-canonical bound-method identity")
	}
	if len(fn.FreeVars) != 1 || fn.FreeVars[0] == nil || fn.FreeVars[0].Parent() != fn ||
		!types.Identical(fn.FreeVars[0].Type(), method.Recv().Type()) {
		return fmt.Errorf("does not capture exactly the declared receiver")
	}
	if fn.Signature.Variadic() != method.Variadic() ||
		!types.Identical(fn.Signature.Params(), method.Params()) ||
		!types.Identical(fn.Signature.Results(), method.Results()) {
		return fmt.Errorf("callable signature does not equal the receiver-free method signature")
	}
	if len(fn.Params) != fn.Signature.Params().Len() {
		return fmt.Errorf("SSA parameters do not match the callable signature")
	}

	var call *ssa.Call
	var ret *ssa.Return
	extracts := make(map[int]*ssa.Extract)
	for _, block := range fn.Blocks {
		if block == nil {
			return fmt.Errorf("contains a nil basic block")
		}
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.DebugRef:
			case *ssa.Call:
				if call != nil {
					return fmt.Errorf("contains more than one call")
				}
				call = instruction
			case *ssa.Extract:
				if _, duplicate := extracts[instruction.Index]; duplicate {
					return fmt.Errorf("contains a duplicate result extract")
				}
				extracts[instruction.Index] = instruction
			case *ssa.Return:
				if ret != nil {
					return fmt.Errorf("contains more than one return")
				}
				ret = instruction
			default:
				return fmt.Errorf("contains non-tail-wrapper instruction %T", instruction)
			}
		}
	}
	if len(fn.Blocks) != 1 || call == nil || ret == nil || call.Block() != ret.Block() {
		return fmt.Errorf("is not one single-block tail call")
	}
	common := call.Common()
	if common == nil {
		return fmt.Errorf("tail call has no CallCommon")
	}
	receiver := fn.FreeVars[0]
	if types.IsInterface(method.Recv().Type()) {
		if !common.IsInvoke() || common.Value != receiver || common.Method != object || len(common.Args) != len(fn.Params) {
			return fmt.Errorf("interface receiver does not use the exact method invoke")
		}
		for index := range fn.Params {
			if common.Args[index] != fn.Params[index] {
				return fmt.Errorf("interface method argument %d is not the wrapper parameter", index)
			}
		}
	} else {
		if common.IsInvoke() || common.Method != nil || common.StaticCallee() == nil || len(common.Args) != len(fn.Params)+1 || common.Args[0] != receiver {
			return fmt.Errorf("concrete receiver does not use one exact receiver-first static call")
		}
		for index := range fn.Params {
			if common.Args[index+1] != fn.Params[index] {
				return fmt.Errorf("concrete method argument %d is not the wrapper parameter", index)
			}
		}
	}

	return validateCoroExactTailCallResults(fn, call, ret, extracts)
}

// validateCoroExactMethodExpressionThunk recognizes the direct method-
// expression thunk synthesized by x/tools/ssa for T.Method. Unlike a bound
// method value, the receiver is the first ordinary parameter and there is no
// captured environment. Restricting this certificate to an exact receiver
// type deliberately leaves promoted-field and implicit-indirection wrappers
// closed until their additional nil/selection operations have their own
// audited recipe.
func validateCoroExactMethodExpressionThunk(fn *ssa.Function) error {
	if fn == nil || fn.Pkg != nil || fn.Parent() != nil || fn.Syntax() != nil {
		return fmt.Errorf("requires one top-level syntax-free generated thunk")
	}
	object, ok := fn.Object().(*types.Func)
	if !ok || object == nil {
		return fmt.Errorf("has no exact method object")
	}
	method, ok := object.Type().(*types.Signature)
	if !ok || method.Recv() == nil || fn.Signature == nil || fn.Signature.Recv() != nil {
		return fmt.Errorf("does not expose exactly one method receiver parameter")
	}
	if fn.Name() != object.Name()+"$thunk" || fn.Synthetic != fmt.Sprintf("thunk for %s", object) {
		return fmt.Errorf("has non-canonical method-expression identity")
	}
	if len(fn.FreeVars) != 0 {
		return fmt.Errorf("method-expression thunk unexpectedly captures an environment")
	}
	params := fn.Signature.Params()
	methodParams := method.Params()
	if params == nil || params.Len() != methodParams.Len()+1 ||
		!types.Identical(params.At(0).Type(), method.Recv().Type()) ||
		fn.Signature.Variadic() != method.Variadic() ||
		!types.Identical(fn.Signature.Results(), method.Results()) {
		return fmt.Errorf("callable signature is not receiver-first method signature")
	}
	for index := 0; index < methodParams.Len(); index++ {
		if !types.Identical(params.At(index+1).Type(), methodParams.At(index).Type()) {
			return fmt.Errorf("callable parameter %d does not match method parameter", index+1)
		}
	}
	if len(fn.Params) != params.Len() || len(fn.Locals) > 1 {
		return fmt.Errorf("SSA parameters or receiver spill do not match the callable signature: params=%d/%d locals=%d", len(fn.Params), params.Len(), len(fn.Locals))
	}

	var receiverAlloc *ssa.Alloc
	var receiverStore *ssa.Store
	var receiverLoad *ssa.UnOp
	var call *ssa.Call
	var ret *ssa.Return
	extracts := make(map[int]*ssa.Extract)
	for _, block := range fn.Blocks {
		if block == nil {
			return fmt.Errorf("contains a nil basic block")
		}
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.DebugRef:
			case *ssa.Alloc:
				if receiverAlloc != nil {
					return fmt.Errorf("contains more than one receiver allocation")
				}
				receiverAlloc = instruction
			case *ssa.Store:
				if receiverStore != nil {
					return fmt.Errorf("contains more than one receiver store")
				}
				receiverStore = instruction
			case *ssa.UnOp:
				if instruction.Op != token.MUL || receiverLoad != nil {
					return fmt.Errorf("contains a non-canonical receiver load")
				}
				receiverLoad = instruction
			case *ssa.Call:
				if call != nil {
					return fmt.Errorf("contains more than one call")
				}
				call = instruction
			case *ssa.Extract:
				if _, duplicate := extracts[instruction.Index]; duplicate {
					return fmt.Errorf("contains a duplicate result extract")
				}
				extracts[instruction.Index] = instruction
			case *ssa.Return:
				if ret != nil {
					return fmt.Errorf("contains more than one return")
				}
				ret = instruction
			default:
				return fmt.Errorf("contains non-direct-thunk instruction %T", instruction)
			}
		}
	}
	if len(fn.Blocks) != 1 || call == nil || ret == nil || call.Block() != ret.Block() {
		return fmt.Errorf("is not one single-block receiver-spill tail call")
	}
	var receiver ssa.Value = fn.Params[0]
	if receiverAlloc != nil || receiverStore != nil || receiverLoad != nil || len(fn.Locals) != 0 {
		if receiverAlloc == nil || receiverStore == nil || receiverLoad == nil || len(fn.Locals) != 1 ||
			fn.Locals[0] != receiverAlloc || receiverStore.Addr != receiverAlloc ||
			receiverStore.Val != fn.Params[0] || receiverLoad.X != receiverAlloc {
			return fmt.Errorf("has an incomplete or non-canonical receiver spill")
		}
		receiver = receiverLoad
	}
	common := call.Common()
	if common == nil {
		return fmt.Errorf("tail call has no CallCommon")
	}
	if types.IsInterface(method.Recv().Type()) {
		if !common.IsInvoke() || common.Value != receiver || common.Method != object || len(common.Args) != len(fn.Params)-1 {
			return fmt.Errorf("interface receiver does not use the exact method invoke")
		}
		for index := 1; index < len(fn.Params); index++ {
			if common.Args[index-1] != fn.Params[index] {
				return fmt.Errorf("interface method argument %d is not the thunk parameter", index)
			}
		}
	} else {
		callee := common.StaticCallee()
		if common.IsInvoke() || common.Method != nil || callee == nil || callee.Object() != object ||
			len(common.Args) != len(fn.Params) || common.Args[0] != receiver {
			return fmt.Errorf("concrete receiver does not use one exact receiver-first static call")
		}
		for index := 1; index < len(fn.Params); index++ {
			if common.Args[index] != fn.Params[index] {
				return fmt.Errorf("concrete method argument %d is not the thunk parameter", index)
			}
		}
	}
	return validateCoroExactTailCallResults(fn, call, ret, extracts)
}

func validateCoroExactTailCallResults(fn *ssa.Function, call *ssa.Call, ret *ssa.Return, extracts map[int]*ssa.Extract) error {
	results := fn.Signature.Results().Len()
	if len(ret.Results) != results {
		return fmt.Errorf("tail return count %d does not match signature count %d", len(ret.Results), results)
	}
	switch results {
	case 0:
		if len(extracts) != 0 {
			return fmt.Errorf("zero-result wrapper contains result extracts")
		}
	case 1:
		if len(extracts) != 0 || ret.Results[0] != call {
			return fmt.Errorf("single-result wrapper does not return its exact call")
		}
	default:
		if len(extracts) != results {
			return fmt.Errorf("multi-result wrapper extract count %d does not match %d", len(extracts), results)
		}
		for index, result := range ret.Results {
			extract := extracts[index]
			if extract == nil || extract.Tuple != call || extract.Index != index || result != extract {
				return fmt.Errorf("multi-result wrapper return %d is not its exact call extract", index)
			}
		}
	}
	return nil
}
