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

// validateCoroExactMethodWrapper recognizes x/tools/ssa's canonical promoted
// or implicit-indirection method wrapper. The wrapper has one receiver spill,
// an optional nil check and field-selection chain, and one exact tail call to
// the declared method. These wrappers are ordinary Go bodies and must remain
// available when a complete method set is colored by the stackless plan.
func validateCoroExactMethodWrapper(fn *ssa.Function) error {
	if fn == nil || fn.Pkg != nil || fn.Parent() != nil || fn.Syntax() != nil ||
		fn.Signature == nil || fn.Signature.Recv() == nil || len(fn.FreeVars) != 0 {
		return fmt.Errorf("requires one top-level syntax-free receiver method")
	}
	object, ok := fn.Object().(*types.Func)
	if !ok || object == nil {
		return fmt.Errorf("has no exact declared method object")
	}
	method, ok := object.Type().(*types.Signature)
	if !ok || method.Recv() == nil {
		return fmt.Errorf("declared method has no receiver")
	}
	if fn.Name() != object.Name() || fn.Synthetic != fmt.Sprintf("wrapper for %s", object) {
		return fmt.Errorf("has non-canonical method-wrapper identity")
	}
	params := coroPhysicalNormalizeSourceSignature(fn.Signature).Params()
	if params == nil || len(fn.Params) != params.Len() || len(fn.Params) == 0 {
		return fmt.Errorf("SSA parameters do not match the receiver-first signature")
	}
	for index, parameter := range fn.Params {
		if parameter == nil || !types.Identical(parameter.Type(), params.At(index).Type()) {
			return fmt.Errorf("SSA parameter %d does not match the receiver-first signature", index)
		}
	}
	if len(fn.Locals) > 1 || len(fn.Locals) == 1 && fn.Locals[0] == nil {
		return fmt.Errorf("contains more than one receiver spill")
	}
	var receiverAlloc *ssa.Alloc
	if len(fn.Locals) == 1 {
		receiverAlloc = fn.Locals[0]
	}

	var receiverStore *ssa.Store
	var wrapperCall *ssa.Call
	var ret *ssa.Return
	extracts := make(map[int]*ssa.Extract)
	derived := func(value ssa.Value) bool { return false }
	derived = func(value ssa.Value) bool {
		switch value := value.(type) {
		case *ssa.Parameter:
			return value == fn.Params[0]
		case *ssa.Alloc:
			return receiverAlloc != nil && value == receiverAlloc
		case *ssa.UnOp:
			return derived(value.X)
		case *ssa.FieldAddr:
			return derived(value.X)
		case *ssa.Field:
			return derived(value.X)
		case *ssa.ChangeType:
			return derived(value.X)
		case *ssa.Convert:
			return derived(value.X)
		case *ssa.Call:
			common := value.Common()
			builtin, ok := common.Value.(*ssa.Builtin)
			return ok && builtin.Name() == "ssa:wrapnilchk" && len(common.Args) == 3 && derived(common.Args[0])
		default:
			return false
		}
	}

	if len(fn.Blocks) != 1 || fn.Blocks[0] == nil {
		return fmt.Errorf("is not one single-block tail wrapper")
	}
	for _, instruction := range fn.Blocks[0].Instrs {
		switch instruction := instruction.(type) {
		case *ssa.DebugRef:
		case *ssa.Alloc:
			if receiverAlloc == nil || instruction != receiverAlloc {
				return fmt.Errorf("contains a non-receiver allocation")
			}
		case *ssa.Store:
			if receiverAlloc == nil || receiverStore != nil || instruction.Addr != receiverAlloc || instruction.Val != fn.Params[0] {
				return fmt.Errorf("contains a non-canonical receiver spill")
			}
			receiverStore = instruction
		case *ssa.UnOp:
			if instruction.Op != token.MUL || !derived(instruction.X) {
				return fmt.Errorf("contains a non-canonical receiver load")
			}
		case *ssa.FieldAddr:
			if !derived(instruction.X) {
				return fmt.Errorf("contains a field selection outside the receiver chain")
			}
		case *ssa.Field:
			if !derived(instruction.X) {
				return fmt.Errorf("contains a field selection outside the receiver chain")
			}
		case *ssa.ChangeType:
			if !derived(instruction.X) {
				return fmt.Errorf("contains a type change outside the receiver chain")
			}
		case *ssa.Convert:
			if !derived(instruction.X) {
				return fmt.Errorf("contains a conversion outside the receiver chain")
			}
		case *ssa.Call:
			common := instruction.Common()
			if common == nil {
				return fmt.Errorf("contains a call without CallCommon")
			}
			if builtin, ok := common.Value.(*ssa.Builtin); ok {
				if builtin.Name() != "ssa:wrapnilchk" || !derived(instruction) {
					return fmt.Errorf("contains a non-canonical wrapper builtin %q", builtin.Name())
				}
				continue
			}
			if wrapperCall != nil {
				return fmt.Errorf("contains more than one method call")
			}
			wrapperCall = instruction
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
			return fmt.Errorf("contains non-wrapper instruction %T", instruction)
		}
	}
	if receiverAlloc != nil && receiverStore == nil || wrapperCall == nil || ret == nil {
		return fmt.Errorf("does not contain one canonical receiver value, method call, and return")
	}
	common := wrapperCall.Common()
	if common.IsInvoke() {
		if common.Method != object || !derived(common.Value) || len(common.Args) != len(fn.Params)-1 {
			return fmt.Errorf("interface wrapper does not invoke the exact declared method")
		}
		for index, argument := range common.Args {
			if argument != fn.Params[index+1] {
				return fmt.Errorf("interface wrapper argument %d is not the source parameter", index)
			}
		}
	} else {
		callee := common.StaticCallee()
		calleeObject, _ := calleeObject(callee).(*types.Func)
		if callee == nil || calleeObject != object || len(common.Args) != len(fn.Params) || !derived(common.Args[0]) {
			return fmt.Errorf("concrete wrapper does not call the exact declared method")
		}
		for index := 1; index < len(common.Args); index++ {
			if common.Args[index] != fn.Params[index] {
				return fmt.Errorf("concrete wrapper argument %d is not the source parameter", index)
			}
		}
	}
	return validateCoroExactTailCallResults(fn, wrapperCall, ret, extracts)
}

func calleeObject(fn *ssa.Function) types.Object {
	if fn == nil {
		return nil
	}
	return fn.Object()
}

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
