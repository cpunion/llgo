package build

import "github.com/xgo-dev/llvm"

// lowerWasmAggregateCopies keeps moderately sized copies out of SelectionDAG's
// scalar aggregate representation. The stack/return ABI limit is intentionally
// unchanged: even an 8 KiB volatile array copy otherwise emits thousands of
// Wasm loads, locals and stores. Only adjacent, single-use load/store pairs and
// zero stores are eligible, so this pass adds no allocation or GC safepoint.
func lowerWasmAggregateCopies(goarch string, td llvm.TargetData, mod llvm.Module) int {
	if goarch != "wasm" {
		return 0
	}
	ctx := mod.Context()
	b := ctx.NewBuilder()
	defer b.Dispose()
	changed := 0
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for instr := bb.FirstInstruction(); !instr.IsNil(); {
				next := llvm.NextInstruction(instr)
				store := instr.IsAStoreInst()
				if !store.IsNil() {
					value := store.Operand(0)
					typ := value.Type()
					kind := typ.TypeKind()
					if (kind == llvm.ArrayTypeKind || kind == llvm.StructTypeKind) && td.TypeAllocSize(typ) >= 4<<10 {
						b.SetInsertPointBefore(store)
						size := llvm.ConstInt(ctx.IntType(td.PointerSize()*8), td.TypeAllocSize(typ), false)
						volatile := uint64(0)
						if store.IsVolatile() {
							volatile = 1
						}
						var copy llvm.Value
						if value.IsNull() {
							copy = b.CreateIntrinsic(ctx.VoidType(), llvm.LookupIntrinsicID("llvm.memset"), []llvm.Value{
								store.Operand(1), llvm.ConstInt(ctx.Int8Type(), 0, false), size,
								llvm.ConstInt(ctx.Int1Type(), volatile, false),
							}, "")
						} else if load := value.IsALoadInst(); !load.IsNil() && llvm.NextInstruction(load) == store && load.FirstUse().NextUse().IsNil() {
							if load.IsVolatile() {
								volatile = 1
							}
							copy = b.CreateIntrinsic(ctx.VoidType(), llvm.LookupIntrinsicID("llvm.memmove"), []llvm.Value{
								store.Operand(1), load.Operand(0), size,
								llvm.ConstInt(ctx.Int1Type(), volatile, false),
							}, "")
							// Erase the consumer before its aggregate-producing load.
							copy.InstructionSetDebugLoc(store.InstructionDebugLoc())
							store.EraseFromParentAsInstruction()
							load.EraseFromParentAsInstruction()
							changed++
							instr = next
							continue
						}
						if !copy.IsNil() {
							copy.InstructionSetDebugLoc(store.InstructionDebugLoc())
							store.EraseFromParentAsInstruction()
							changed++
						}
					}
				}
				instr = next
			}
		}
	}
	return changed
}
