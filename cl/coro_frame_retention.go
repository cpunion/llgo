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
	"go/token"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroTimerPrepareAfterOrAbortSymbolV1    = "__llgo_coro_timer_prepare_after_or_abort_v1"
	coroTimerRetireCompletedOrAbortSymbolV1 = "__llgo_coro_timer_retire_completed_or_abort_v1"
)

type coroFrameRetentionInstructionRole uint8

const (
	coroFrameRetentionInstructionNone coroFrameRetentionInstructionRole = iota
	coroFrameRetentionInstructionPrepare
	coroFrameRetentionInstructionPark
	coroFrameRetentionInstructionRetire
)

// coroFrameRetentionProof is derived twice from the same immutable SSA and
// frozen emission universe: preflight uses it to accept selected x/tools Heap
// Allocs, and codegen uses it to lower those exact Allocs into the LLVM
// coroutine frame and to suppress ordinary preemption inside the transaction.
// The maps are never exposed outside cl and are immutable after construction.
type coroFrameRetentionProof struct {
	allocations map[*ssa.Alloc]struct{}
	roles       map[ssa.Instruction]coroFrameRetentionInstructionRole
}

type coroFrameRetentionTransaction struct {
	prepare      *ssa.Call
	park         *ssa.Call
	retire       *ssa.Call
	token        *ssa.Alloc
	ticket       *ssa.Alloc
	slot         *ssa.Alloc
	gen          *ssa.Alloc
	parkTicket   *ssa.UnOp
	retireTicket *ssa.UnOp
	retireSlot   *ssa.UnOp
	retireGen    *ssa.UnOp
}

type coroFrameRetentionCallKind uint8

const (
	coroFrameRetentionCallNone coroFrameRetentionCallKind = iota
	coroFrameRetentionCallPrepare
	coroFrameRetentionCallPark
	coroFrameRetentionCallRetire
)

func (a *coroPhysicalPureSSAAudit) frameRetainsAllocation(alloc *ssa.Alloc) bool {
	proof := a.currentFrameRetentionProof()
	if proof == nil {
		return false
	}
	_, ok := proof.allocations[alloc]
	return ok
}

func (a *coroPhysicalPureSSAAudit) currentFrameRetentionProof() *coroFrameRetentionProof {
	if a == nil {
		return nil
	}
	if !a.frameRetentionBuilt {
		a.frameRetentionBuilt = true
		a.frameRetentionProofCache = a.proveCurrentFrameRetention()
	}
	return a.frameRetentionProofCache
}

func (a *coroPhysicalPureSSAAudit) proveCurrentFrameRetention() *coroFrameRetentionProof {
	proof := &coroFrameRetentionProof{
		allocations: make(map[*ssa.Alloc]struct{}),
		roles:       make(map[ssa.Instruction]coroFrameRetentionInstructionRole),
	}
	if a.frameRetentionABI != CoroFrameRetentionTimerABIV1 || a.universe == nil || a.ctx == nil || a.fn == nil {
		return proof
	}

	var prepares []*ssa.Call
	for _, block := range a.fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			kind, ok := a.classifyFrameRetentionCall(call)
			if ok && kind == coroFrameRetentionCallPrepare {
				prepares = append(prepares, call)
			}
		}
	}

	transactions := make([]coroFrameRetentionTransaction, 0, len(prepares))
	allocationUses := make(map[*ssa.Alloc]int)
	callUses := make(map[*ssa.Call]int)
	for _, prepare := range prepares {
		transaction, ok := a.proveFrameRetentionTransaction(prepare)
		if !ok {
			continue
		}
		transactions = append(transactions, transaction)
		allocations := []*ssa.Alloc{transaction.token, transaction.ticket, transaction.slot, transaction.gen}
		for _, alloc := range allocations {
			allocationUses[alloc]++
		}
		for _, call := range []*ssa.Call{transaction.prepare, transaction.park, transaction.retire} {
			callUses[call]++
		}
	}
	for _, transaction := range transactions {
		allocations := []*ssa.Alloc{transaction.token, transaction.ticket, transaction.slot, transaction.gen}
		unique := true
		for _, alloc := range allocations {
			unique = unique && allocationUses[alloc] == 1
		}
		for _, call := range []*ssa.Call{transaction.prepare, transaction.park, transaction.retire} {
			unique = unique && callUses[call] == 1
		}
		if !unique {
			continue
		}
		for _, alloc := range allocations {
			proof.allocations[alloc] = struct{}{}
		}
		proof.roles[transaction.prepare] = coroFrameRetentionInstructionPrepare
		proof.roles[transaction.park] = coroFrameRetentionInstructionPark
		proof.roles[transaction.retire] = coroFrameRetentionInstructionRetire
	}
	return proof
}

func (a *coroPhysicalPureSSAAudit) proveFrameRetentionTransaction(prepare *ssa.Call) (coroFrameRetentionTransaction, bool) {
	transaction := coroFrameRetentionTransaction{prepare: prepare}
	if prepare == nil || prepare.Parent() != a.fn || prepare.Common() == nil || len(prepare.Common().Args) != 5 {
		return transaction, false
	}
	transaction.token = coroFrameRetentionDirectAllocRoot(prepare.Common().Args[0], make(map[ssa.Value]bool))
	transaction.ticket = coroFrameRetentionDirectAllocRoot(prepare.Common().Args[2], make(map[ssa.Value]bool))
	transaction.slot = coroFrameRetentionDirectAllocRoot(prepare.Common().Args[3], make(map[ssa.Value]bool))
	transaction.gen = coroFrameRetentionDirectAllocRoot(prepare.Common().Args[4], make(map[ssa.Value]bool))
	allocations := []*ssa.Alloc{transaction.token, transaction.ticket, transaction.slot, transaction.gen}
	seen := make(map[*ssa.Alloc]bool, len(allocations))
	for index, alloc := range allocations {
		if alloc == nil || alloc.Parent() != a.fn || !alloc.Heap || seen[alloc] ||
			a.ctx.skipSyntheticMakeSliceAlloc(alloc) || isEmissionVargsAlloc(a.ctx, alloc) {
			return transaction, false
		}
		seen[alloc] = true
		if index == 0 {
			if !a.exactWaitTokenFrameShape(alloc) {
				return transaction, false
			}
		} else if !coroFrameRetentionExactUint32Alloc(a, alloc) {
			return transaction, false
		}
	}

	for _, block := range a.fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			kind, classified := a.classifyFrameRetentionCall(call)
			if !classified || kind == coroFrameRetentionCallPrepare {
				continue
			}
			common := call.Common()
			if common == nil || len(common.Args) == 0 ||
				coroFrameRetentionDirectAllocRoot(common.Args[0], make(map[ssa.Value]bool)) != transaction.token {
				continue
			}
			switch kind {
			case coroFrameRetentionCallPark:
				if transaction.park != nil || len(common.Args) != 2 {
					return coroFrameRetentionTransaction{}, false
				}
				transaction.parkTicket = coroFrameRetentionScalarLoadFrom(common.Args[1], transaction.ticket)
				if transaction.parkTicket == nil {
					return coroFrameRetentionTransaction{}, false
				}
				transaction.park = call
			case coroFrameRetentionCallRetire:
				if transaction.retire != nil || len(common.Args) != 4 {
					return coroFrameRetentionTransaction{}, false
				}
				transaction.retireTicket = coroFrameRetentionScalarLoadFrom(common.Args[1], transaction.ticket)
				transaction.retireSlot = coroFrameRetentionScalarLoadFrom(common.Args[2], transaction.slot)
				transaction.retireGen = coroFrameRetentionScalarLoadFrom(common.Args[3], transaction.gen)
				if transaction.retireTicket == nil || transaction.retireSlot == nil || transaction.retireGen == nil {
					return coroFrameRetentionTransaction{}, false
				}
				transaction.retire = call
			}
		}
	}
	if transaction.park == nil || transaction.retire == nil ||
		prepare.Block() != transaction.park.Block() || prepare.Block() != transaction.retire.Block() {
		return coroFrameRetentionTransaction{}, false
	}
	if !coroFrameRetentionScalarUsesMatch(transaction.parkTicket, transaction.park, 1) ||
		!coroFrameRetentionScalarUsesMatch(transaction.retireTicket, transaction.retire, 1) ||
		!coroFrameRetentionScalarUsesMatch(transaction.retireSlot, transaction.retire, 2) ||
		!coroFrameRetentionScalarUsesMatch(transaction.retireGen, transaction.retire, 3) {
		return coroFrameRetentionTransaction{}, false
	}
	prepareIndex := coroFrameRetentionInstructionIndex(prepare)
	parkIndex := coroFrameRetentionInstructionIndex(transaction.park)
	retireIndex := coroFrameRetentionInstructionIndex(transaction.retire)
	if prepareIndex < 0 || parkIndex <= prepareIndex || retireIndex <= parkIndex ||
		!a.frameRetentionSpanIsPure(prepare.Block(), prepareIndex+1, parkIndex, transaction) ||
		!a.frameRetentionSpanIsPure(prepare.Block(), parkIndex+1, retireIndex, transaction) {
		return coroFrameRetentionTransaction{}, false
	}

	allowedTokenCalls := map[*ssa.Call]int{prepare: 0, transaction.park: 0, transaction.retire: 0}
	if !coroFrameRetentionAddressUsesMatch(transaction.token, allowedTokenCalls, nil) {
		return coroFrameRetentionTransaction{}, false
	}
	outputLoads := [][]*ssa.UnOp{
		{transaction.parkTicket, transaction.retireTicket},
		{transaction.retireSlot},
		{transaction.retireGen},
	}
	for index, alloc := range []*ssa.Alloc{transaction.ticket, transaction.slot, transaction.gen} {
		allowedLoads := make(map[*ssa.UnOp]struct{}, len(outputLoads[index]))
		for _, load := range outputLoads[index] {
			allowedLoads[load] = struct{}{}
		}
		if !coroFrameRetentionAddressUsesMatch(alloc, map[*ssa.Call]int{prepare: index + 2}, allowedLoads) {
			return coroFrameRetentionTransaction{}, false
		}
	}
	return transaction, true
}

func (a *coroPhysicalPureSSAAudit) exactWaitTokenFrameShape(alloc *ssa.Alloc) bool {
	if alloc == nil {
		return false
	}
	pointer, ok := types.Unalias(a.typeOf(alloc.Type())).Underlying().(*types.Pointer)
	if !ok || coroTypeContainsGCPointer(pointer.Elem(), make(map[types.Type]bool)) {
		return false
	}
	structure, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Struct)
	if !ok || structure.NumFields() != 1 || !coroFrameRetentionExactBasic(structure.Field(0).Type(), types.Uint32) {
		return false
	}
	physical := a.ctx.type_(pointer.Elem(), llssa.InGo)
	return a.ctx.prog.SizeOf(physical) == 4 && a.ctx.prog.AlignOf(physical) == 4 && a.ctx.prog.OffsetOf(physical, 0) == 0
}

func coroFrameRetentionExactUint32Alloc(a *coroPhysicalPureSSAAudit, alloc *ssa.Alloc) bool {
	if a == nil || alloc == nil {
		return false
	}
	pointer, ok := types.Unalias(a.typeOf(alloc.Type())).Underlying().(*types.Pointer)
	return ok && coroFrameRetentionExactBasic(pointer.Elem(), types.Uint32) &&
		!coroTypeContainsGCPointer(pointer.Elem(), make(map[types.Type]bool))
}

func (a *coroPhysicalPureSSAAudit) classifyFrameRetentionCall(call *ssa.Call) (coroFrameRetentionCallKind, bool) {
	if call == nil || call.Common() == nil || call.Common().IsInvoke() || call.Parent() != a.fn {
		return coroFrameRetentionCallNone, false
	}
	semantics, intrinsic, err := a.universe.CoroIntrinsicCallSiteSemantics(call)
	if err == nil && intrinsic && semantics == CoroIntrinsicCallInlineSuspend {
		return coroFrameRetentionCallPark, true
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		return coroFrameRetentionCallNone, false
	}
	kind, ok := a.universe.coroFrameRetentionOwnerCallSite(call)
	if !ok {
		return coroFrameRetentionCallNone, false
	}
	switch kind {
	case coroFrameRetentionCallPrepare:
		return coroFrameRetentionCallPrepare, true
	case coroFrameRetentionCallRetire:
		return coroFrameRetentionCallRetire, true
	}
	return coroFrameRetentionCallNone, false
}

// coroFrameRetentionOwnerCallSite is producer-side derivation from the
// immutable metadata that created CoroForeignNoBlockCertificate.ID. External
// certificate consumers must compare IDs and may not infer capability from the
// diagnostic PhysicalSymbol/ABISignature fields. This method instead reopens
// the private frozen final key and certificate map inside EmissionUniverse,
// then validates the exact direct SSA call before returning one of the two
// compiler-owned retention semantics.
func (u *EmissionUniverse) coroFrameRetentionOwnerCallSite(call *ssa.Call) (coroFrameRetentionCallKind, bool) {
	if u == nil || call == nil || call.Common() == nil || call.Common().IsInvoke() {
		return coroFrameRetentionCallNone, false
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		return coroFrameRetentionCallNone, false
	}
	canonical := u.canonicalAlias(callee)
	if canonical == nil {
		return coroFrameRetentionCallNone, false
	}
	certificate, certified := u.foreignNoBlock[canonical]
	if !certified || certificate.ID == "" {
		return coroFrameRetentionCallNone, false
	}
	var frozen coroForeignPhysicalABI
	haveFrozen := false
	for _, owner := range u.sortedUseOwners(canonical) {
		key := u.finalKeys[emissionFunctionOwnerKey{function: canonical, owner: owner}]
		background, symbol, signature, ok := splitManagedSymbolKey(key)
		if !ok || background != cFunc {
			continue
		}
		candidate := coroForeignPhysicalABI{symbol: symbol, signature: signature}
		if haveFrozen && candidate != frozen {
			return coroFrameRetentionCallNone, false
		}
		frozen, haveFrozen = candidate, true
	}
	if !haveFrozen || frozen.symbol != certificate.PhysicalSymbol || frozen.signature != certificate.ABISignature {
		return coroFrameRetentionCallNone, false
	}
	switch frozen.symbol {
	case coroTimerPrepareAfterOrAbortSymbolV1:
		if coroFrameRetentionPrepareSignature(call.Common().Signature()) {
			return coroFrameRetentionCallPrepare, true
		}
	case coroTimerRetireCompletedOrAbortSymbolV1:
		if coroFrameRetentionRetireSignature(call.Common().Signature()) {
			return coroFrameRetentionCallRetire, true
		}
	}
	return coroFrameRetentionCallNone, false
}

func coroFrameRetentionPrepareSignature(signature *types.Signature) bool {
	if !coroFrameRetentionBaseSignature(signature, 5) || !coroFrameRetentionExactBasic(signature.Params().At(0).Type(), types.UnsafePointer) ||
		!coroFrameRetentionExactBasic(signature.Params().At(1).Type(), types.Int64) {
		return false
	}
	for index := 2; index < 5; index++ {
		if !types.Identical(types.Unalias(signature.Params().At(index).Type()), types.NewPointer(types.Typ[types.Uint32])) {
			return false
		}
	}
	return true
}

func coroFrameRetentionRetireSignature(signature *types.Signature) bool {
	if !coroFrameRetentionBaseSignature(signature, 4) || !coroFrameRetentionExactBasic(signature.Params().At(0).Type(), types.UnsafePointer) {
		return false
	}
	for index := 1; index < 4; index++ {
		if !coroFrameRetentionExactBasic(signature.Params().At(index).Type(), types.Uint32) {
			return false
		}
	}
	return true
}

func coroFrameRetentionBaseSignature(signature *types.Signature, parameters int) bool {
	return signature != nil && signature.Recv() == nil && !signature.Variadic() &&
		coroFrameRetentionTypeParamLen(signature.TypeParams()) == 0 && coroFrameRetentionTypeParamLen(signature.RecvTypeParams()) == 0 &&
		signature.Params() != nil && signature.Params().Len() == parameters &&
		(signature.Results() == nil || signature.Results().Len() == 0)
}

func coroFrameRetentionTypeParamLen(list *types.TypeParamList) int {
	if list == nil {
		return 0
	}
	return list.Len()
}

func coroFrameRetentionPointerLike(typ types.Type) bool {
	underlying := types.Unalias(typ).Underlying()
	if _, ok := underlying.(*types.Pointer); ok {
		return true
	}
	basic, ok := underlying.(*types.Basic)
	return ok && basic.Kind() == types.UnsafePointer
}

func coroFrameRetentionExactBasic(typ types.Type, kind types.BasicKind) bool {
	return types.Identical(types.Unalias(typ), types.Typ[kind])
}

func coroFrameRetentionDirectAllocRoot(value ssa.Value, visiting map[ssa.Value]bool) *ssa.Alloc {
	if value == nil || visiting[value] {
		return nil
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.Alloc:
		return value
	case *ssa.ChangeType:
		if value.X != nil && coroFrameRetentionPointerLike(value.Type()) && coroFrameRetentionPointerLike(value.X.Type()) {
			return coroFrameRetentionDirectAllocRoot(value.X, visiting)
		}
	case *ssa.Convert:
		if value.X != nil && coroFrameRetentionPointerLike(value.Type()) && coroFrameRetentionPointerLike(value.X.Type()) {
			return coroFrameRetentionDirectAllocRoot(value.X, visiting)
		}
	}
	return nil
}

func coroFrameRetentionScalarLoadFrom(value ssa.Value, alloc *ssa.Alloc) *ssa.UnOp {
	load, ok := value.(*ssa.UnOp)
	if !ok || load.Op != token.MUL || coroFrameRetentionDirectAllocRoot(load.X, make(map[ssa.Value]bool)) != alloc {
		return nil
	}
	return load
}

func coroFrameRetentionScalarUsesMatch(load *ssa.UnOp, allowed *ssa.Call, argument int) bool {
	if load == nil || allowed == nil || allowed.Common() == nil || argument < 0 || argument >= len(allowed.Common().Args) ||
		allowed.Common().Args[argument] != load {
		return false
	}
	refs := load.Referrers()
	if refs == nil {
		return false
	}
	seen := false
	for _, reference := range *refs {
		switch reference := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.Call:
			if reference != allowed || seen {
				return false
			}
			seen = true
		default:
			return false
		}
	}
	return seen
}

func coroFrameRetentionInstructionIndex(instruction ssa.Instruction) int {
	if instruction == nil || instruction.Block() == nil {
		return -1
	}
	for index, candidate := range instruction.Block().Instrs {
		if candidate == instruction {
			return index
		}
	}
	return -1
}

func (a *coroPhysicalPureSSAAudit) frameRetentionSpanIsPure(block *ssa.BasicBlock, begin, end int, transaction coroFrameRetentionTransaction) bool {
	if block == nil || begin < 0 || end < begin || end > len(block.Instrs) {
		return false
	}
	outputs := map[*ssa.Alloc]bool{transaction.ticket: true, transaction.slot: true, transaction.gen: true}
	all := map[*ssa.Alloc]bool{
		transaction.token: true, transaction.ticket: true, transaction.slot: true, transaction.gen: true,
	}
	for _, instruction := range block.Instrs[begin:end] {
		switch instruction := instruction.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			origin := coroFrameRetentionScalarLoadOrigin(instruction, make(map[ssa.Value]bool))
			if instruction.Op != token.MUL || !outputs[origin] {
				return false
			}
		case *ssa.ChangeType:
			if !coroFrameRetentionAllowedPointerConversion(instruction.X, instruction, all) {
				return false
			}
		case *ssa.Convert:
			if !coroFrameRetentionAllowedPointerConversion(instruction.X, instruction, all) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func coroFrameRetentionAllowedPointerConversion(source ssa.Value, result ssa.Value, all map[*ssa.Alloc]bool) bool {
	if source == nil || result == nil {
		return false
	}
	if coroFrameRetentionPointerLike(source.Type()) && coroFrameRetentionPointerLike(result.Type()) {
		return all[coroFrameRetentionDirectAllocRoot(result, make(map[ssa.Value]bool))]
	}
	return false
}

func coroFrameRetentionScalarLoadOrigin(value ssa.Value, visiting map[ssa.Value]bool) *ssa.Alloc {
	if value == nil || visiting[value] {
		return nil
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.UnOp:
		if value.Op == token.MUL {
			return coroFrameRetentionDirectAllocRoot(value.X, make(map[ssa.Value]bool))
		}
	}
	return nil
}

func coroFrameRetentionAddressUsesMatch(alloc *ssa.Alloc, allowedCalls map[*ssa.Call]int, allowedLoads map[*ssa.UnOp]struct{}) bool {
	aliases := make(map[ssa.Value]bool)
	queue := []ssa.Value{alloc}
	aliases[alloc] = true
	for head := 0; head < len(queue); head++ {
		value := queue[head]
		refs := value.Referrers()
		if refs == nil {
			return false
		}
		for _, reference := range *refs {
			var alias ssa.Value
			switch instruction := reference.(type) {
			case *ssa.ChangeType:
				if instruction.X == value && coroFrameRetentionPointerLike(instruction.Type()) && coroFrameRetentionPointerLike(value.Type()) {
					alias = instruction
				}
			case *ssa.Convert:
				if instruction.X == value && coroFrameRetentionPointerLike(instruction.Type()) && coroFrameRetentionPointerLike(value.Type()) {
					alias = instruction
				}
			}
			if alias != nil && !aliases[alias] {
				aliases[alias] = true
				queue = append(queue, alias)
			}
		}
	}

	seenCalls := make(map[*ssa.Call]bool)
	seenLoads := make(map[*ssa.UnOp]bool)
	semanticUses := make(map[ssa.Value]int, len(aliases))
	for value := range aliases {
		refs := value.Referrers()
		if refs == nil {
			return false
		}
		for _, reference := range *refs {
			switch instruction := reference.(type) {
			case *ssa.DebugRef:
			case *ssa.ChangeType:
				if instruction.X != value || !aliases[instruction] {
					return false
				}
				semanticUses[value]++
			case *ssa.Convert:
				if instruction.X != value || !aliases[instruction] {
					return false
				}
				semanticUses[value]++
			case *ssa.UnOp:
				if instruction.Op != token.MUL || instruction.X != value {
					return false
				}
				if _, ok := allowedLoads[instruction]; !ok {
					return false
				}
				seenLoads[instruction] = true
				semanticUses[value]++
			case *ssa.Store:
				return false
			case *ssa.Call:
				if seenCalls[instruction] {
					continue
				}
				seenCalls[instruction] = true
				want, ok := allowedCalls[instruction]
				if !ok || instruction.Common() == nil {
					return false
				}
				matches := 0
				for index, argument := range instruction.Common().Args {
					if aliases[argument] {
						if index != want {
							return false
						}
						matches++
					}
				}
				if matches != 1 {
					return false
				}
				semanticUses[value]++
			default:
				return false
			}
		}
	}
	for alias := range aliases {
		if semanticUses[alias] == 0 {
			return false
		}
	}
	return len(seenCalls) == len(allowedCalls) && len(seenLoads) == len(allowedLoads)
}
