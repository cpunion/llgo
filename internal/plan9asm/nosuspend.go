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

package plan9asm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	gllvm "github.com/xgo-dev/llvm"
)

// NoSuspendLeafProof is an exact proof over one translated Plan9 assembly
// function and the complete direct-call closure that can execute beneath it.
// It proves only that execution cannot enter an indirect/external/blocking
// call boundary; it does not claim async-signal safety or erase the physical
// assembly call from generated code.
type NoSuspendLeafProof struct {
	Symbol        string
	Signature     string
	CallClosure   []string
	ClosureSHA256 string
}

// ProveNoSuspendLeaf accepts the deliberately small LLVM instruction/call
// language emitted for bounded Plan9 assembly leaves. Every call must resolve
// either to another defined function in this module or to an LLVM intrinsic
// carrying nofree+nosync+nounwind+willreturn, or to one of the exact bounded
// inline-assembly forms emitted by the pinned Plan9 assembly translator.
// Unknown opcodes, indirect calls, declarations, other inline asm,
// invoke/callbr, and synchronization primitives all fail closed.
func ProveNoSuspendLeaf(translation *ModuleTranslation, symbol string) (NoSuspendLeafProof, error) {
	if translation == nil || translation.Module.IsNil() || symbol == "" {
		return NoSuspendLeafProof{}, fmt.Errorf("plan9asm no-suspend proof requires a translated module and symbol")
	}
	signature, ok := translation.Signatures[symbol]
	if !ok {
		return NoSuspendLeafProof{}, fmt.Errorf("plan9asm no-suspend proof: symbol %q has no translated signature", symbol)
	}
	root := translation.Module.NamedFunction(symbol)
	if root.IsNil() || root.IsDeclaration() || root.BasicBlocksCount() == 0 {
		return NoSuspendLeafProof{}, fmt.Errorf("plan9asm no-suspend proof: symbol %q has no translated definition", symbol)
	}

	visiting := make(map[gllvm.Value]bool)
	proved := make(map[gllvm.Value]bool)
	closure := make(map[string]gllvm.Value)
	var prove func(gllvm.Value) error
	prove = func(function gllvm.Value) error {
		if proved[function] {
			return nil
		}
		if visiting[function] {
			return fmt.Errorf("recursive direct-call closure reaches %q", function.Name())
		}
		if function.IsNil() || function.IsDeclaration() || function.BasicBlocksCount() == 0 {
			return fmt.Errorf("direct callee %q has no definition", function.Name())
		}
		visiting[function] = true
		closure[function.Name()] = function
		for block := function.FirstBasicBlock(); !block.IsNil(); block = gllvm.NextBasicBlock(block) {
			for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = gllvm.NextInstruction(instruction) {
				opcode := instruction.InstructionOpcode()
				if !plan9AsmNoSuspendOpcode(opcode) {
					return fmt.Errorf("function %q contains unsupported LLVM opcode %d", function.Name(), uint32(opcode))
				}
				if opcode != gllvm.Call {
					continue
				}
				calledValue := instruction.CalledValue()
				callee := calledValue.IsAFunction()
				if callee.IsNil() {
					inlineAsm := calledValue.IsAInlineAsm()
					if inlineAsm.IsNil() {
						return fmt.Errorf("function %q contains an indirect call", function.Name())
					}
					if err := validateNoSuspendPlan9AsmInlineAsm(instruction, inlineAsm); err != nil {
						return fmt.Errorf("function %q contains unsupported inline assembly: %w", function.Name(), err)
					}
					continue
				}
				if callee.IntrinsicID() != 0 {
					if err := validateNoSuspendLLVMIntrinsic(callee); err != nil {
						return fmt.Errorf("function %q calls intrinsic %q: %w", function.Name(), callee.Name(), err)
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
		return NoSuspendLeafProof{}, fmt.Errorf("plan9asm no-suspend proof for %q: %w", symbol, err)
	}

	names := make([]string, 0, len(closure))
	for name := range closure {
		names = append(names, name)
	}
	sort.Strings(names)
	var frozen strings.Builder
	for _, name := range names {
		function := closure[name]
		fmt.Fprintf(&frozen, "%d:%s\n%d:%s\n", len(name), name, len(function.String()), function.String())
		if function.IntrinsicID() != 0 {
			attrs := []string{"nocallback", "nofree", "nosync", "nounwind", "willreturn"}
			for _, attr := range attrs {
				present := !function.GetEnumFunctionAttribute(gllvm.AttributeKindID(attr)).IsNil()
				fmt.Fprintf(&frozen, "%d:%s=%t\n", len(attr), attr, present)
			}
		}
	}
	sum := sha256.Sum256([]byte(frozen.String()))
	signatureJSON, err := json.Marshal(signature)
	if err != nil {
		return NoSuspendLeafProof{}, fmt.Errorf("plan9asm no-suspend proof for %q: encode signature: %w", symbol, err)
	}
	return NoSuspendLeafProof{
		Symbol:        symbol,
		Signature:     string(signatureJSON),
		CallClosure:   names,
		ClosureSHA256: hex.EncodeToString(sum[:]),
	}, nil
}

type noSuspendInlineAsmSpec struct {
	constraints     string
	hasSideEffects  bool
	returnAggregate bool
	returnWidths    []int
	parameterWidths []int
}

func validateNoSuspendPlan9AsmInlineAsm(call, inlineAsm gllvm.Value) error {
	if inlineAsm.InlineAsmNeedsAlignedStack() {
		return fmt.Errorf("aligned-stack flag is not allowed")
	}
	if inlineAsm.InlineAsmCanThrow() {
		return fmt.Errorf("can-throw flag is not allowed")
	}
	if dialect := inlineAsm.InlineAsmDialect(); dialect != gllvm.InlineAsmDialectATT {
		return fmt.Errorf("dialect %d is not AT&T", dialect)
	}

	asm := inlineAsm.InlineAsmString()
	var spec noSuspendInlineAsmSpec
	switch asm {
	case "cpuid":
		spec = noSuspendInlineAsmSpec{
			constraints:     "={ax},={bx},={cx},={dx},{ax},{cx},~{dirflag},~{fpsr},~{flags}",
			hasSideEffects:  true,
			returnAggregate: true,
			returnWidths:    []int{32, 32, 32, 32},
			parameterWidths: []int{32, 32},
		}
	case "xgetbv":
		spec = noSuspendInlineAsmSpec{
			constraints:     "={ax},={dx},{cx},~{dirflag},~{fpsr},~{flags}",
			hasSideEffects:  true,
			returnAggregate: true,
			returnWidths:    []int{32, 32},
			parameterWidths: []int{32},
		}
	default:
		if !isPlan9AsmMRS(asm) {
			return fmt.Errorf("template %q is not a bounded translator emission", asm)
		}
		spec = noSuspendInlineAsmSpec{
			constraints:    "=r",
			returnWidths:   []int{64},
			hasSideEffects: false,
		}
		if inlineAsm.InlineAsmConstraintString() == "=r,~{memory}" {
			spec.constraints = "=r,~{memory}"
			spec.hasSideEffects = true
		}
	}

	if constraints := inlineAsm.InlineAsmConstraintString(); constraints != spec.constraints {
		return fmt.Errorf("template %q has constraints %q, want %q", asm, constraints, spec.constraints)
	}
	if sideEffects := inlineAsm.InlineAsmHasSideEffects(); sideEffects != spec.hasSideEffects {
		return fmt.Errorf("template %q side-effects flag is %t, want %t", asm, sideEffects, spec.hasSideEffects)
	}
	if err := validateNoSuspendInlineAsmSignature(call.CalledFunctionType(), spec); err != nil {
		return fmt.Errorf("template %q: %w", asm, err)
	}
	return nil
}

func isPlan9AsmMRS(asm string) bool {
	const prefix = "mrs $0, "
	if !strings.HasPrefix(asm, prefix) || len(asm) == len(prefix) {
		return false
	}
	for _, char := range asm[len(prefix):] {
		if char != '_' && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validateNoSuspendInlineAsmSignature(functionType gllvm.Type, spec noSuspendInlineAsmSpec) error {
	if functionType.TypeKind() != gllvm.FunctionTypeKind || functionType.IsFunctionVarArg() {
		return fmt.Errorf("call does not have a fixed function type")
	}
	parameters := functionType.ParamTypes()
	if err := validateNoSuspendIntegerTypes("parameters", parameters, spec.parameterWidths); err != nil {
		return err
	}

	returnType := functionType.ReturnType()
	if spec.returnAggregate {
		if returnType.TypeKind() != gllvm.StructTypeKind || returnType.IsStructPacked() {
			return fmt.Errorf("return type is not an unpacked struct")
		}
		return validateNoSuspendIntegerTypes("return fields", returnType.StructElementTypes(), spec.returnWidths)
	}
	return validateNoSuspendIntegerTypes("return values", []gllvm.Type{returnType}, spec.returnWidths)
}

func validateNoSuspendIntegerTypes(kind string, types []gllvm.Type, widths []int) error {
	if len(types) != len(widths) {
		return fmt.Errorf("%s count is %d, want %d", kind, len(types), len(widths))
	}
	for index, typ := range types {
		if typ.TypeKind() != gllvm.IntegerTypeKind || typ.IntTypeWidth() != widths[index] {
			return fmt.Errorf("%s[%d] is %s, want i%d", kind, index, typ.String(), widths[index])
		}
	}
	return nil
}

func validateNoSuspendLLVMIntrinsic(function gllvm.Value) error {
	for _, name := range []string{"nofree", "nosync", "nounwind", "willreturn"} {
		if function.GetEnumFunctionAttribute(gllvm.AttributeKindID(name)).IsNil() {
			return fmt.Errorf("missing %s attribute", name)
		}
	}
	return nil
}

func plan9AsmNoSuspendOpcode(opcode gllvm.Opcode) bool {
	switch opcode {
	case gllvm.Ret, gllvm.Br, gllvm.Switch,
		gllvm.Add, gllvm.FAdd, gllvm.Sub, gllvm.FSub, gllvm.Mul, gllvm.FMul,
		gllvm.UDiv, gllvm.SDiv, gllvm.FDiv, gllvm.URem, gllvm.SRem, gllvm.FRem,
		gllvm.Shl, gllvm.LShr, gllvm.AShr, gllvm.And, gllvm.Or, gllvm.Xor,
		gllvm.Alloca, gllvm.Load, gllvm.Store, gllvm.GetElementPtr,
		gllvm.Trunc, gllvm.ZExt, gllvm.SExt, gllvm.FPToUI, gllvm.FPToSI,
		gllvm.UIToFP, gllvm.SIToFP, gllvm.FPTrunc, gllvm.FPExt,
		gllvm.PtrToInt, gllvm.IntToPtr, gllvm.BitCast,
		gllvm.ICmp, gllvm.FCmp, gllvm.PHI, gllvm.Call, gllvm.Select,
		gllvm.ExtractElement, gllvm.InsertElement, gllvm.ShuffleVector,
		gllvm.ExtractValue, gllvm.InsertValue,
		llvmFNegOpcode:
		return true
	default:
		return false
	}
}

// LLVMFNeg is part of LLVMOpcode's stable C API, but the Go binding currently
// exposes only Builder.CreateFNeg and not the matching opcode constant.
const llvmFNegOpcode gllvm.Opcode = 66
