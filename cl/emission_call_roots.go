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
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// emissionCallValueRoot is one SSA value for which callEx actually invokes
// compileValue, or one static function for which it invokes compileFunction.
// The distinction matters for llgo intrinsics: only a function value needs an
// addressable call wrapper.
type emissionCallValueRoot struct {
	value          ssa.Value
	directFunction bool
}

type emissionIntrinsicOperandPolicy uint8

const (
	emissionIntrinsicNoValues emissionIntrinsicOperandPolicy = iota
	emissionIntrinsicRawAllValues
	emissionIntrinsicCompileValues
	emissionIntrinsicFirstValue
	emissionIntrinsicFirstTwoValues
	emissionIntrinsicFixedBeforeVArg
	emissionIntrinsicFuncAddr
	emissionIntrinsicFuncPCABI0
	emissionIntrinsicAsm
)

// emissionIntrinsicPolicy is intentionally exhaustive for valid, type-checked
// intrinsic calls. Adding an llgoInstr without declaring how its lowering
// consumes SSA values must fail the active universe instead of silently
// widening or narrowing its function/ABI roots. Signature/constant validation
// remains the lowering's responsibility.
func emissionIntrinsicPolicy(instruction int) (emissionIntrinsicOperandPolicy, error) {
	switch instruction {
	case llgoCstr, llgoPyStr,
		llgoSkip, llgoCgoCheckPointer,
		llgoSigjmpbuf, llgoDeferData, llgoUnreachable, llgoStackSave,
		llgoCoroYield, llgoCoroCriticalEnter, llgoCoroCriticalExit,
		llgoCoroGoexit, llgoCoroOSThreadLock, llgoCoroOSThreadUnlock:
		return emissionIntrinsicNoValues, nil

	case llgoAdvance, llgoIndex,
		llgoSigsetjmp, llgoSiglongjmp,
		llgoCgoGoStringN, llgoCgoGoBytes:
		return emissionIntrinsicFirstTwoValues, nil

	case llgoAlloca, llgoAllocaCStr, llgoAllocCStr, llgoAllocaCStrs,
		llgoString, llgoStringData,
		llgoCgoCString, llgoCgoCBytes, llgoCgoGoString, llgoCgoCMalloc,
		llgoCgoCgocall:
		return emissionIntrinsicFirstValue, nil

	case llgoPyList, llgoPyTuple:
		return emissionIntrinsicFixedBeforeVArg, nil

	case llgoFuncAddr:
		return emissionIntrinsicFuncAddr, nil
	case llgoFuncPCABI0:
		return emissionIntrinsicFuncPCABI0, nil
	case llgoAsm:
		return emissionIntrinsicAsm, nil

	case llgoSyscall, llgoSyscall32, llgoSyscallPtr:
		return emissionIntrinsicRawAllValues, nil
	case llgoBoolToUint8,
		llgoAtomicLoad, llgoAtomicStore, llgoAtomicCmpXchg,
		llgoAtomicCmpXchgOK, llgoAtomicAddReturnNew,
		llgoAtomicLoadUnsafe, llgoAtomicStoreUnsafe,
		llgoCoroPark, llgoCoroTimerSleep, llgoCoroPollWait, llgoCoroControlledTimerWait:
		return emissionIntrinsicCompileValues, nil
	default:
		if instruction >= llgoAtomicOpBase && instruction <= llgoAtomicOpLast {
			return emissionIntrinsicCompileValues, nil
		}
		return 0, fmt.Errorf("unknown llgo intrinsic instruction %d", instruction)
	}
}

func emissionRoots(values []ssa.Value, directFunction bool) []emissionCallValueRoot {
	roots := make([]emissionCallValueRoot, len(values))
	for index, value := range values {
		roots[index] = emissionCallValueRoot{value: value, directFunction: directFunction}
	}
	return roots
}

func emissionCompileValuesRoots(values []ssa.Value, kind int) ([]emissionCallValueRoot, error) {
	limit := len(values)
	if kind == fnHasVArg {
		if limit == 0 {
			return nil, fmt.Errorf("variadic lowering has no SSA varargs value")
		}
		// compileVArg reads the frontend's already-populated varargs slots; it
		// does not call compileValue on the trailing slice itself.
		limit--
	}
	return emissionRoots(values[:limit], false), nil
}

func emissionStaticArrayLenRoot(argument ssa.Value) (ssa.Value, bool) {
	if load, ok := argument.(*ssa.UnOp); ok && load.Op == token.MUL {
		if _, ok := types.Unalias(load.Type()).Underlying().(*types.Array); ok {
			return load.X, true
		}
	}
	if pointer, ok := types.Unalias(argument.Type()).Underlying().(*types.Pointer); ok {
		if _, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array); ok {
			return argument, true
		}
	}
	return nil, false
}

func emissionIsStaticOffsetOfArgument(argument ssa.Value) bool {
	if field, ok := argument.(*ssa.Field); ok {
		structure, ok := field.X.Type().Underlying().(*types.Struct)
		return ok && field.Field >= 0 && field.Field < structure.NumFields()
	}
	load, ok := argument.(*ssa.UnOp)
	if !ok || load.Op != token.MUL {
		return false
	}
	field, ok := load.X.(*ssa.FieldAddr)
	if !ok {
		return false
	}
	_, structure, ok := fieldAddrStruct(field)
	return ok && field.Field >= 0 && field.Field < structure.NumFields()
}

func (u *EmissionUniverse) builtinCallValueRoots(ctx *context, builtin *ssa.Builtin, call *ssa.CallCommon) ([]emissionCallValueRoot, error) {
	if builtin == nil || call == nil {
		return nil, fmt.Errorf("incomplete builtin call")
	}
	arguments := call.Args
	switch builtin.Name() {
	case "len", "cap":
		if len(arguments) == 1 {
			if sideEffect, ok := emissionStaticArrayLenRoot(arguments[0]); ok {
				return emissionRoots([]ssa.Value{sideEffect}, false), nil
			}
		}
	case "Offsetof":
		if len(arguments) == 1 && emissionIsStaticOffsetOfArgument(arguments[0]) {
			return nil, nil
		}
	}
	return emissionRoots(arguments, false), nil
}

func (u *EmissionUniverse) intrinsicCallValueRoots(instruction int, arguments []ssa.Value, kind int) ([]emissionCallValueRoot, error) {
	policy, err := emissionIntrinsicPolicy(instruction)
	if err != nil {
		return nil, err
	}
	require := func(count int) error {
		if len(arguments) < count {
			return fmt.Errorf("llgo intrinsic instruction %d has %d arguments; need at least %d", instruction, len(arguments), count)
		}
		return nil
	}
	switch policy {
	case emissionIntrinsicNoValues:
		return nil, nil
	case emissionIntrinsicRawAllValues:
		return emissionRoots(arguments, false), nil
	case emissionIntrinsicCompileValues:
		return emissionCompileValuesRoots(arguments, kind)
	case emissionIntrinsicFirstValue:
		if err := require(1); err != nil {
			return nil, err
		}
		return emissionRoots(arguments[:1], false), nil
	case emissionIntrinsicFirstTwoValues:
		if err := require(2); err != nil {
			return nil, err
		}
		return emissionRoots(arguments[:2], false), nil
	case emissionIntrinsicFixedBeforeVArg:
		return emissionCompileValuesRoots(arguments, fnHasVArg)
	case emissionIntrinsicFuncAddr:
		if err := require(1); err != nil {
			return nil, err
		}
		makeInterface, ok := arguments[0].(*ssa.MakeInterface)
		if !ok {
			return nil, fmt.Errorf("llgo.funcAddr argument has SSA type %T; want *ssa.MakeInterface", arguments[0])
		}
		if function, ok := makeInterface.X.(*ssa.Function); ok {
			return []emissionCallValueRoot{{value: function, directFunction: true}}, nil
		}
		return []emissionCallValueRoot{{value: makeInterface.X}}, nil
	case emissionIntrinsicFuncPCABI0:
		if err := require(1); err != nil {
			return nil, err
		}
		return emissionFuncPCABI0Roots(arguments[0])
	case emissionIntrinsicAsm:
		if len(arguments) < 1 || len(arguments) > 2 {
			return nil, fmt.Errorf("llgo.asm has %d arguments; want one or two", len(arguments))
		}
		if len(arguments) == 1 {
			return nil, nil
		}
		registerMap, ok := arguments[1].(*ssa.MakeMap)
		if !ok {
			return nil, nil
		}
		refs := registerMap.Referrers()
		if refs == nil {
			return nil, nil
		}
		var roots []emissionCallValueRoot
		for _, reference := range *refs {
			update, ok := reference.(*ssa.MapUpdate)
			if !ok {
				continue
			}
			value, ok := update.Value.(*ssa.MakeInterface)
			if !ok {
				return nil, fmt.Errorf("llgo.asm register value has SSA type %T; want *ssa.MakeInterface", update.Value)
			}
			roots = append(roots, emissionCallValueRoot{value: value.X})
		}
		return roots, nil
	default:
		return nil, fmt.Errorf("unsupported llgo intrinsic operand policy %d", policy)
	}
}

func emissionFuncPCABI0Roots(value ssa.Value) ([]emissionCallValueRoot, error) {
	switch value := value.(type) {
	case *ssa.MakeInterface:
		return emissionFuncPCABI0Roots(value.X)
	case *ssa.Function:
		if extractTrampolineCName(value.Name()) != "" {
			// funcPCABI0 synthesizes the corresponding C declaration directly.
			return nil, nil
		}
		return []emissionCallValueRoot{{value: value, directFunction: true}}, nil
	case *ssa.MakeClosure:
		return emissionFuncPCABI0Roots(value.Fn)
	default:
		if value.Type() != nil {
			if _, ok := types.Unalias(value.Type()).Underlying().(*types.Interface); ok {
				return []emissionCallValueRoot{{value: value}}, nil
			}
		}
		return nil, fmt.Errorf("llgo.funcPCABI0 argument has unsupported SSA type %T", value)
	}
}

// callValueRoots mirrors callEx's compileValue/compileFunction dispatch. It is
// shared by ordinary-body function discovery and the dedicated cgo lowering.
func (u *EmissionUniverse) callValueRoots(ctx *context, call *ssa.CallCommon) ([]emissionCallValueRoot, error) {
	if ctx == nil || call == nil || call.Value == nil {
		return nil, fmt.Errorf("incomplete SSA call")
	}
	if call.IsInvoke() {
		kind := fnNormal
		if llssa.HasNameValist(call.Signature()) {
			kind = fnHasVArg
		}
		arguments, err := emissionCompileValuesRoots(call.Args, kind)
		if err != nil {
			return nil, err
		}
		return append([]emissionCallValueRoot{{value: call.Value}}, arguments...), nil
	}
	switch callee := call.Value.(type) {
	case *ssa.Builtin:
		return u.builtinCallValueRoots(ctx, callee, call)
	case *ssa.Function:
		kind := ctx.funcKind(callee)
		if kind == fnIgnore {
			return nil, nil
		}
		_, name, ftype := ctx.funcName(callee)
		roots := []emissionCallValueRoot{{value: callee, directFunction: true}}
		switch ftype {
		case goFunc, cFunc, pyFunc:
			arguments, err := emissionCompileValuesRoots(call.Args, kind)
			if err != nil {
				return nil, err
			}
			return append(roots, arguments...), nil
		case llgoInstr:
			instruction, ok := llgoInstrs[name]
			if !ok {
				return nil, fmt.Errorf("unknown llgo intrinsic %q", name)
			}
			arguments, err := u.intrinsicCallValueRoots(instruction, call.Args, kind)
			if err != nil {
				return nil, fmt.Errorf("llgo intrinsic %q: %w", name, err)
			}
			return append(roots, arguments...), nil
		case ignoredFunc:
			return nil, fmt.Errorf("ignored function %q reached call lowering", callee.Name())
		default:
			return nil, fmt.Errorf("function %q has unknown lowering kind %d", callee.Name(), ftype)
		}
	default:
		arguments, err := emissionCompileValuesRoots(call.Args, fnNormal)
		if err != nil {
			return nil, err
		}
		return append([]emissionCallValueRoot{{value: call.Value}}, arguments...), nil
	}
}

func emissionCallIntrinsicInstruction(ctx *context, call *ssa.CallCommon) (int, bool) {
	if ctx == nil || call == nil {
		return 0, false
	}
	function, ok := call.Value.(*ssa.Function)
	if !ok {
		return 0, false
	}
	_, name, ftype := ctx.funcName(function)
	if ftype != llgoInstr {
		return 0, false
	}
	instruction, ok := llgoInstrs[name]
	return instruction, ok
}

func emissionCallVArgValue(ctx *context, call *ssa.CallCommon) (ssa.Value, bool, error) {
	if ctx == nil || call == nil || call.Value == nil {
		return nil, false, nil
	}
	last := func() (ssa.Value, bool, error) {
		if len(call.Args) == 0 {
			return nil, false, fmt.Errorf("variadic call has no trailing SSA value")
		}
		return call.Args[len(call.Args)-1], true, nil
	}
	if call.IsInvoke() {
		if llssa.HasNameValist(call.Signature()) {
			return last()
		}
		return nil, false, nil
	}
	function, ok := call.Value.(*ssa.Function)
	if !ok {
		return nil, false, nil
	}
	kind := ctx.funcKind(function)
	_, name, ftype := ctx.funcName(function)
	switch ftype {
	case goFunc, cFunc, pyFunc:
		if kind == fnHasVArg {
			return last()
		}
	case llgoInstr:
		instruction, ok := llgoInstrs[name]
		if !ok {
			return nil, false, nil
		}
		switch instruction {
		case llgoString:
			if len(call.Args) < 2 {
				return nil, false, fmt.Errorf("llgo.string has no varargs SSA value")
			}
			return call.Args[1], true, nil
		case llgoPyList, llgoPyTuple:
			return last()
		default:
			policy, err := emissionIntrinsicPolicy(instruction)
			if err != nil {
				return nil, false, err
			}
			if policy == emissionIntrinsicCompileValues && kind == fnHasVArg {
				return last()
			}
		}
	}
	return nil, false, nil
}

func emissionCgoVArgSlots(ctx *context, value ssa.Value) (int64, error) {
	switch value := value.(type) {
	case *ssa.Slice:
		alloc, ok := value.X.(*ssa.Alloc)
		if !ok || !emissionIsVargsAlloc(ctx, alloc) {
			return 0, fmt.Errorf("varargs slice is not backed by a recognized varargs allocation")
		}
		pointer, ok := types.Unalias(alloc.Type()).Underlying().(*types.Pointer)
		if !ok {
			return 0, fmt.Errorf("varargs allocation has non-pointer type %v", alloc.Type())
		}
		array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
		if !ok {
			return 0, fmt.Errorf("varargs allocation has non-array element type %v", pointer.Elem())
		}
		return array.Len(), nil
	case *ssa.Const:
		if value.Value == nil {
			return 0, nil
		}
	case *ssa.Parameter:
		if value.Parent() != nil && llssa.HasNameValist(value.Parent().Signature) {
			return 0, nil
		}
	}
	return 0, fmt.Errorf("unsupported varargs SSA value %T", value)
}

type emissionCgoLoweredCall struct {
	call     *ssa.Call
	compiled bool
	roots    []emissionCallValueRoot
}

type emissionCgoLoweringPlan struct {
	calls         []emissionCgoLoweredCall
	evaluated     map[ssa.Instruction]none
	directReturns map[*ssa.Return]ssa.Value
}

func emissionSkipsSyntheticMakeSliceAlloc(alloc *ssa.Alloc) bool {
	if alloc == nil || alloc.Comment != "makeslice" {
		return false
	}
	refs := alloc.Referrers()
	if refs == nil || len(*refs) != 1 {
		return false
	}
	slice, ok := (*refs)[0].(*ssa.Slice)
	if !ok || slice.X != alloc || slice.Low != nil || slice.High == nil || slice.Max != nil {
		return false
	}
	pointer, ok := types.Unalias(alloc.Type()).Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
	if !ok {
		return false
	}
	if high, ok := slice.High.(*ssa.Const); ok {
		if length, exact := constant.Int64Val(high.Value); exact && length >= 0 && length <= array.Len() {
			return false
		}
	}
	return true
}

// cgoLoweringPlan mirrors compileBlock's dedicated first-block path. An SSA
// instruction passed to compileValue is usable only if an earlier selected
// Alloc, _cgo_ pointer load, or Call already populated bvals; compileValue does
// not recursively lower its producer.
func (u *EmissionUniverse) cgoLoweringPlan(ctx *context, fn *ssa.Function) (*emissionCgoLoweringPlan, error) {
	plan := &emissionCgoLoweringPlan{
		evaluated:     make(map[ssa.Instruction]none),
		directReturns: make(map[*ssa.Return]ssa.Value),
	}
	if fn == nil || len(fn.Blocks) == 0 {
		return plan, nil
	}
	macro := isCgoCmacro(fn.Name())
	available := make(map[ssa.Instruction]none)
	validate := func(call *ssa.Call, roots []emissionCallValueRoot) error {
		for _, root := range roots {
			instruction, ok := root.value.(ssa.Instruction)
			if !ok {
				continue
			}
			if _, ok := available[instruction]; !ok {
				return fmt.Errorf(
					"prepare emission universe: cgo function %q call %q consumes unavailable SSA producer %T %q; dedicated cgo lowering does not compile that instruction",
					fn.Name(), call.String(), instruction, instruction.String(),
				)
			}
		}
		return nil
	}
	for _, instruction := range fn.Blocks[0].Instrs {
		switch instruction := instruction.(type) {
		case *ssa.Alloc:
			if emissionIsVargsAlloc(ctx, instruction) || emissionSkipsSyntheticMakeSliceAlloc(instruction) {
				// Both lowering fast paths return before recording bvals.
				continue
			}
			available[instruction] = none{}
			plan.evaluated[instruction] = none{}
		case *ssa.UnOp:
			if instruction.Op == token.MUL && strings.HasPrefix(instruction.X.Name(), "_cgo_") {
				available[instruction] = none{}
				plan.evaluated[instruction] = none{}
			}
		case *ssa.ChangeType:
			// Go 1.26's _Cfunc__CMalloc wrapper converts its named size_t
			// parameter to uint64 before entering the generated
			// //go:cgo_unsafe_args adapter. This is a value-only operation and
			// belongs to the same dedicated first-block lowering; certify its
			// producer explicitly instead of making compileValue reconstruct an
			// unplanned instruction.
			if producer, ok := instruction.X.(ssa.Instruction); ok {
				if _, ready := available[producer]; !ready {
					return nil, fmt.Errorf(
						"prepare emission universe: cgo function %q ChangeType consumes unavailable SSA producer %T %q",
						fn.Name(), producer, producer.String(),
					)
				}
			}
			available[instruction] = none{}
			plan.evaluated[instruction] = none{}
		case *ssa.Call:
			var roots []emissionCallValueRoot
			var err error
			compiled := !macro
			if macro {
				if len(instruction.Call.Args) == 0 {
					return nil, fmt.Errorf("prepare emission universe: cgo macro %q call has no result-pointer argument", fn.Name())
				}
				roots = []emissionCallValueRoot{{value: instruction.Call.Args[0]}}
			} else {
				roots, err = u.callValueRoots(ctx, &instruction.Call)
				if err != nil {
					return nil, fmt.Errorf("prepare emission universe: cgo function %q: %w", fn.Name(), err)
				}
				if varargs, ok, varargErr := emissionCallVArgValue(ctx, &instruction.Call); varargErr != nil {
					return nil, fmt.Errorf("prepare emission universe: cgo function %q: %w", fn.Name(), varargErr)
				} else if ok {
					slots, slotErr := emissionCgoVArgSlots(ctx, varargs)
					if slotErr != nil {
						return nil, fmt.Errorf("prepare emission universe: cgo function %q: %w", fn.Name(), slotErr)
					}
					if slots != 0 {
						return nil, fmt.Errorf("prepare emission universe: cgo function %q has %d non-empty varargs slots whose Store instructions are not lowered", fn.Name(), slots)
					}
				}
			}
			if err := validate(instruction, roots); err != nil {
				return nil, err
			}
			plan.calls = append(plan.calls, emissionCgoLoweredCall{call: instruction, compiled: compiled, roots: roots})
			plan.evaluated[instruction] = none{}
			if compiled {
				available[instruction] = none{}
			}
		case *ssa.Return:
			plan.evaluated[instruction] = none{}
			// Traditional generated adapters return the side-channel result
			// installed by _cgo_runtime_cgocall. Go 1.26's _Cfunc__CMalloc
			// instead returns one evaluated direct-call SSA value. Freeze that
			// exceptional dataflow here so codegen does not rediscover it from
			// a function name or from whatever happens to be present in bvals.
			if len(instruction.Results) == 1 {
				if resultCall, ok := instruction.Results[0].(*ssa.Call); ok {
					if _, ready := available[resultCall]; ready {
						plan.directReturns[instruction] = resultCall
					}
				}
			}
		}
	}
	return plan, nil
}
