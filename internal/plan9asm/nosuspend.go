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
// carrying nofree+nosync+nounwind+willreturn. Unknown opcodes, indirect calls,
// declarations, inline asm, invoke/callbr, and synchronization primitives all
// fail closed.
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
				callee := instruction.CalledValue().IsAFunction()
				if callee.IsNil() {
					return fmt.Errorf("function %q contains an indirect or inline-assembly call", function.Name())
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
		gllvm.ExtractValue, gllvm.InsertValue:
		return true
	default:
		return false
	}
}
