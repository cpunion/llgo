/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"fmt"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// coroForwardingSourceShape is a source-only observation, not permission to
// remove a frame or transfer result custody. ProgramIR freezes its exact call,
// parameter projections, and unchanged return once. Consumers independently
// bind semantic/ABI/cleanup facts without rediscovering the Go SSA body.
type coroForwardingSourceShape struct {
	call             *ssa.Call
	target           *ssa.Function
	args             []coroForwardingSourceArgument
	adapters         []coroForwardingSourceAdapter
	projections      []ssa.Instruction
	methodProjection bool
	// This canonical wrapper still has a receiver load/nil check to execute.
	// Custody may ignore an unused receiver, but frame elision may not.
	receiverEvaluation ssa.Value
}

type coroForwardingSourceAdapter struct {
	instruction ssa.Instruction
	sourceType  types.Type
	resultType  types.Type
}

type coroForwardingSourceArgument struct {
	sourceParameter int
	// A bound method captures its receiver value directly. Observing this
	// identity does not authorize rereading an indirect lexical capture cell.
	capture       *ssa.FreeVar
	constant      *ssa.Const
	valueType     types.Type
	projectedType types.Type
	fieldPath     []int
	direct        bool
}

func coroForwardingProjectedType(source types.Type, fields []int) (types.Type, bool) {
	for _, field := range fields {
		structure, ok := types.Unalias(source).Underlying().(*types.Struct)
		if !ok || field < 0 || field >= structure.NumFields() {
			return nil, false
		}
		source = structure.Field(field).Type()
	}
	return source, source != nil
}

func (ir *coroProgramIR) forwardingSourceShape(function *ssa.Function) (*coroForwardingSourceShape, error) {
	if ir == nil || !ir.callsFrozen || function == nil {
		return nil, fmt.Errorf("forwarding source shape requires one frozen function")
	}
	facts, present := ir.localBodyFacts[function]
	if !present {
		return nil, fmt.Errorf("function %q has no frozen forwarding source facts", function.Name())
	}
	return facts.forwarding, nil
}

func deriveCoroForwardingSourceShape(function *ssa.Function) *coroForwardingSourceShape {
	if function == nil || function.Signature == nil || len(function.Blocks) != 1 ||
		len(function.Blocks[0].Succs) != 0 || len(function.Blocks[0].Instrs) < 2 {
		return nil
	}
	instructions := function.Blocks[0].Instrs
	adapterParameters := make(map[ssa.Value]*ssa.Parameter)
	valueProjections := make(map[ssa.Instruction]none)
	callIndex := 0
scanAdapters:
	for callIndex < len(instructions) {
		var value ssa.Value
		switch adapter := instructions[callIndex].(type) {
		case *ssa.ChangeType:
			value = adapter.X
		case *ssa.Convert:
			value = adapter.X
		case *ssa.Field, *ssa.FieldAddr, *ssa.Alloc, *ssa.Store:
			// A local copy of an evaluated aggregate is not a pointee read.
			// Every instruction must belong to a proven parameter path below.
			valueProjections[instructions[callIndex]] = none{}
			callIndex++
			continue
		case *ssa.UnOp:
			if adapter.Op != token.MUL {
				break scanAdapters
			}
			valueProjections[adapter] = none{}
			callIndex++
			continue
		default:
			break scanAdapters
		}
		parameter, exact := value.(*ssa.Parameter)
		if !exact || parameter == nil {
			return nil
		}
		adapterParameters[instructions[callIndex].(ssa.Value)] = parameter
		callIndex++
	}
	var call *ssa.Call
	if callIndex < len(instructions) {
		call, _ = instructions[callIndex].(*ssa.Call)
	}
	methodProjection := false
	if call == nil || call.Common() == nil || call.Common().StaticCallee() == nil ||
		len(valueProjections) != 0 && function.Synthetic != "" && function.Signature.Recv() != nil {
		if validateCoroExactMethodWrapper(function) != nil {
			return nil
		}
		call = nil // A leading wrapnilchk is not the forwarded method call.
		for index, instruction := range instructions {
			candidate, isCall := instruction.(*ssa.Call)
			if !isCall || candidate.Common() == nil || candidate.Common().StaticCallee() == nil {
				continue
			}
			if call != nil {
				return nil
			}
			call, callIndex = candidate, index
		}
		if call == nil {
			return nil
		}
		methodProjection = true
		adapterParameters = nil
	}
	if call == nil || call.Common() == nil || call.Common().IsInvoke() ||
		call.Common().Method != nil || call.Common().StaticCallee() == nil {
		return nil
	}
	// DebugRef is not skipped: preserving debug-visible boundaries remains the
	// same conservative source grammar used by the former physical-body scan.
	resultCount := function.Signature.Results().Len()
	returned := make([]ssa.Value, 0, resultCount)
	cursor := callIndex + 1
	switch resultCount {
	case 0:
	case 1:
		returned = append(returned, call)
	default:
		for index := 0; index < resultCount; index++ {
			if cursor >= len(instructions)-1 {
				return nil
			}
			extract, exact := instructions[cursor].(*ssa.Extract)
			if !exact || extract.Tuple != call || extract.Index != index {
				return nil
			}
			returned = append(returned, extract)
			cursor++
		}
	}
	if cursor != len(instructions)-1 {
		return nil
	}
	ret, exact := instructions[cursor].(*ssa.Return)
	if !exact || len(ret.Results) != len(returned) {
		return nil
	}
	for index := range returned {
		if ret.Results[index] != returned[index] {
			return nil
		}
	}

	parameterIndex := make(map[*ssa.Parameter]int, len(function.Params))
	for index, parameter := range function.Params {
		parameterIndex[parameter] = index
	}
	shape := &coroForwardingSourceShape{call: call, target: call.Common().StaticCallee(), methodProjection: methodProjection,
		args: make([]coroForwardingSourceArgument, len(call.Common().Args))}
	usedAdapters := make(map[ssa.Value]none, len(adapterParameters))
	projections := make(map[ssa.Instruction]none)
	for index, value := range call.Common().Args {
		parameter, direct := value.(*ssa.Parameter)
		var fieldPath []int
		if !direct && adapterParameters[value] == nil {
			var consumed map[ssa.Instruction]none
			var projected bool
			parameter, fieldPath, consumed, projected = deriveCoroForwardingValueProjection(function, value)
			if projected {
				for instruction := range consumed {
					projections[instruction] = none{}
				}
				direct = len(fieldPath) == 0 && value == parameter
			}
		} else if !direct {
			parameter = adapterParameters[value]
		}
		argument := coroForwardingSourceArgument{sourceParameter: -1, valueType: value.Type(), direct: direct}
		if parameter != nil {
			sourceIndex, owned := parameterIndex[parameter]
			projectedType, projected := coroForwardingProjectedType(parameter.Type(), fieldPath)
			if !owned || !projected {
				return nil
			}
			argument.sourceParameter = sourceIndex
			argument.projectedType = projectedType
			argument.fieldPath = append([]int(nil), fieldPath...)
			if adapterParameters[value] != nil {
				usedAdapters[value] = none{}
			}
		} else if capture, exact := value.(*ssa.FreeVar); exact && capture.Parent() == function {
			owned := false
			for _, free := range function.FreeVars {
				owned = owned || free == capture
			}
			if !owned {
				return nil
			}
			argument.capture, argument.projectedType, argument.direct = capture, capture.Type(), true
		} else if methodProjection && index == 0 {
			shape.receiverEvaluation = value
		} else {
			literal, exact := value.(*ssa.Const)
			if !exact || literal.Type() == nil {
				return nil
			}
			argument.constant = literal
		}
		shape.args[index] = argument
	}
	if methodProjection {
		for index := 0; index < callIndex; index++ {
			instruction := instructions[index]
			if _, consumed := projections[instruction]; !consumed {
				// ExactMethodWrapper already proved these instructions belong
				// to receiver evaluation. Keep them in the physical body unless
				// the value-only projection consumed every one of them.
				shape.receiverEvaluation = call.Common().Args[0]
			}
			shape.projections = append(shape.projections, instruction)
		}
	} else {
		if len(usedAdapters) != len(adapterParameters) {
			return nil
		}
		for _, instruction := range instructions[:callIndex] {
			if _, projection := valueProjections[instruction]; projection {
				if _, consumed := projections[instruction]; !consumed {
					return nil
				}
				shape.projections = append(shape.projections, instruction)
				continue
			}
			value := instruction.(ssa.Value)
			shape.adapters = append(shape.adapters, coroForwardingSourceAdapter{
				instruction: instruction, sourceType: adapterParameters[value].Type(), resultType: value.Type(),
			})
		}
	}

	return shape
}

// deriveCoroForwardingValueProjection observes pure parameter field paths,
// including the compiler-created receiver spill of a promoted method wrapper.
// Dereferencing a source pointer can fault, and borrowing an eliminated spill
// would outlive a frame-free ramp. Final physical semantics are checked by the
// consumer, not granted by this source observation.
func deriveCoroForwardingValueProjection(
	function *ssa.Function,
	value ssa.Value,
) (parameter *ssa.Parameter, fields []int, consumed map[ssa.Instruction]none, ok bool) {
	if function == nil || len(function.Blocks) != 1 {
		return nil, nil, nil, false
	}
	consumed = make(map[ssa.Instruction]none)
	var valueProjection func(ssa.Value) (*ssa.Parameter, []int, bool)
	var addressProjection func(ssa.Value) (*ssa.Parameter, []int, bool)
	appendField := func(sourceType types.Type, path []int, field int) ([]int, bool) {
		candidate := append(append([]int(nil), path...), field)
		if _, exact := coroForwardingProjectedType(sourceType, candidate); !exact {
			return nil, false
		}
		return candidate, true
	}
	addressProjection = func(address ssa.Value) (*ssa.Parameter, []int, bool) {
		switch address := address.(type) {
		case *ssa.Alloc:
			owned := false
			for _, local := range function.Locals {
				owned = owned || local == address
			}
			if !owned || address.Heap {
				return nil, nil, false
			}
			var store *ssa.Store
			for _, instruction := range function.Blocks[0].Instrs {
				candidate, isStore := instruction.(*ssa.Store)
				if isStore && candidate.Addr == address {
					if store != nil {
						return nil, nil, false
					}
					store = candidate
					continue
				}
				if store == nil {
					for _, operand := range instruction.Operands(nil) {
						if operand != nil && *operand == address {
							return nil, nil, false // A zero-value read precedes the parameter copy.
						}
					}
				}
			}
			if store == nil {
				return nil, nil, false
			}
			root, exact := store.Val.(*ssa.Parameter)
			if !exact || root == nil || root.Parent() != function {
				return nil, nil, false
			}
			consumed[address] = none{}
			consumed[store] = none{}
			return root, nil, true
		case *ssa.FieldAddr:
			base, path, exact := addressProjection(address.X)
			if !exact {
				return nil, nil, false
			}
			path, exact = appendField(base.Type(), path, address.Field)
			if !exact {
				return nil, nil, false
			}
			consumed[address] = none{}
			return base, path, true
		default:
			// In particular, never turn &pointer.Field into a ramp-time
			// projection: it owns a Go nil edge that needs a coroutine outcome.
			return nil, nil, false
		}
	}
	valueProjection = func(candidate ssa.Value) (*ssa.Parameter, []int, bool) {
		switch candidate := candidate.(type) {
		case *ssa.Parameter:
			if candidate.Parent() != function {
				return nil, nil, false
			}
			return candidate, nil, true
		case *ssa.UnOp:
			if candidate.Op != token.MUL {
				return nil, nil, false
			}
			base, path, exact := addressProjection(candidate.X)
			if !exact {
				return nil, nil, false
			}
			consumed[candidate] = none{}
			return base, path, true
		case *ssa.Field:
			base, path, exact := valueProjection(candidate.X)
			if !exact {
				return nil, nil, false
			}
			path, exact = appendField(base.Type(), path, candidate.Field)
			if !exact {
				return nil, nil, false
			}
			consumed[candidate] = none{}
			return base, path, true
		default:
			return nil, nil, false
		}
	}
	parameter, fields, ok = valueProjection(value)
	if !ok {
		return nil, nil, nil, false
	}
	return parameter, fields, consumed, true
}
