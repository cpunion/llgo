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
	"strings"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroWorkerForeignInterfaceCallShape is the narrowly-scoped bridge for a
// closed interface call whose sole method entry is an x/tools promoted wrapper
// around one variadic native declaration. A Go []any cannot be forwarded to a
// C ellipsis. The call site still owns the concrete synthetic vararg operands,
// so this recipe projects the receiver there and builds the ordinary typed
// worker record directly. It neither adds a dynamic registry nor changes the
// universal Go descriptor ABI.
type coroWorkerForeignInterfaceCallShape struct {
	candidate   coroInterfaceDispatchCandidate
	wrapper     *ssa.Function
	foreignCall *ssa.Call
	projection  ssa.Value
}

func validateCoroWorkerForeignInterfaceCall(
	audit *coroPhysicalPureSSAAudit,
	call *ssa.Call,
	pointerSize int,
) (shape coroWorkerForeignCallShape, recognized bool, err error) {
	if audit == nil || audit.plan == nil || audit.universe == nil || audit.ctx == nil ||
		call == nil || call.Common() == nil {
		return shape, false, nil
	}
	common := call.Common()
	if !common.IsInvoke() || common.StaticCallee() != nil || common.Method == nil ||
		!llssa.HasNameValist(common.Signature()) {
		return shape, false, nil
	}
	callPlan, planned := audit.plan.CallPlan(call)
	if !planned || callPlan.Open || len(callPlan.Targets) != 1 {
		return shape, false, nil
	}
	dispatch, dispatchErr := resolveCoroInterfaceDispatchPlan(audit.plan, audit.universe, call)
	if dispatchErr != nil || dispatch == nil || len(dispatch.candidates) != 1 {
		return shape, false, nil
	}
	candidate := dispatch.candidates[0]
	wrapper := candidate.methodEntry
	if wrapper == nil || wrapper != candidate.function || wrapper.Signature == nil ||
		!strings.HasPrefix(wrapper.Synthetic, "wrapper for func ") ||
		len(wrapper.Params) < 2 || !llssa.HasNameValist(wrapper.Signature) {
		return shape, false, nil
	}

	foreignCall, projection, wrapperErr := validateCoroWorkerForeignInterfaceWrapper(wrapper)
	if wrapperErr != nil {
		return shape, true, wrapperErr
	}
	base, foreign, foreignErr := validateCoroWorkerForeignCallWithAuthority(
		audit.foreignCallAuthority(), foreignCall, pointerSize,
	)
	if !foreign {
		return shape, false, nil
	}
	if foreignErr != nil {
		return shape, true, fmt.Errorf("promoted native method call: %w", foreignErr)
	}
	if base.mode != coroForeignCallModeWorker {
		return shape, true, fmt.Errorf(
			"promoted variadic native method requires the bounded worker mode, got %d", base.mode,
		)
	}
	if len(base.rawCallbacks) != 0 || len(base.reentryCallbacks) != 0 ||
		base.reentry != coro.ReentryNone {
		return shape, true, fmt.Errorf("promoted variadic native method has an unsupported callback boundary")
	}

	lastWrapperParam := len(wrapper.Params) - 1
	if len(common.Args) != lastWrapperParam || len(foreignCall.Common().Args) != len(wrapper.Params) ||
		foreignCall.Common().Args[len(foreignCall.Common().Args)-1] != wrapper.Params[lastWrapperParam] {
		return shape, true, fmt.Errorf("promoted variadic native method lost its exact source argument mapping")
	}
	for index := 1; index < lastWrapperParam; index++ {
		if foreignCall.Common().Args[index] != wrapper.Params[index] {
			return shape, true, fmt.Errorf(
				"promoted variadic native method fixed argument %d is not forwarded exactly", index-1,
			)
		}
	}
	if base.signature == nil || base.signature.Params().Len() != lastWrapperParam ||
		base.argc != lastWrapperParam {
		return shape, true, fmt.Errorf("promoted variadic native method has an inconsistent fixed C signature")
	}

	varargs, varargErr := coroWorkerForeignVariadicValues(
		audit.ctx, call, common.Args[len(common.Args)-1],
	)
	if varargErr != nil {
		return shape, true, fmt.Errorf("promoted variadic native method arguments: %w", varargErr)
	}
	params := make([]*types.Var, 0, base.signature.Params().Len()+len(varargs))
	for index := 0; index < base.signature.Params().Len(); index++ {
		parameter := base.signature.Params().At(index)
		params = append(params, types.NewParam(parameter.Pos(), parameter.Pkg(), parameter.Name(), parameter.Type()))
	}
	for index, value := range varargs {
		if value == nil {
			return shape, true, fmt.Errorf("promoted variadic native method argument %d is nil", index)
		}
		typ := audit.ctx.patchType(value.Type())
		if !coroWorkerForeignRecordValueType(
			audit.universe, typ, true, make(map[types.Type]bool),
		) {
			return shape, true, fmt.Errorf(
				"promoted variadic native method argument %d type %s cannot enter a typed worker record",
				index, typ,
			)
		}
		params = append(params, types.NewParam(token.NoPos, nil, fmt.Sprintf("v%d", index), typ))
	}
	results := base.signature.Results()
	recordSignature := types.NewSignatureType(
		nil, nil, nil, types.NewTuple(params...), results, false,
	)

	arguments := make([]ssa.Value, 0, len(common.Args)-1+len(varargs))
	arguments = append(arguments, common.Args[:len(common.Args)-1]...)
	arguments = append(arguments, varargs...)
	for index, value := range arguments[:len(common.Args)-1] {
		sourceType := audit.ctx.patchType(value.Type())
		targetType := recordSignature.Params().At(index + 1).Type()
		if !types.Identical(sourceType, targetType) &&
			!coroWorkerForeignOpaquePointerFacadeCompatible(sourceType, targetType) {
			return shape, true, fmt.Errorf(
				"promoted variadic native method fixed argument %d has incompatible transport %v -> %v",
				index, sourceType, targetType,
			)
		}
	}

	base.interfaceCall = &coroWorkerForeignInterfaceCallShape{
		candidate: candidate, wrapper: wrapper, foreignCall: foreignCall, projection: projection,
	}
	base.signature = recordSignature
	base.arguments = arguments
	base.argc = 1 + len(arguments) // receiver projection plus source arguments
	base.variadic = true
	base.nilGuard = false
	base.record, base.resultField, base.argumentBase =
		coroWorkerForeignRecordLayout(recordSignature, base.result, nil)
	return base, true, nil
}

// validateCoroWorkerForeignInterfaceWrapper accepts only the complete x/tools
// promoted-wrapper shape needed by the call-site specialization. The receiver
// projection may contain field address/load and representation-only pointer
// conversions; all fixed parameters and the terminal result must be forwarded
// exactly to one native call.
func validateCoroWorkerForeignInterfaceWrapper(
	wrapper *ssa.Function,
) (*ssa.Call, ssa.Value, error) {
	if wrapper == nil || wrapper.Signature == nil || len(wrapper.Blocks) != 1 ||
		len(wrapper.Blocks[0].Succs) != 0 || len(wrapper.Params) < 2 {
		return nil, nil, fmt.Errorf("promoted variadic native method requires one exact single-block wrapper")
	}
	block := wrapper.Blocks[0]
	before := make(map[ssa.Instruction]bool)
	var foreignCall *ssa.Call
	var terminal *ssa.Return
	for _, instruction := range block.Instrs {
		switch instruction := instruction.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.FieldAddr, *ssa.UnOp, *ssa.ChangeType, *ssa.Convert:
			if foreignCall != nil {
				return nil, nil, fmt.Errorf("promoted variadic native method computes a receiver after its native call")
			}
			before[instruction] = true
		case *ssa.Call:
			if foreignCall != nil {
				return nil, nil, fmt.Errorf("promoted variadic native method contains multiple calls")
			}
			foreignCall = instruction
		case *ssa.Return:
			if terminal != nil || instruction != block.Instrs[len(block.Instrs)-1] {
				return nil, nil, fmt.Errorf("promoted variadic native method has no exact terminal return")
			}
			terminal = instruction
		default:
			return nil, nil, fmt.Errorf(
				"promoted variadic native method contains unsupported instruction %T", instruction,
			)
		}
	}
	if foreignCall == nil || foreignCall.Common() == nil || terminal == nil ||
		foreignCall.Common().StaticCallee() == nil || foreignCall.Common().IsInvoke() ||
		len(foreignCall.Common().Args) != len(wrapper.Params) {
		return nil, nil, fmt.Errorf("promoted variadic native method has no exact static native call")
	}
	resultCount := wrapper.Signature.Results().Len()
	if resultCount == 0 {
		if len(terminal.Results) != 0 {
			return nil, nil, fmt.Errorf("promoted variadic native method returns an unexpected value")
		}
	} else if resultCount != 1 || len(terminal.Results) != 1 || terminal.Results[0] != foreignCall {
		return nil, nil, fmt.Errorf("promoted variadic native method does not return its exact native result")
	}
	projection := foreignCall.Common().Args[0]
	if err := validateCoroWorkerForeignReceiverProjection(wrapper, projection, before); err != nil {
		return nil, nil, err
	}
	return foreignCall, projection, nil
}

func validateCoroWorkerForeignReceiverProjection(
	wrapper *ssa.Function, value ssa.Value, before map[ssa.Instruction]bool,
) error {
	seen := make(map[ssa.Value]bool)
	var visit func(ssa.Value) error
	visit = func(value ssa.Value) error {
		if value == nil {
			return fmt.Errorf("promoted variadic native method has a nil receiver projection")
		}
		if value == wrapper.Params[0] {
			return nil
		}
		if seen[value] {
			return fmt.Errorf("promoted variadic native method has a cyclic receiver projection")
		}
		seen[value] = true
		instruction, ok := value.(ssa.Instruction)
		if !ok || !before[instruction] {
			return fmt.Errorf("promoted variadic native method receiver uses %T outside its exact wrapper prefix", value)
		}
		switch value := value.(type) {
		case *ssa.FieldAddr:
			return visit(value.X)
		case *ssa.UnOp:
			if value.Op != token.MUL {
				return fmt.Errorf("promoted variadic native method receiver uses unary %s", value.Op)
			}
			return visit(value.X)
		case *ssa.ChangeType:
			return visit(value.X)
		case *ssa.Convert:
			return visit(value.X)
		default:
			return fmt.Errorf("promoted variadic native method receiver uses unsupported %T", value)
		}
	}
	return visit(value)
}

func (p *context) compileCoroWorkerForeignInterfaceCall(
	b llssa.Builder, call *ssa.Call, shape coroWorkerForeignCallShape,
) llssa.Expr {
	info := shape.interfaceCall
	if p == nil || !p.hasCoroPhysicalBody() || call == nil || info == nil ||
		info.wrapper == nil || info.foreignCall == nil || info.projection == nil ||
		shape.mode != coroForeignCallModeWorker || shape.target == nil ||
		shape.signature == nil || shape.record == nil || shape.argc != 1+len(shape.arguments) {
		panic("coroutine promoted native interface lowering escaped its frozen typed recipe")
	}
	common := call.Common()
	p.emitPCLineLabel(b, call.Pos())
	intf := p.compileValue(b, common.Value)
	p.compileCoroImplicitNilAccessGuard(b, b.InterfaceTypeWord(intf))
	data := b.InterfaceData(intf)
	receiverType := p.type_(info.candidate.receiver, llssa.InGo)
	var dynamicReceiver llssa.Expr
	if _, pointer := types.Unalias(info.candidate.receiver).Underlying().(*types.Pointer); pointer {
		dynamicReceiver = b.Convert(receiverType, data)
	} else {
		receiverAddress := b.Convert(p.prog.Pointer(receiverType), data)
		p.compileCoroImplicitNilAccessGuard(b, receiverAddress)
		dynamicReceiver = b.LoadKnownNonNil(receiverAddress)
	}
	projection := p.compileCoroWorkerForeignReceiverProjection(
		b, info.wrapper, info.foreignCall, info.projection, dynamicReceiver,
	)

	oldInCFunc := p.inCFunc
	p.inCFunc = true
	compiled := make([]llssa.Expr, 0, shape.argc)
	compiled = append(compiled, projection)
	for _, argument := range shape.arguments {
		compiled = append(compiled, p.compileValue(b, argument))
	}
	p.inCFunc = oldInCFunc
	return p.compileCoroWorkerForeignTransaction(
		b, shape, llssa.Nil, compiled, p.compileCoroCallKeepaliveSlots(b, call),
	)
}

func (p *context) compileCoroWorkerForeignReceiverProjection(
	b llssa.Builder,
	wrapper *ssa.Function,
	foreignCall *ssa.Call,
	projection ssa.Value,
	dynamicReceiver llssa.Expr,
) llssa.Expr {
	values := map[ssa.Value]llssa.Expr{wrapper.Params[0]: dynamicReceiver}
	for _, instruction := range wrapper.Blocks[0].Instrs {
		if instruction == foreignCall {
			break
		}
		switch instruction := instruction.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.FieldAddr:
			source, ok := values[instruction.X]
			if !ok {
				panic("promoted native receiver field address lost its frozen operand")
			}
			p.compileCoroImplicitNilAccessGuard(b, source)
			values[instruction] = b.FieldAddr(source, instruction.Field)
		case *ssa.UnOp:
			source, ok := values[instruction.X]
			if !ok || instruction.Op != token.MUL {
				panic("promoted native receiver load lost its frozen operand")
			}
			p.compileCoroImplicitNilAccessGuard(b, source)
			values[instruction] = b.LoadKnownNonNil(source)
		case *ssa.ChangeType:
			source, ok := values[instruction.X]
			if !ok {
				panic("promoted native receiver change-type lost its frozen operand")
			}
			values[instruction] = b.ChangeType(p.type_(instruction.Type(), llssa.InGo), source)
		case *ssa.Convert:
			source, ok := values[instruction.X]
			if !ok {
				panic("promoted native receiver conversion lost its frozen operand")
			}
			values[instruction] = b.Convert(p.type_(instruction.Type(), llssa.InGo), source)
		default:
			panic(fmt.Sprintf("promoted native receiver projection contains unsupported %T", instruction))
		}
	}
	value, ok := values[projection]
	if !ok {
		panic("promoted native receiver projection did not produce its terminal value")
	}
	return value
}
