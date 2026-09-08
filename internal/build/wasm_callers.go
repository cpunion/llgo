package build

import (
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// coalesceWasmPanicLocations removes repeated writes of the same shadow-stack
// location within a basic block. LLVM sees these as opaque calls, so generated
// aggregate initializers otherwise retain many identical calls for each line.
// Any other call, memory write, synchronization, or unrecognized instruction
// ends the window. In particular, do not carry a location across suspension.
func coalesceWasmPanicLocations(goarch string, mod llvm.Module) int {
	if goarch != "wasm" {
		return 0
	}
	record := mod.NamedFunction(llssa.PkgRuntime + ".RecordPanicLocationWasm")
	if record.IsNil() {
		return 0
	}
	removed := 0
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			var previous llvm.Value
			for instr := bb.FirstInstruction(); !instr.IsNil(); {
				next := llvm.NextInstruction(instr)
				switch instr.InstructionOpcode() {
				case llvm.Call:
					if instr.CalledValue() != record {
						previous = llvm.Value{}
					} else if sameWasmPanicLocation(previous, instr) {
						instr.EraseFromParentAsInstruction()
						removed++
					} else {
						previous = instr
					}
				case llvm.Load:
					if instr.IsVolatile() || instr.Ordering() != llvm.AtomicOrderingNotAtomic {
						previous = llvm.Value{}
					}
				case llvm.Alloca, llvm.GetElementPtr, llvm.ExtractValue, llvm.InsertValue,
					llvm.ExtractElement, llvm.InsertElement, llvm.ShuffleVector, llvm.Select:
					// These instructions only construct addresses or values.
				default:
					if instr.IsABinaryOperator().IsNil() && instr.IsACastInst().IsNil() && instr.IsACmpInst().IsNil() {
						previous = llvm.Value{}
					}
				}
				instr = next
			}
		}
	}
	return removed
}

func sameWasmPanicLocation(previous, current llvm.Value) bool {
	// Six scalar arguments followed by the callee operand. Unknown ABIs or
	// operand bundles are deliberately left unchanged.
	if previous.IsNil() || previous.OperandsCount() != 7 || current.OperandsCount() != 7 {
		return false
	}
	for i := 0; i < 6; i++ {
		if previous.Operand(i) != current.Operand(i) {
			return false
		}
	}
	return true
}
