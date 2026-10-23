// Package abi contains target-independent lowering for LLGo's internal ABI.
package abi

import "github.com/xgo-dev/llvm"

const (
	// MaxStackVarSize matches cmd/compile's default limit for explicitly
	// declared variables. Values larger than this must not live on LLGo's
	// fixed native stack.
	MaxStackVarSize uint64 = 128 * 1024
	// MaxImplicitStackVarSize matches cmd/compile's default limit for
	// compiler-generated temporaries.
	MaxImplicitStackVarSize uint64 = 64 * 1024
	// Bound zero-store scalarization independently of the permitted stack
	// allocation size. SROA can expand a direct 64KiB aggregate zero store into
	// over 130,000 instructions even when the local itself is valid.
	minAggregateZeroMemsetSize uint64 = 1024

	runtimeAllocU = "github.com/xgo-dev/llgo/runtime/internal/runtime.AllocU"
)

// LowerLargeAggregates converts oversized direct aggregate returns and copies
// to indirect memory operations before target-specific C ABI lowering runs.
func LowerLargeAggregates(td llvm.TargetData, m llvm.Module) {
	l := largeAggregateLowerer{td: td}
	l.transformModule(m)
}

type largeAggregateLowerer struct {
	td llvm.TargetData
}

func (l largeAggregateLowerer) isLargeAggregate(typ llvm.Type) bool {
	switch typ.TypeKind() {
	case llvm.ArrayTypeKind, llvm.StructTypeKind:
		return l.td.TypeAllocSize(typ) > MaxImplicitStackVarSize
	}
	return false
}

func (l largeAggregateLowerer) shouldLowerAggregateZeroStore(typ llvm.Type) bool {
	switch typ.TypeKind() {
	case llvm.ArrayTypeKind, llvm.StructTypeKind:
		return l.td.TypeAllocSize(typ) >= minAggregateZeroMemsetSize
	}
	return false
}

func (l largeAggregateLowerer) indirectType(ctx llvm.Context, typ llvm.Type) llvm.Type {
	params := append([]llvm.Type{llvm.PointerType(typ.ReturnType(), 0)}, typ.ParamTypes()...)
	return llvm.FunctionType(ctx.VoidType(), params, typ.IsFunctionVarArg())
}

func (l largeAggregateLowerer) transformModule(m llvm.Module) {
	l.foldLargeAggregateProjections(m)
	var calls []llvm.Value
	var funcs []llvm.Value
	for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.IntrinsicID() == 0 && l.isLargeAggregate(fn.GlobalValueType().ReturnType()) {
			funcs = append(funcs, fn)
		}
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for instr := bb.FirstInstruction(); !instr.IsNil(); instr = llvm.NextInstruction(instr) {
				if call := instr.IsACallInst(); !call.IsNil() &&
					call.CalledValue().IntrinsicID() == 0 &&
					l.isLargeAggregate(call.CalledFunctionType().ReturnType()) {
					calls = append(calls, call)
				}
			}
		}
	}
	for _, call := range calls {
		l.transformCall(m, call)
	}
	for _, fn := range funcs {
		l.transformFunc(m, fn)
	}
	l.transformLargeMemory(m)
}

// A source multi-result call may assemble a tuple and immediately extract its
// fields. Canonicalize those exact SSA identities before looking for memory
// copies, rather than letting a dead insert/extract chain hide a large load
// until LLVM's later optimization pipeline, after this mandatory lowering.
func (l largeAggregateLowerer) foldLargeAggregateProjections(m llvm.Module) {
	var extracts, inserts []llvm.Value
	for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for value := bb.FirstInstruction(); !value.IsNil(); value = llvm.NextInstruction(value) {
				if !value.IsAExtractValueInst().IsNil() && l.isLargeAggregate(value.Operand(0).Type()) {
					extracts = append(extracts, value)
				} else if !value.IsAInsertValueInst().IsNil() && l.isLargeAggregate(value.Type()) {
					inserts = append(inserts, value)
				}
			}
		}
	}
	for _, extract := range extracts {
		if value, exact := insertedAggregateProjection(extract.Operand(0), extract.Indices()); exact {
			extract.ReplaceAllUsesWith(value)
			extract.EraseFromParentAsInstruction()
		}
	}
	// Builder.Aggregate emits each construction chain in one basic block.
	// Reverse order drops the now-dead chain, exposing its original loads to
	// the existing memory lowerer. Live/partially overwritten tuples stay put.
	for index := len(inserts) - 1; index >= 0; index-- {
		if value := inserts[index]; value.FirstUse().IsNil() {
			value.EraseFromParentAsInstruction()
		}
	}
}

func insertedAggregateProjection(value llvm.Value, path []uint32) (llvm.Value, bool) {
	for !value.IsAInsertValueInst().IsNil() {
		written := value.Indices()
		common := 0
		for common < len(path) && common < len(written) && path[common] == written[common] {
			common++
		}
		if common == len(written) {
			if common == len(path) {
				return value.Operand(1), true
			}
			return insertedAggregateProjection(value.Operand(1), path[common:])
		}
		if common == len(path) {
			return llvm.Value{}, false // A subfield overwrite changed this container.
		}
		value = value.Operand(0) // This insertion wrote a disjoint member.
	}
	return llvm.Value{}, false
}

// transformLargeMemory keeps zero stores beyond the scalarization budget,
// discarded large loads and copies out of target aggregate scalarization.
// Lower an adjacent load/store pair to
// memmove; delayed/multiple stores retain one snapshot at the original load.
// Volatile accesses remain untouched: ordinary memcpy/memset must not replace
// observable source memory operations.
func (l largeAggregateLowerer) transformLargeMemory(m llvm.Module) {
	var loads, zeroStores []llvm.Value
	for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for instr := bb.FirstInstruction(); !instr.IsNil(); instr = llvm.NextInstruction(instr) {
				if store := instr.IsAStoreInst(); !store.IsNil() && !store.IsVolatile() &&
					!store.Operand(0).IsAConstantAggregateZero().IsNil() && l.shouldLowerAggregateZeroStore(store.Operand(0).Type()) {
					zeroStores = append(zeroStores, store)
				}
				load := instr.IsALoadInst()
				if !load.IsNil() && !load.IsVolatile() && l.isLargeAggregate(load.Type()) {
					if _, _, ok := l.largeMemoryUsers(load); ok {
						loads = append(loads, load)
					}
				}
			}
		}
	}
	ctx := m.Context()
	b := ctx.NewBuilder()
	defer b.Dispose()
	for _, store := range zeroStores {
		b.SetInsertPointBefore(store)
		size := llvm.ConstInt(ctx.IntType(l.td.PointerSize()*8), l.td.TypeAllocSize(store.Operand(0).Type()), false)
		zero := b.CreateIntrinsic(ctx.VoidType(), llvm.LookupIntrinsicID("llvm.memset"), []llvm.Value{
			store.Operand(1), llvm.ConstInt(ctx.Int8Type(), 0, false), size, llvm.ConstInt(ctx.Int1Type(), 0, false),
		}, "")
		zero.InstructionSetDebugLoc(store.InstructionDebugLoc())
		store.EraseFromParentAsInstruction()
	}
	for _, load := range loads {
		l.transformStoredLoad(m, load)
	}
}

// Projections must share the same evaluated snapshot as stores. A large
// projected member is admissible only when its entire use tree has the same
// memory form; otherwise even one remaining extract scalarizes the parent.
func (l largeAggregateLowerer) largeMemoryUsers(value llvm.Value) (stores, extracts []llvm.Value, ok bool) {
	for use := value.FirstUse(); !use.IsNil(); use = use.NextUse() {
		user := use.User()
		if store := user.IsAStoreInst(); !store.IsNil() && !store.IsVolatile() && store.Operand(0) == value {
			stores = append(stores, store)
		} else if extract := user.IsAExtractValueInst(); !extract.IsNil() {
			if l.isLargeAggregate(extract.Type()) {
				if _, _, valid := l.largeMemoryUsers(extract); !valid {
					return nil, nil, false
				}
			}
			extracts = append(extracts, extract)
		} else {
			return nil, nil, false
		}
	}
	return stores, extracts, true
}

func (l largeAggregateLowerer) transformStoredLoad(m llvm.Module, load llvm.Value) {
	if load.FirstUse().IsNil() {
		load.EraseFromParentAsInstruction()
		return
	}
	stores, extracts, ok := l.largeMemoryUsers(load)
	if !ok {
		return
	}
	ctx := m.Context()
	b := ctx.NewBuilder()
	defer b.Dispose()
	typ := load.Type()

	b.SetInsertPointBefore(load)
	if len(stores) == 1 && len(extracts) == 0 && llvm.NextInstruction(load) == stores[0] {
		copy := l.callMemmove(ctx, b, stores[0].Operand(1), load.Operand(0), typ)
		copy.InstructionSetDebugLoc(load.InstructionDebugLoc())
		stores[0].EraseFromParentAsInstruction()
		load.EraseFromParentAsInstruction()
		return
	}
	snapshot := l.allocResult(m, ctx, b, typ)
	copy := l.callMemcpy(ctx, b, snapshot, load.Operand(0), typ)
	copy.InstructionSetDebugLoc(load.InstructionDebugLoc())
	l.rewriteMemoryUsers(ctx, load, snapshot, typ, stores, extracts)
}

func (l largeAggregateLowerer) transformCall(m llvm.Module, call llvm.Value) {
	ctx := m.Context()
	oldType := call.CalledFunctionType()
	retType := oldType.ReturnType()
	newType := l.indirectType(ctx, oldType)
	b := ctx.NewBuilder()
	defer b.Dispose()
	b.SetInsertPointBefore(call)

	result := l.allocResult(m, ctx, b, retType)
	params := make([]llvm.Value, 1, oldType.ParamTypesCount()+1)
	params[0] = result
	reflectNameParam := -1
	for i := 0; i < oldType.ParamTypesCount(); i++ {
		params = append(params, call.Operand(i))
		if !call.GetCallSiteStringAttribute(i+1, "llgo.reflect.methodbyname.name").IsNil() {
			reflectNameParam = i + 2
		}
	}
	newCall := llvm.CreateCall(b, newType, call.CalledValue(), params)
	for _, attr := range call.GetCallSiteAttributesAtIndex(-1) {
		newCall.AddCallSiteAttribute(-1, attr)
	}
	newCall.AddCallSiteAttribute(1, sretAttribute(ctx, retType))
	for i := 0; i < oldType.ParamTypesCount(); i++ {
		copyClosureContextCallSiteAttributes(call, i+1, newCall, i+2)
	}
	if reflectNameParam >= 0 {
		newCall.AddCallSiteAttribute(reflectNameParam, ctx.CreateStringAttribute(
			"llgo.reflect.methodbyname.name", "1",
		))
	}
	newCall.SetInstructionCallConv(call.InstructionCallConv())
	newCall.InstructionSetDebugLoc(call.InstructionDebugLoc())

	value := b.CreateLoad(retType, result, "")
	value.InstructionSetDebugLoc(call.InstructionDebugLoc())
	call.ReplaceAllUsesWith(value)
	call.EraseFromParentAsInstruction()
	l.rewriteStoredResult(ctx, value, result, retType)
}

func (l largeAggregateLowerer) transformFunc(m llvm.Module, fn llvm.Value) {
	ctx := m.Context()
	oldType := fn.GlobalValueType()
	retType := oldType.ReturnType()
	newType := l.indirectType(ctx, oldType)
	name := fn.Name()
	fn.SetName("")
	nfn := llvm.AddFunction(m, name, newType)
	nfn.SetLinkage(fn.Linkage())
	nfn.SetFunctionCallConv(fn.FunctionCallConv())
	nfn.AddAttributeAtIndex(1, sretAttribute(ctx, retType))
	for _, attr := range fn.GetFunctionAttributes() {
		nfn.AddFunctionAttr(attr)
	}
	for i := 0; i < oldType.ParamTypesCount(); i++ {
		for _, attr := range fn.GetAttributesAtIndex(i + 1) {
			nfn.AddAttributeAtIndex(i+2, attr)
		}
	}
	if sp := fn.Subprogram(); !sp.IsNil() {
		nfn.SetSubprogram(sp)
	}

	if !fn.IsDeclaration() {
		var blocks []llvm.BasicBlock
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			blocks = append(blocks, bb)
		}
		for _, bb := range blocks {
			bb.RemoveFromParent()
			llvm.AppendExistingBasicBlock(nfn, bb)
		}
		for i := 0; i < oldType.ParamTypesCount(); i++ {
			fn.Param(i).ReplaceAllUsesWith(nfn.Param(i + 1))
		}
		l.rewriteReturns(ctx, nfn, retType)
	}

	fn.ReplaceAllUsesWith(nfn)
	fn.EraseFromParentAsFunction()
}

func (l largeAggregateLowerer) rewriteReturns(ctx llvm.Context, fn llvm.Value, retType llvm.Type) {
	var returns []llvm.Value
	var selfCopies [][2]llvm.Value
	for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
		for instr := bb.FirstInstruction(); !instr.IsNil(); instr = llvm.NextInstruction(instr) {
			if !instr.IsAReturnInst().IsNil() {
				returns = append(returns, instr)
				continue
			}
			store := instr.IsAStoreInst()
			if store.IsNil() || store.IsVolatile() {
				continue
			}
			load := store.Operand(0).IsALoadInst()
			if !load.IsNil() && !load.IsVolatile() && l.isLargeAggregate(load.Type()) &&
				store.Operand(1) == load.Operand(0) && hasSingleUse(load, store) {
				selfCopies = append(selfCopies, [2]llvm.Value{load, store})
			}
		}
	}
	for _, copy := range selfCopies {
		copy[1].EraseFromParentAsInstruction()
		copy[0].EraseFromParentAsInstruction()
	}

	b := ctx.NewBuilder()
	defer b.Dispose()
	result := fn.Param(0)
	for _, ret := range returns {
		value := ret.Operand(0)
		load := value.IsALoadInst()
		if !load.IsNil() && !load.IsVolatile() && hasSingleUse(load, ret) {
			// Copy at the original load so a later source mutation cannot change
			// the already-evaluated return value (issue #1608).
			b.SetInsertPointBefore(load)
			l.callMemcpy(ctx, b, result, load.Operand(0), retType)
			b.SetInsertPointBefore(ret)
			b.CreateRetVoid()
			ret.EraseFromParentAsInstruction()
			load.EraseFromParentAsInstruction()
			continue
		}
		b.SetInsertPointBefore(ret)
		b.CreateStore(value, result)
		b.CreateRetVoid()
		ret.EraseFromParentAsInstruction()
	}
}

func (l largeAggregateLowerer) rewriteStoredResult(ctx llvm.Context, value, result llvm.Value, typ llvm.Type) {
	stores, extracts, ok := l.largeMemoryUsers(value)
	if !ok {
		return
	}
	l.rewriteMemoryUsers(ctx, value, result, typ, stores, extracts)
}

// The backing cell is an immutable evaluated result, not the mutable source
// address. Project large members directly into their memory users so this
// lowering neither recreates an aggregate SSA value nor allocates a second
// snapshot for a nested member. Scalar projections remain ordinary loads.
func (l largeAggregateLowerer) rewriteMemoryUsers(ctx llvm.Context, value, result llvm.Value, typ llvm.Type, stores, extracts []llvm.Value) {
	b := ctx.NewBuilder()
	defer b.Dispose()
	for _, store := range stores {
		b.SetInsertPointBefore(store)
		l.callMemcpy(ctx, b, store.Operand(1), result, typ)
		store.EraseFromParentAsInstruction()
	}
	for _, extract := range extracts {
		b.SetInsertPointBefore(extract)
		indices := []llvm.Value{llvm.ConstInt(ctx.Int32Type(), 0, false)}
		for _, index := range extract.Indices() {
			indices = append(indices, llvm.ConstInt(ctx.Int32Type(), uint64(index), false))
		}
		field := b.CreateInBoundsGEP(typ, result, indices, "")
		if l.isLargeAggregate(extract.Type()) {
			stores, projections, valid := l.largeMemoryUsers(extract)
			if !valid {
				panic("large result projection lost its validated memory users")
			}
			l.rewriteMemoryUsers(ctx, extract, field, extract.Type(), stores, projections)
		} else {
			projected := b.CreateLoad(extract.Type(), field, "")
			// A packed member may not have its type's ABI alignment even
			// when the enclosing snapshot itself is naturally aligned.
			projected.SetAlignment(1)
			projected.InstructionSetDebugLoc(extract.InstructionDebugLoc())
			extract.ReplaceAllUsesWith(projected)
			extract.EraseFromParentAsInstruction()
		}
	}
	value.EraseFromParentAsInstruction()
}

func (l largeAggregateLowerer) allocResult(m llvm.Module, ctx llvm.Context, b llvm.Builder, typ llvm.Type) llvm.Value {
	intType := ctx.IntType(l.td.PointerSize() * 8)
	ptrType := llvm.PointerType(ctx.Int8Type(), 0)
	fnType := llvm.FunctionType(ptrType, []llvm.Type{intType}, false)
	fn := m.NamedFunction(runtimeAllocU)
	if fn.IsNil() {
		fn = llvm.AddFunction(m, runtimeAllocU, fnType)
	}
	size := llvm.ConstInt(intType, l.td.TypeAllocSize(typ), false)
	return llvm.CreateCall(b, fnType, fn, []llvm.Value{size})
}

func (l largeAggregateLowerer) callMemcpy(ctx llvm.Context, b llvm.Builder, dst, src llvm.Value, typ llvm.Type) llvm.Value {
	return l.callMemoryCopy(ctx, b, "llvm.memcpy", dst, src, typ)
}

func (l largeAggregateLowerer) callMemmove(ctx llvm.Context, b llvm.Builder, dst, src llvm.Value, typ llvm.Type) llvm.Value {
	return l.callMemoryCopy(ctx, b, "llvm.memmove", dst, src, typ)
}

func (l largeAggregateLowerer) callMemoryCopy(ctx llvm.Context, b llvm.Builder, intrinsic string, dst, src llvm.Value, typ llvm.Type) llvm.Value {
	size := llvm.ConstInt(ctx.IntType(l.td.PointerSize()*8), l.td.TypeAllocSize(typ), false)
	return b.CreateIntrinsic(ctx.VoidType(), llvm.LookupIntrinsicID(intrinsic), []llvm.Value{
		dst, src, size, llvm.ConstInt(ctx.Int1Type(), 0, false),
	}, "")
}

func sretAttribute(ctx llvm.Context, typ llvm.Type) llvm.Attribute {
	return ctx.CreateTypeAttribute(llvm.AttributeKindID("sret"), typ)
}

func copyClosureContextCallSiteAttributes(from llvm.Value, fromIndex int, to llvm.Value, toIndex int) {
	for _, name := range []string{"nest", "swiftself"} {
		kind := llvm.AttributeKindID(name)
		if kind == 0 {
			continue
		}
		if attr := from.GetCallSiteEnumAttribute(fromIndex, kind); !attr.IsNil() {
			to.AddCallSiteAttribute(toIndex, attr)
		}
	}
}

func hasSingleUse(value, user llvm.Value) bool {
	use := value.FirstUse()
	return !use.IsNil() && use.User() == user && use.NextUse().IsNil()
}
