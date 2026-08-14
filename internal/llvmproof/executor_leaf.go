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

// Package llvmproof contains fail-closed structural proofs over target-selected
// LLVM modules. It deliberately knows nothing about Go declarations or
// coroutine planning; the frontend binds a returned proof to an exact physical
// symbol and typed ABI separately.
package llvmproof

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/xgo-dev/llvm"
)

// ExecutorLeafProof proves that one LLVM definition and its complete direct
// call closure execute an acyclic LLVM instruction graph without entering an
// external, indirect, inline-assembly, or callback boundary.
//
// The proof permits ordinary memory operations and only target-supported,
// naturally aligned atomics up to 64 bits. It does not claim async-signal
// safety, memory safety, or absence of traps; those are independent API
// contracts. Its sole authority is executor progress and non-retention of
// pointer-bearing ABI parameters: the closure cannot wait for an event,
// transfer control to an implementation absent from the exact target-selected
// module, or publish a caller pointer beyond return.
type ExecutorLeafProof struct {
	Symbol        string
	Signature     string
	TargetTriple  string
	DataLayout    string
	CallClosure   []string
	ClosureSHA256 string
}

// LLVM C's opcode enum has kept these values stable since before the supported
// LLVM 19 floor. The Go binding currently omits symbolic Opcode constants for
// them even though it exposes their builders.
const (
	llvmOpcodeFence         llvm.Opcode = 55
	llvmOpcodeAtomicCmpXchg llvm.Opcode = 56
	llvmOpcodeAtomicRMW     llvm.Opcode = 57
	llvmOpcodeAddrSpaceCast llvm.Opcode = 60
	llvmOpcodeFreeze        llvm.Opcode = 68
)

// ProveExecutorLeaf constructs an exact proof for symbol. Unknown instructions,
// CFG cycles, recursive calls, external/indirect calls, inline assembly, and
// exception edges fail closed. LLVM's exact trap/debugtrap intrinsics are
// accepted as finite terminal operations; every other intrinsic must carry the
// complete progress attribute set below.
func ProveExecutorLeaf(module llvm.Module, symbol string) (ExecutorLeafProof, error) {
	if module.IsNil() || symbol == "" {
		return ExecutorLeafProof{}, fmt.Errorf("LLVM executor-leaf proof requires a module and symbol")
	}
	root := module.NamedFunction(symbol)
	if root.IsNil() || root.IsDeclaration() || root.BasicBlocksCount() == 0 {
		return ExecutorLeafProof{}, fmt.Errorf("LLVM executor-leaf proof: symbol %q has no definition", symbol)
	}
	if root.FunctionCallConv() != llvm.CCallConv {
		return ExecutorLeafProof{}, fmt.Errorf(
			"LLVM executor-leaf proof: symbol %q uses non-C calling convention %d",
			symbol, root.FunctionCallConv(),
		)
	}

	visiting := make(map[llvm.Value]bool)
	proved := make(map[llvm.Value]bool)
	closure := make(map[string]llvm.Value)
	var prove func(llvm.Value) error
	prove = func(function llvm.Value) error {
		if proved[function] {
			return nil
		}
		if visiting[function] {
			return fmt.Errorf("recursive direct-call closure reaches %q", function.Name())
		}
		if function.IsNil() || function.IsDeclaration() || function.BasicBlocksCount() == 0 {
			return fmt.Errorf("direct callee %q has no definition", function.Name())
		}
		if err := proveAcyclicCFG(function); err != nil {
			return err
		}
		if err := proveExecutorLeafAtomics(module, function); err != nil {
			return err
		}
		if err := proveNoPointerRetention(function); err != nil {
			return err
		}
		visiting[function] = true
		closure[function.Name()] = function
		for block := function.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
			for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
				opcode := instruction.InstructionOpcode()
				if !executorLeafOpcode(opcode) {
					return fmt.Errorf(
						"function %q contains unsupported LLVM opcode %d",
						function.Name(), uint32(opcode),
					)
				}
				if opcode != llvm.Call {
					continue
				}
				callee := instruction.CalledValue().IsAFunction()
				if callee.IsNil() {
					return fmt.Errorf(
						"function %q contains an indirect or inline-assembly call",
						function.Name(),
					)
				}
				if callee.IntrinsicID() != 0 {
					if err := validateExecutorLeafIntrinsic(callee); err != nil {
						return fmt.Errorf(
							"function %q calls intrinsic %q: %w",
							function.Name(), callee.Name(), err,
						)
					}
					closure[callee.Name()] = callee
					continue
				}
				if err := prove(callee); err != nil {
					return fmt.Errorf("function %q: %w", function.Name(), err)
				}
			}
		}
		delete(visiting, function)
		proved[function] = true
		return nil
	}
	if err := prove(root); err != nil {
		return ExecutorLeafProof{}, fmt.Errorf(
			"LLVM executor-leaf proof for %q: %w", symbol, err,
		)
	}

	names := make([]string, 0, len(closure))
	for name := range closure {
		names = append(names, name)
	}
	sort.Strings(names)
	closureSHA256 := executorLeafClosureSHA256(module, names, module.DataLayout())
	return ExecutorLeafProof{
		Symbol:        symbol,
		Signature:     root.GlobalValueType().String(),
		TargetTriple:  module.Target(),
		DataLayout:    module.DataLayout(),
		CallClosure:   names,
		ClosureSHA256: closureSHA256,
	}, nil
}

func executorLeafClosureSHA256(module llvm.Module, names []string, dataLayout string) string {
	var frozen strings.Builder
	fmt.Fprintf(
		&frozen,
		"%d:%s\n%d:%s\n",
		len(module.Target()), module.Target(),
		len(dataLayout), dataLayout,
	)
	for _, name := range names {
		function := module.NamedFunction(name)
		fmt.Fprintf(
			&frozen,
			"%d:%s\n%d:%s\n",
			len(name), name,
			len(function.String()), function.String(),
		)
		if function.IntrinsicID() != 0 {
			for _, attribute := range []string{
				"nocallback", "nofree", "nosync", "nounwind", "willreturn",
			} {
				present := !function.GetEnumFunctionAttribute(
					llvm.AttributeKindID(attribute),
				).IsNil()
				fmt.Fprintf(
					&frozen,
					"%d:%s=%t\n",
					len(attribute), attribute, present,
				)
			}
		}
	}
	sum := sha256.Sum256([]byte(frozen.String()))
	return hex.EncodeToString(sum[:])
}

// ProveExecutorLeafForDataLayout rebinds a structural executor-leaf proof to
// another target-data spelling only when every LLVM type actually reachable
// from that closure has identical ABI size, alignment, and aggregate offsets.
// This is intentionally closure-local: LLVM 22 wasm Clang may omit an i128
// alignment entry used by LLGo's target machine, but a void debug-trap leaf is
// unaffected while a closure which mentions i128 remains rejected.
func ProveExecutorLeafForDataLayout(
	module llvm.Module,
	symbol string,
	dataLayout string,
) (ExecutorLeafProof, error) {
	proof, err := ProveExecutorLeaf(module, symbol)
	if err != nil {
		return ExecutorLeafProof{}, err
	}
	if dataLayout == proof.DataLayout {
		return proof, nil
	}
	if dataLayout == "" {
		return ExecutorLeafProof{}, fmt.Errorf(
			"LLVM executor-leaf proof for %q: alternate data layout is empty", symbol,
		)
	}
	if err := executorLeafClosureDataLayoutsCompatible(
		module, proof.CallClosure, dataLayout,
	); err != nil {
		return ExecutorLeafProof{}, fmt.Errorf(
			"LLVM executor-leaf proof for %q: alternate data layout: %w", symbol, err,
		)
	}
	proof.DataLayout = dataLayout
	proof.ClosureSHA256 = executorLeafClosureSHA256(
		module, proof.CallClosure, dataLayout,
	)
	return proof, nil
}

func executorLeafClosureDataLayoutsCompatible(
	module llvm.Module,
	closure []string,
	dataLayout string,
) error {
	if module.IsNil() || module.DataLayout() == "" || dataLayout == "" {
		return fmt.Errorf("requires two non-empty target data layouts")
	}
	current := llvm.NewTargetData(module.DataLayout())
	alternate := llvm.NewTargetData(dataLayout)
	defer current.Dispose()
	defer alternate.Dispose()
	if current.ByteOrder() != alternate.ByteOrder() ||
		current.PointerSize() != alternate.PointerSize() {
		return fmt.Errorf("byte order or default pointer width differs")
	}

	seen := make(map[llvm.Type]bool)
	var compareType func(llvm.Type) error
	compareSized := func(typ llvm.Type) error {
		if current.TypeAllocSize(typ) != alternate.TypeAllocSize(typ) ||
			current.ABITypeAlignment(typ) != alternate.ABITypeAlignment(typ) {
			return fmt.Errorf("type %q has different ABI layout", typ.String())
		}
		return nil
	}
	compareType = func(typ llvm.Type) error {
		if typ.IsNil() || seen[typ] {
			return nil
		}
		seen[typ] = true
		switch typ.TypeKind() {
		case llvm.VoidTypeKind, llvm.LabelTypeKind,
			llvm.MetadataTypeKind, llvm.TokenTypeKind:
			return nil
		case llvm.FunctionTypeKind:
			if err := compareType(typ.ReturnType()); err != nil {
				return err
			}
			for _, parameter := range typ.ParamTypes() {
				if err := compareType(parameter); err != nil {
					return err
				}
			}
			return nil
		case llvm.StructTypeKind:
			elements := typ.StructElementTypes()
			// A named zero-field body can be opaque through the C API. Reject it
			// rather than asking TargetData to size a potentially unsized type.
			if typ.StructName() != "" && len(elements) == 0 {
				return fmt.Errorf("named zero-field type %q is not layout-provable", typ.String())
			}
			for index, element := range elements {
				if err := compareType(element); err != nil {
					return err
				}
				if current.ElementOffset(typ, index) != alternate.ElementOffset(typ, index) {
					return fmt.Errorf("type %q field %d has a different ABI offset", typ.String(), index)
				}
			}
			return compareSized(typ)
		case llvm.ArrayTypeKind, llvm.VectorTypeKind:
			if err := compareType(typ.ElementType()); err != nil {
				return err
			}
			return compareSized(typ)
		case llvm.IntegerTypeKind, llvm.FloatTypeKind, llvm.DoubleTypeKind,
			llvm.X86_FP80TypeKind, llvm.FP128TypeKind, llvm.PPC_FP128TypeKind,
			llvm.PointerTypeKind:
			return compareSized(typ)
		default:
			return fmt.Errorf("type %q has unsupported kind %d", typ.String(), typ.TypeKind())
		}
	}

	for _, name := range closure {
		function := module.NamedFunction(name)
		if function.IsNil() {
			return fmt.Errorf("closure function %q is absent", name)
		}
		if err := compareType(function.GlobalValueType()); err != nil {
			return fmt.Errorf("function %q signature: %w", name, err)
		}
		for _, parameter := range function.Params() {
			if err := compareType(parameter.Type()); err != nil {
				return fmt.Errorf("function %q parameter: %w", name, err)
			}
		}
		for block := function.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
			for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
				if err := compareType(instruction.Type()); err != nil {
					return fmt.Errorf("function %q instruction result: %w", name, err)
				}
				for operand := 0; operand < instruction.OperandsCount(); operand++ {
					if err := compareType(instruction.Operand(operand).Type()); err != nil {
						return fmt.Errorf("function %q instruction operand: %w", name, err)
					}
				}
				switch instruction.InstructionOpcode() {
				case llvm.Alloca:
					if err := compareType(instruction.AllocatedType()); err != nil {
						return fmt.Errorf("function %q alloca: %w", name, err)
					}
				case llvm.GetElementPtr:
					if err := compareType(instruction.GEPSourceElementType()); err != nil {
						return fmt.Errorf("function %q GEP: %w", name, err)
					}
				case llvm.Call:
					if err := compareType(instruction.CalledFunctionType()); err != nil {
						return fmt.Errorf("function %q call: %w", name, err)
					}
				}
			}
		}
	}
	return nil
}

// proveNoPointerRetention rejects a definition that can publish a
// pointer-bearing parameter, or a value derived from one, outside
// function-local alloca storage. This is intentionally conservative: passing a
// caller pointer to any intrinsic or direct helper is rejected rather than
// assuming nocapture from an ABI shape. A later interprocedural capture summary
// can relax that restriction without weakening this proof.
func proveNoPointerRetention(function llvm.Value) error {
	tainted := make(map[llvm.Value]bool)
	localRoot := make(map[llvm.Value]llvm.Value)
	localTainted := make(map[llvm.Value]bool)
	localAddressDerived := make(map[llvm.Value]bool)
	localAddressStored := make(map[llvm.Value]bool)
	instructions := make([]llvm.Value, 0)
	for index := 0; index < function.ParamsCount(); index++ {
		parameter := function.Param(index)
		if llvmTypeContainsPointer(parameter.Type()) {
			tainted[parameter] = true
		}
	}
	for block := function.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			instructions = append(instructions, instruction)
			if instruction.InstructionOpcode() == llvm.Alloca {
				localRoot[instruction] = instruction
				localAddressDerived[instruction] = true
			}
		}
	}

	for changed := true; changed; {
		changed = false
		for _, instruction := range instructions {
			if root := executorLeafLocalAddressRoot(instruction, localRoot); !root.IsNil() {
				if previous, exists := localRoot[instruction]; !exists || previous != root {
					localRoot[instruction] = root
					changed = true
				}
			}
			opcode := instruction.InstructionOpcode()
			if opcode == llvm.Store && instruction.OperandsCount() >= 2 {
				value := instruction.Operand(0)
				destination := instruction.Operand(1)
				if tainted[value] {
					if root := localRoot[destination]; !root.IsNil() && !localTainted[root] {
						localTainted[root] = true
						changed = true
					}
				}
				if localAddressDerived[value] {
					if root := localRoot[destination]; !root.IsNil() &&
						!localAddressStored[root] {
						localAddressStored[root] = true
						changed = true
					}
				}
			}
			if opcode == llvm.Load && instruction.OperandsCount() != 0 {
				source := instruction.Operand(0)
				if tainted[source] &&
					!tainted[instruction] {
					tainted[instruction] = true
					changed = true
				}
				if root := localRoot[source]; !root.IsNil() {
					if localTainted[root] &&
						!tainted[instruction] {
						tainted[instruction] = true
						changed = true
					}
					if localAddressStored[root] &&
						!localAddressDerived[instruction] {
						localAddressDerived[instruction] = true
						changed = true
					}
				}
			}
			if !executorLeafPointerProvenanceOpcode(opcode) {
				continue
			}
			for operandIndex := 0; operandIndex < instruction.OperandsCount(); operandIndex++ {
				operand := instruction.Operand(operandIndex)
				if !tainted[instruction] && tainted[operand] {
					tainted[instruction] = true
					changed = true
				}
				if !localAddressDerived[instruction] &&
					localAddressDerived[operand] {
					localAddressDerived[instruction] = true
					changed = true
				}
			}
		}
	}

	for _, instruction := range instructions {
		opcode := instruction.InstructionOpcode()
		switch opcode {
		case llvm.Store:
			if instruction.OperandsCount() < 2 {
				continue
			}
			value := instruction.Operand(0)
			destinationRoot := localRoot[instruction.Operand(1)]
			if localAddressDerived[value] && destinationRoot.IsNil() {
				return fmt.Errorf(
					"function %q publishes a function-local storage address",
					function.Name(),
				)
			}
			if tainted[value] && destinationRoot.IsNil() {
				return fmt.Errorf(
					"function %q stores pointer-derived data outside local storage",
					function.Name(),
				)
			}
		case llvmOpcodeAtomicRMW:
			if instruction.OperandsCount() < 2 {
				continue
			}
			value := instruction.Operand(1)
			if localAddressDerived[value] {
				return fmt.Errorf(
					"function %q atomically publishes a function-local storage address",
					function.Name(),
				)
			}
			if tainted[value] {
				return fmt.Errorf(
					"function %q atomically stores pointer-derived data",
					function.Name(),
				)
			}
		case llvmOpcodeAtomicCmpXchg:
			if instruction.OperandsCount() < 3 {
				continue
			}
			value := instruction.Operand(2)
			if localAddressDerived[value] {
				return fmt.Errorf(
					"function %q atomically publishes a function-local storage address",
					function.Name(),
				)
			}
			if tainted[value] {
				return fmt.Errorf(
					"function %q atomically stores pointer-derived data",
					function.Name(),
				)
			}
		case llvm.Ret:
			for operandIndex := 0; operandIndex < instruction.OperandsCount(); operandIndex++ {
				if localAddressDerived[instruction.Operand(operandIndex)] {
					return fmt.Errorf(
						"function %q returns a function-local storage address",
						function.Name(),
					)
				}
			}
		case llvm.Call:
			operandCount := instruction.OperandsCount()
			for operandIndex := 0; operandIndex+1 < operandCount; operandIndex++ {
				operand := instruction.Operand(operandIndex)
				if !tainted[operand] && !localAddressDerived[operand] {
					continue
				}
				callee := instruction.CalledValue().IsAFunction()
				if !callee.IsNil() && callee.IntrinsicID() == 0 &&
					!callee.IsDeclaration() &&
					operandIndex < callee.ParamsCount() &&
					llvmTypeContainsPointer(callee.Param(operandIndex).Type()) {
					// The recursive closure proof seeds this exact callee
					// parameter and proves that it cannot escape there.
					continue
				}
				if !callee.IsNil() && callee.IntrinsicID() == 0 &&
					callee.IsDeclaration() {
					// The direct-call closure check reports the stronger
					// missing-definition failure independently.
					continue
				}
				if localAddressDerived[operand] {
					return fmt.Errorf(
						"function %q passes a function-local storage address across a call",
						function.Name(),
					)
				}
				return fmt.Errorf(
					"function %q passes pointer-derived data across a call",
					function.Name(),
				)
			}
		}
	}
	return nil
}

func proveExecutorLeafAtomics(module llvm.Module, function llvm.Value) error {
	atomics := make([]llvm.Value, 0)
	for block := function.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			switch instruction.InstructionOpcode() {
			case llvmOpcodeFence, llvmOpcodeAtomicCmpXchg, llvmOpcodeAtomicRMW:
				atomics = append(atomics, instruction)
			case llvm.Load, llvm.Store:
				if instruction.Ordering() != llvm.AtomicOrderingNotAtomic {
					atomics = append(atomics, instruction)
				}
			}
		}
	}
	if len(atomics) == 0 {
		return nil
	}
	if !executorLeafAtomicTargetSupported(module.Target(), function) {
		return fmt.Errorf(
			"function %q uses atomics on unsupported target %q",
			function.Name(), module.Target(),
		)
	}
	if module.DataLayout() == "" {
		return fmt.Errorf(
			"function %q uses atomics without an exact target data layout",
			function.Name(),
		)
	}
	targetData := llvm.NewTargetData(module.DataLayout())
	defer targetData.Dispose()
	for _, instruction := range atomics {
		if instruction.InstructionOpcode() == llvmOpcodeFence {
			continue
		}
		valueType, ok := executorLeafAtomicValueType(instruction)
		if !ok {
			return fmt.Errorf(
				"function %q contains an atomic operation with no exact value type",
				function.Name(),
			)
		}
		bits := targetData.TypeSizeInBits(valueType)
		if bits != 8 && bits != 16 && bits != 32 && bits != 64 {
			return fmt.Errorf(
				"function %q contains an unsupported %d-bit atomic operation",
				function.Name(), bits,
			)
		}
		alignment := instruction.Alignment()
		if alignment == 0 || uint64(alignment)*8 < bits {
			return fmt.Errorf(
				"function %q contains a %d-bit atomic operation with insufficient alignment %d",
				function.Name(), bits, alignment,
			)
		}
	}
	return nil
}

func executorLeafAtomicValueType(instruction llvm.Value) (llvm.Type, bool) {
	switch instruction.InstructionOpcode() {
	case llvm.Load:
		return instruction.Type(), true
	case llvm.Store:
		if instruction.OperandsCount() >= 1 {
			return instruction.Operand(0).Type(), true
		}
	case llvmOpcodeAtomicRMW:
		if instruction.OperandsCount() >= 2 {
			return instruction.Operand(1).Type(), true
		}
	case llvmOpcodeAtomicCmpXchg:
		if instruction.OperandsCount() >= 2 {
			return instruction.Operand(1).Type(), true
		}
	}
	return llvm.Type{}, false
}

func executorLeafAtomicTargetSupported(target string, function llvm.Value) bool {
	architecture := strings.ToLower(target)
	if index := strings.IndexByte(architecture, '-'); index >= 0 {
		architecture = architecture[:index]
	}
	switch architecture {
	case "aarch64", "arm64", "loongarch64", "powerpc64", "powerpc64le",
		"ppc64", "ppc64le", "s390x", "x86_64":
		return true
	case "riscv64":
		return executorLeafFunctionHasTargetFeature(function, "+a")
	case "wasm32", "wasm64":
		return executorLeafFunctionHasTargetFeature(function, "+atomics")
	default:
		return false
	}
}

func executorLeafFunctionHasTargetFeature(function llvm.Value, want string) bool {
	for _, attribute := range function.GetFunctionAttributes() {
		if !attribute.IsString() ||
			attribute.GetStringKind() != "target-features" {
			continue
		}
		for _, feature := range strings.Split(attribute.GetStringValue(), ",") {
			if strings.TrimSpace(feature) == want {
				return true
			}
		}
	}
	return false
}

func executorLeafLocalAddressRoot(
	value llvm.Value,
	roots map[llvm.Value]llvm.Value,
) llvm.Value {
	switch value.InstructionOpcode() {
	case llvm.GetElementPtr, llvm.BitCast, llvmOpcodeAddrSpaceCast:
		if value.OperandsCount() != 0 {
			return roots[value.Operand(0)]
		}
	case llvm.PHI:
		var root llvm.Value
		for index := 0; index < value.OperandsCount(); index++ {
			candidate := roots[value.Operand(index)]
			if candidate.IsNil() || !root.IsNil() && candidate != root {
				return llvm.Value{}
			}
			root = candidate
		}
		return root
	case llvm.Select:
		if value.OperandsCount() >= 3 {
			left := roots[value.Operand(1)]
			right := roots[value.Operand(2)]
			if !left.IsNil() && left == right {
				return left
			}
		}
	}
	return llvm.Value{}
}

func executorLeafPointerProvenanceOpcode(opcode llvm.Opcode) bool {
	switch opcode {
	case llvm.Add, llvm.Sub, llvm.Mul,
		llvm.UDiv, llvm.SDiv, llvm.URem, llvm.SRem,
		llvm.Shl, llvm.LShr, llvm.AShr, llvm.And, llvm.Or, llvm.Xor,
		llvm.GetElementPtr,
		llvm.Trunc, llvm.ZExt, llvm.SExt,
		llvm.PtrToInt, llvm.IntToPtr, llvm.BitCast, llvmOpcodeAddrSpaceCast,
		llvm.PHI, llvm.Call, llvm.Select,
		llvm.ExtractElement, llvm.InsertElement, llvm.ShuffleVector,
		llvm.ExtractValue, llvm.InsertValue, llvmOpcodeFreeze:
		return true
	default:
		return false
	}
}

func llvmTypeContainsPointer(typ llvm.Type) bool {
	switch typ.TypeKind() {
	case llvm.PointerTypeKind:
		return true
	case llvm.StructTypeKind:
		for _, element := range typ.StructElementTypes() {
			if llvmTypeContainsPointer(element) {
				return true
			}
		}
	case llvm.ArrayTypeKind, llvm.VectorTypeKind:
		return llvmTypeContainsPointer(typ.ElementType())
	}
	return false
}

func proveAcyclicCFG(function llvm.Value) error {
	visiting := make(map[llvm.BasicBlock]bool)
	proved := make(map[llvm.BasicBlock]bool)
	var visit func(llvm.BasicBlock) error
	visit = func(block llvm.BasicBlock) error {
		if proved[block] {
			return nil
		}
		if visiting[block] {
			return fmt.Errorf("function %q contains a CFG cycle", function.Name())
		}
		visiting[block] = true
		terminator := block.LastInstruction()
		if terminator.IsNil() {
			return fmt.Errorf("function %q contains an unterminated basic block", function.Name())
		}
		for index := 0; index < terminator.SuccessorsCount(); index++ {
			if err := visit(terminator.Successor(index)); err != nil {
				return err
			}
		}
		delete(visiting, block)
		proved[block] = true
		return nil
	}
	for block := function.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
		if err := visit(block); err != nil {
			return err
		}
	}
	return nil
}

func validateExecutorLeafIntrinsic(function llvm.Value) error {
	switch function.IntrinsicID() {
	case llvm.LookupIntrinsicID("llvm.trap"),
		llvm.LookupIntrinsicID("llvm.debugtrap"):
		// These exact LLVM operations terminate or enter a debugger. Neither
		// can wait for an application event or transfer a coroutine frame.
		return nil
	}
	for _, name := range []string{
		"nocallback", "nofree", "nosync", "nounwind", "willreturn",
	} {
		if function.GetEnumFunctionAttribute(llvm.AttributeKindID(name)).IsNil() {
			return fmt.Errorf("missing %s attribute", name)
		}
	}
	return nil
}

func executorLeafOpcode(opcode llvm.Opcode) bool {
	switch opcode {
	case llvm.Ret, llvm.Br, llvm.Switch, llvm.Unreachable,
		llvm.Add, llvm.FAdd, llvm.Sub, llvm.FSub, llvm.Mul, llvm.FMul,
		llvm.UDiv, llvm.SDiv, llvm.FDiv, llvm.URem, llvm.SRem, llvm.FRem,
		llvm.Shl, llvm.LShr, llvm.AShr, llvm.And, llvm.Or, llvm.Xor,
		llvm.Alloca, llvm.Load, llvm.Store, llvm.GetElementPtr,
		llvm.Trunc, llvm.ZExt, llvm.SExt, llvm.FPToUI, llvm.FPToSI,
		llvm.UIToFP, llvm.SIToFP, llvm.FPTrunc, llvm.FPExt,
		llvm.PtrToInt, llvm.IntToPtr, llvm.BitCast, llvmOpcodeAddrSpaceCast,
		llvm.ICmp, llvm.FCmp, llvm.PHI, llvm.Call, llvm.Select,
		llvm.ExtractElement, llvm.InsertElement, llvm.ShuffleVector,
		llvm.ExtractValue, llvm.InsertValue, llvmOpcodeFreeze,
		llvmOpcodeFence, llvmOpcodeAtomicCmpXchg, llvmOpcodeAtomicRMW:
		return true
	default:
		return false
	}
}
