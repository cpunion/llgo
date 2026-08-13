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
	"crypto/sha256"
	"encoding/hex"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// coroFrameRetentionProof is derived twice from the same immutable SSA and
// frozen emission universe: preflight uses it to accept selected x/tools Heap
// Allocs, and codegen uses it to lower those exact Allocs into the LLVM
// coroutine frame.
// The maps are never exposed outside cl and are immutable after construction.
type coroFrameRetentionProof struct {
	// allocations are the exact park-transaction cells reclassified from an
	// x/tools Heap Alloc into storage owned by the LLVM coroutine frame.
	allocations map[*ssa.Alloc]struct{}
	// borrowedAllocations are ordinary fresh Go cells whose complete static
	// address graph is proven not to survive the owning function. x/tools marks
	// them Heap at a conservative interprocedural call boundary; the stronger
	// closed-world proof permits target-bounded LLVM frame/native-stack storage
	// without source annotations or per-operation runtime scratch fields.
	borrowedAllocations map[*ssa.Alloc]coro.SSABorrowedAllocationProof
	// managedHeapAllocations remain ordinary Go heap allocations. Each fact is
	// admitted only after the frozen lowered-call plan proves its exact AllocZ
	// path; the resulting pointer may then be conservatively scanned from this
	// coroutine frame while it is suspended.
	managedHeapAllocations map[*ssa.Alloc]coroFrameRetentionManagedHeapAllocation
	// terminalResultAllocations are the exact managed heap cells reloaded after
	// RunDefers to reconstruct named results. Codegen defines only this narrow
	// subset before the initial suspend so compiler-owned cleanup/cancel
	// continuations have a dominating pointer without moving ordinary heap
	// allocations out of their source blocks.
	terminalResultAllocations map[*ssa.Alloc]struct{}
	// exactRoots, stableAddresses, uintptrValues, and callKeepalives are a
	// capability proof, not a tracing-GC root map. They name the exact SSA
	// values that LLVM may retain in a PhysicalABIV1 coroutine frame under a
	// non-moving conservative collector (or no collector), and the exact uses
	// for which address/uintptr provenance was proved. A precise or moving
	// collector must not consume this profile until the coroutine ABI also
	// publishes typed frame maps and relocation barriers.
	exactRoots      map[ssa.Value]coroFrameRetentionExactRoot
	stableAddresses map[coroFrameRetentionAddressUse]coroFrameRetentionAddressFact
	uintptrValues   map[ssa.Value]coroFrameRetentionUintptrFact
	callKeepalives  map[ssa.CallInstruction]coroFrameRetentionCallFact
	rootDigest      string
}

// This is the sole current root profile. There is intentionally no v1
// compatibility path. LLVM CoroSplit materializes every SSA pointer live over
// a suspend in the heap-backed coroutine frame. BDWGC allocates that frame with
// scanned MallocUncollectable storage; tinygogc reaches it through the live
// scheduler task and conservatively scans it; nogc/WASM have no tracing
// collector that could reclaim its referent. A future precise or moving
// collector must publish typed frame maps, relocation, and write barriers under
// a new ABI instead of reusing this identity.
const coroFrameRetentionExactRootProfileV2 = "physical-v1.nonmoving-conservative-or-none.exact-roots-managed-heap.v2"

type coroFrameRetentionManagedHeapAllocation struct {
	zeroSized      bool
	helper         string
	helperTarget   coro.FunctionID
	helperEmission coro.BodyEmission
}

type coroFrameRetentionRootKind uint8

const (
	coroFrameRetentionRootInvalid coroFrameRetentionRootKind = iota
	coroFrameRetentionRootReceiver
	coroFrameRetentionRootPointerParameter
	coroFrameRetentionRootSliceParameter
	coroFrameRetentionRootLocalSlice
	coroFrameRetentionRootLocalAddress
	coroFrameRetentionRootClosureFreeVar
	coroFrameRetentionRootManagedHeapAllocation
	coroFrameRetentionRootInterfaceParameter
)

type coroFrameRetentionExactRoot struct {
	value ssa.Value
	kind  coroFrameRetentionRootKind
	order int
}

type coroFrameRetentionAddressUse struct {
	value ssa.Value
	use   ssa.Instruction
}

type coroFrameRetentionAddressFact struct {
	roots    []ssa.Value
	evidence []ssa.Instruction
	// nonNil distinguishes an address whose source is statically non-nil or
	// protected by exact dominating SSA evidence from a transport-stable but
	// nullable address.  The latter is still a valid frame root, but every
	// dereference must take the compiler-owned explicit fault edge first.
	nonNil bool
}

type coroFrameRetentionUintptrFact struct {
	roots []ssa.Value
}

type coroFrameRetentionCallKindV1 uint8

const (
	coroFrameRetentionCallInvalidV1 coroFrameRetentionCallKindV1 = iota
	coroFrameRetentionCallManagedChildV1
	coroFrameRetentionCallWorkerV1
	coroFrameRetentionCallParkOwnerV1
)

type coroFrameRetentionCallFact struct {
	kind    coroFrameRetentionCallKindV1
	roots   []ssa.Value
	sources []ssa.Value
}

// exactRootCapabilityProfile is deliberately separate from the target GC
// configuration. The manifest/physical-ABI consumer must match this profile
// only for a non-moving conservative or non-collecting target.
func (p *coroFrameRetentionProof) exactRootCapabilityProfile() string {
	if p == nil || p.rootDigest == "" {
		return ""
	}
	return coroFrameRetentionExactRootProfileV2
}

// exactRootCapabilityDigest is a read-only identity for all exact root,
// address-use, park transaction, and uintptr-keepalive facts in this proof.
// It is rebuilt from deterministic SSA ordinals rather than SSA pointer
// identity or diagnostic strings.
func (p *coroFrameRetentionProof) exactRootCapabilityDigest() string {
	if p == nil {
		return ""
	}
	return p.rootDigest
}

func (p *coroFrameRetentionProof) exactRetainedRoots() []ssa.Value {
	if p == nil || len(p.exactRoots) == 0 {
		return nil
	}
	ordered := make([]coroFrameRetentionExactRoot, 0, len(p.exactRoots))
	for _, root := range p.exactRoots {
		ordered = append(ordered, root)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
	values := make([]ssa.Value, len(ordered))
	for index, root := range ordered {
		values[index] = root.value
	}
	return values
}

func coroTerminalResultAllocationSetMatches(
	proof *coroFrameRetentionProof,
	allocations []*ssa.Alloc,
) bool {
	if proof == nil || len(proof.terminalResultAllocations) != len(allocations) {
		return proof == nil && len(allocations) == 0
	}
	seen := make(map[*ssa.Alloc]struct{}, len(allocations))
	for _, allocation := range allocations {
		if allocation == nil {
			return false
		}
		if _, duplicate := seen[allocation]; duplicate {
			return false
		}
		seen[allocation] = struct{}{}
		if _, selected := proof.terminalResultAllocations[allocation]; !selected {
			return false
		}
		if _, managed := proof.managedHeapAllocations[allocation]; !managed {
			return false
		}
	}
	return true
}

func (p *coroFrameRetentionProof) exactCallKeepaliveRoots(call ssa.CallInstruction) []ssa.Value {
	if p == nil || call == nil {
		return nil
	}
	fact, ok := p.callKeepalives[call]
	if !ok {
		return nil
	}
	return append([]ssa.Value(nil), fact.roots...)
}

// exactCallKeepaliveSources returns the exact transport values consumed by a
// bounded call. Unlike provenance roots, these values are guaranteed by Go
// SSA to dominate that call even when the root trace crossed a Phi component.
// Codegen must spill these sources at the call boundary and use roots only as
// the immutable proof that each source carries valid pointer provenance.
func (p *coroFrameRetentionProof) exactCallKeepaliveSources(call ssa.CallInstruction) []ssa.Value {
	if p == nil || call == nil {
		return nil
	}
	fact, ok := p.callKeepalives[call]
	if !ok {
		return nil
	}
	return append([]ssa.Value(nil), fact.sources...)
}

func (p *coroFrameRetentionProof) provesDominatedStableAddress(value ssa.Value, use ssa.Instruction) bool {
	if p == nil || value == nil || use == nil {
		return false
	}
	fact, ok := p.stableAddresses[coroFrameRetentionAddressUse{value: value, use: use}]
	return ok && fact.nonNil
}

func (p *coroFrameRetentionProof) provesGuardableStableAddress(value ssa.Value, use ssa.Instruction) bool {
	if p == nil || value == nil || use == nil {
		return false
	}
	_, ok := p.stableAddresses[coroFrameRetentionAddressUse{value: value, use: use}]
	return ok
}

func (p *coroFrameRetentionProof) requiresImplicitNilFault(value ssa.Value, use ssa.Instruction) bool {
	if p == nil || value == nil || use == nil {
		return false
	}
	fact, ok := p.stableAddresses[coroFrameRetentionAddressUse{value: value, use: use}]
	return ok && !fact.nonNil
}

func (p *coroFrameRetentionProof) provesTraceableUintptr(value ssa.Value) bool {
	if p == nil || value == nil {
		return false
	}
	_, ok := p.uintptrValues[value]
	return ok
}

func (p *coroFrameRetentionProof) provesInterfaceParameter(parameter *ssa.Parameter) bool {
	if p == nil || parameter == nil {
		return false
	}
	root, ok := p.exactRoots[parameter]
	return ok &&
		root.value == parameter &&
		root.kind == coroFrameRetentionRootInterfaceParameter
}

func (a *coroPhysicalPureSSAAudit) frameRetainsAllocation(alloc *ssa.Alloc) bool {
	proof := a.currentFrameRetentionProof()
	if proof == nil {
		return false
	}
	if _, ok := proof.allocations[alloc]; ok {
		return true
	}
	_, ok := proof.borrowedAllocations[alloc]
	return ok
}

func (a *coroPhysicalPureSSAAudit) frameRetainsBorrowedAllocation(alloc *ssa.Alloc) bool {
	proof := a.currentFrameRetentionProof()
	if proof == nil {
		return false
	}
	_, ok := proof.borrowedAllocations[alloc]
	return ok
}

func (a *coroPhysicalPureSSAAudit) frameRetainsManagedHeapAllocation(alloc *ssa.Alloc) bool {
	proof := a.currentFrameRetentionProof()
	if proof == nil {
		return false
	}
	_, ok := proof.managedHeapAllocations[alloc]
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
		allocations:               make(map[*ssa.Alloc]struct{}),
		borrowedAllocations:       make(map[*ssa.Alloc]coro.SSABorrowedAllocationProof),
		managedHeapAllocations:    make(map[*ssa.Alloc]coroFrameRetentionManagedHeapAllocation),
		terminalResultAllocations: make(map[*ssa.Alloc]struct{}),
		exactRoots:                make(map[ssa.Value]coroFrameRetentionExactRoot),
		stableAddresses:           make(map[coroFrameRetentionAddressUse]coroFrameRetentionAddressFact),
		uintptrValues:             make(map[ssa.Value]coroFrameRetentionUintptrFact),
		callKeepalives:            make(map[ssa.CallInstruction]coroFrameRetentionCallFact),
	}
	if a.universe == nil || a.ctx == nil || a.fn == nil || emitShadowStackInstrumentation {
		return proof
	}
	if a.frameRetentionABI == CoroFrameRetentionParkABIV2 {
		a.proveParkFrameRetention(proof)
	}
	terminalAllocations, err := coroStaticTerminalReconstructionAllocations(a.fn)
	if err != nil {
		// Static-cleanup preflight reports the precise structural error. Do not
		// publish a root capability digest from a partial proof in the meantime.
		return proof
	}
	for _, allocation := range terminalAllocations {
		proof.terminalResultAllocations[allocation] = struct{}{}
	}
	a.proveBorrowedHeapAllocations(proof)
	a.proveManagedHeapAllocations(proof)
	newCoroFrameRetentionRootBuilder(a, proof).prove()
	proof.rootDigest = coroFrameRetentionRootDigest(a, proof)
	return proof
}

// proveBorrowedHeapAllocations strengthens x/tools' conservative Heap bit only
// for fresh cells whose complete interprocedural address graph remains bounded
// by the owning call. The exact physical type must fit the target's native
// local limit: outcome-plain functions use an entry alloca, while CoroSplit
// incorporates the same storage into a stackless frame. Each physical recipe
// still zeroes the cell at the source Alloc instruction, preserving loop and
// conditional allocation semantics despite entry-owned storage.
func (a *coroPhysicalPureSSAAudit) proveBorrowedHeapAllocations(proof *coroFrameRetentionProof) {
	if a == nil || a.ctx == nil || a.ctx.prog == nil || a.fn == nil || proof == nil {
		return
	}
	bitcastAllocation := (*ssa.Alloc)(nil)
	if bitcast, exact := coro.ProveSSAExactScalarBitcast(a.fn); exact {
		bitcastAllocation = bitcast.Allocation
	}
	for _, block := range a.fn.Blocks {
		for _, instruction := range block.Instrs {
			allocation, ok := instruction.(*ssa.Alloc)
			if !ok || !allocation.Heap || allocation == bitcastAllocation ||
				a.ctx.skipSyntheticMakeSliceAlloc(allocation) || isEmissionVargsAlloc(a.ctx, allocation) {
				continue
			}
			if _, park := proof.allocations[allocation]; park {
				continue
			}
			if _, terminal := proof.terminalResultAllocations[allocation]; terminal {
				continue
			}
			pointer, ok := types.Unalias(a.typeOf(allocation.Type())).Underlying().(*types.Pointer)
			if !ok || a.ctx.prog.LocalGoTypeExceedsNativeStack(a.typeOf(pointer.Elem())) ||
				validateCoroPhysicalSSAValueType(a.typeOf(pointer.Elem())) != nil {
				continue
			}
			borrow, exact := coro.ProveSSABorrowedAllocation(allocation)
			if exact {
				proof.borrowedAllocations[allocation] = borrow
			}
		}
	}
}

func (a *coroPhysicalPureSSAAudit) proveParkFrameRetention(proof *coroFrameRetentionProof) {
	if a == nil || proof == nil || a.universe == nil || a.fn == nil {
		return
	}
	type candidate struct {
		allocation *ssa.Alloc
		prepare    *ssa.Call
		park       *ssa.Call
	}
	var candidates []candidate
	for _, block := range a.fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			semantics, intrinsic, err := coroIntrinsicCallSiteSemantics(a.universe, call)
			if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineSuspend ||
				call.Common() == nil || len(call.Common().Args) != 2 {
				continue
			}
			allocation := coroFrameRetentionDirectAllocRoot(call.Common().Args[0], make(map[ssa.Value]bool))
			if allocation == nil || allocation.Parent() != a.fn || !allocation.Heap ||
				a.ctx.skipSyntheticMakeSliceAlloc(allocation) || isEmissionVargsAlloc(a.ctx, allocation) {
				continue
			}
			pointer, pointerOK := types.Unalias(a.typeOf(allocation.Type())).Underlying().(*types.Pointer)
			if !pointerOK || coroTypeContainsGCPointer(pointer.Elem(), make(map[types.Type]bool)) {
				continue
			}
			prepare, ok := a.coroParkBorrowPrepare(allocation, call)
			if !ok || !coroFrameRetentionAddressUsesMatch(
				allocation,
				map[*ssa.Call]int{prepare: 0, call: 0},
				nil,
			) {
				continue
			}
			candidates = append(candidates, candidate{allocation: allocation, prepare: prepare, park: call})
		}
	}
	allocationUses := make(map[*ssa.Alloc]int)
	callUses := make(map[*ssa.Call]int)
	for _, candidate := range candidates {
		allocationUses[candidate.allocation]++
		callUses[candidate.prepare]++
		callUses[candidate.park]++
	}
	for _, candidate := range candidates {
		if allocationUses[candidate.allocation] != 1 || callUses[candidate.prepare] != 1 || callUses[candidate.park] != 1 {
			continue
		}
		proof.allocations[candidate.allocation] = struct{}{}
	}
}

// coroParkBorrowPrepare selects the sole call which initializes one opaque
// park state before llgo.coroPark. The callable certificate, rather than a C
// symbol allow-list, proves that the call is executor-safe and borrows the
// state only until it returns. This is the extension boundary for every keyed
// event source: adding a source does not change compiler code.
func (a *coroPhysicalPureSSAAudit) coroParkBorrowPrepare(allocation *ssa.Alloc, park *ssa.Call) (*ssa.Call, bool) {
	if a == nil || a.universe == nil || allocation == nil || park == nil ||
		allocation.Parent() != a.fn || park.Parent() != a.fn || park.Block() == nil {
		return nil, false
	}
	aliases := map[ssa.Value]bool{allocation: true}
	queue := []ssa.Value{allocation}
	for head := 0; head < len(queue); head++ {
		value := queue[head]
		refs := value.Referrers()
		if refs == nil {
			return nil, false
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

	var prepare *ssa.Call
	for alias := range aliases {
		refs := alias.Referrers()
		if refs == nil {
			return nil, false
		}
		for _, reference := range *refs {
			call, ok := reference.(*ssa.Call)
			if !ok || call == park {
				continue
			}
			if prepare != nil && prepare != call || call.Common() == nil || call.Common().IsInvoke() ||
				len(call.Common().Args) == 0 || call.Common().Args[0] != alias || call.Block() != park.Block() ||
				coroFrameRetentionInstructionIndex(call) >= coroFrameRetentionInstructionIndex(park) {
				return nil, false
			}
			callee := a.universe.canonicalAlias(call.Common().StaticCallee())
			certificate, certified := a.universe.callableContracts[callee]
			if callee == nil || !certified || certificate.Scope != coro.CallableContractScopeDeclaration ||
				certificate.Contract.Progress != coro.ProgressExecutorSafe ||
				certificate.Contract.Reentry != coro.ReentryNone ||
				certificate.Contract.Memory != coro.MemoryBorrowUntilReturn ||
				(certificate.Contract.Affinity != coro.AffinityCallerThread && certificate.Contract.Affinity != coro.AffinityAnyThread) {
				return nil, false
			}
			prepare = call
		}
	}
	return prepare, prepare != nil
}

// proveManagedHeapAllocations freezes the exact allocations whose managed
// allocator edge and suspended-frame root profile are both proven. This
// includes semantic escapes and target-layout promotion of an oversized local.
// Unlike proveParkFrameRetention, it never changes code generation to an
// alloca: managed identity and lifetime remain those of final LLSSA lowering.
func (a *coroPhysicalPureSSAAudit) proveManagedHeapAllocations(proof *coroFrameRetentionProof) {
	if a == nil || a.fn == nil || proof == nil {
		return
	}
	for _, block := range a.fn.Blocks {
		for _, instruction := range block.Instrs {
			alloc, ok := instruction.(*ssa.Alloc)
			if !ok || !coroAllocationUsesManagedStorage(a.ctx, alloc) {
				continue
			}
			if _, frameLocal := proof.allocations[alloc]; frameLocal {
				continue
			}
			if _, borrowed := proof.borrowedAllocations[alloc]; borrowed {
				continue
			}
			fact, reason := a.managedHeapAllocationCapability(alloc)
			if reason == "" {
				proof.managedHeapAllocations[alloc] = fact
			}
		}
	}
}

// coroFrameRetentionRootBuilder proves a deliberately small transport model:
// exact source roots may be retained by LLVM's ordinary SSA liveness in a
// stackless coroutine frame, and otherwise-dead pointer sources converted to
// uintptr are attached to the exact bounded child/worker call that needs the
// Go uintptrkeepalive lifetime. It never treats an arbitrary pointer-shaped
// value or function name as evidence.
type coroFrameRetentionRootBuilder struct {
	audit        *coroPhysicalPureSSAAudit
	proof        *coroFrameRetentionProof
	valueOrder   map[ssa.Value]int
	instrOrder   map[ssa.Instruction]int
	parameterPos map[*ssa.Parameter]int
}

type coroFrameRetentionTrace struct {
	roots    map[ssa.Value]struct{}
	evidence map[ssa.Instruction]struct{}
}

func newCoroFrameRetentionRootBuilder(audit *coroPhysicalPureSSAAudit, proof *coroFrameRetentionProof) *coroFrameRetentionRootBuilder {
	builder := &coroFrameRetentionRootBuilder{
		audit:        audit,
		proof:        proof,
		valueOrder:   make(map[ssa.Value]int),
		instrOrder:   make(map[ssa.Instruction]int),
		parameterPos: make(map[*ssa.Parameter]int),
	}
	next := 0
	if audit != nil && audit.fn != nil {
		for _, free := range audit.fn.FreeVars {
			if free == nil {
				continue
			}
			builder.valueOrder[free] = next
			next++
		}
		for index, parameter := range audit.fn.Params {
			if parameter == nil {
				continue
			}
			builder.parameterPos[parameter] = index
			builder.valueOrder[parameter] = next
			next++
		}
		for _, block := range audit.fn.Blocks {
			for _, instruction := range block.Instrs {
				builder.instrOrder[instruction] = next
				if value, ok := instruction.(ssa.Value); ok {
					builder.valueOrder[value] = next
				}
				next++
			}
		}
	}
	return builder
}

func (b *coroFrameRetentionRootBuilder) prove() {
	if b == nil || b.audit == nil || b.audit.fn == nil || b.proof == nil {
		return
	}
	// An interface parameter arrives as a copied type/data pair owned by this
	// invocation. LLVM may spill those exact words into the coroutine frame,
	// and terminal panic publication copies them into the parent-owned
	// completion record before that frame is destroyed. The dynamic data word
	// remains governed by ordinary Go call/escape lifetime and the selected
	// nonmoving-conservative-or-none root profile.
	for _, parameter := range b.audit.fn.Params {
		if parameter == nil {
			continue
		}
		if _, ok := types.Unalias(b.audit.typeOf(parameter.Type())).Underlying().(*types.Interface); ok {
			b.addExactRoot(parameter, coroFrameRetentionRootInterfaceParameter)
		}
	}
	// Ordinary escaping Allocs keep their Go heap identity. Recording the exact
	// SSA pointer here states only that LLVM may spill that pointer into the
	// scanned coroutine frame; it does not turn the referent into frame storage.
	for allocation := range b.proof.managedHeapAllocations {
		b.addExactRoot(allocation, coroFrameRetentionRootManagedHeapAllocation)
	}
	// First freeze the exact address/use pairs. This includes ordinary local
	// struct fields so a later ABI consumer can distinguish "LLVM kept this
	// exact alloca/address live" from a blanket local-pointer policy.
	for _, block := range b.audit.fn.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.FieldAddr:
				b.recordStableAddress(instruction, instruction)
			case *ssa.IndexAddr:
				b.recordStableAddress(instruction, instruction)
			case *ssa.UnOp:
				if instruction.Op == token.MUL {
					b.recordStableAddress(instruction.X, instruction)
				}
			case *ssa.Store:
				b.recordStableAddress(instruction.Addr, instruction)
			case *ssa.Slice:
				// Slicing a *array retains the pointer transport independently of
				// whether bounds are explicit. ExplicitStatus lowering owns the nil
				// and bounds branches; this fact certifies only the exact root/use.
				if _, pointer := types.Unalias(b.audit.typeOf(instruction.X.Type())).Underlying().(*types.Pointer); pointer {
					b.recordStableAddress(instruction.X, instruction)
				}
			}
		}
	}

	// A pointer->uintptr value is certified only when every semantic use is a
	// value-preserving integer conversion/Phi, a scalar comparison terminal, or
	// one exact bounded managed-child/worker call. Returning, storing, general
	// arithmetic on, dynamically dispatching, or passing it to an arbitrary
	// foreign declaration leaves it uncertified. The conversion chain is
	// deliberately one-way: converting an integer alias back to a pointer is
	// still admitted only by the separate exact same-expression roundtrip proof
	// below.
	for _, block := range b.audit.fn.Blocks {
		for _, instruction := range block.Instrs {
			conversion, ok := instruction.(*ssa.Convert)
			if ok && coroFrameRetentionPointerToUintptr(conversion) {
				b.proveUintptrKeepalive(conversion)
			}
		}
	}
	// Pointer/slice arguments to a static managed child are already typed, but
	// their source root still belongs in the digest and in the exact call fact.
	// Nil is legal for transport; dereference sites require their own dominance
	// proof above.
	for _, block := range b.audit.fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil {
				continue
			}
			kind, bounded := b.boundedCallInstructionKind(call)
			if !bounded {
				continue
			}
			arguments := call.Common().Args
			specializedWorkerVarargs := false
			if kind == coroFrameRetentionCallWorkerV1 {
				if direct, ok := call.(*ssa.Call); ok {
					shape, recognized, err := validateCoroWorkerForeignCallWithAuthority(
						b.audit.foreignCallAuthority(),
						direct, b.audit.universe.prog.PointerSize(),
					)
					if recognized && err == nil && shape.variadic {
						// The ordinary frontend deliberately erases the synthetic
						// [N]any allocation and its []any Slice into a side table.
						// The worker record likewise carries the already-unboxed
						// physical arguments, so freeze those exact SSA values as
						// retention sources rather than a slice which has no emitted
						// LLVM value.
						arguments = shape.arguments
						specializedWorkerVarargs = true
					}
				}
			}
			for _, argument := range arguments {
				var trace coroFrameRetentionTrace
				var traced bool
				switch types.Unalias(b.audit.typeOf(argument.Type())).Underlying().(type) {
				case *types.Pointer:
					trace, traced = b.traceAddress(argument, call, false, make(map[ssa.Value]bool))
				case *types.Slice:
					trace, traced = b.traceSlice(argument, call, make(map[ssa.Value]bool))
				case *types.Basic:
					if coroFrameRetentionUnsafePointer(argument.Type()) {
						trace, traced = b.traceAddress(argument, call, false, make(map[ssa.Value]bool))
					} else if specializedWorkerVarargs && coroFrameRetentionUintptrLike(argument.Type()) {
						trace, traced = b.traceSpecializedWorkerUintptr(
							argument, call, make(map[ssa.Value]bool),
						)
						if traced {
							b.proof.uintptrValues[argument] = coroFrameRetentionUintptrFact{
								roots: b.sortedValues(trace.roots),
							}
						}
					}
				}
				if traced {
					b.mergeCallFact(call, kind, trace.roots, []ssa.Value{argument})
				}
			}
		}
	}
}

// traceSpecializedWorkerUintptr proves the pointer provenance of one concrete
// C-varargs operand after the frontend's synthetic []any transport has been
// erased. Unlike the general use-graph proof, this is occurrence-scoped to the
// exact frozen worker call above: unrelated uses of the same integer do not
// weaken the fact that this physical record field carries the traced address.
// Only value-preserving conversions and address +/- scalar offset are admitted.
func (b *coroFrameRetentionRootBuilder) traceSpecializedWorkerUintptr(
	value ssa.Value,
	use ssa.Instruction,
	visiting map[ssa.Value]bool,
) (coroFrameRetentionTrace, bool) {
	trace := newCoroFrameRetentionTrace()
	if value == nil || visiting[value] || !coroFrameRetentionUintptrLike(value.Type()) {
		return trace, false
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.Convert:
		if value.X == nil {
			return trace, false
		}
		if coroFrameRetentionPointerLike(value.X.Type()) {
			return b.traceAddress(value.X, value, false, make(map[ssa.Value]bool))
		}
		if coroFrameRetentionIntegerLike(value.X.Type()) {
			return b.traceSpecializedWorkerUintptr(value.X, use, visiting)
		}
	case *ssa.ChangeType:
		if value.X != nil && coroFrameRetentionIntegerLike(value.X.Type()) {
			return b.traceSpecializedWorkerUintptr(value.X, use, visiting)
		}
	case *ssa.BinOp:
		if value.Op != token.ADD && value.Op != token.SUB {
			return trace, false
		}
		xHasPointer := coroFrameRetentionIntegerHasPointerProvenance(value.X, make(map[ssa.Value]bool))
		yHasPointer := coroFrameRetentionIntegerHasPointerProvenance(value.Y, make(map[ssa.Value]bool))
		if xHasPointer == yHasPointer || value.Op == token.SUB && !xHasPointer {
			return trace, false
		}
		provenance := value.X
		offset := value.Y
		if yHasPointer {
			provenance, offset = value.Y, value.X
		}
		if coroFrameRetentionIntegerHasPointerProvenance(offset, make(map[ssa.Value]bool)) {
			return trace, false
		}
		return b.traceSpecializedWorkerUintptr(provenance, use, visiting)
	case *ssa.Phi:
		if len(value.Edges) == 0 {
			return trace, false
		}
		for _, edge := range value.Edges {
			part, ok := b.traceSpecializedWorkerUintptr(edge, use, visiting)
			if !ok {
				return newCoroFrameRetentionTrace(), false
			}
			trace.merge(part)
		}
		return trace, true
	}
	return trace, false
}

func (b *coroFrameRetentionRootBuilder) recordStableAddress(value ssa.Value, use ssa.Instruction) {
	if value == nil || use == nil {
		return
	}
	// Freeze transport/root provenance independently of nil-access safety. A
	// nullable parameter is a sound exact frame root; rejecting it here would
	// conflate liveness with Go's implicit nil-dereference semantics.
	trace, ok := b.traceAddress(value, use, false, make(map[ssa.Value]bool))
	if !ok {
		return
	}
	fact := coroFrameRetentionAddressFact{
		roots:    b.sortedValues(trace.roots),
		evidence: b.sortedInstructions(trace.evidence),
	}
	// A second, stricter trace proves that this exact use needs no compiler
	// fault edge. It may succeed with no dynamic evidence for globals/allocas;
	// retain the explicit boolean rather than overloading evidence length.
	if nonNil, proved := b.traceAddress(value, use, true, make(map[ssa.Value]bool)); proved {
		fact.roots = b.sortedValues(nonNil.roots)
		fact.evidence = b.sortedInstructions(nonNil.evidence)
		fact.nonNil = true
	}
	b.proof.stableAddresses[coroFrameRetentionAddressUse{value: value, use: use}] = fact
}

func (b *coroFrameRetentionRootBuilder) traceAddress(value ssa.Value, use ssa.Instruction, requireNonNil bool, visiting map[ssa.Value]bool) (coroFrameRetentionTrace, bool) {
	trace := newCoroFrameRetentionTrace()
	if value == nil || visiting[value] {
		return trace, false
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.Global:
		_, ok := types.Unalias(b.audit.typeOf(value.Type())).Underlying().(*types.Pointer)
		return trace, ok
	case *ssa.Const:
		// x/tools Const.IsNil omits the basic unsafe.Pointer type. The shared
		// SSA helper uses the representation-level Value == nil fact after the
		// surrounding address trace has already required a pointer-like type.
		return trace, !requireNonNil && coroFrameRetentionNilConst(value)
	case *ssa.Parameter:
		if !coroFrameRetentionPointerLike(value.Type()) {
			return trace, false
		}
		if requireNonNil {
			evidence, ok := b.dominatingNonNilEvidence(value, use)
			if !ok {
				return trace, false
			}
			trace.addEvidence(evidence...)
		}
		kind := coroFrameRetentionRootPointerParameter
		if index, ok := b.parameterPos[value]; ok && index == 0 && b.audit.fn.Signature != nil && b.audit.fn.Signature.Recv() != nil {
			kind = coroFrameRetentionRootReceiver
		}
		b.addExactRoot(value, kind)
		trace.addRoot(value)
		return trace, true
	case *ssa.FreeVar:
		// A FreeVar in a capability-certified captured coroutine entry is either
		// a pointer to one exact closure cell loaded from the typed descriptor
		// environment, or an all-zero-sized capture recreated from the canonical
		// non-nil module sentinel. The environment and either exact value may be
		// retained by the LLVM coroutine frame. Only the ordinary environment
		// form remains nullable and therefore needs dominating evidence or the
		// compiler-owned nil-fault edge.
		zeroSizedSentinel, exact := b.classifyExactCoroClosureFreeVar(value)
		if !exact {
			return trace, false
		}
		if requireNonNil && !zeroSizedSentinel {
			evidence, ok := b.dominatingNonNilEvidence(value, use)
			if !ok {
				return trace, false
			}
			trace.addEvidence(evidence...)
		}
		b.addExactRoot(value, coroFrameRetentionRootClosureFreeVar)
		trace.addRoot(value)
		return trace, true
	case *ssa.Alloc:
		kind := coroFrameRetentionRootLocalAddress
		if _, managed := b.proof.managedHeapAllocations[value]; managed {
			kind = coroFrameRetentionRootManagedHeapAllocation
		} else if value.Heap {
			_, parkRetained := b.proof.allocations[value]
			_, borrowRetained := b.proof.borrowedAllocations[value]
			if !parkRetained && !borrowRetained {
				return trace, false
			}
		}
		if b.audit.ctx != nil && (b.audit.ctx.skipSyntheticMakeSliceAlloc(value) || isEmissionVargsAlloc(b.audit.ctx, value)) {
			return trace, false
		}
		b.addExactRoot(value, kind)
		trace.addRoot(value)
		return trace, true
	case *ssa.FieldAddr:
		pointer, ok := types.Unalias(b.audit.typeOf(value.X.Type())).Underlying().(*types.Pointer)
		if !ok {
			return trace, false
		}
		structure, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Struct)
		if !ok || value.Field < 0 || value.Field >= structure.NumFields() {
			return trace, false
		}
		return b.traceAddress(value.X, use, requireNonNil, visiting)
	case *ssa.IndexAddr:
		underlying := types.Unalias(b.audit.typeOf(value.X.Type())).Underlying()
		switch container := underlying.(type) {
		case *types.Pointer:
			array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array)
			if !ok {
				return trace, false
			}
			if coroConstantIndexInBounds(value.Index, array.Len()) {
				return b.traceAddress(value.X, use, requireNonNil, visiting)
			}
			trace, ok = b.traceAddress(value.X, use, false, visiting)
			if !ok {
				return trace, false
			}
			if evidence, bounded := b.dominatingFixedArrayIndexEvidence(value.Index, array.Len(), value); bounded {
				trace.addEvidence(evidence...)
				if requireNonNil {
					nonNil, proved := b.traceAddress(value.X, use, true, visiting)
					if !proved {
						return newCoroFrameRetentionTrace(), false
					}
					trace.merge(nonNil)
				}
			} else if requireNonNil {
				// Transporting the fixed-array pointer and its derived address is
				// frame-safe, but code generation must take both the bounds fault
				// and possible nil fault edges before forming the GEP.
				return newCoroFrameRetentionTrace(), false
			}
			return trace, true
		case *types.Slice:
			traced, ok := b.traceSlice(value.X, use, visiting)
			if !ok {
				return trace, false
			}
			trace = traced
			if evidence, bounded := b.dominatingSliceIndexEvidence(value.X, value.Index, value); bounded {
				trace.addEvidence(evidence...)
			} else if requireNonNil {
				// Transporting the slice and derived address is safe under the
				// selected frame-root profile, but dereferencing it requires the
				// compiler-owned bounds branch first.
				return newCoroFrameRetentionTrace(), false
			}
			return trace, true
		default:
			return trace, false
		}
	case *ssa.SliceToArrayPointer:
		length, exact := coroSliceToArrayPointerLen(value, b.audit.typeOf)
		if !exact {
			return trace, false
		}
		trace, ok := b.traceSlice(value.X, use, visiting)
		if !ok {
			return trace, false
		}
		if requireNonNil && length == 0 {
			// The zero-length conversion intentionally preserves a nil slice data
			// word. Keep an exact dominating p!=nil fact when present; a synthetic
			// [0]T value load is recognized separately and does not request this
			// strict trace, while every unguarded explicit dereference remains
			// guardable through the explicit-status nil fault.
			evidence, nonNil := b.dominatingNonNilEvidence(value, use)
			if !nonNil {
				return newCoroFrameRetentionTrace(), false
			}
			trace.addEvidence(evidence...)
		}
		// For N>0, reaching a use of the conversion means its len>=N check
		// completed normally, which also proves a non-nil data pointer.
		return trace, true
	case *ssa.ChangeType:
		if value.X != nil && coroFrameRetentionPointerLike(value.Type()) && coroFrameRetentionPointerLike(value.X.Type()) {
			return b.traceAddress(value.X, use, requireNonNil, visiting)
		}
	case *ssa.Convert:
		if value.X != nil && coroFrameRetentionPointerLike(value.Type()) && coroFrameRetentionPointerLike(value.X.Type()) {
			return b.traceAddress(value.X, use, requireNonNil, visiting)
		}
	case *ssa.Call:
		if coroPhysicalUnsafeAddCall(value, b.audit.typeOf) {
			return b.traceAddress(value.Common().Args[0], use, requireNonNil, visiting)
		}
	case *ssa.Phi:
		return b.traceAddressPhiComponent(value, use, requireNonNil, visiting)
	}
	// A pointer-producing SSA value owned by this function is itself an exact
	// transport root under the current non-moving/conservative-or-no-GC frame
	// profile. Its producer is audited independently; a dereference additionally
	// requires a dominating non-nil fact.
	if coroFrameRetentionPointerLike(value.Type()) {
		if _, local := b.valueOrder[value]; !local {
			return trace, false
		}
		if requireNonNil {
			evidence, ok := b.dominatingNonNilEvidence(value, use)
			if !ok {
				return trace, false
			}
			trace.addEvidence(evidence...)
		}
		b.addExactRoot(value, coroFrameRetentionRootLocalAddress)
		trace.addRoot(value)
		return trace, true
	}
	return trace, false
}

// traceAddressPhiComponent treats mutually recursive pointer phis as one
// transport component. Requiring every recursively visited phi to discover an
// independent seed makes a valid loop SCC depend on DFS order (and rejected
// map bucket loops with several mutually recursive merge nodes). The component
// proof instead traces every external edge exactly once and requires at least
// one such seed; a closed phi-only cycle remains rejected.
func (b *coroFrameRetentionRootBuilder) traceAddressPhiComponent(
	root *ssa.Phi,
	use ssa.Instruction,
	requireNonNil bool,
	visiting map[ssa.Value]bool,
) (coroFrameRetentionTrace, bool) {
	trace := newCoroFrameRetentionTrace()
	if root == nil || len(root.Edges) == 0 || !coroFrameRetentionPointerLike(root.Type()) {
		return trace, false
	}
	component := map[*ssa.Phi]bool{root: true}
	queue := []*ssa.Phi{root}
	for head := 0; head < len(queue); head++ {
		phi := queue[head]
		if !coroFrameRetentionPointerLike(phi.Type()) {
			return newCoroFrameRetentionTrace(), false
		}
		for _, edge := range phi.Edges {
			if nested, ok := edge.(*ssa.Phi); ok && !component[nested] {
				component[nested] = true
				queue = append(queue, nested)
			}
		}
	}

	edgeRequiresNonNil := requireNonNil
	if requireNonNil {
		// One dominating check of the selected merged value proves whichever
		// incoming edge reaches this use; transport ownership is still traced
		// through every external edge below.
		if evidence, guarded := b.dominatingNonNilEvidence(root, use); guarded {
			trace.addEvidence(evidence...)
			edgeRequiresNonNil = false
		}
	}
	componentVisiting := make(map[ssa.Value]bool, len(visiting)+len(component))
	for value, active := range visiting {
		componentVisiting[value] = active
	}
	for phi := range component {
		componentVisiting[phi] = true
	}
	externalSeeds := 0
	for _, phi := range queue {
		for _, edge := range phi.Edges {
			if nested, ok := edge.(*ssa.Phi); ok && component[nested] {
				continue
			}
			edgeUse, _ := edge.(ssa.Instruction)
			if edgeUse == nil {
				edgeUse = phi
			}
			part, ok := b.traceAddress(edge, edgeUse, edgeRequiresNonNil, componentVisiting)
			if !ok {
				return newCoroFrameRetentionTrace(), false
			}
			trace.merge(part)
			externalSeeds++
		}
	}
	return trace, externalSeeds != 0
}

// classifyExactCoroClosureFreeVar returns whether value is one exact physical
// closure free variable and whether code generation replaces its elided
// zero-sized environment with the canonical non-nil module sentinel. Keeping
// the second fact explicit prevents a source FreeVar from acquiring blanket
// non-nil authority merely because it is captured.
func (b *coroFrameRetentionRootBuilder) classifyExactCoroClosureFreeVar(value *ssa.FreeVar) (
	zeroSizedSentinel bool,
	exact bool,
) {
	if b == nil || b.audit == nil || b.audit.fn == nil || b.audit.plan == nil ||
		b.audit.universe == nil || value == nil || !coroFrameRetentionPointerLike(value.Type()) {
		return false, false
	}
	found := false
	for _, free := range b.audit.fn.FreeVars {
		if free == value {
			found = true
			break
		}
	}
	if !found {
		return false, false
	}
	function, planned := b.audit.plan.FunctionPlan(b.audit.fn)
	if !planned || function.External != coro.Defined || function.Emission != coro.EmitCoroutine ||
		function.Primary != coro.PrimaryCoroutine ||
		(function.FuncRep != coro.Dispatch && function.FuncRep != coro.DirectCoro) {
		return false, false
	}
	if b.audit.universe.closureEnvironments.elidesZeroSizedFreeVar(b.audit.fn, value) {
		return true, true
	}
	effective, err := b.audit.universe.coroPhysicalEntrySourceSignature(b.audit.fn)
	return false, err == nil && effective != nil && effective.Params().Len() != 0 &&
		coroPhysicalClosureContextMatches(b.audit.fn, effective.Params().At(0).Type())
}

func (b *coroFrameRetentionRootBuilder) traceSlice(value ssa.Value, use ssa.Instruction, visiting map[ssa.Value]bool) (coroFrameRetentionTrace, bool) {
	trace := newCoroFrameRetentionTrace()
	if value == nil {
		return trace, false
	}
	if constantValue, ok := value.(*ssa.Const); ok {
		return trace, constantValue.IsNil()
	}
	if !coroFrameRetentionSliceLike(b.audit.typeOf(value.Type())) {
		return trace, false
	}
	if _, local := b.valueOrder[value]; !local {
		return trace, false
	}
	kind := coroFrameRetentionRootLocalSlice
	if _, parameter := value.(*ssa.Parameter); parameter {
		kind = coroFrameRetentionRootSliceParameter
	}
	b.addExactRoot(value, kind)
	trace.addRoot(value)
	return trace, true
}

func (b *coroFrameRetentionRootBuilder) proveUintptrKeepalive(conversion *ssa.Convert) {
	trace, ok := b.traceAddress(conversion.X, conversion, false, make(map[ssa.Value]bool))
	if !ok {
		return
	}
	aliases, calls, ok := b.boundedUintptrUses(conversion)
	if !ok || len(calls) == 0 {
		// Go also permits an exact pointer -> uintptr arithmetic -> pointer
		// roundtrip in one expression.  Keep that proof separate from the
		// managed-call uintptrkeepalive proof above: a roundtrip has a single
		// linear address-word lifetime and no call terminal that could silently
		// broaden the older capability.
		aliases, ok = b.exactUintptrRoundtripUses(conversion)
		if !ok {
			return
		}
		calls = nil
	}
	roots := b.sortedValues(trace.roots)
	for alias := range aliases {
		b.proof.uintptrValues[alias] = coroFrameRetentionUintptrFact{roots: append([]ssa.Value(nil), roots...)}
	}
	for call, kind := range calls {
		// Retain only the pointer-derived aliases that this exact call consumes.
		// The complete alias set can span sibling CFG branches and therefore
		// does not necessarily dominate every call in the graph. Call arguments
		// do dominate their call by SSA construction and are evaluated before
		// compileCoroCallKeepaliveSlots publishes the child.
		callSources := make(map[ssa.Value]struct{})
		if common := call.Common(); common != nil {
			for _, argument := range common.Args {
				if _, pointerDerived := aliases[argument]; pointerDerived {
					callSources[argument] = struct{}{}
				}
			}
		}
		if len(callSources) == 0 {
			// boundedUintptrUses records a call only after finding at least one
			// exact alias argument. Preserve fail-closed behavior if that
			// invariant ever diverges.
			continue
		}
		b.mergeCallFact(call, kind, trace.roots, b.sortedValueSet(callSources))
	}
}

// exactUintptrRoundtripUses recognizes the deliberately narrow SSA image of
// the unsafe.Pointer rule that permits address arithmetic between a
// pointer->uintptr conversion and the conversion back to a pointer in the same
// expression.  x/tools SSA does not retain expression nodes, so we require the
// stronger structural surrogate used here: one linear semantic-use chain in
// one basic block, with exactly one pointer reconstruction terminal.
//
// A managed child may still suspend between instructions in that block.  Under
// the selected non-moving conservative/no-GC profile the uintptr SSA value is
// then spilled in the coroutine frame and remains an address-shaped scanned
// word.  This is not a typed root-map or moving-GC proof, and the capability
// profile above intentionally prevents either consumer from claiming it.
func (b *coroFrameRetentionRootBuilder) exactUintptrRoundtripUses(root ssa.Value) (map[ssa.Value]struct{}, bool) {
	rootInstruction, ok := root.(ssa.Instruction)
	if !ok || rootInstruction.Block() == nil || !coroFrameRetentionUintptrLike(root.Type()) {
		return nil, false
	}
	block := rootInstruction.Block()
	aliases := map[ssa.Value]struct{}{root: {}}
	queue := []ssa.Value{root}
	pointerTerminals := 0
	for head := 0; head < len(queue); head++ {
		value := queue[head]
		refs := value.Referrers()
		if refs == nil {
			return nil, false
		}
		semanticUses := 0
		for _, reference := range *refs {
			switch instruction := reference.(type) {
			case *ssa.DebugRef:
			case *ssa.ChangeType:
				if instruction.Block() != block || instruction.X != value ||
					!coroFrameRetentionUintptrLike(value.Type()) || !coroFrameRetentionUintptrLike(instruction.Type()) {
					return nil, false
				}
				semanticUses++
				if _, seen := aliases[instruction]; !seen {
					aliases[instruction] = struct{}{}
					queue = append(queue, instruction)
				}
			case *ssa.Convert:
				if instruction.Block() != block || instruction.X != value || !coroFrameRetentionUintptrLike(value.Type()) {
					return nil, false
				}
				semanticUses++
				switch {
				case coroFrameRetentionUintptrLike(instruction.Type()):
					if _, seen := aliases[instruction]; !seen {
						aliases[instruction] = struct{}{}
						queue = append(queue, instruction)
					}
				case coroFrameRetentionPointerLike(instruction.Type()):
					pointerTerminals++
				default:
					return nil, false
				}
			case *ssa.BinOp:
				if instruction.Block() != block || !coroFrameRetentionUintptrLike(instruction.Type()) ||
					!coroFrameRetentionUintptrLike(instruction.X.Type()) || !coroFrameRetentionUintptrLike(instruction.Y.Type()) {
					return nil, false
				}
				xProvenance := instruction.X == value
				yProvenance := instruction.Y == value
				if xProvenance == yProvenance { // neither operand, or value+value
					return nil, false
				}
				other := instruction.Y
				if yProvenance {
					other = instruction.X
				}
				if _, alreadyProvenance := aliases[other]; alreadyProvenance || coroFrameRetentionIntegerHasPointerProvenance(other, make(map[ssa.Value]bool)) {
					return nil, false
				}
				switch instruction.Op {
				case token.ADD:
				case token.SUB:
					// Address-minus-offset preserves provenance; offset-minus-address
					// does not denote the same allocation.
					if !xProvenance {
						return nil, false
					}
				default:
					return nil, false
				}
				semanticUses++
				if _, seen := aliases[instruction]; !seen {
					aliases[instruction] = struct{}{}
					queue = append(queue, instruction)
				}
			default:
				return nil, false
			}
		}
		// One use is what makes this a single expression-shaped lifetime rather
		// than a stored/reused uintptr program variable.  It also prevents one
		// pointer word from being reconstructed on only some CFG paths.
		if semanticUses != 1 {
			return nil, false
		}
	}
	if pointerTerminals != 1 {
		return nil, false
	}
	return aliases, true
}

// coroFrameRetentionIntegerHasPointerProvenance rejects an offset operand that
// is itself derived from another pointer word.  Parameters and call results are
// valid scalar offsets; only an SSA derivation that visibly contains a pointer
// conversion is provenance-bearing here.
func coroFrameRetentionIntegerHasPointerProvenance(value ssa.Value, visiting map[ssa.Value]bool) bool {
	if value == nil || visiting[value] {
		return false
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.Convert:
		if value.X == nil {
			return false
		}
		if coroFrameRetentionPointerLike(value.X.Type()) && coroFrameRetentionUintptrLike(value.Type()) {
			return true
		}
		if coroFrameRetentionUintptrLike(value.Type()) {
			return coroFrameRetentionIntegerHasPointerProvenance(value.X, visiting)
		}
	case *ssa.ChangeType:
		if value.X != nil && coroFrameRetentionUintptrLike(value.Type()) {
			return coroFrameRetentionIntegerHasPointerProvenance(value.X, visiting)
		}
	case *ssa.BinOp:
		if coroFrameRetentionUintptrLike(value.Type()) {
			return coroFrameRetentionIntegerHasPointerProvenance(value.X, visiting) ||
				coroFrameRetentionIntegerHasPointerProvenance(value.Y, visiting)
		}
	case *ssa.Phi:
		for _, edge := range value.Edges {
			if coroFrameRetentionIntegerHasPointerProvenance(edge, visiting) {
				return true
			}
		}
	}
	return false
}

func (b *coroFrameRetentionRootBuilder) boundedUintptrUses(root ssa.Value) (map[ssa.Value]struct{}, map[ssa.CallInstruction]coroFrameRetentionCallKindV1, bool) {
	aliases := map[ssa.Value]struct{}{root: {}}
	calls := make(map[ssa.CallInstruction]coroFrameRetentionCallKindV1)
	queue := []ssa.Value{root}
	for head := 0; head < len(queue); head++ {
		value := queue[head]
		refs := value.Referrers()
		if refs == nil {
			return nil, nil, false
		}
		semanticUses := 0
		for _, reference := range *refs {
			switch instruction := reference.(type) {
			case *ssa.DebugRef:
			case *ssa.ChangeType:
				if instruction.X != value || !coroFrameRetentionIntegerLike(instruction.Type()) || !coroFrameRetentionIntegerLike(value.Type()) {
					return nil, nil, false
				}
				semanticUses++
				if _, seen := aliases[instruction]; !seen {
					aliases[instruction] = struct{}{}
					queue = append(queue, instruction)
				}
			case *ssa.Convert:
				if instruction.X != value || !coroFrameRetentionIntegerLike(instruction.Type()) || !coroFrameRetentionIntegerLike(value.Type()) {
					return nil, nil, false
				}
				semanticUses++
				if _, seen := aliases[instruction]; !seen {
					aliases[instruction] = struct{}{}
					queue = append(queue, instruction)
				}
			case *ssa.Phi:
				if !coroFrameRetentionIntegerLike(instruction.Type()) || !coroFrameRetentionIntegerLike(value.Type()) {
					return nil, nil, false
				}
				semanticUses++
				if _, seen := aliases[instruction]; !seen {
					aliases[instruction] = struct{}{}
					queue = append(queue, instruction)
				}
			case *ssa.BinOp:
				if instruction.X != value && instruction.Y != value ||
					!coroFrameRetentionIntegerLike(value.Type()) ||
					!coroFrameRetentionIntegerLike(instruction.X.Type()) ||
					!coroFrameRetentionIntegerLike(instruction.Y.Type()) {
					return nil, nil, false
				}
				switch instruction.Op {
				case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
					// Comparisons consume the address-shaped word and produce a
					// plain bool. Do not add the result to the provenance queue:
					// it cannot later reconstruct or transport the pointer.
					semanticUses++
				default:
					return nil, nil, false
				}
			case *ssa.Call:
				if b.exactScalarBitcastTransform(instruction, value) {
					semanticUses++
					if _, seen := aliases[instruction]; !seen {
						aliases[instruction] = struct{}{}
						queue = append(queue, instruction)
					}
					continue
				}
				kind, bounded := b.boundedUintptrCallKind(instruction, value)
				if !bounded || instruction.Common() == nil {
					return nil, nil, false
				}
				matches := 0
				for _, argument := range instruction.Common().Args {
					if argument == value {
						matches++
					}
				}
				if matches == 0 {
					return nil, nil, false
				}
				semanticUses += matches
				if previous, exists := calls[instruction]; exists && previous != kind {
					return nil, nil, false
				}
				calls[instruction] = kind
			case *ssa.Defer:
				kind, bounded := b.boundedUintptrCallKind(instruction, value)
				if !bounded || instruction.Common() == nil {
					return nil, nil, false
				}
				matches := 0
				for _, argument := range instruction.Common().Args {
					if argument == value {
						matches++
					}
				}
				if matches == 0 {
					return nil, nil, false
				}
				semanticUses += matches
				if previous, exists := calls[instruction]; exists && previous != kind {
					return nil, nil, false
				}
				calls[instruction] = kind
			default:
				return nil, nil, false
			}
		}
		if semanticUses == 0 {
			return nil, nil, false
		}
	}
	return aliases, calls, true
}

// exactScalarBitcastTransform recognizes one defined, side-effect-free Go SSA
// body that reinterprets all bits of a single scalar parameter as its
// same-width scalar result.  The plan checks alone are intentionally
// insufficient: an arbitrary DirectPlain function can still store its input.
// The body proof below binds the call to the exact local
// store -> unsafe-pointer conversions -> load -> return shape, so the result
// may continue the pointer-word provenance chain until its final managed-child
// terminal.
func (b *coroFrameRetentionRootBuilder) exactScalarBitcastTransform(call *ssa.Call, value ssa.Value) bool {
	if b == nil || b.audit == nil || b.audit.plan == nil || b.audit.universe == nil ||
		call == nil || call.Common() == nil || call.Common().IsInvoke() || call.Parent() != b.audit.fn ||
		value == nil || len(call.Common().Args) != 1 || call.Common().Args[0] != value {
		return false
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		return false
	}
	canonical := b.audit.universe.canonicalAlias(callee)
	if canonical == nil || len(canonical.Blocks) != 1 || canonical.Signature == nil ||
		canonical.Signature.Recv() != nil || canonical.Signature.Variadic() ||
		canonical.Signature.Params().Len() != 1 || canonical.Signature.Results().Len() != 1 {
		return false
	}
	plan, planned := b.audit.plan.FunctionPlan(canonical)
	if !planned || plan.External != coro.Defined || plan.Demand == coro.NoDemand ||
		plan.Emission != coro.EmitPlain || plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.DirectPlain ||
		plan.Effect != coro.NoSuspend || plan.Exec != 0 {
		return false
	}
	source := b.audit.typeOf(canonical.Signature.Params().At(0).Type())
	target := b.audit.typeOf(canonical.Signature.Results().At(0).Type())
	if !types.Identical(b.audit.typeOf(value.Type()), source) ||
		!types.Identical(b.audit.typeOf(call.Type()), target) {
		return false
	}
	_, exact := coro.ProveSSAExactScalarBitcast(canonical)
	return exact
}

// boundedUintptrCallKind extends the ordinary exact-call proof with one
// compiler-owned composite lowering: builtin print/println.  The builtin does
// not have an SSA StaticCallee, but LLSSA lowers each operand through one
// owner-scoped runtime Print* edge.  Admit a pointer-derived integer operand
// only when the complete builtin lowering is frozen and the helper for this
// exact operand is a demanded coroutine child.  A plain, foreign, elided, or
// otherwise unresolved helper is not a uintptr keepalive terminal.
func (b *coroFrameRetentionRootBuilder) boundedUintptrCallKind(call ssa.CallInstruction, value ssa.Value) (coroFrameRetentionCallKindV1, bool) {
	if kind, bounded := b.boundedCallInstructionKind(call); bounded {
		return kind, true
	}
	if ordinary, ok := call.(*ssa.Call); ok && b.boundedManagedPrintArgument(ordinary, value) {
		return coroFrameRetentionCallManagedChildV1, true
	}
	return coroFrameRetentionCallInvalidV1, false
}

func (b *coroFrameRetentionRootBuilder) boundedCallInstructionKind(
	call ssa.CallInstruction,
) (coroFrameRetentionCallKindV1, bool) {
	if call == nil || call.Common() == nil || call.Parent() != b.audit.fn {
		return coroFrameRetentionCallInvalidV1, false
	}
	switch call := call.(type) {
	case *ssa.Call:
		return b.boundedCallKind(call)
	case *ssa.Defer:
		if b.audit.universe == nil || !b.audit.universe.CoroWorkerSupported() {
			return coroFrameRetentionCallInvalidV1, false
		}
		if _, recognized, err := validateCoroWorkerCgoCall(
			b.audit.plan, b.audit.universe, call,
		); recognized && err == nil {
			return coroFrameRetentionCallWorkerV1, true
		}
	}
	return coroFrameRetentionCallInvalidV1, false
}

func (b *coroFrameRetentionRootBuilder) boundedManagedPrintArgument(call *ssa.Call, value ssa.Value) bool {
	if b == nil || b.audit == nil || b.audit.plan == nil || b.audit.universe == nil ||
		call == nil || call.Common() == nil || call.Parent() != b.audit.fn || value == nil {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "print" && builtin.Name() != "println" ||
		b.audit.validatePrintBuiltin(call, builtin.Name()) != "" {
		return false
	}
	found := false
	for _, argument := range call.Common().Args {
		if argument != value {
			continue
		}
		found = true
		helper := runtimePrintHelper(b.audit.typeOf(argument.Type()))
		target, planned := b.audit.plan.ResolveLoweredCall(b.audit.fn, helper)
		if !planned || target == nil {
			return false
		}
		plan, planned := b.audit.plan.FunctionPlan(target)
		if !planned || plan.External != coro.Defined || plan.Emission != coro.EmitCoroutine ||
			plan.Primary != coro.PrimaryCoroutine ||
			(plan.FuncRep != coro.DirectCoro && plan.FuncRep != coro.Dispatch) ||
			!plan.Demand.Contains(coro.AsyncDemand) || !plan.Effect.MaySuspend() {
			return false
		}
	}
	return found
}

func (b *coroFrameRetentionRootBuilder) boundedCallKind(call *ssa.Call) (coroFrameRetentionCallKindV1, bool) {
	if call == nil || call.Common() == nil || call.Common().IsInvoke() || call.Parent() != b.audit.fn {
		return coroFrameRetentionCallInvalidV1, false
	}
	// A generic park prepare is a bounded lifetime edge only when the immutable
	// proof selected its first argument as frame-owned opaque state. The proof
	// has already joined the exact call with its borrow-until-return callable
	// certificate; no event-source symbol is recovered here.
	if len(call.Common().Args) != 0 {
		allocation := coroFrameRetentionDirectAllocRoot(call.Common().Args[0], make(map[ssa.Value]bool))
		if _, retained := b.proof.allocations[allocation]; retained {
			semantics, intrinsic, err := coroIntrinsicCallSiteSemantics(b.audit.universe, call)
			if err == nil && (!intrinsic || semantics != CoroIntrinsicCallInlineSuspend) {
				return coroFrameRetentionCallParkOwnerV1, true
			}
		}
	}
	if b.audit.universe.CoroWorkerSupported() {
		semantics, intrinsic, err := coroIntrinsicCallSiteSemantics(b.audit.universe, call)
		if err == nil && intrinsic && semantics == CoroIntrinsicCallInlineForeignSuspend {
			if _, recognized, cgoErr := validateCoroWorkerCgoErrnoCall(
				b.audit.plan, b.audit.ctx, call,
			); recognized && cgoErr == nil {
				return coroFrameRetentionCallWorkerV1, true
			}
		}
		if err == nil && intrinsic && semantics == CoroIntrinsicCallInlineSuspend {
			workerCertified := false
			if b.audit.plan == nil {
				// Report-only physical audits have no lowering authority. They may
				// consume the immutable universe proof to inspect frame roots; real
				// preflight/codegen always joins it with the exact SSA plan below.
				certificate, certified, certificateErr := b.audit.universe.CoroWorkerSyscallCertificate(call)
				workerCertified = certificateErr == nil && certified && certificate.ID != ""
			} else {
				workerCertified = validateCoroWorkerSyscallCall(b.audit.plan, b.audit.universe, call) == nil
			}
			if workerCertified {
				return coroFrameRetentionCallWorkerV1, true
			}
		}
		if _, recognized, cgoErr := validateCoroWorkerCgoCall(
			b.audit.plan, b.audit.universe, call,
		); recognized && cgoErr == nil {
			return coroFrameRetentionCallWorkerV1, true
		}
		if _, recognized, foreignErr := validateCoroWorkerForeignCallWithAuthority(
			b.audit.foreignCallAuthority(),
			call, b.audit.universe.prog.PointerSize(),
		); recognized && foreignErr == nil {
			return coroFrameRetentionCallWorkerV1, true
		}
	}
	// A capability-aware dynamic descriptor call may create a child just like
	// an exact static coroutine call. Admit it only after the same immutable
	// CallPlan/ValuePlan/target validation used by preflight, and only when the
	// call can actually select a coroutine primary. This keeps pointer, slice,
	// and pointer-derived uintptr sources rooted in the parent's frame while the
	// dynamically selected child is running.
	if b.audit.plan != nil {
		if callPlan, planned := b.audit.plan.CallPlan(call); planned &&
			callPlan.Rep == coro.Dispatch && !callPlan.SyncDispatch &&
			(callPlan.Open || coroDispatchCallHasCoroutineTarget(b.audit.plan, callPlan)) {
			ownerPlan, ownerPlanned := b.audit.plan.FunctionPlan(b.audit.fn)
			if ownerPlanned && ownerPlan.Emission == coro.EmitCoroutine && ownerPlan.Primary == coro.PrimaryCoroutine &&
				validateCoroManagedDispatchCall(b.audit.plan, b.audit.fn, call, callPlan, b.audit.universe) == nil {
				return coroFrameRetentionCallManagedChildV1, true
			}
		}
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		return coroFrameRetentionCallInvalidV1, false
	}
	canonical := b.audit.universe.canonicalAlias(callee)
	if canonical == nil || len(canonical.Blocks) == 0 {
		return coroFrameRetentionCallInvalidV1, false
	}
	if _, frozen := b.audit.universe.required[canonical]; !frozen {
		return coroFrameRetentionCallInvalidV1, false
	}
	return coroFrameRetentionCallManagedChildV1, true
}

func (b *coroFrameRetentionRootBuilder) mergeCallFact(call ssa.CallInstruction, kind coroFrameRetentionCallKindV1, roots map[ssa.Value]struct{}, sources []ssa.Value) {
	if call == nil || kind == coroFrameRetentionCallInvalidV1 {
		return
	}
	fact := b.proof.callKeepalives[call]
	if fact.kind != coroFrameRetentionCallInvalidV1 && fact.kind != kind {
		delete(b.proof.callKeepalives, call)
		return
	}
	fact.kind = kind
	rootSet := make(map[ssa.Value]struct{}, len(fact.roots)+len(roots))
	for _, value := range fact.roots {
		rootSet[value] = struct{}{}
	}
	for value := range roots {
		rootSet[value] = struct{}{}
	}
	sourceSet := make(map[ssa.Value]struct{}, len(fact.sources)+len(sources))
	for _, value := range fact.sources {
		sourceSet[value] = struct{}{}
	}
	for _, value := range sources {
		if value != nil {
			sourceSet[value] = struct{}{}
		}
	}
	fact.roots = b.sortedValues(rootSet)
	fact.sources = b.sortedValueSet(sourceSet)
	b.proof.callKeepalives[call] = fact
}

func (b *coroFrameRetentionRootBuilder) addExactRoot(value ssa.Value, kind coroFrameRetentionRootKind) {
	if value == nil || kind == coroFrameRetentionRootInvalid {
		return
	}
	order, ok := b.valueOrder[value]
	if !ok {
		return
	}
	if previous, exists := b.proof.exactRoots[value]; exists {
		if previous.kind != kind {
			delete(b.proof.exactRoots, value)
		}
		return
	}
	b.proof.exactRoots[value] = coroFrameRetentionExactRoot{value: value, kind: kind, order: order}
}

func (b *coroFrameRetentionRootBuilder) dominatingNonNilEvidence(value ssa.Value, use ssa.Instruction) ([]ssa.Instruction, bool) {
	if value == nil || use == nil || use.Block() == nil {
		return nil, false
	}
	for _, block := range b.audit.fn.Blocks {
		if len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok || (comparison.Op != token.EQL && comparison.Op != token.NEQ) {
			continue
		}
		matches := (comparison.X == value && coroFrameRetentionNilConst(comparison.Y)) ||
			(comparison.Y == value && coroFrameRetentionNilConst(comparison.X))
		if !matches {
			continue
		}
		successor := 0
		if comparison.Op == token.EQL {
			successor = 1
		}
		if block.Succs[successor].Dominates(use.Block()) {
			return []ssa.Instruction{comparison, branch}, true
		}
	}
	return nil, false
}

func (b *coroFrameRetentionRootBuilder) dominatingNonEmptySliceEvidence(value ssa.Value, use ssa.Instruction) ([]ssa.Instruction, bool) {
	if value == nil || use == nil || use.Block() == nil {
		return nil, false
	}
	for _, block := range b.audit.fn.Blocks {
		if len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok {
			continue
		}
		lenCall, zeroOnRight := coroFrameRetentionLenZeroComparison(comparison, value)
		if lenCall == nil {
			continue
		}
		successor, proves := coroFrameRetentionPositiveLengthSuccessor(comparison.Op, zeroOnRight)
		if proves && block.Succs[successor].Dominates(use.Block()) {
			return []ssa.Instruction{lenCall, comparison, branch}, true
		}
	}
	return nil, false
}

// dominatingSliceIndexEvidence recognizes the two canonical x/tools SSA range
// shapes. In both forms the true edge of `index < len(slice)` dominates the
// IndexAddr, while the induction variable starts at zero (or -1 immediately
// before a +1) and advances by one. The comparison therefore proves both the
// lower and upper bounds without treating an arbitrary slice address as stable.
func (b *coroFrameRetentionRootBuilder) dominatingSliceIndexEvidence(
	slice, index ssa.Value,
	use ssa.Instruction,
) ([]ssa.Instruction, bool) {
	if slice == nil || index == nil || use == nil || use.Block() == nil {
		return nil, false
	}
	if coroFrameRetentionExactZeroIndex(index) {
		return b.dominatingNonEmptySliceEvidence(slice, use)
	}
	if subtraction, ok := index.(*ssa.BinOp); ok && subtraction.Op == token.SUB &&
		coroFrameRetentionExactLenCall(subtraction.X, slice) != nil &&
		coroFrameRetentionExactInteger(subtraction.Y, 1) {
		evidence, proved := b.dominatingNonEmptySliceEvidence(slice, use)
		if proved {
			return append(evidence, subtraction), true
		}
	}
	for _, block := range b.audit.fn.Blocks {
		if len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok || comparison.Op != token.LSS || comparison.X != index {
			continue
		}
		lenCall := coroFrameRetentionExactLenCall(comparison.Y, slice)
		if lenCall == nil || !block.Succs[0].Dominates(use.Block()) {
			continue
		}
		inductionEvidence, ok := coroFrameRetentionNonNegativeRangeIndex(index, block, block.Succs[0], use, 0)
		if !ok {
			continue
		}
		evidence := append([]ssa.Instruction(nil), inductionEvidence...)
		evidence = append(evidence, lenCall, comparison, branch)
		return evidence, true
	}
	return nil, false
}

// dominatingFixedArrayIndexEvidence accepts the canonical SSA induction shape
// only when a true loop edge proves index < limit and the constant limit fits
// the frozen array bound. Unlike a slice, the array storage is already part of
// its traced base root; this proof exists solely to make the implicit bounds
// helper unreachable.
func (b *coroFrameRetentionRootBuilder) dominatingFixedArrayIndexEvidence(
	index ssa.Value,
	bound int64,
	use ssa.Instruction,
) ([]ssa.Instruction, bool) {
	if b == nil || b.audit == nil || b.audit.fn == nil ||
		!coro.ProveSSAExactSafeFixedArrayIndex(b.audit.fn, index, bound, use) {
		return nil, false
	}
	for _, block := range b.audit.fn.Blocks {
		if len(block.Instrs) == 0 || len(block.Succs) != 2 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		comparison, ok := branch.Cond.(*ssa.BinOp)
		if !ok || comparison.Op != token.LSS || comparison.X != index ||
			!coroFrameRetentionIntegerAtMost(comparison.Y, bound) ||
			!block.Succs[0].Dominates(use.Block()) {
			continue
		}
		limit, ok := coroFrameRetentionExactPositiveInteger(comparison.Y)
		if !ok {
			continue
		}
		inductionEvidence, ok := coroFrameRetentionNonNegativeRangeIndex(index, block, block.Succs[0], use, limit)
		if !ok {
			continue
		}
		evidence := append([]ssa.Instruction(nil), inductionEvidence...)
		evidence = append(evidence, comparison, branch)
		return evidence, true
	}
	return nil, false
}

func coroFrameRetentionNonNegativeRangeIndex(
	index ssa.Value,
	header *ssa.BasicBlock,
	trueSuccessor *ssa.BasicBlock,
	use ssa.Instruction,
	constantUpperBound int64,
) ([]ssa.Instruction, bool) {
	if index == nil || header == nil || trueSuccessor == nil || use == nil || use.Block() == nil ||
		!trueSuccessor.Dominates(use.Block()) {
		return nil, false
	}
	basic, ok := types.Unalias(index.Type()).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 {
		return nil, false
	}
	if basic.Info()&types.IsUnsigned != 0 {
		return nil, true
	}
	if constantIndex, ok := index.(*ssa.Const); ok {
		return nil, constantIndex.Value != nil && constant.Sign(constantIndex.Value) >= 0
	}
	if len(header.Succs) != 2 {
		return nil, false
	}

	var phi *ssa.Phi
	var next *ssa.BinOp
	initial := int64(0)
	indexIsNext := false
	if candidate, ok := index.(*ssa.Phi); ok {
		phi = candidate
	} else if add, ok := index.(*ssa.BinOp); ok && add.Op == token.ADD {
		candidate, increment := coroFrameRetentionPhiAndConstant(add.X, add.Y)
		if candidate == nil || increment != 1 {
			return nil, false
		}
		phi = candidate
		next = add
		initial = -1
		indexIsNext = true
	} else {
		return nil, false
	}
	if phi.Block() != header || len(header.Preds) < 2 || len(phi.Edges) != len(header.Preds) {
		return nil, false
	}
	if !indexIsNext {
		for _, edge := range phi.Edges {
			add, ok := edge.(*ssa.BinOp)
			if !ok || add.Op != token.ADD {
				continue
			}
			edgePhi, increment := coroFrameRetentionPhiAndConstant(add.X, add.Y)
			if edgePhi == phi && increment > 0 {
				next = add
				break
			}
		}
	}
	if next == nil {
		return nil, false
	}
	_, increment := coroFrameRetentionPhiAndConstant(next.X, next.Y)
	if increment <= 0 {
		return nil, false
	}
	if constantUpperBound == 0 && increment != 1 {
		// len(slice) may be MaxInt; a larger step could overflow after the
		// last accepted iteration before the next header comparison.
		return nil, false
	}
	if constantUpperBound > 0 {
		maximum, ok := coroFrameRetentionSignedIntegerMax(basic.Kind())
		if !ok || constantUpperBound-1 > maximum || increment > maximum-(constantUpperBound-1) {
			return nil, false
		}
	}

	initialCount, recursiveCount := 0, 0
	var recursivePredecessors []*ssa.BasicBlock
	for edgeIndex, edge := range phi.Edges {
		predecessor := header.Preds[edgeIndex]
		if predecessor == nil {
			return nil, false
		}
		if value, ok := edge.(*ssa.Const); ok && value.Value != nil {
			integer, exact := constant.Int64Val(value.Value)
			if !exact || integer != initial || header.Dominates(predecessor) {
				return nil, false
			}
			initialCount++
			continue
		}
		if edge != next || !header.Dominates(predecessor) || !trueSuccessor.Dominates(predecessor) {
			return nil, false
		}
		if indexIsNext {
			if next.Block() != header {
				return nil, false
			}
		} else if next.Block() != predecessor {
			return nil, false
		}
		recursiveCount++
		recursivePredecessors = append(recursivePredecessors, predecessor)
	}
	if initialCount != 1 || recursiveCount == 0 {
		return nil, false
	}
	for _, predecessor := range recursivePredecessors {
		if coroFrameRetentionBlockCanReachWithoutCrossing(header.Succs[1], predecessor, header) {
			return nil, false
		}
	}
	evidence := []ssa.Instruction{phi, next}
	return evidence, true
}

func coroFrameRetentionBlockCanReachWithoutCrossing(from, target, stop *ssa.BasicBlock) bool {
	if from == nil || target == nil {
		return false
	}
	seen := make(map[*ssa.BasicBlock]bool)
	queue := []*ssa.BasicBlock{from}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		if block == target {
			return true
		}
		if block == nil || block == stop || seen[block] {
			continue
		}
		seen[block] = true
		queue = append(queue, block.Succs...)
	}
	return false
}

func coroFrameRetentionSignedIntegerMax(kind types.BasicKind) (int64, bool) {
	switch kind {
	case types.Int8:
		return 1<<7 - 1, true
	case types.Int16:
		return 1<<15 - 1, true
	case types.Int32:
		return 1<<31 - 1, true
	case types.Int64:
		return 1<<63 - 1, true
	case types.Int:
		// Every supported Go target has at least a 32-bit int. This deliberately
		// uses the portable lower bound instead of host architecture state.
		return 1<<31 - 1, true
	default:
		return 0, false
	}
}

func coroFrameRetentionPhiAndConstant(left, right ssa.Value) (*ssa.Phi, int64) {
	if phi, ok := left.(*ssa.Phi); ok {
		if value, ok := right.(*ssa.Const); ok && value.Value != nil {
			integer, exact := constant.Int64Val(value.Value)
			if exact {
				return phi, integer
			}
		}
	}
	if phi, ok := right.(*ssa.Phi); ok {
		if value, ok := left.(*ssa.Const); ok && value.Value != nil {
			integer, exact := constant.Int64Val(value.Value)
			if exact {
				return phi, integer
			}
		}
	}
	return nil, 0
}

func newCoroFrameRetentionTrace() coroFrameRetentionTrace {
	return coroFrameRetentionTrace{roots: make(map[ssa.Value]struct{}), evidence: make(map[ssa.Instruction]struct{})}
}

func (t *coroFrameRetentionTrace) addRoot(value ssa.Value) {
	if t != nil && value != nil {
		t.roots[value] = struct{}{}
	}
}

func (t *coroFrameRetentionTrace) addEvidence(instructions ...ssa.Instruction) {
	if t == nil {
		return
	}
	for _, instruction := range instructions {
		if instruction != nil {
			t.evidence[instruction] = struct{}{}
		}
	}
}

func (t *coroFrameRetentionTrace) merge(other coroFrameRetentionTrace) {
	for root := range other.roots {
		t.addRoot(root)
	}
	for evidence := range other.evidence {
		t.addEvidence(evidence)
	}
}

func (b *coroFrameRetentionRootBuilder) sortedValues(values map[ssa.Value]struct{}) []ssa.Value {
	result := b.sortedValueSet(values)
	for _, value := range result {
		if _, ok := b.valueOrder[value]; !ok {
			return nil
		}
	}
	return result
}

func (b *coroFrameRetentionRootBuilder) sortedValueSet(values map[ssa.Value]struct{}) []ssa.Value {
	result := make([]ssa.Value, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return b.valueOrder[result[i]] < b.valueOrder[result[j]] })
	return result
}

func (b *coroFrameRetentionRootBuilder) sortedInstructions(values map[ssa.Instruction]struct{}) []ssa.Instruction {
	result := make([]ssa.Instruction, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return b.instrOrder[result[i]] < b.instrOrder[result[j]] })
	return result
}

func coroFrameRetentionPointerToUintptr(value *ssa.Convert) bool {
	return value != nil && value.X != nil && coroFrameRetentionPointerLike(value.X.Type()) && coroFrameRetentionUintptrLike(value.Type())
}

func coroFrameRetentionUintptrLike(typ types.Type) bool {
	if typ == nil {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Uintptr
}

func coroFrameRetentionIntegerLike(typ types.Type) bool {
	if typ == nil {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsInteger != 0
}

func coroFrameRetentionUnsafePointer(typ types.Type) bool {
	if typ == nil {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.UnsafePointer
}

func coroFrameRetentionSliceLike(typ types.Type) bool {
	if typ == nil {
		return false
	}
	_, ok := types.Unalias(typ).Underlying().(*types.Slice)
	return ok
}

func coroFrameRetentionNilConst(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	if !ok || constant.Value != nil {
		return false
	}
	// x/tools/ssa.Const.IsNil deliberately follows its own nillable helper,
	// which currently omits unsafe.Pointer even though the Go language permits
	// comparing an unsafe.Pointer with nil. Preserve the exact zero-value check
	// and recognize pointer-like constants ourselves so this proof matches Go's
	// source semantics rather than an x/tools implementation detail.
	if constant.Type() != nil {
		if basic, ok := types.Unalias(constant.Type()).Underlying().(*types.Basic); ok &&
			basic.Kind() == types.UntypedNil {
			return true
		}
	}
	return constant.IsNil() || coroFrameRetentionPointerLike(constant.Type())
}

func coroFrameRetentionExactZeroIndex(value ssa.Value) bool {
	return coroFrameRetentionExactInteger(value, 0)
}

func coroFrameRetentionExactInteger(value ssa.Value, want int64) bool {
	constantValue, ok := value.(*ssa.Const)
	if !ok || constantValue.Value == nil {
		return false
	}
	integer, exact := constant.Int64Val(constantValue.Value)
	return exact && integer == want
}

func coroFrameRetentionIntegerAtMost(value ssa.Value, bound int64) bool {
	integer, ok := coroFrameRetentionExactPositiveInteger(value)
	return ok && integer <= bound
}

func coroFrameRetentionExactPositiveInteger(value ssa.Value) (int64, bool) {
	constantValue, ok := value.(*ssa.Const)
	if !ok || constantValue.Value == nil {
		return 0, false
	}
	integer, exact := constant.Int64Val(constantValue.Value)
	return integer, exact && integer > 0
}

func coroFrameRetentionLenZeroComparison(comparison *ssa.BinOp, slice ssa.Value) (*ssa.Call, bool) {
	if comparison == nil {
		return nil, false
	}
	if call := coroFrameRetentionExactLenCall(comparison.X, slice); call != nil && coroFrameRetentionExactZeroIndex(comparison.Y) {
		return call, true
	}
	if call := coroFrameRetentionExactLenCall(comparison.Y, slice); call != nil && coroFrameRetentionExactZeroIndex(comparison.X) {
		return call, false
	}
	return nil, false
}

func coroFrameRetentionExactLenCall(value ssa.Value, operand ssa.Value) *ssa.Call {
	call, ok := value.(*ssa.Call)
	if !ok || call.Common() == nil || len(call.Common().Args) != 1 || call.Common().Args[0] != operand {
		return nil
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "len" {
		return nil
	}
	return call
}

// zeroOnRight describes "len(s) op 0". Slice lengths are non-negative, so
// these are the only zero comparisons that prove strict positivity without a
// range/value analysis.
func coroFrameRetentionPositiveLengthSuccessor(op token.Token, zeroOnRight bool) (int, bool) {
	if zeroOnRight {
		switch op {
		case token.GTR, token.NEQ:
			return 0, true
		case token.EQL, token.LEQ:
			return 1, true
		}
	} else {
		switch op {
		case token.LSS, token.NEQ:
			return 0, true
		case token.EQL, token.GEQ:
			return 1, true
		}
	}
	return 0, false
}

func coroFrameRetentionRootDigest(a *coroPhysicalPureSSAAudit, proof *coroFrameRetentionProof) string {
	if a == nil || a.fn == nil || proof == nil {
		return ""
	}
	builder := newCoroFrameRetentionRootBuilder(a, proof)
	typeKey := structuralEmissionTypeKey
	if a.universe != nil {
		typeKey = a.universe.cachedStrictEmissionTypeKey
	}
	valueID := func(value ssa.Value) string {
		if value == nil {
			return "none"
		}
		if order, ok := builder.valueOrder[value]; ok {
			return "v" + strconv.Itoa(order)
		}
		return "outside"
	}
	instructionID := func(instruction ssa.Instruction) string {
		if instruction == nil {
			return "none"
		}
		if order, ok := builder.instrOrder[instruction]; ok {
			return "i" + strconv.Itoa(order)
		}
		return "outside"
	}
	fields := []string{coroFrameRetentionExactRootProfileV2}
	for _, rootValue := range proof.exactRetainedRoots() {
		root := proof.exactRoots[rootValue]
		fields = append(fields, framedEmissionKey(
			"root", valueID(root.value), strconv.Itoa(int(root.kind)), typeKey(a.typeOf(root.value.Type())),
		))
	}
	managedAllocationKeys := make([]*ssa.Alloc, 0, len(proof.managedHeapAllocations))
	for allocation := range proof.managedHeapAllocations {
		managedAllocationKeys = append(managedAllocationKeys, allocation)
	}
	sort.Slice(managedAllocationKeys, func(i, j int) bool {
		return builder.valueOrder[managedAllocationKeys[i]] < builder.valueOrder[managedAllocationKeys[j]]
	})
	for _, allocation := range managedAllocationKeys {
		fact := proof.managedHeapAllocations[allocation]
		mode := "allocz"
		if fact.zeroSized {
			mode = "module-zero-sentinel"
		}
		fields = append(fields, framedEmissionKey(
			"managed-heap-allocation", valueID(allocation), typeKey(a.typeOf(allocation.Type())),
			mode, fact.helper, string(fact.helperTarget), fact.helperEmission.String(),
		))
	}
	terminalAllocationKeys := make([]*ssa.Alloc, 0, len(proof.terminalResultAllocations))
	for allocation := range proof.terminalResultAllocations {
		terminalAllocationKeys = append(terminalAllocationKeys, allocation)
	}
	sort.Slice(terminalAllocationKeys, func(i, j int) bool {
		return builder.valueOrder[terminalAllocationKeys[i]] < builder.valueOrder[terminalAllocationKeys[j]]
	})
	for _, allocation := range terminalAllocationKeys {
		fields = append(fields, framedEmissionKey(
			"cleanup-terminal-result-allocation", valueID(allocation), typeKey(a.typeOf(allocation.Type())),
		))
	}
	addressKeys := make([]coroFrameRetentionAddressUse, 0, len(proof.stableAddresses))
	for key := range proof.stableAddresses {
		addressKeys = append(addressKeys, key)
	}
	sort.Slice(addressKeys, func(i, j int) bool {
		left, right := builder.instrOrder[addressKeys[i].use], builder.instrOrder[addressKeys[j].use]
		if left != right {
			return left < right
		}
		return builder.valueOrder[addressKeys[i].value] < builder.valueOrder[addressKeys[j].value]
	})
	for _, key := range addressKeys {
		fact := proof.stableAddresses[key]
		nilMode := "guard"
		if fact.nonNil {
			nilMode = "non-nil"
		}
		entry := []string{"address", valueID(key.value), instructionID(key.use), typeKey(a.typeOf(key.value.Type())), nilMode}
		for _, evidence := range fact.evidence {
			entry = append(entry, "evidence="+instructionID(evidence))
		}
		fields = append(fields, framedEmissionKey(entry...))
	}
	uintptrKeys := make([]ssa.Value, 0, len(proof.uintptrValues))
	for value := range proof.uintptrValues {
		uintptrKeys = append(uintptrKeys, value)
	}
	sort.Slice(uintptrKeys, func(i, j int) bool { return builder.valueOrder[uintptrKeys[i]] < builder.valueOrder[uintptrKeys[j]] })
	for _, value := range uintptrKeys {
		entry := []string{"uintptr", valueID(value)}
		for _, root := range proof.uintptrValues[value].roots {
			entry = append(entry, "root="+valueID(root))
		}
		fields = append(fields, framedEmissionKey(entry...))
	}
	callKeys := make([]ssa.CallInstruction, 0, len(proof.callKeepalives))
	for call := range proof.callKeepalives {
		callKeys = append(callKeys, call)
	}
	sort.Slice(callKeys, func(i, j int) bool { return builder.instrOrder[callKeys[i]] < builder.instrOrder[callKeys[j]] })
	for _, call := range callKeys {
		fact := proof.callKeepalives[call]
		entry := []string{"call", instructionID(call), strconv.Itoa(int(fact.kind))}
		for _, root := range fact.roots {
			entry = append(entry, "root="+valueID(root))
		}
		for _, source := range fact.sources {
			entry = append(entry, "source="+valueID(source))
		}
		fields = append(fields, framedEmissionKey(entry...))
	}
	allocationKeys := make([]*ssa.Alloc, 0, len(proof.allocations))
	for allocation := range proof.allocations {
		allocationKeys = append(allocationKeys, allocation)
	}
	sort.Slice(allocationKeys, func(i, j int) bool {
		return builder.valueOrder[allocationKeys[i]] < builder.valueOrder[allocationKeys[j]]
	})
	for _, allocation := range allocationKeys {
		fields = append(fields, framedEmissionKey("park-allocation", valueID(allocation)))
	}
	borrowedAllocationKeys := make([]*ssa.Alloc, 0, len(proof.borrowedAllocations))
	for allocation := range proof.borrowedAllocations {
		borrowedAllocationKeys = append(borrowedAllocationKeys, allocation)
	}
	sort.Slice(borrowedAllocationKeys, func(i, j int) bool {
		return builder.valueOrder[borrowedAllocationKeys[i]] < builder.valueOrder[borrowedAllocationKeys[j]]
	})
	for _, allocation := range borrowedAllocationKeys {
		borrow := proof.borrowedAllocations[allocation]
		fields = append(fields, framedEmissionKey(
			"borrowed-allocation", valueID(allocation), typeKey(a.typeOf(allocation.Type())),
			strconv.FormatUint(uint64(borrow.FunctionsVisited), 10),
			strconv.FormatUint(uint64(borrow.ParametersProven), 10),
		))
	}
	sum := sha256.Sum256([]byte(framedEmissionKey(fields...)))
	return hex.EncodeToString(sum[:])
}

func coroFrameRetentionPointerLike(typ types.Type) bool {
	underlying := types.Unalias(typ).Underlying()
	if _, ok := underlying.(*types.Pointer); ok {
		return true
	}
	basic, ok := underlying.(*types.Basic)
	return ok && basic.Kind() == types.UnsafePointer
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
