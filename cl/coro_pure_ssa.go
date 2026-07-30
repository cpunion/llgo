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
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroPhysicalPureSSAAudit is the deliberately small proof boundary for SSA
// operations that remain ordinary LLVM values across a coro suspend. It is not
// a general instruction allowlist. Every accepted case below mirrors the
// corresponding compileInstr/compileInstrOrValue and LLSSA Builder lowering.
// An operation that can perform dynamic dispatch or introduce a new panic edge
// is rejected here. A hidden runtime helper is accepted only when the immutable
// whole-program plan binds that exact logical helper to either one demanded
// non-unwind NoSuspend plain body, or an explicitly capability-gated
// structured-outcome coroutine.
//
// PhysicalABIV1's current frame allocator profiles are conservative or
// non-collecting. Pointer/interface/slice values may therefore live in the LLVM
// coroutine frame, but this slice does not claim a precise frame root map or a
// moving-GC write barrier. A future precise collector must add those two ABI
// capabilities before enabling the same local-frame operations for that
// profile.
type coroPhysicalPureSSAAudit struct {
	universe                 *EmissionUniverse
	plan                     *coro.SSAPlan
	ctx                      *context
	fn                       *ssa.Function
	reachableBlocks          map[*ssa.BasicBlock]bool
	frameRetentionABI        string
	frameRetentionBuilt      bool
	frameRetentionProofCache *coroFrameRetentionProof
	libraryForeign           map[*ssa.Function]coro.LibraryEffectForeignCallable
	// allowImplicitNilFault is enabled only by PhysicalABIV1 preflight after
	// the target-wide explicit-status panic identity has been selected. It
	// never weakens transport/root validation; it lets implicit nil and bounds
	// faults rely on compiler-owned terminal edges instead of stackful helpers.
	allowImplicitNilFault bool
	// Recover is an independent structured capability even though the first
	// explicit-status identity enables both gates together.
	allowExplicitRecover bool
}

func (a *coroPhysicalPureSSAAudit) foreignCallAuthority() coroStaticForeignCallAuthority {
	if a == nil {
		return coroStaticForeignCallAuthority{}
	}
	return coroStaticForeignCallAuthority{
		plan: a.plan, universe: a.universe, libraryForeign: a.libraryForeign,
	}
}

// coroInterfaceDerefConsumer projects the active source type exactly once at
// the physical-proof boundary, then delegates structural consumer recognition
// to the target-independent helper scanner.
func coroInterfaceDerefConsumer(
	ctx *context,
	deref *ssa.UnOp,
) (*ssa.MakeInterface, coroInterfaceDerefFusion) {
	if ctx == nil || ctx.prog == nil || deref == nil {
		return nil, coroInterfaceDerefNotFused
	}
	physical := ctx.prog.PhysicalType(ctx.patchType(deref.Type()), llssa.InGo)
	return coroInterfaceDerefConsumerForPhysicalType(
		deref, physical, ctx.prog.PointerSize(),
	)
}

func newCoroPhysicalPureSSAAudit(
	universe *EmissionUniverse,
	plan *coro.SSAPlan,
	fn *ssa.Function,
	frameRetentionABI string,
) (*coroPhysicalPureSSAAudit, error) {
	return newCoroPhysicalPureSSAAuditForOwner(universe, plan, fn, nil, frameRetentionABI)
}

func newCoroPhysicalPureSSAAuditForOwner(
	universe *EmissionUniverse,
	plan *coro.SSAPlan,
	fn *ssa.Function,
	owner *preparedEmissionPackage,
	frameRetentionABI string,
) (*coroPhysicalPureSSAAudit, error) {
	audit := &coroPhysicalPureSSAAudit{
		universe:          universe,
		plan:              plan,
		fn:                fn,
		frameRetentionABI: frameRetentionABI,
		reachableBlocks:   coroPhysicalConstantReachableBlocks(fn),
	}
	if universe == nil {
		// Structural unit tests may call the validator directly. Active
		// Compilation paths always supply their prepared emission universe.
		return audit, nil
	}
	if fn == nil {
		return nil, fmt.Errorf("nil function")
	}
	if canonical := universe.canonicalAlias(fn); canonical == nil || canonical != fn {
		return nil, fmt.Errorf("function %q is not the exact canonical emission owner", fn.Name())
	}
	if _, frozen := universe.required[fn]; !frozen {
		return nil, fmt.Errorf("function %q is outside the prepared emission universe", fn.Name())
	}
	if owner == nil {
		owner = universe.ownerOf(fn)
	}
	if owner == nil {
		return nil, fmt.Errorf("function %q has no exact emission owner", fn.Name())
	}
	owned := false
	for _, candidate := range universe.sortedUseOwners(fn) {
		owned = owned || candidate == owner
	}
	if !owned {
		return nil, fmt.Errorf("function %q is not materialized for emission owner %q", fn.Name(), owner.identity)
	}
	ctx, err := universe.functionABIContext(fn, owner)
	if err != nil {
		return nil, err
	}
	audit.ctx = ctx
	return audit, nil
}

func (a *coroPhysicalPureSSAAudit) validate(instr ssa.Instruction) (handled bool, reason string) {
	if instr != nil && a != nil && a.ctx != nil {
		if _, unevaluated := a.ctx.unevaluatedSSA[instr]; unevaluated {
			return true, ""
		}
	}
	if instr != nil && a != nil && len(a.reachableBlocks) != 0 && !a.reachableBlocks[instr.Block()] {
		return true, ""
	}
	switch instr := instr.(type) {
	case *ssa.Alloc:
		return true, a.validateAlloc(instr)
	case *ssa.FieldAddr:
		return true, a.validateFieldAddr(instr)
	case *ssa.IndexAddr:
		return true, a.validateIndexAddr(instr)
	case *ssa.Index:
		return true, a.validateIndex(instr)
	case *ssa.Slice:
		return true, a.validateSlice(instr)
	case *ssa.SliceToArrayPointer:
		return true, a.validateSliceToArrayPointer(instr)
	case *ssa.Extract:
		return true, a.validateExtract(instr)
	case *ssa.Field:
		return true, a.validateField(instr)
	case *ssa.MakeInterface:
		return true, a.validateMakeInterface(instr)
	case *ssa.ChangeInterface:
		return true, a.validateChangeInterface(instr)
	case *ssa.TypeAssert:
		return true, a.validateTypeAssert(instr)
	case *ssa.MakeSlice:
		return true, a.validateMakeSlice(instr)
	case *ssa.MakeMap:
		return true, a.validateMakeMap(instr)
	case *ssa.MakeChan:
		return true, a.validateMakeChan(instr)
	case *ssa.Lookup:
		return true, a.validateLookup(instr)
	case *ssa.MapUpdate:
		return true, a.validateMapUpdate(instr)
	case *ssa.Range:
		return true, a.validateRange(instr)
	case *ssa.Next:
		return true, a.validateNext(instr)
	case *ssa.MakeClosure:
		return true, a.validateMakeClosure(instr)
	case *ssa.ChangeType:
		return true, a.validateChangeType(instr)
	case *ssa.Convert:
		return true, a.validateConvert(instr)
	case *ssa.Phi:
		return true, a.validatePhi(instr)
	case *ssa.BinOp:
		return true, a.validateBinOp(instr)
	case *ssa.UnOp:
		if instr.Op == token.MUL || instr.Op == token.SUB || instr.Op == token.XOR || instr.Op == token.NOT {
			return true, a.validateUnOp(instr)
		}
	case *ssa.Store:
		return true, a.validateStore(instr)
	case *ssa.Call:
		if _, builtin := instr.Call.Value.(*ssa.Builtin); builtin {
			return true, a.validateBuiltin(instr)
		}
	}
	return false, ""
}

func (a *coroPhysicalPureSSAAudit) validateMakeClosure(closure *ssa.MakeClosure) string {
	if closure == nil {
		return "incomplete closure construction"
	}
	target, ok := closure.Fn.(*ssa.Function)
	if !ok || target == nil || a.plan == nil {
		return "closure has no exact function target"
	}
	if len(closure.Bindings) != len(target.FreeVars) {
		return fmt.Sprintf("closure bindings=%d do not match target free variables=%d", len(closure.Bindings), len(target.FreeVars))
	}
	for index, binding := range closure.Bindings {
		if binding == nil || target.FreeVars[index] == nil || !types.Identical(binding.Type(), target.FreeVars[index].Type()) {
			return fmt.Sprintf("closure binding %d does not match its target free variable", index)
		}
	}
	if a.universe != nil {
		resolved, frozen := a.universe.Resolve(target)
		if !frozen || resolved == nil {
			return "closure target is outside the frozen emission universe"
		}
		target = resolved
	}
	targetID, ok := a.plan.FunctionID(target)
	if !ok {
		return "closure target has no FunctionID"
	}
	value, ok := a.plan.ValuePlan(closure)
	if !ok || len(value.Funcs) != 1 || len(value.Funcs[0].Path) != 0 {
		return "closure has no exact scalar callable representation"
	}
	leaf := value.Funcs[0]
	// MakeClosure itself is an exact non-nil producer even when its value later
	// joins a nil or another callable through Phi/storage flow. ValuePlan carries
	// the representation required by that complete flow, so its target and nil
	// sets may be conservative here. The source SSA operand still fixes this
	// producer's target; require that the plan contains it, then use only the
	// frozen representation below.
	targetPresent := false
	for _, candidate := range leaf.Targets {
		if candidate == targetID {
			targetPresent = true
			break
		}
	}
	if !targetPresent {
		return "closure exact target is absent from its scalar callable representation"
	}
	if leaf.Transport != coro.ManagedTransport {
		return fmt.Sprintf("closure has non-managed callable transport %s", leaf.Transport)
	}
	if len(target.FreeVars) == 0 && (leaf.Rep == coro.DirectPlain || leaf.Rep == coro.DirectCoro) {
		return a.requireNoRuntimeHelpers(closure)
	}
	if len(target.FreeVars) != 0 && leaf.Rep == coro.DirectPlain {
		targetPlan, planned := a.plan.FunctionPlan(target)
		if !planned || targetPlan.ID != targetID || targetPlan.External != coro.Defined ||
			targetPlan.Emission != coro.EmitPlain || targetPlan.Primary != coro.PrimaryPlain {
			return "captured direct plain target has no canonical managed plain body"
		}
		// An exact, non-escaping plain closure remains LLGo's ordinary
		// {code,environment} value. Value-flow upgrades it to Dispatch whenever
		// it can reach a dynamic consumer, so retaining DirectPlain here means
		// only the environment allocation is needed in the physical body.
		return a.requireFrozenCoroSafeRuntimeHelpers(closure, "AllocU")
	}
	if len(target.FreeVars) != 0 && leaf.Rep == coro.DirectCoro {
		targetPlan, planned := a.plan.FunctionPlan(target)
		if !planned || targetPlan.ID != targetID || targetPlan.External != coro.Defined ||
			targetPlan.Emission != coro.EmitCoroutine || targetPlan.Primary != coro.PrimaryCoroutine ||
			(targetPlan.FuncRep != coro.DirectCoro && targetPlan.FuncRep != coro.Dispatch) {
			return "captured direct coroutine target has no canonical physical context plan"
		}
		return a.requireFrozenCoroSafeRuntimeHelpers(closure, "AllocU")
	}
	if leaf.Rep != coro.Dispatch {
		return "captured or descriptor-backed closure has no exact Dispatch representation"
	}
	targetPlan, planned := a.plan.FunctionPlan(target)
	if !planned || targetPlan.ID != targetID {
		return "descriptor-backed closure target has no canonical function plan"
	}
	if err := validateCoroDynamicDispatchTarget(target, targetPlan, a.universe); err != nil {
		return "descriptor-backed closure target: " + err.Error()
	}
	if len(target.FreeVars) != 0 {
		return a.requireFrozenCoroSafeRuntimeHelpers(closure, "AllocU")
	}
	return a.requireNoRuntimeHelpers(closure)
}

func coroPhysicalConstantReachableBlocks(fn *ssa.Function) map[*ssa.BasicBlock]bool {
	reachable := make(map[*ssa.BasicBlock]bool)
	if fn == nil || len(fn.Blocks) == 0 || fn.Blocks[0] == nil {
		return reachable
	}
	queue := []*ssa.BasicBlock{fn.Blocks[0]}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		if block == nil || reachable[block] {
			continue
		}
		reachable[block] = true
		successors := block.Succs
		if len(block.Instrs) != 0 && len(successors) == 2 {
			if branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If); ok {
				if condition, ok := branch.Cond.(*ssa.Const); ok && condition.Value != nil && condition.Value.Kind() == constant.Bool {
					if constant.BoolVal(condition.Value) {
						successors = successors[:1]
					} else {
						successors = successors[1:]
					}
				}
			}
		}
		for _, successor := range successors {
			if successor != nil && !reachable[successor] {
				queue = append(queue, successor)
			}
		}
	}
	return reachable
}

func (a *coroPhysicalPureSSAAudit) validateAlloc(alloc *ssa.Alloc) string {
	if alloc == nil {
		return "heap allocation requires managed allocation and coroutine GC-root lowering"
	}
	if a.ctx != nil && isEmissionVargsAlloc(a.ctx, alloc) {
		// The ordinary compiler materializes this synthetic array only in its
		// vargs side table. Individual stores evaluate their unboxed operands and
		// the variadic call consumes those values directly; no address or backing
		// allocation crosses a suspension boundary.
		return ""
	}
	if a.ctx != nil && a.ctx.skipSyntheticMakeSliceAlloc(alloc) {
		// x/tools represents make([]T, dynamicLen, constantCap) as a synthetic
		// heap Alloc consumed by one Slice. compileValue emits no code for this
		// Alloc; the paired Slice owns the complete runtime.MakeSlice operation
		// and its coroutine child-await. Keep the two proof obligations separate:
		// this frontend-elided node must remain helper-free.
		return a.requireNoRuntimeHelpers(alloc)
	}
	if a.frameRetainsManagedHeapAllocation(alloc) {
		// The exact capability already proved AllocZ and the suspended-frame
		// pointer root for either an escape or an oversized local promotion.
		return ""
	}
	if alloc.Heap {
		if a.frameRetainsAllocation(alloc) {
			// The complete address-use proof changes this exact lowering from
			// runtime.AllocZ to an LLVM alloca in the current coroutine frame. Do
			// not consult the ordinary Heap helper-demand table for that allocation.
			return ""
		}
		_, reason := a.managedHeapAllocationCapability(alloc)
		if reason == "" {
			reason = "allocation is absent from the immutable managed-heap root proof"
		}
		return "heap allocation requires managed allocation and coroutine GC-root lowering: " + reason
	}
	pointer, ok := types.Unalias(a.typeOf(alloc.Type())).Underlying().(*types.Pointer)
	if !ok {
		return "local allocation does not have a pointer type"
	}
	if err := validateCoroPhysicalSSAValueType(pointer.Elem()); err != nil {
		return "local allocation has unsupported value type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(alloc)
}

// managedHeapAllocationCapability proves one exact managed Alloc without
// changing its final LLSSA storage identity. Non-zero objects must lower only
// through the owner-scoped frozen AllocZ edge. Zero-sized objects use LLGo's
// module sentinel and therefore must have no hidden allocator helper at all.
// The proof is intentionally unavailable under the legacy shadow-stack mode:
// a precise or moving collector needs typed coroutine-frame maps and barriers,
// neither of which this capability claims.
func (a *coroPhysicalPureSSAAudit) managedHeapAllocationCapability(alloc *ssa.Alloc) (coroFrameRetentionManagedHeapAllocation, string) {
	fact := coroFrameRetentionManagedHeapAllocation{}
	if a == nil || a.ctx == nil || a.universe == nil || a.plan == nil || a.fn == nil {
		return fact, "requires an owned body, complete emission universe, and whole-build plan"
	}
	if emitShadowStackInstrumentation {
		return fact, "requires the non-moving conservative-or-no-GC coroutine frame root profile"
	}
	if !a.universe.CompleteRuntimeABI() {
		return fact, "requires a complete frozen runtime ABI"
	}
	if alloc == nil || alloc.Parent() != a.fn ||
		!coroAllocationUsesManagedStorage(a.ctx, alloc) {
		return fact, "does not use one exact owned managed allocation"
	}
	if a.ctx.skipSyntheticMakeSliceAlloc(alloc) || isEmissionVargsAlloc(a.ctx, alloc) {
		return fact, "synthetic slice/varargs storage has no standalone managed-allocation capability"
	}
	pointer, ok := types.Unalias(a.typeOf(alloc.Type())).Underlying().(*types.Pointer)
	if !ok {
		return fact, "allocation result does not have pointer type"
	}
	if err := validateCoroPhysicalSSAValueType(pointer.Elem()); err != nil {
		return fact, "allocation element has unsupported physical type: " + err.Error()
	}
	physical := a.ctx.type_(pointer.Elem(), llssa.InGo)
	helpers, helperReason := a.plannedRuntimeHelpers(alloc)
	if helperReason != "" {
		return fact, helperReason
	}
	if a.ctx.prog.SizeOf(physical) == 0 {
		if len(helpers) != 0 {
			return fact, "zero-sized module-sentinel allocation unexpectedly lowers through " + strings.Join(helpers, ", ")
		}
		fact.zeroSized = true
		return fact, ""
	}
	if len(helpers) != 1 || helpers[0] != "AllocZ" {
		return fact, "non-zero allocation does not lower through exactly one AllocZ helper"
	}
	if reason := a.requireFrozenCoroSafeRuntimeHelpers(alloc, "AllocZ"); reason != "" {
		return fact, reason
	}
	target, planned := a.plan.ResolveLoweredCall(a.fn, "AllocZ")
	if !planned || target == nil {
		return fact, "AllocZ lacks one exact owner-scoped lowered-call target"
	}
	targetPlan, planned := a.plan.FunctionPlan(target)
	if !planned || targetPlan.ID == "" {
		return fact, "AllocZ target lacks one canonical function plan"
	}
	fact.helper = "AllocZ"
	fact.helperTarget = targetPlan.ID
	fact.helperEmission = targetPlan.Emission
	return fact, ""
}

func (a *coroPhysicalPureSSAAudit) validateFieldAddr(field *ssa.FieldAddr) string {
	if field == nil {
		return "nil field address"
	}
	if _, reason := a.stableAddressAt(field, field, make(map[ssa.Value]bool)); reason != "" {
		return reason
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(field.Type())); err != nil {
		return "field address has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpersExcept(field, "AssertNilDeref")
}

func (a *coroPhysicalPureSSAAudit) fieldAddrRequiresImplicitNilFault(field *ssa.FieldAddr) (bool, string) {
	if a == nil || field == nil {
		return false, ""
	}
	helpers, reason := a.plannedRuntimeHelpers(field)
	if reason != "" {
		return false, reason
	}
	for _, helper := range helpers {
		if helper == "AssertNilDeref" {
			// The frozen logical SitePlan is the lowering input. A stronger frame
			// proof may later justify a dedicated proved-non-null recipe, but it
			// cannot silently turn an expected helper into ordinary codegen.
			return true, ""
		}
	}
	// A value field load is represented as FieldAddr followed by UnOp. Its
	// logical helper belongs to the load, while the physical FieldAddr recipe
	// must still own the earlier base-pointer guard so no GEP is formed from a
	// nil address. Preserve that frame-proof projection in addition to the
	// direct helper-owned address-of case above.
	if ssaAddressValueProvenNonNilAt(field.X, field) {
		return false, ""
	}
	proof := a.currentFrameRetentionProof()
	return proof != nil && proof.requiresImplicitNilFault(field, field), ""
}

func (a *coroPhysicalPureSSAAudit) derefRequiresImplicitNilFault(deref *ssa.UnOp) bool {
	if a == nil || deref == nil || deref.Op != token.MUL {
		return false
	}
	if ssaValueProvenNonNilAt(deref.X, deref) {
		return false
	}
	if _, _, synthetic := coroSliceToArrayValueDeref(deref, a.typeOf); synthetic {
		// The conversion owns the N>0 length fault. N==0 array-value
		// conversion is the zero value and must remain legal for a nil slice.
		return false
	}
	if _, fusion := coroInterfaceDerefConsumer(a.ctx, deref); fusion == coroInterfaceDerefLarge {
		// MakeInterfaceFromPtr is the exact physical owner of the large
		// operation's nil check and typed copy; the UnOp intentionally emits no
		// load and therefore must remain an ordinary producer. Zero-sized
		// dereferences still own their source-language nil edge.
		return false
	}
	proof := a.currentFrameRetentionProof()
	if proof == nil {
		return false
	}
	if owns, _ := a.derefAddressProducerOwnsImplicitFault(deref); owns {
		return false
	}
	return proof.requiresImplicitNilFault(deref.X, deref)
}

// derefAddressProducerOwnsImplicitFault identifies the address recipes that
// establish every condition required by a following load. The consumer may
// then elide its legacy nil helper, but it must still carry a frozen deref
// recipe so that ownership is explicit and observable during codegen.
func (a *coroPhysicalPureSSAAudit) derefAddressProducerOwnsImplicitFault(
	deref *ssa.UnOp,
) (bool, string) {
	if a == nil || deref == nil || deref.Op != token.MUL {
		return false, ""
	}
	switch address := deref.X.(type) {
	case *ssa.FieldAddr:
		owns, reason := a.fieldAddrRequiresImplicitNilFault(address)
		if reason != "" {
			return false, reason
		}
		return owns, ""
	case *ssa.IndexAddr:
		if a.ctx != nil && emissionIsVargsAlloc(a.ctx, address.X) {
			return false, ""
		}
		// IndexAddr owns its bounds check and, for *array containers, its nil
		// guard before it publishes the element address.
		return true, ""
	default:
		return false, ""
	}
}

// storeRequiresImplicitNilFault selects an explicit-status nil edge only when
// the Store itself owns the final raw pointer dereference. FieldAddr and
// IndexAddr producers already publish their checked address on the normal
// edge, so guarding them again would duplicate the source fault.
func (a *coroPhysicalPureSSAAudit) storeRequiresImplicitNilFault(store *ssa.Store) (bool, string) {
	if a == nil || store == nil || store.Addr == nil {
		return false, ""
	}
	switch address := store.Addr.(type) {
	case *ssa.FieldAddr:
		owns, reason := a.fieldAddrRequiresImplicitNilFault(address)
		if reason != "" {
			return false, reason
		}
		if owns {
			return false, ""
		}
	case *ssa.IndexAddr:
		if a.ctx != nil && emissionIsVargsAlloc(a.ctx, address.X) {
			return false, ""
		}
		// IndexAddr owns all bounds checks and the nullable *array guard.
		return false, ""
	}
	if ssaAddressValueProvenNonNilAt(store.Addr, store) {
		return false, ""
	}
	proof := a.currentFrameRetentionProof()
	return proof != nil && proof.requiresImplicitNilFault(store.Addr, store), ""
}

func (a *coroPhysicalPureSSAAudit) validateIndexAddr(index *ssa.IndexAddr) string {
	if index == nil {
		return "nil index address"
	}
	if a.ctx != nil && emissionIsVargsAlloc(a.ctx, index.X) {
		return ""
	}
	if _, reason := a.stableAddressAt(index, index, make(map[ssa.Value]bool)); reason != "" {
		detail := ""
		if add, ok := index.Index.(*ssa.BinOp); ok {
			detail = fmt.Sprintf(", operands=(%T %s, %T %s)", add.X, add.X, add.Y, add.Y)
			if phi, ok := add.X.(*ssa.Phi); ok {
				detail += fmt.Sprintf(", phi-edges=%v", phi.Edges)
			}
		}
		return fmt.Sprintf("%s (base=%T %s, index=%T %s%s)", reason, index.X, index.X, index.Index, index.Index, detail)
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(index.Type())); err != nil {
		return "index address has unsupported type: " + err.Error()
	}
	if a.allowImplicitNilFault {
		proof := a.currentFrameRetentionProof()
		if proof != nil && proof.provesGuardableStableAddress(index, index) {
			// ExplicitStatus codegen replaces CheckIndexRange (and a possible
			// *array nil helper) with compiler-owned terminal branches before the
			// unchecked address is formed.
			return a.requireOnlyCompilerElidedRuntimeHelpers(index, "CheckIndexRange", "AssertNilDeref")
		}
	}
	return a.requireNoRuntimeHelpersExcept(index, "CheckIndexRange", "AssertNilDeref")
}

func (a *coroPhysicalPureSSAAudit) validateIndex(index *ssa.Index) string {
	if index == nil || index.X == nil || index.Index == nil {
		return "incomplete index operation"
	}
	if a.allowImplicitNilFault {
		switch container := types.Unalias(a.typeOf(index.X.Type())).Underlying().(type) {
		case *types.Basic:
			if !coroPhysicalStringBasic(container) {
				return "index has unsupported basic container type"
			}
		case *types.Array, *types.Slice:
		case *types.Pointer:
			if _, ok := types.Unalias(container.Elem()).Underlying().(*types.Array); !ok {
				return "index pointer base is not a fixed array"
			}
		default:
			return fmt.Sprintf("index has unsupported container type %T", container)
		}
		if err := validateCoroPhysicalSSAValueType(a.typeOf(index.Type())); err != nil {
			return "index has unsupported result type: " + err.Error()
		}
		// ExplicitStatus codegen consumes these logical helper edges by emitting
		// a terminal bounds branch (and, for *array, a terminal nil branch)
		// before an unchecked load.
		if a.ctx != nil && emissionIndexNeedsManagedArrayTemporary(a.ctx, index) {
			return a.requireCompilerElidedAndDirectNoSuspendRuntimeHelper(
				index, "AllocZ", "CheckIndexRange", "AssertNilDeref",
			)
		}
		return a.requireOnlyCompilerElidedRuntimeHelpers(index, "CheckIndexRange", "AssertNilDeref")
	}
	array, ok := types.Unalias(a.typeOf(index.X.Type())).Underlying().(*types.Array)
	if !ok || !coroConstantIndexInBounds(index.Index, array.Len()) {
		return "index may panic; pure coroutine indexing requires a compile-time in-range fixed-array index"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(index.Type())); err != nil {
		return "array index has unsupported result type: " + err.Error()
	}
	if a.ctx != nil && emissionIndexNeedsManagedArrayTemporary(a.ctx, index) {
		return a.requireCompilerElidedAndDirectNoSuspendRuntimeHelper(index, "AllocZ")
	}
	return a.requireNoRuntimeHelpers(index)
}

func coroPhysicalStringBasic(basic *types.Basic) bool {
	return basic != nil && (basic.Kind() == types.String || basic.Kind() == types.UntypedString)
}

func (a *coroPhysicalPureSSAAudit) validateSlice(slice *ssa.Slice) string {
	if slice == nil || slice.X == nil || slice.Type() == nil {
		return "incomplete slice operation"
	}
	if a.ctx != nil && emissionIsVargsAlloc(a.ctx, slice.X) {
		return ""
	}
	if a.ctx != nil {
		if _, synthetic := a.ctx.syntheticMakeSliceCap(slice); synthetic {
			if _, ok := types.Unalias(a.typeOf(slice.Type())).Underlying().(*types.Slice); !ok {
				return "synthetic MakeSlice result is not a slice"
			}
			length, ok := types.Unalias(a.typeOf(slice.High.Type())).Underlying().(*types.Basic)
			if !ok || length.Info()&types.IsInteger == 0 {
				return "synthetic MakeSlice length is not an integer"
			}
			if err := validateCoroPhysicalSSAValueType(a.typeOf(slice.Type())); err != nil {
				return "synthetic MakeSlice result has unsupported type: " + err.Error()
			}
			return a.requireFrozenOutcomeRuntimeHelper(slice, "MakeSlice")
		}
	}

	baseType := a.typeOf(slice.X.Type())
	resultType := a.typeOf(slice.Type())
	var helper string
	switch base := types.Unalias(baseType).Underlying().(type) {
	case *types.Basic:
		if !coroPhysicalStringBasic(base) || slice.Max != nil {
			return "slice basic base must be a two-index string"
		}
		result, ok := types.Unalias(resultType).Underlying().(*types.Basic)
		if !ok || result.Kind() != types.String ||
			(base.Kind() == types.String && !types.Identical(baseType, resultType)) ||
			(base.Kind() == types.UntypedString && !types.Identical(resultType, types.Typ[types.String])) {
			return "string slice result does not preserve its source type"
		}
		helper = "StringSlice2"
	case *types.Slice:
		if !types.Identical(baseType, resultType) {
			return "slice expression result does not preserve its source slice type"
		}
		if slice.Max == nil {
			helper = "NewSlice2"
		} else {
			helper = "NewSlice3Bounds"
		}
	case *types.Pointer:
		array, ok := types.Unalias(base.Elem()).Underlying().(*types.Array)
		if !ok {
			return "slice pointer base is not a fixed array"
		}
		result, ok := types.Unalias(resultType).Underlying().(*types.Slice)
		if !ok || !types.Identical(a.typeOf(array.Elem()), a.typeOf(result.Elem())) {
			return "pointer-to-array slice result has a different element type"
		}
		if _, reason := a.stableAddressAt(slice.X, slice, make(map[ssa.Value]bool)); reason != "" {
			return "slice base: " + reason
		}
		if slice.Low == nil && slice.High == nil && slice.Max == nil {
			helper = ""
		} else if slice.Max == nil {
			helper = "NewSlice2"
		} else {
			helper = "NewSlice3Bounds"
		}
	default:
		return fmt.Sprintf("slice has unsupported base type %T", base)
	}
	if slice.Max != nil && slice.High == nil {
		return "three-index slice requires explicit high and max bounds"
	}
	for name, bound := range map[string]ssa.Value{
		"low": slice.Low, "high": slice.High, "max": slice.Max,
	} {
		if bound == nil {
			continue
		}
		basic, ok := types.Unalias(a.typeOf(bound.Type())).Underlying().(*types.Basic)
		if !ok || basic.Info()&types.IsInteger == 0 {
			return "slice " + name + " bound is not an integer"
		}
		if err := validateCoroPhysicalSSAValueType(a.typeOf(bound.Type())); err != nil {
			return "slice " + name + " bound has unsupported type: " + err.Error()
		}
	}
	physicalBaseType := baseType
	if base, ok := types.Unalias(baseType).Underlying().(*types.Basic); ok && base.Kind() == types.UntypedString {
		// SSA retains the untyped kind on a string constant even when a dynamic
		// slice defaults that operand to the concrete string representation.
		// compileValue applies the same types.Default conversion before emission.
		physicalBaseType = types.Typ[types.String]
	}
	for _, typ := range []types.Type{physicalBaseType, resultType} {
		if err := validateCoroPhysicalSSAValueType(typ); err != nil {
			return "slice has unsupported physical value type: " + err.Error()
		}
	}
	if emissionBoundsChecksDisabled(a.ctx) {
		// -B removes only the range predicates. Slicing a nullable *[N]T
		// still performs an implicit dereference, which the frozen physical
		// Slice recipe replaces with its structured nil-fault branch.
		if _, pointer := types.Unalias(baseType).Underlying().(*types.Pointer); pointer &&
			emissionArrayPointerNeedsNilCheck(slice.X, slice) {
			return a.requireOnlyCompilerElidedRuntimeHelpers(slice, "AssertNilDeref")
		}
		return a.requireOnlyCompilerElidedRuntimeHelpers(slice)
	}
	if !a.allowImplicitNilFault {
		if slice.Low != nil || slice.High != nil || slice.Max != nil {
			return "slice bounds require the explicit-status panic ABI"
		}
		if _, pointer := types.Unalias(baseType).Underlying().(*types.Pointer); !pointer {
			return "dynamic slice bounds require the explicit-status panic ABI"
		}
		return a.requireNoRuntimeHelpers(slice)
	}
	if helper == "" {
		return a.requireOnlyCompilerElidedRuntimeHelpers(slice)
	}
	if err := validateCoroPhysicalSSAValueType(resultType); err != nil {
		return "slice view has unsupported type: " + err.Error()
	}
	// ExplicitStatus codegen owns the bounds predicate and constructs the
	// aggregate only in the normal continuation; the logical helper remains in
	// the frozen inventory solely for effect/outcome propagation.
	return a.requireOnlyCompilerElidedRuntimeHelpers(slice, helper)
}

func (a *coroPhysicalPureSSAAudit) validateSliceToArrayPointer(conversion *ssa.SliceToArrayPointer) string {
	if conversion == nil || conversion.X == nil || conversion.Type() == nil {
		return "incomplete slice-to-array-pointer conversion"
	}
	source, result := a.typeOf(conversion.X.Type()), a.typeOf(conversion.Type())
	array, reason := coroSliceToArrayPointerShape(source, result)
	if reason != "" {
		return "invalid slice-to-array-pointer conversion: " + reason
	}
	for _, typ := range []types.Type{source, result} {
		if err := validateCoroPhysicalSSAValueType(typ); err != nil {
			return "slice-to-array-pointer conversion has unsupported physical type: " + err.Error()
		}
	}
	if array.Len() == 0 {
		// This is a pure data-word projection. It must preserve nil rather than
		// manufacture a non-nil sentinel, and has no PanicSliceConvert edge.
		return a.requireOnlyCompilerElidedRuntimeHelpers(conversion)
	}
	if !a.allowImplicitNilFault {
		return "slice-to-array-pointer length fault requires the explicit-status panic ABI"
	}
	return a.requireOnlyCompilerElidedRuntimeHelpers(conversion, "PanicSliceConvert")
}

func (a *coroPhysicalPureSSAAudit) validateExtract(extract *ssa.Extract) string {
	if extract == nil || extract.Tuple == nil {
		return "incomplete tuple extract"
	}
	tuple, ok := types.Unalias(a.typeOf(extract.Tuple.Type())).Underlying().(*types.Tuple)
	if !ok || extract.Index < 0 || extract.Index >= tuple.Len() {
		return "tuple extract index is outside its frozen aggregate shape"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(extract.Type())); err != nil {
		return "tuple extract has unsupported result type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(extract)
}

func (a *coroPhysicalPureSSAAudit) validateField(field *ssa.Field) string {
	if field == nil || field.X == nil {
		return "incomplete aggregate field extraction"
	}
	structure, ok := types.Unalias(a.typeOf(field.X.Type())).Underlying().(*types.Struct)
	if !ok || field.Field < 0 || field.Field >= structure.NumFields() {
		return "aggregate field index is outside its frozen struct shape"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(field.Type())); err != nil {
		return "aggregate field has unsupported result type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(field)
}

func (a *coroPhysicalPureSSAAudit) validateMakeInterface(box *ssa.MakeInterface) string {
	if box == nil || box.X == nil {
		return "incomplete interface construction"
	}
	target, ok := types.Unalias(a.typeOf(box.Type())).Underlying().(*types.Interface)
	if !ok {
		return "MakeInterface target is not an interface"
	}
	target.Complete()
	source := a.typeOf(box.X.Type())
	emitsABIType := true
	if a.universe != nil {
		emitsABIType = a.universe.makeInterfaceEmitsABIType(box, a.ctx)
	}
	if coroPhysicalTypeContainsFunctionValue(source, make(map[types.Type]bool)) {
		if !emitsABIType {
			return a.validateCompilerElidedFunctionInterface(box)
		}
		if err := validateCoroCallableTransportValue(a.plan, a.fn, box.X, a.universe); err != nil {
			return "function-valued interface payload: " + err.Error()
		}
	}
	if err := validateCoroPhysicalSSAValueType(source); err != nil {
		return "interface payload has unsupported type: " + err.Error()
	}
	if !emitsABIType {
		// Varargs and compiler ABI inspection sites consume the concrete operand
		// directly and emit no interface helper or physical interface value.
		return ""
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(box.Type())); err != nil {
		return "interface result has unsupported type: " + err.Error()
	}
	if a == nil || a.ctx == nil {
		return "interface construction requires a frozen emission context"
	}

	// Mirror the complete LLSSA MakeInterface recipe independently of the
	// frozen helper inventory. Integer and aggregate payloads need stable
	// backing storage, non-empty interfaces additionally need an itab, and the
	// large/zero-sized dereference recipe owns its explicit nil check and typed
	// copy. Every emitted call is then checked against the owner-scoped plan by
	// the same structured helper gate used for maps and future composite
	// lowerings. This admits ordinary `return errno` error paths without
	// granting a symbol-name exception to syscall or to error itself.
	physical := a.ctx.type_(box.X.Type(), llssa.InGo)
	needsAlloc := !emissionDirectIfaceType(physical.RawType())
	needsNilCheck := false
	needsTypedMove := false
	if unop, ok := box.X.(*ssa.UnOp); ok && unop.Op == token.MUL &&
		(a.ctx.isLargeNonPointerValue(physical) || a.ctx.isZeroSizedValue(physical)) {
		needsAlloc = true
		needsNilCheck = !isKnownNonNilAddr(unop.X) && !ssaValueProvenNonNilAt(unop.X, box)
		needsTypedMove = true
	}

	// loweredRuntimeHelpers is sorted, so keep the independently-derived exact
	// inventory in lexical order as well. The structured gate compares sets and
	// cardinality and therefore also rejects duplicate or newly-added helpers.
	expected := make([]string, 0, 4)
	if needsAlloc {
		expected = append(expected, "AllocU")
	}
	if needsNilCheck {
		expected = append(expected, "AssertNilDeref")
	}
	if !target.Empty() {
		expected = append(expected, "NewItab")
	}
	if needsTypedMove {
		expected = append(expected, "Typedmemmove")
	}
	if len(expected) == 0 {
		return a.requireNoRuntimeHelpers(box)
	}
	return a.requireFrozenStructuredRuntimeHelpers(box, expected...)
}

func (a *coroPhysicalPureSSAAudit) validateChangeInterface(change *ssa.ChangeInterface) string {
	if change == nil || change.X == nil {
		return "incomplete interface conversion"
	}
	sourceType := a.typeOf(change.X.Type())
	targetType := a.typeOf(change.Type())
	source, ok := types.Unalias(sourceType).Underlying().(*types.Interface)
	if !ok {
		return "ChangeInterface source is not an interface"
	}
	target, ok := types.Unalias(targetType).Underlying().(*types.Interface)
	if !ok {
		return "ChangeInterface target is not an interface"
	}
	source.Complete()
	target.Complete()
	if err := validateCoroPhysicalSSAValueType(sourceType); err != nil {
		return "interface conversion has unsupported source type: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(targetType); err != nil {
		return "interface conversion has unsupported target type: " + err.Error()
	}

	// LLSSA extracts the dynamic ABI type through IfaceType when the source is
	// non-empty, then constructs a fresh itab when the destination is non-empty.
	// Empty-interface sides need only aggregate extract/insert operations. Bind
	// exactly that recipe to the frozen owner-scoped helper plan.
	expected := make([]string, 0, 2)
	if !source.Empty() {
		expected = append(expected, "IfaceType")
	}
	if !target.Empty() {
		expected = append(expected, "NewItab")
	}
	if len(expected) == 0 {
		return a.requireNoRuntimeHelpers(change)
	}
	return a.requireFrozenStructuredRuntimeHelpers(change, expected...)
}

func (a *coroPhysicalPureSSAAudit) validateTypeAssert(assertion *ssa.TypeAssert) string {
	if assertion == nil || assertion.X == nil || assertion.AssertedType == nil {
		return "incomplete type assertion"
	}
	sourceType := a.typeOf(assertion.X.Type())
	assertedType := a.typeOf(assertion.AssertedType)
	resultType := a.typeOf(assertion.Type())
	source, ok := types.Unalias(sourceType).Underlying().(*types.Interface)
	if !ok {
		return "type assertion source is not an interface"
	}
	source.Complete()
	if err := validateCoroPhysicalSSAValueType(sourceType); err != nil {
		return "type assertion has unsupported source type: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(assertedType); err != nil {
		return "type assertion has unsupported asserted type: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(resultType); err != nil {
		return "type assertion has unsupported result type: " + err.Error()
	}
	if assertion.CommaOk {
		tuple, ok := types.Unalias(resultType).Underlying().(*types.Tuple)
		if !ok || tuple.Len() != 2 || !types.Identical(a.typeOf(tuple.At(0).Type()), assertedType) ||
			!types.Identical(a.typeOf(tuple.At(1).Type()), types.Typ[types.Bool]) {
			return "comma-ok type assertion has an incompatible result tuple"
		}
	} else if !types.Identical(resultType, assertedType) {
		return "single-value type assertion result does not match its asserted type"
	}
	if coroPhysicalTypeContainsFunctionValue(assertedType, make(map[types.Type]bool)) {
		// Builder.TypeAssert copies the concrete callable's canonical physical
		// bytes. The frozen result ValuePlan proves whether each leaf is a managed
		// {descriptor,env} closure or an exact raw C code pointer; neither
		// transport may be reinterpreted as the other at this boundary.
		if err := validateCoroCallableTransportValue(a.plan, a.fn, assertion, a.universe); err != nil {
			return "function-valued type assertion result: " + err.Error()
		}
	}

	// Mirror Builder.TypeAssert independently. A non-empty source needs its
	// dynamic ABI type; assertions to another interface use Implements and, for
	// a non-empty result, NewItab; managed function assertions use MatchesClosure
	// after the result's descriptor ValuePlan is certified. Raw C function
	// assertions copy their direct pointer payload without that helper. A single-value
	// assertion additionally has the exact PanicTypeAssert terminal edge. Every
	// helper is then bound to the frozen owner-scoped plan so a newly suspending
	// or unwinding helper cannot hide beneath a live LLVM coroutine frame.
	expected := make([]string, 0, 4)
	if !types.Identical(sourceType, assertedType) {
		switch asserted := types.Unalias(assertedType).Underlying().(type) {
		case *types.Interface:
			asserted.Complete()
			expected = append(expected, "Implements")
			if !asserted.Empty() {
				expected = append(expected, "NewItab")
			}
		case *types.Signature:
			if !coroTypeAssertUsesManagedClosure(a.ctx, assertion) {
				break
			}
			expected = append(expected, "MatchesClosure")
		}
	}
	if !source.Empty() {
		expected = append(expected, "IfaceType")
	}
	if !assertion.CommaOk {
		expected = append(expected, "PanicTypeAssert")
	}
	return a.requireFrozenTypeAssertRuntimeHelpers(assertion, expected...)
}

// coroTypeAssertUsesManagedClosure mirrors Builder.TypeAssert's physical
// branch, rather than inferring the representation from the logical Go
// signature. Exact //llgo:type C functions remain one raw code pointer and
// must never enter MatchesClosure, whose payload contract is the managed
// two-pointer closure aggregate.
func coroTypeAssertUsesManagedClosure(ctx *context, assertion *ssa.TypeAssert) bool {
	if ctx == nil || assertion == nil || assertion.AssertedType == nil {
		return false
	}
	physical := ctx.type_(assertion.AssertedType, llssa.InGo)
	closure, ok := types.Unalias(physical.RawType()).Underlying().(*types.Struct)
	return ok && llssa.IsClosure(closure)
}

// validateCompilerElidedFunctionInterface accepts no ordinary function box.
// It certifies the transient MakeInterface node that x/tools SSA inserts for
// the exact func(any) operand of llgo.funcAddr/llgo.funcPCABI0. Those
// intrinsics inspect the static SSA function and emit its address/PC directly;
// compileValue never materializes the interface representation.
func (a *coroPhysicalPureSSAAudit) validateCompilerElidedFunctionInterface(box *ssa.MakeInterface) string {
	if a == nil || a.universe == nil || a.ctx == nil || box == nil ||
		!a.universe.makeInterfaceConsumedByFuncAddress(box, a.ctx) {
		return "function interface is not an exact compiler-elided address operand"
	}
	refs := box.Referrers()
	if refs == nil || len(*refs) != 1 {
		return "compiler-elided function interface does not have one exact consumer"
	}
	call, ok := (*refs)[0].(*ssa.Call)
	if !ok || call.Parent() != a.fn || call.Common() == nil || len(call.Common().Args) != 1 || call.Common().Args[0] != box {
		return "compiler-elided function interface is not the sole argument of its owning direct call"
	}
	semantics, intrinsic, err := coroIntrinsicCallSiteSemantics(a.universe, call)
	if err != nil {
		return "compiler-elided function address intrinsic: " + err.Error()
	}
	if !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		return "compiler-elided function interface consumer is not one exact inline no-suspend address intrinsic"
	}
	return a.requireNoRuntimeHelpers(box)
}

func (a *coroPhysicalPureSSAAudit) validateMakeSlice(makeSlice *ssa.MakeSlice) string {
	if makeSlice == nil || makeSlice.Len == nil || makeSlice.Cap == nil {
		return "incomplete slice allocation"
	}
	if _, ok := types.Unalias(a.typeOf(makeSlice.Type())).Underlying().(*types.Slice); !ok {
		return "MakeSlice result is not a slice"
	}
	for _, size := range []ssa.Value{makeSlice.Len, makeSlice.Cap} {
		basic, ok := types.Unalias(a.typeOf(size.Type())).Underlying().(*types.Basic)
		if !ok || basic.Info()&types.IsInteger == 0 {
			return "MakeSlice length and capacity must be integer values"
		}
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(makeSlice.Type())); err != nil {
		return "MakeSlice result has unsupported type: " + err.Error()
	}
	return a.requireFrozenOutcomeRuntimeHelper(makeSlice, "MakeSlice")
}

func (a *coroPhysicalPureSSAAudit) validateMakeMap(makeMap *ssa.MakeMap) string {
	if makeMap == nil || makeMap.Type() == nil {
		return "incomplete map allocation"
	}
	if _, ok := types.Unalias(a.typeOf(makeMap.Type())).Underlying().(*types.Map); !ok {
		return "MakeMap result is not a map"
	}
	if makeMap.Reserve != nil {
		reserve, ok := types.Unalias(a.typeOf(makeMap.Reserve.Type())).Underlying().(*types.Basic)
		if !ok || reserve.Info()&types.IsInteger == 0 {
			return "MakeMap reserve is not an integer"
		}
		if err := validateCoroPhysicalSSAValueType(a.typeOf(makeMap.Reserve.Type())); err != nil {
			return "MakeMap reserve has unsupported type: " + err.Error()
		}
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(makeMap.Type())); err != nil {
		return "MakeMap result has unsupported type: " + err.Error()
	}
	return a.requireFrozenStructuredRuntimeHelpers(makeMap, "MakeMap")
}

func (a *coroPhysicalPureSSAAudit) validateMakeChan(makeChan *ssa.MakeChan) string {
	if makeChan == nil || makeChan.Size == nil || makeChan.Type() == nil {
		return "incomplete channel allocation"
	}
	if _, ok := types.Unalias(a.typeOf(makeChan.Type())).Underlying().(*types.Chan); !ok {
		return "MakeChan result is not a channel"
	}
	size, ok := types.Unalias(a.typeOf(makeChan.Size.Type())).Underlying().(*types.Basic)
	if !ok || size.Info()&types.IsInteger == 0 || size.Info()&types.IsUntyped != 0 {
		return "MakeChan capacity is not a concrete integer"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(makeChan.Size.Type())); err != nil {
		return "MakeChan capacity has unsupported type: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(makeChan.Type())); err != nil {
		return "MakeChan result has unsupported type: " + err.Error()
	}
	// NewChan rejects negative or overflowing capacities. Its exact managed
	// helper therefore returns through the same ExplicitStatus child-await path
	// as make([]T, n), never by unwinding across the live LLVM coroutine frame.
	return a.requireFrozenOutcomeRuntimeHelper(makeChan, "NewChan")
}

func (a *coroPhysicalPureSSAAudit) validateLookup(lookup *ssa.Lookup) string {
	if lookup == nil || lookup.X == nil || lookup.Index == nil || lookup.Type() == nil {
		return "incomplete map lookup"
	}
	mapType, ok := types.Unalias(a.typeOf(lookup.X.Type())).Underlying().(*types.Map)
	if !ok {
		return "Lookup source is not a map"
	}
	if !types.Identical(a.typeOf(lookup.Index.Type()), a.typeOf(mapType.Key())) {
		return "Lookup key does not match the map key type"
	}
	if lookup.CommaOk {
		result, ok := types.Unalias(a.typeOf(lookup.Type())).Underlying().(*types.Tuple)
		if !ok || result.Len() != 2 ||
			!types.Identical(a.typeOf(result.At(0).Type()), a.typeOf(mapType.Elem())) ||
			!coroPhysicalBoolType(a.typeOf(result.At(1).Type())) {
			return "comma-ok Lookup result is not the exact (element, bool) tuple"
		}
	} else if !types.Identical(a.typeOf(lookup.Type()), a.typeOf(mapType.Elem())) {
		return "Lookup result does not match the map element type"
	}
	for name, typ := range map[string]types.Type{
		"map": lookup.X.Type(), "key": lookup.Index.Type(), "result": lookup.Type(),
	} {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(typ)); err != nil {
			return "Lookup " + name + " has unsupported type: " + err.Error()
		}
	}
	helper := "MapAccess1"
	if lookup.CommaOk {
		helper = "MapAccess2"
	}
	return a.requireFrozenStructuredRuntimeHelpers(lookup, "AllocU", helper)
}

func (a *coroPhysicalPureSSAAudit) validateMapUpdate(update *ssa.MapUpdate) string {
	if update == nil || update.Map == nil || update.Key == nil || update.Value == nil {
		return "incomplete map update"
	}
	mapType, ok := types.Unalias(a.typeOf(update.Map.Type())).Underlying().(*types.Map)
	if !ok {
		return "MapUpdate target is not a map"
	}
	if !types.Identical(a.typeOf(update.Key.Type()), a.typeOf(mapType.Key())) {
		return "MapUpdate key does not match the map key type"
	}
	if !types.Identical(a.typeOf(update.Value.Type()), a.typeOf(mapType.Elem())) {
		return "MapUpdate value does not match the map element type"
	}
	for name, typ := range map[string]types.Type{
		"map": update.Map.Type(), "key": update.Key.Type(), "value": update.Value.Type(),
	} {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(typ)); err != nil {
			return "MapUpdate " + name + " has unsupported type: " + err.Error()
		}
	}
	return a.requireFrozenStructuredRuntimeHelpers(update, "AllocU", "MapAssign")
}

func (a *coroPhysicalPureSSAAudit) validateRange(rng *ssa.Range) string {
	if rng == nil || rng.X == nil {
		return "incomplete range iterator construction"
	}
	var helper string
	sourceType := a.typeOf(rng.X.Type())
	if physicalString, stringSource := coroPhysicalRangeStringType(sourceType); stringSource {
		helper = "NewStringIter"
		sourceType = physicalString
	} else {
		switch source := types.Unalias(sourceType).Underlying().(type) {
		case *types.Basic:
			return "Range basic source is not a string"
		case *types.Map:
			helper = "NewMapIter"
		default:
			return fmt.Sprintf("Range has unsupported source type %T", source)
		}
	}
	if err := validateCoroPhysicalSSAValueType(sourceType); err != nil {
		return "Range source has unsupported type: " + err.Error()
	}
	// x/tools intentionally gives Range an opaque iterator type. The exact
	// helper result supplies the physical pointer representation; Next below
	// proves that the opaque value never escapes that pair of lowerings.
	refs := rng.Referrers()
	if refs == nil {
		return "Range iterator has no frozen use list"
	}
	for _, ref := range *refs {
		next, ok := ref.(*ssa.Next)
		if !ok || next.Iter != rng || next.Parent() != rng.Parent() {
			return "Range iterator escapes its exact Next lowering"
		}
	}
	return a.requireFrozenStructuredRuntimeHelpers(rng, helper)
}

func (a *coroPhysicalPureSSAAudit) validateNext(next *ssa.Next) string {
	if next == nil || next.Iter == nil || next.Type() == nil {
		return "incomplete range iterator advance"
	}
	rng, ok := next.Iter.(*ssa.Range)
	if !ok || rng.X == nil || rng.Parent() != next.Parent() {
		return "Next does not consume one exact local Range iterator"
	}
	result, ok := types.Unalias(a.typeOf(next.Type())).Underlying().(*types.Tuple)
	if !ok || result.Len() != 3 || !coroPhysicalBoolType(a.typeOf(result.At(0).Type())) {
		return "Next result is not an exact (bool, key, value) tuple"
	}
	var helper string
	var keyType, valueType types.Type
	sourceType := a.typeOf(rng.X.Type())
	if _, stringSource := coroPhysicalRangeStringType(sourceType); stringSource {
		if !next.IsString {
			return "Next string marker disagrees with its Range source"
		}
		helper = "StringIterNext"
		keyType, valueType = types.Typ[types.Int], types.Typ[types.Rune]
	} else {
		switch source := types.Unalias(sourceType).Underlying().(type) {
		case *types.Basic:
			return "Next string marker disagrees with its Range source"
		case *types.Map:
			if next.IsString {
				return "Next map iterator is marked as a string iterator"
			}
			helper = "MapIterNext"
			keyType, valueType = source.Key(), source.Elem()
		default:
			return fmt.Sprintf("Next has unsupported Range source type %T", source)
		}
	}
	for index, expected := range []types.Type{keyType, valueType} {
		actual := a.typeOf(result.At(index + 1).Type())
		if coroPhysicalInvalidType(actual) {
			continue
		}
		// sourceType was already patched as one complete map/string type above;
		// its key/element projections therefore belong to the selected emission
		// owner. Re-patching a projected instantiated named type can select its
		// pre-substitution origin a second time and create a false mismatch.
		if !types.Identical(actual, expected) && !types.AssignableTo(expected, actual) {
			return fmt.Sprintf(
				"Next tuple field %d is not assignable from the range source (source=%s target=%s)",
				index+1,
				types.TypeString(expected, nil),
				types.TypeString(actual, nil),
			)
		}
		if err := validateCoroPhysicalSSAValueType(actual); err != nil {
			return fmt.Sprintf("Next tuple field %d has unsupported type: %v", index+1, err)
		}
	}
	helpers := []string{helper}
	if a.universe != nil {
		helpers = append(helpers, a.universe.nextAssignmentRuntimeHelperNames(a.ctx, next)...)
	}
	sort.Strings(helpers)
	return a.requireFrozenStructuredRuntimeHelpers(next, helpers...)
}

// coroPhysicalRangeStringType gives an untyped string constant the concrete
// string representation that Builder.Range already emits. x/tools retains the
// constant's untyped basic kind in Range.X even though Go default typing at
// this operation is string; rejecting it would make a valid standard-library
// range depend on an incidental SSA type spelling.
func coroPhysicalRangeStringType(typ types.Type) (types.Type, bool) {
	if typ == nil {
		return nil, false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	if !ok || basic.Kind() != types.String && basic.Kind() != types.UntypedString {
		return nil, false
	}
	if basic.Kind() == types.UntypedString {
		return types.Typ[types.String], true
	}
	return typ, true
}

func coroPhysicalBoolType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Bool
}

func coroPhysicalInvalidType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Invalid
}

func (a *coroPhysicalPureSSAAudit) validateChangeType(change *ssa.ChangeType) string {
	if change == nil || change.X == nil {
		return "incomplete value-preserving type change"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(change.X.Type())); err != nil {
		return "type-change source is unsupported: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(change.Type())); err != nil {
		return "type-change result is unsupported: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(change)
}

func (a *coroPhysicalPureSSAAudit) validateConvert(convert *ssa.Convert) string {
	if convert == nil || convert.X == nil {
		return "incomplete conversion"
	}
	source, target := a.typeOf(convert.X.Type()), a.typeOf(convert.Type())
	if !coroPureConversion(source, target) {
		helper := coroRuntimeConversionHelper(source, target)
		if helper == "" {
			return "conversion may allocate or call the runtime; pure coroutine conversion supports only numeric and pointer/unsafe-pointer representations"
		}
		if err := validateCoroPhysicalSSAValueType(source); err != nil {
			return "conversion source is unsupported: " + err.Error()
		}
		if err := validateCoroPhysicalSSAValueType(target); err != nil {
			return "conversion result is unsupported: " + err.Error()
		}
		// LLSSA lowers every supported string conversion through exactly one
		// named runtime helper. Bind that independently-derived recipe to the
		// owner-scoped lowered-call plan; allocation remains legal only when the
		// helper is a demanded no-suspend/no-unwind plain body (or an explicitly
		// structured coroutine helper under the same shared gate).
		return a.requireFrozenExactRuntimeHelper(convert, helper)
	}
	proof := a.currentFrameRetentionProof()
	if coroFrameRetentionPointerLike(source) && coroFrameRetentionUintptrLike(target) &&
		(proof == nil || !proof.provesTraceableUintptr(convert)) &&
		!a.coroPointerUintptrScalarTerminal(convert) &&
		!a.coroPointerUintptrAtomicTerminal(convert) &&
		!coroPointerUintptrReflectHeaderStoreTerminal(convert) &&
		(a.universe == nil || !a.universe.coroRuntimeCodeAddressType(source)) {
		reason := "pointer-to-uintptr conversion is not bound to an exact managed-child/worker uintptrkeepalive source, scalar terminal, inline atomic terminal, or reflect-header store"
		if coroPointerUintptrScalarTerminal(convert) && a != nil && a.plan != nil && a.fn != nil {
			plan, planned := a.plan.FunctionPlan(a.fn)
			reason += fmt.Sprintf(" (structural scalar terminal; planned=%t effect=%s exec=%s)", planned, plan.Effect, plan.Exec)
		}
		return reason
	}
	if coroFrameRetentionUintptrLike(source) && coroFrameRetentionPointerLike(target) &&
		(proof == nil || !proof.provesTraceableUintptr(convert.X)) &&
		!a.provesWorkerForeignPointerResult(convert.X) &&
		!coroConstantUintptrAddress(convert.X) {
		return "uintptr-to-pointer conversion has no traceable exact pointer provenance"
	}
	if err := validateCoroPhysicalSSAValueType(source); err != nil {
		return "conversion source is unsupported: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(target); err != nil {
		return "conversion result is unsupported: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(convert)
}

// coroConstantUintptrAddress accepts an exact raw numeric address. Such a
// value has no Go referent whose liveness or relocation must be proved; using
// the resulting unsafe.Pointer remains the program's responsibility. Besides
// a direct integer constant, x/tools may spill a captured constant local before
// later closures are built. Admit only the entry-block image with one direct
// constant Store before the exact load and no earlier address escape.
func coroConstantUintptrAddress(value ssa.Value) bool {
	switch value := value.(type) {
	case *ssa.Const:
		basic, ok := types.Unalias(value.Type()).Underlying().(*types.Basic)
		return value.Value != nil && ok && basic.Info()&types.IsInteger != 0
	case *ssa.ChangeType:
		return value.X != nil && coroFrameRetentionIntegerLike(value.Type()) &&
			coroConstantUintptrAddress(value.X)
	case *ssa.Convert:
		return value.X != nil && coroFrameRetentionIntegerLike(value.Type()) &&
			coroFrameRetentionIntegerLike(value.X.Type()) &&
			coroConstantUintptrAddress(value.X)
	case *ssa.UnOp:
		if value.Op != token.MUL || value.Block() == nil || value.Block().Index != 0 {
			return false
		}
		allocation, ok := value.X.(*ssa.Alloc)
		if !ok || allocation.Parent() != value.Parent() || allocation.Referrers() == nil {
			return false
		}
		loadIndex := -1
		for index, instruction := range value.Block().Instrs {
			if instruction == value {
				loadIndex = index
				break
			}
		}
		if loadIndex < 0 {
			return false
		}
		stores := 0
		for _, reference := range *allocation.Referrers() {
			instruction, ok := reference.(ssa.Instruction)
			if !ok || instruction.Block() != value.Block() {
				continue
			}
			index := -1
			for candidate, blockInstruction := range value.Block().Instrs {
				if blockInstruction == instruction {
					index = candidate
					break
				}
			}
			if index < 0 || index >= loadIndex {
				continue
			}
			switch instruction := instruction.(type) {
			case *ssa.DebugRef:
			case *ssa.Store:
				if instruction.Addr != allocation || !coroConstantUintptrAddress(instruction.Val) {
					return false
				}
				stores++
			case *ssa.UnOp:
				if instruction.Op != token.MUL || instruction.X != allocation {
					return false
				}
			default:
				// In particular, no closure, call, conversion, or address
				// transport may expose the cell before this load.
				return false
			}
		}
		return stores == 1
	}
	return false
}

// coroPointerUintptrReflectHeaderStoreTerminal recognizes the legacy standard
// unsafe construction of reflect.StringHeader/reflect.SliceHeader. The raw
// address word must flow directly into exactly one Data field Store; only that
// Store's pure FieldAddr may be formed between them. Thus no scheduler poll,
// child await, call, arithmetic, Phi, return, or hidden memory roundtrip can
// split this local conversion. The header's subsequent lifetime remains
// governed by Go's unsafe rules; this certificate grants no uintptr-to-pointer
// provenance.
func coroPointerUintptrReflectHeaderStoreTerminal(value ssa.Value) bool {
	root, ok := value.(ssa.Instruction)
	if value == nil || !ok || root.Block() == nil || value.Referrers() == nil {
		return false
	}
	var terminal *ssa.Store
	for _, reference := range *value.Referrers() {
		switch instruction := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if terminal != nil || instruction.Val != value ||
				instruction.Block() != root.Block() ||
				!coroReflectHeaderDataAddress(instruction.Addr) {
				return false
			}
			terminal = instruction
		default:
			return false
		}
	}
	if terminal == nil {
		return false
	}
	rootIndex, storeIndex := -1, -1
	for index, instruction := range root.Block().Instrs {
		switch instruction {
		case root:
			rootIndex = index
		case terminal:
			storeIndex = index
		}
	}
	if rootIndex < 0 || storeIndex <= rootIndex {
		return false
	}
	for _, instruction := range root.Block().Instrs[rootIndex+1 : storeIndex] {
		switch instruction := instruction.(type) {
		case *ssa.DebugRef:
		case ssa.Value:
			if instruction != terminal.Addr {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func coroReflectHeaderDataAddress(address ssa.Value) bool {
	field, ok := address.(*ssa.FieldAddr)
	if !ok || field.X == nil {
		return false
	}
	pointer, ok := types.Unalias(field.X.Type()).Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil ||
		named.Obj().Pkg().Path() != "reflect" ||
		named.Obj().Name() != "StringHeader" && named.Obj().Name() != "SliceHeader" {
		return false
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok || field.Field < 0 || field.Field >= structure.NumFields() {
		return false
	}
	member := structure.Field(field.Field)
	basic, ok := types.Unalias(member.Type()).Underlying().(*types.Basic)
	return member.Name() == "Data" && ok && basic.Kind() == types.Uintptr
}

// coroPointerUintptrScalarTerminal binds the structural scalar-observation
// recipe below to the immutable whole-function suspension plan. Without a
// frame-retention identity, the chain is accepted only in a non-preempting,
// non-suspending body. Under ParkABIV2, an inserted poll or managed-child await
// may split the chain: LLVM then retains the live address-shaped word in the
// coroutine frame, and the sole nonmoving-conservative-or-none profile keeps
// its referent stable. OutcomeStructured describes only terminal Return/Panic
// transport and is therefore harmless in the non-suspending fallback too.
func (a *coroPhysicalPureSSAAudit) coroPointerUintptrScalarTerminal(value ssa.Value) bool {
	if a == nil || a.plan == nil || a.fn == nil || !coroPointerUintptrScalarTerminal(value) {
		return false
	}
	plan, planned := a.plan.FunctionPlan(a.fn)
	if planned && a.frameRetentionABI == CoroFrameRetentionParkABIV2 &&
		plan.Emission == coro.EmitCoroutine && !plan.Exec.IsOpaque() {
		// Return, map-key, comparison, remainder, affine comparison, and exact
		// pointer-distance terminals are all forward-only scalar observations.
		// None grants uintptr-to-pointer reconstruction authority. A future
		// precise or moving collector must introduce a different retention ABI
		// and explicit pin/relocation rules before reusing this branch.
		return true
	}
	return planned && !plan.Exec.Contains(coro.NeedsPreempt) &&
		plan.Effect&^coro.OutcomeStructured == coro.NoSuspend
}

// coroPointerUintptrAtomicTerminal recognizes the other standard forward-only
// address-word observation used by sync.copyChecker: an exact inline atomic
// operation consumes (and may store) the integer representation without
// suspending or reconstructing a pointer. The conversion and every consuming
// atomic must remain in the same SSA block, so no poll/await can split this
// local lifetime. Both ProgramIR and SSAPlan must agree that the source call is
// an elided atomic intrinsic; an ordinary call, a helper-backed intrinsic, or
// any additional use keeps the conversion fail-closed.
func (a *coroPhysicalPureSSAAudit) coroPointerUintptrAtomicTerminal(value ssa.Value) bool {
	root, ok := value.(ssa.Instruction)
	if !ok || root == nil || root.Block() == nil || value.Referrers() == nil ||
		a == nil || a.universe == nil || a.universe.coroProgramIR == nil || a.plan == nil {
		return false
	}
	uses := 0
	for _, reference := range *value.Referrers() {
		switch instruction := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.Call:
			if instruction.Block() != root.Block() || instruction.Common() == nil ||
				instruction.Common().IsInvoke() || !a.plan.ElidesCall(instruction) {
				return false
			}
			frozen, found, err := a.universe.coroProgramIR.callSitePlan(instruction)
			if err != nil || !found || frozen.failure != "" ||
				!frozen.plan.Intrinsic || !frozen.plan.ElidesCall() ||
				frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend ||
				!isCoroAtomicIntrinsic(frozen.opcode) {
				return false
			}
			matches := 0
			for _, argument := range instruction.Common().Args {
				if argument == value {
					matches++
				}
			}
			if matches == 0 {
				return false
			}
			uses += matches
		default:
			return false
		}
	}
	return uses != 0
}

type coroWorkerPointerResultVisit struct {
	function *ssa.Function
	result   int
}

// provesWorkerForeignPointerResult accepts one exact producer-injected result
// fact from a certified worker call. Besides the worker call's direct tuple
// extract, the fact may cross an exact wrapper return and a closed ordinary
// call result. That is the minimum operation-level propagation needed by
// standard-library wrappers such as syscall.mmap and mmapper.Mmap.
//
// Integer arithmetic, storage, Phi merging, parameters, open calls, and
// multiple-target calls still destroy the fact. The callable shadow injects
// metadata at FuncPCABI0 formation; the worker-result projection binds a
// private carrier result to one incoming producer; and the immutable CallPlan
// selects every wrapper target before physical lowering consumes the fact.
func (a *coroPhysicalPureSSAAudit) provesWorkerForeignPointerResult(value ssa.Value) bool {
	if a == nil || a.plan == nil || a.universe == nil || value == nil {
		return false
	}
	return a.provesWorkerForeignPointerValue(
		a.fn, value, make(map[coroWorkerPointerResultVisit]bool),
	)
}

func (a *coroPhysicalPureSSAAudit) provesWorkerForeignPointerValue(
	owner *ssa.Function,
	value ssa.Value,
	visiting map[coroWorkerPointerResultVisit]bool,
) bool {
	switch value := value.(type) {
	case *ssa.Extract:
		if value.Index < 0 || value.Index >= coroWorkerResultProjectionWidthV1 {
			return false
		}
		call, ok := value.Tuple.(*ssa.Call)
		if !ok || call == nil || call.Parent() != owner {
			return false
		}
		return a.provesWorkerForeignPointerCallResult(owner, call, value.Index, visiting)
	case *ssa.Call:
		// SSA represents a single-result call as the call value itself rather
		// than as Extract(call, 0). Treat both encodings identically so an
		// exact private wrapper does not need source metadata merely because it
		// forwards one result instead of a tuple.
		common := value.Common()
		if value.Parent() != owner || common == nil || common.Signature() == nil ||
			common.Signature().Results() == nil ||
			common.Signature().Results().Len() != 1 {
			return false
		}
		return a.provesWorkerForeignPointerCallResult(owner, value, 0, visiting)
	default:
		return false
	}
}

func (a *coroPhysicalPureSSAAudit) provesWorkerForeignPointerCallResult(
	owner *ssa.Function,
	call *ssa.Call,
	result int,
	visiting map[coroWorkerPointerResultVisit]bool,
) bool {
	if owner == nil || call == nil || call.Parent() != owner ||
		result < 0 || result >= coroWorkerResultProjectionWidthV1 {
		return false
	}
	if validateCoroWorkerSyscallCall(a.plan, a.universe, call) == nil {
		certificate, certified, err := a.universe.CoroWorkerSyscallCertificate(call)
		if err == nil && certified && certificate.ID != "" &&
			certificate.ForeignPointerResultMask&(uint8(1)<<uint(result)) != 0 {
			return true
		}
	}
	if validateCoroWorkerProjectedForeignPointerResult(
		a.plan, a.universe, call, result,
	) == nil {
		return true
	}

	callPlan, planned := a.plan.CallPlan(call)
	if !planned || callPlan.Kind != coro.CallDirect || callPlan.Open ||
		len(callPlan.Targets) != 1 {
		return false
	}
	target, found := a.plan.Function(callPlan.Targets[0])
	if !found || target == nil || len(target.Blocks) == 0 ||
		a.universe.canonicalAlias(target) != target {
		return false
	}
	targetPlan, targetPlanned := a.plan.FunctionPlan(target)
	if !targetPlanned || targetPlan.ID != callPlan.Targets[0] ||
		targetPlan.External != coro.Defined || target.Signature == nil ||
		target.Signature.Results() == nil || result >= target.Signature.Results().Len() ||
		!coroWorkerUintptrType(target.Signature.Results().At(result).Type()) {
		return false
	}

	visit := coroWorkerPointerResultVisit{function: target, result: result}
	if visiting[visit] {
		return false
	}
	visiting[visit] = true
	defer delete(visiting, visit)

	reachable := coroPhysicalConstantReachableBlocks(target)
	returns := 0
	for _, block := range target.Blocks {
		if !reachable[block] {
			continue
		}
		for _, instruction := range block.Instrs {
			returned, ok := instruction.(*ssa.Return)
			if !ok {
				continue
			}
			returns++
			if result >= len(returned.Results) ||
				!a.provesWorkerForeignPointerValue(target, returned.Results[result], visiting) {
				return false
			}
		}
	}
	return returns != 0
}

// coroRuntimeConversionHelper mirrors Builder.Convert's complete allocating
// string-conversion recipe without consulting the mutable helper inventory.
// Keeping this derivation local means a future Builder conversion cannot become
// coroutine-safe merely by acquiring an arbitrary runtime call of the same
// source/result width.
func coroRuntimeConversionHelper(source, target types.Type) string {
	source = types.Unalias(source).Underlying()
	target = types.Unalias(target).Underlying()
	if basic, ok := target.(*types.Basic); ok && basic.Kind() == types.String {
		switch source := source.(type) {
		case *types.Slice:
			element, ok := types.Unalias(source.Elem()).Underlying().(*types.Basic)
			if !ok {
				return ""
			}
			switch element.Kind() {
			case types.Byte:
				return "StringFromBytes"
			case types.Rune:
				return "StringFromRunes"
			}
		case *types.Basic:
			if source.Info()&types.IsInteger == 0 {
				return ""
			}
			if source.Info()&types.IsUnsigned != 0 {
				return "StringFromUint64"
			}
			return "StringFromInt64"
		}
		return ""
	}
	if slice, ok := target.(*types.Slice); ok {
		basic, ok := source.(*types.Basic)
		if !ok || basic.Kind() != types.String {
			return ""
		}
		element, ok := types.Unalias(slice.Elem()).Underlying().(*types.Basic)
		if !ok {
			return ""
		}
		switch element.Kind() {
		case types.Byte:
			return "StringToBytes"
		case types.Rune:
			return "StringToRunes"
		}
	}
	return ""
}

// coroPointerUintptrScalarTerminal recognizes an address word whose complete
// semantic lifetime ends in an integer comparison, as one operand of an exact
// pointer-distance expression, uintptr(end)-uintptr(start), as a scalar map
// key, or at a direct Go return. None of these terminals authorizes pointer
// reconstruction. If a safepoint splits a map-key/return observation, the
// address word itself is retained in LLVM's conservatively scanned/non-GC
// coroutine frame; the active frame profile explicitly excludes precise or
// moving collectors. General storage, call transport, pointer reconstruction,
// and unbounded arithmetic remain fail-closed.
func coroPointerUintptrScalarTerminal(value ssa.Value) bool {
	root, rootIsInstruction := value.(ssa.Instruction)
	if value == nil || !rootIsInstruction || root.Block() == nil || value.Referrers() == nil {
		return false
	}
	if coroPointerUintptrReturnTerminal(value) || coroPointerUintptrMapKeyTerminal(value) {
		return true
	}
	uses := 0
	affine := false
	for _, reference := range *value.Referrers() {
		switch instruction := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if instruction.Block() != root.Block() {
				return false
			}
			switch instruction.Op {
			case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
				uses++
			case token.ADD:
				if !coroPointerUintptrAffineComparisonResult(value, instruction) {
					return false
				}
				affine = true
				uses++
			case token.REM:
				// Alignment tests commonly lower as uintptr(pointer)%C == 0.
				// Accept only a constant non-zero divisor and require the remainder
				// itself to terminate exclusively in scalar comparisons; returning,
				// storing, calling with, or reconstructing from it stays rejected.
				if instruction.X != value || !constantIntegerKnownNonZero(instruction.Y) ||
					!coroPointerUintptrComparisonResult(instruction) {
					return false
				}
				uses++
			case token.SUB:
				left, leftOK := instruction.X.(*ssa.Convert)
				right, rightOK := instruction.Y.(*ssa.Convert)
				if leftOK && rightOK &&
					coroFrameRetentionPointerToUintptr(left) &&
					coroFrameRetentionPointerToUintptr(right) &&
					(value == left || value == right) {
					uses++
					break
				}
				if !coroPointerUintptrAffineComparisonResult(value, instruction) {
					return false
				}
				affine = true
				uses++
			default:
				return false
			}
		default:
			return false
		}
	}
	if affine {
		// An affine observation is deliberately linear: the address word has
		// exactly one semantic consumer and the derived word has exactly one
		// comparison terminal. It cannot also be returned, stored, called with,
		// merged through a Phi, or reconstructed as a pointer.
		return uses == 1
	}
	return uses != 0
}

// coroPointerUintptrMapKeyTerminal accepts a forward-only integer expression
// whose every path ends as a map key. This is an observation, not pointer
// provenance: no alias is added to frameRetention.uintptrValues, so loading the
// key later or converting any derived hash back to a pointer remains rejected.
//
// Each arithmetic step may combine the address-derived word with one scalar
// operand, but never with a second pointer-derived integer. Phi merging,
// returns, calls, stores, and use as a map value are deliberately excluded.
func coroPointerUintptrMapKeyTerminal(root ssa.Value) bool {
	if root == nil || !coroFrameRetentionIntegerLike(root.Type()) {
		return false
	}
	const (
		coroMapKeyVisiting uint8 = 1
		coroMapKeyAccepted uint8 = 2
		coroMapKeyRejected uint8 = 3
	)
	state := make(map[ssa.Value]uint8)
	var visit func(ssa.Value) bool
	visit = func(value ssa.Value) bool {
		switch state[value] {
		case coroMapKeyVisiting, coroMapKeyRejected:
			return false
		case coroMapKeyAccepted:
			return true
		}
		state[value] = coroMapKeyVisiting
		refs := value.Referrers()
		if refs == nil {
			state[value] = coroMapKeyRejected
			return false
		}
		uses := 0
		for _, reference := range *refs {
			switch instruction := reference.(type) {
			case *ssa.DebugRef:
			case *ssa.BinOp:
				if instruction.X != value && instruction.Y != value ||
					!coroFrameRetentionIntegerLike(instruction.Type()) {
					state[value] = coroMapKeyRejected
					return false
				}
				switch instruction.Op {
				case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
					token.AND, token.OR, token.XOR, token.AND_NOT, token.SHL, token.SHR:
				default:
					state[value] = coroMapKeyRejected
					return false
				}
				other := instruction.X
				if other == value {
					other = instruction.Y
				}
				if other == value || !coroFrameRetentionIntegerLike(other.Type()) ||
					coroFrameRetentionIntegerHasPointerProvenance(other, make(map[ssa.Value]bool)) ||
					!visit(instruction) {
					state[value] = coroMapKeyRejected
					return false
				}
				uses++
			case *ssa.Lookup:
				if instruction.Index != value || !coroPointerUintptrExactMapKey(instruction.X, value) {
					state[value] = coroMapKeyRejected
					return false
				}
				uses++
			case *ssa.MapUpdate:
				if instruction.Key != value || !coroPointerUintptrExactMapKey(instruction.Map, value) {
					state[value] = coroMapKeyRejected
					return false
				}
				uses++
			default:
				state[value] = coroMapKeyRejected
				return false
			}
		}
		if uses == 0 {
			state[value] = coroMapKeyRejected
			return false
		}
		state[value] = coroMapKeyAccepted
		return true
	}
	return visit(root)
}

func coroPointerUintptrExactMapKey(mapping, key ssa.Value) bool {
	if mapping == nil || key == nil {
		return false
	}
	typ, ok := types.Unalias(mapping.Type()).Underlying().(*types.Map)
	return ok && types.Identical(typ.Key(), key.Type())
}

// coroPointerUintptrReturnTerminal accepts only the address word itself as an
// exact return operand. Phi merging, arithmetic, storage, calls, and conversion
// back to a pointer are deliberately not inferred here. Debug references do
// not extend the semantic lifetime.
func coroPointerUintptrReturnTerminal(value ssa.Value) bool {
	instruction, ok := value.(ssa.Instruction)
	if value == nil || !ok || instruction.Parent() == nil || value.Referrers() == nil {
		return false
	}
	uses := 0
	for _, reference := range *value.Referrers() {
		switch terminal := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.Return:
			if terminal.Parent() != instruction.Parent() {
				return false
			}
			found := false
			for _, result := range terminal.Results {
				if result == value {
					found = true
					break
				}
			}
			if !found {
				return false
			}
			uses++
		default:
			return false
		}
	}
	return uses != 0
}

// coroPointerUintptrAffineComparisonResult accepts one same-block affine
// endpoint observation, address+offset or address-offset, whose derived word
// has exactly one scalar comparison consumer. The offset must not carry
// pointer provenance. This is intentionally an observation, not a general
// uintptr provenance fact: no value is added to frameRetention.uintptrValues,
// and conversion back to a pointer remains fail-closed.
func coroPointerUintptrAffineComparisonResult(address ssa.Value, operation *ssa.BinOp) bool {
	addressInstruction, ok := address.(ssa.Instruction)
	if !ok || addressInstruction.Block() == nil || operation == nil || operation.Block() != addressInstruction.Block() ||
		operation.Referrers() == nil || !coroFrameRetentionUintptrLike(address.Type()) ||
		!coroFrameRetentionUintptrLike(operation.Type()) {
		return false
	}
	xAddress, yAddress := operation.X == address, operation.Y == address
	if xAddress == yAddress {
		return false
	}
	offset := operation.Y
	if yAddress {
		offset = operation.X
	}
	if !coroFrameRetentionUintptrLike(offset.Type()) ||
		coroFrameRetentionIntegerHasPointerProvenance(offset, make(map[ssa.Value]bool)) {
		return false
	}
	switch operation.Op {
	case token.ADD:
	case token.SUB:
		if !xAddress { // scalar-address does not preserve this endpoint.
			return false
		}
	default:
		return false
	}
	uses := 0
	for _, reference := range *operation.Referrers() {
		switch comparison := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if comparison.Block() != operation.Block() ||
				(comparison.X != operation && comparison.Y != operation) {
				return false
			}
			switch comparison.Op {
			case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
				uses++
			default:
				return false
			}
		default:
			return false
		}
	}
	return uses == 1
}

func coroPointerUintptrComparisonResult(value ssa.Value) bool {
	producer, ok := value.(ssa.Instruction)
	if value == nil || !ok || producer.Block() == nil || value.Referrers() == nil {
		return false
	}
	uses := 0
	for _, reference := range *value.Referrers() {
		switch instruction := reference.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if instruction.Block() != producer.Block() ||
				(instruction.X != value && instruction.Y != value) {
				return false
			}
			switch instruction.Op {
			case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
				uses++
			default:
				return false
			}
		default:
			return false
		}
	}
	return uses != 0
}

func (a *coroPhysicalPureSSAAudit) validatePhi(phi *ssa.Phi) string {
	if phi == nil {
		return "nil phi"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(phi.Type())); err != nil {
		return "phi has unsupported value type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(phi)
}

func (a *coroPhysicalPureSSAAudit) validateBinOp(op *ssa.BinOp) string {
	if op == nil || op.X == nil || op.Y == nil {
		return "incomplete binary operation"
	}
	if op.Op == token.ADD &&
		coroPureStringType(a.typeOf(op.X.Type())) &&
		coroPureStringType(a.typeOf(op.Y.Type())) &&
		coroPureStringType(a.typeOf(op.Type())) {
		for _, typ := range []types.Type{a.typeOf(op.X.Type()), a.typeOf(op.Y.Type()), a.typeOf(op.Type())} {
			if err := validateCoroPhysicalSSAValueType(typ); err != nil {
				return "string concatenation has unsupported physical value type: " + err.Error()
			}
		}
		// Builder.BinOp does not emit an LLVM aggregate operation for string +.
		// It lowers through the owner-scoped runtime.StringCat edge. StringCat
		// validates the combined length and allocates the result, so its panic
		// outcome must return through the shared ExplicitStatus child-await ABI;
		// a direct possibly-unwinding native-stack call may not cross this frame.
		return a.requireFrozenOutcomeRuntimeHelper(op, "StringCat")
	}
	if (op.Op == token.EQL || op.Op == token.NEQ) &&
		(coroPureAggregateType(a.typeOf(op.X.Type())) || coroPureAggregateType(a.typeOf(op.Y.Type()))) {
		left, right := a.typeOf(op.X.Type()), a.typeOf(op.Y.Type())
		helperSet := make(map[string]struct{})
		if !types.Identical(left, right) ||
			!coroAggregateEqualityRuntimeHelpers(left, make(map[types.Type]bool), helperSet) {
			return "aggregate equality is not an exact supported comparable layout"
		}
		for _, typ := range []types.Type{left, right, a.typeOf(op.Type())} {
			if err := validateCoroPhysicalSSAValueType(typ); err != nil {
				return "aggregate equality has unsupported physical value type: " + err.Error()
			}
		}
		// LLSSA Builder.BinOp recursively extracts array elements and non-blank
		// struct fields, combines their comparisons, and negates once for !=.
		// Scalar leaves stay inline. String/interface leaves use the same frozen
		// StringEqual/EfaceEqual/IfaceType edges as direct comparisons, so bind
		// the complete unique helper inventory to the owner-scoped plan instead
		// of rejecting an otherwise ordinary Go comparable aggregate.
		helpers := make([]string, 0, len(helperSet))
		for helper := range helperSet {
			helpers = append(helpers, helper)
		}
		sort.Strings(helpers)
		if len(helpers) == 0 {
			return a.requireNoRuntimeHelpers(op)
		}
		return a.requireFrozenStructuredRuntimeHelpers(op, helpers...)
	}
	if (op.Op == token.EQL || op.Op == token.NEQ) &&
		coroPureDirectEqualityType(a.typeOf(op.X.Type())) &&
		coroPureDirectEqualityType(a.typeOf(op.Y.Type())) {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(op.Type())); err != nil {
			return "direct equality has unsupported result type: " + err.Error()
		}
		return a.requireNoRuntimeHelpers(op)
	}
	if (op.Op == token.EQL || op.Op == token.NEQ) &&
		coroFrameRetentionNilConst(op.X) && coroFrameRetentionNilConst(op.Y) {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(op.Type())); err != nil {
			return "nil constant equality has unsupported result type: " + err.Error()
		}
		// x/tools emits this exact constant comparison while lowering a nil
		// switch case for instantiated generic nilable types. LLSSA lowers both
		// operands as the same null pointer and emits one inline comparison.
		return a.requireNoRuntimeHelpers(op)
	}
	if op.Op == token.EQL || op.Op == token.NEQ || op.Op == token.LSS || op.Op == token.LEQ || op.Op == token.GTR || op.Op == token.GEQ {
		left, leftOK := types.Unalias(a.typeOf(op.X.Type())).Underlying().(*types.Basic)
		right, rightOK := types.Unalias(a.typeOf(op.Y.Type())).Underlying().(*types.Basic)
		if leftOK && rightOK && coroPhysicalStringBasic(left) && coroPhysicalStringBasic(right) {
			for _, typ := range []types.Type{a.typeOf(op.X.Type()), a.typeOf(op.Y.Type()), a.typeOf(op.Type())} {
				if err := validateCoroPhysicalSSAValueType(typ); err != nil {
					return "string comparison has unsupported physical value type: " + err.Error()
				}
			}
			helper := "StringLess"
			if op.Op == token.EQL || op.Op == token.NEQ {
				helper = "StringEqual"
			}
			// LLSSA implements equality and ordering with one exact helper;
			// <=/>=/!= only invert or swap its pure boolean result. The helper
			// must remain a demanded no-suspend/no-unwind body in the frozen plan.
			return a.requireFrozenExactRuntimeHelper(op, helper)
		}
	}
	if (op.Op == token.EQL || op.Op == token.NEQ) &&
		(coroInterfaceType(a.typeOf(op.X.Type())) && coroFrameRetentionNilConst(op.Y) ||
			coroInterfaceType(a.typeOf(op.Y.Type())) && coroFrameRetentionNilConst(op.X)) {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(op.Type())); err != nil {
			return "empty-interface nil comparison has unsupported result type: " + err.Error()
		}
		// Physical codegen compares the empty-interface type word directly. The
		// ordinary helper inventory still records LLSSA's EfaceEqual recipe (and
		// permits IfaceType for future interface normalization), but neither call
		// is emitted by this exact instruction.
		return a.requireOnlyCompilerElidedRuntimeHelpers(op, "EfaceEqual", "IfaceType")
	}
	if op.Op == token.EQL || op.Op == token.NEQ {
		leftInterface, leftOK := types.Unalias(a.typeOf(op.X.Type())).Underlying().(*types.Interface)
		rightInterface, rightOK := types.Unalias(a.typeOf(op.Y.Type())).Underlying().(*types.Interface)
		if leftOK || rightOK {
			for _, typ := range []types.Type{a.typeOf(op.X.Type()), a.typeOf(op.Y.Type()), a.typeOf(op.Type())} {
				if err := validateCoroPhysicalSSAValueType(typ); err != nil {
					return "interface equality has unsupported physical value type: " + err.Error()
				}
			}
			helpers := []string{"EfaceEqual"}
			if leftOK && !leftInterface.Empty() || rightOK && !rightInterface.Empty() {
				helpers = append(helpers, "IfaceType")
			}
			// LLSSA normalizes non-empty interfaces through IfaceType and then
			// compares the two dynamic values through EfaceEqual. EfaceEqual may
			// panic for an uncomparable dynamic type, so every helper must use its
			// exact owner-scoped plain/child-await lowering and a MayUnwind helper
			// must return through ExplicitStatus. This preserves ordinary Go
			// interface comparison semantics without native-stack unwinding across
			// the live LLVM coroutine frame.
			return a.requireFrozenStructuredRuntimeHelpers(op, helpers...)
		}
	}
	if (op.Op == token.EQL || op.Op == token.NEQ) &&
		((coroPureNilComparableType(a.typeOf(op.X.Type())) && coroFrameRetentionNilConst(op.Y)) ||
			(coroPureNilComparableType(a.typeOf(op.Y.Type())) && coroFrameRetentionNilConst(op.X))) {
		return a.requireNoRuntimeHelpers(op)
	}
	if coroPureComplexArithmeticBinOp(
		op.Op,
		a.typeOf(op.Type()),
		a.typeOf(op.X.Type()),
		a.typeOf(op.Y.Type()),
	) {
		for _, typ := range []types.Type{a.typeOf(op.Type()), a.typeOf(op.X.Type()), a.typeOf(op.Y.Type())} {
			if err := validateCoroPhysicalSSAValueType(typ); err != nil {
				return "complex arithmetic has unsupported physical value type: " + err.Error()
			}
		}
		if op.Op == token.QUO {
			// LLSSA implements Go's scale-safe complex division through this
			// exact helper. It neither has an integer-style zero-divisor panic
			// nor needs a special physical recipe; the normal owner-scoped
			// lowered-call resolver selects its proven plain or coroutine entry.
			return a.requireFrozenExactRuntimeHelper(op, "Complex128Div")
		}
		return a.requireNoRuntimeHelpers(op)
	}
	if !coroPureBasicScalar(a.typeOf(op.Type())) || !coroPureBasicScalar(a.typeOf(op.X.Type())) || !coroPureBasicScalar(a.typeOf(op.Y.Type())) {
		return "potentially panicking or non-scalar binary operation"
	}
	switch op.Op {
	case token.QUO, token.REM:
		operand, _ := types.Unalias(a.typeOf(op.X.Type())).Underlying().(*types.Basic)
		// Only integer division and remainder can panic on a zero divisor.
		// Floating-point division follows Go's IEEE-754 semantics and produces
		// infinities or NaNs, so it requires neither a panic helper nor a
		// non-zero dominance proof.
		if operand != nil && operand.Info()&types.IsInteger != 0 && !ssaIntegerValueProvenNonZeroAt(op.Y, op) {
			return a.requireFrozenOutcomeRuntimeHelper(op, "AssertDivideByZero")
		}
	case token.SHL, token.SHR:
		if signedIntegerMayBeNegative(op.Y) {
			// Builder.BinOp emits exactly one AssertNegativeShift predicate
			// before the LLVM shift. Under ExplicitStatus that potentially
			// panicking helper must be a managed outcome child, so a negative
			// count enters the parent's ordinary panic/cleanup path without
			// unwinding a native stack through the live coroutine frame.
			return a.requireFrozenOutcomeRuntimeHelper(op, "AssertNegativeShift")
		}
	}
	return a.requireNoRuntimeHelpers(op)
}

func coroPureComplexArithmeticBinOp(op token.Token, values ...types.Type) bool {
	switch op {
	case token.ADD, token.SUB, token.MUL, token.QUO:
	default:
		return false
	}
	if len(values) == 0 {
		return false
	}
	for _, typ := range values {
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		if !ok || basic.Info()&types.IsComplex == 0 {
			return false
		}
	}
	return true
}

func coroEmptyInterfaceType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	iface, ok := types.Unalias(typ).Underlying().(*types.Interface)
	if !ok {
		return false
	}
	iface.Complete()
	return iface.Empty()
}

func coroInterfaceType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	_, ok := types.Unalias(typ).Underlying().(*types.Interface)
	return ok
}

func coroPureStringType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
}

func coroPureAggregateType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch types.Unalias(typ).Underlying().(type) {
	case *types.Array, *types.Struct:
		return true
	default:
		return false
	}
}

// coroAggregateEqualityRuntimeHelpers mirrors the complete comparable
// aggregate recursion in LLSSA Builder.BinOp and records its unique logical
// helper leaves. StringEqual is non-panicking; EfaceEqual may panic for a
// dynamically uncomparable payload and is therefore accepted only later by
// the structured ExplicitStatus helper gate. Blank struct fields are not
// compared by Go or LLSSA and contribute no leaf requirement.
func coroAggregateEqualityRuntimeHelpers(
	typ types.Type,
	visiting map[types.Type]bool,
	helpers map[string]struct{},
) bool {
	if typ == nil || visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch underlying := types.Unalias(typ).Underlying().(type) {
	case *types.Basic:
		if underlying.Kind() == types.String {
			helpers["StringEqual"] = struct{}{}
			return true
		}
		return underlying.Kind() == types.UnsafePointer ||
			underlying.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat|types.IsComplex) != 0
	case *types.Pointer, *types.Chan:
		return true
	case *types.Interface:
		helpers["EfaceEqual"] = struct{}{}
		underlying.Complete()
		if !underlying.Empty() {
			helpers["IfaceType"] = struct{}{}
		}
		return true
	case *types.Array:
		return coroAggregateEqualityRuntimeHelpers(underlying.Elem(), visiting, helpers)
	case *types.Struct:
		for index := 0; index < underlying.NumFields(); index++ {
			field := underlying.Field(index)
			if field.Name() == "_" {
				continue
			}
			if !coroAggregateEqualityRuntimeHelpers(field.Type(), visiting, helpers) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// coroPureDirectEqualityType is deliberately narrower than Go's comparable
// set. These representations lower to target-local scalar comparisons and
// cannot invoke user/runtime code or panic. Interfaces and aggregate values
// remain outside this gate; map, slice, and function values remain legal only
// through the existing comparison-to-nil path.
func coroPureDirectEqualityType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch underlying := types.Unalias(typ).Underlying().(type) {
	case *types.Pointer, *types.Chan:
		return true
	case *types.Basic:
		return underlying.Kind() == types.UnsafePointer || underlying.Info()&types.IsComplex != 0
	default:
		return false
	}
}

func coroPureNilComparableType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch underlying := types.Unalias(typ).Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return true
	case *types.Basic:
		return underlying.Kind() == types.UnsafePointer
	default:
		return false
	}
}

func (a *coroPhysicalPureSSAAudit) validateUnOp(op *ssa.UnOp) string {
	if op == nil || op.X == nil {
		return "incomplete unary operation"
	}
	if op.Op != token.MUL {
		if !coroPureBasicScalar(a.typeOf(op.Type())) {
			return "unsupported unary operation"
		}
		return a.requireNoRuntimeHelpers(op)
	}
	if _, reason := a.stableAddressAt(op.X, op, make(map[ssa.Value]bool)); reason != "" {
		return "typed load: " + reason
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(op.Type())); err != nil {
		return "typed load has unsupported value type: " + err.Error()
	}
	// A zero-sized load still has Go's nil-dereference semantics. Physical
	// coroutine code emits the same explicit-status nil guard as an ordinary
	// load and then materializes the zero value without touching memory. The
	// legacy AssertNilDeref inventory entry is therefore compiler-elided by the
	// independently validated frame-retention/fault proof below.
	if a.allowImplicitNilFault {
		proof := a.currentFrameRetentionProof()
		if proof != nil && proof.requiresImplicitNilFault(op.X, op) {
			// Value-receiver calls use AssertNilDerefPtr on the native stack so
			// the checked pointer remains available to the subsequent load. The
			// physical coroutine recipe preserves that same base value across its
			// explicit-status branch and therefore elides either helper spelling.
			return a.requireOnlyCompilerElidedRuntimeHelpers(op, "AssertNilDeref", "AssertNilDerefPtr")
		}
	}
	return a.requireNoRuntimeHelpersExcept(op, "AssertNilDeref", "AssertNilDerefPtr")
}

func (a *coroPhysicalPureSSAAudit) validateStore(store *ssa.Store) string {
	if store == nil || store.Addr == nil || store.Val == nil {
		return "incomplete typed store"
	}
	if index, ok := store.Addr.(*ssa.IndexAddr); ok && a.ctx != nil && emissionIsVargsAlloc(a.ctx, index.X) {
		value := store.Val
		if boxed, ok := value.(*ssa.MakeInterface); ok {
			value = boxed.X
		}
		if value == nil {
			return "synthetic varargs store has no concrete operand"
		}
		if err := validateCoroPhysicalSSAValueType(a.typeOf(value.Type())); err != nil {
			return "synthetic varargs operand has unsupported type: " + err.Error()
		}
		return ""
	}
	root, reason := a.stableAddressAt(store.Addr, store, make(map[ssa.Value]bool))
	if reason != "" {
		return "typed store: " + reason
	}
	pointer, ok := types.Unalias(a.typeOf(store.Addr.Type())).Underlying().(*types.Pointer)
	if !ok || !types.Identical(pointer.Elem(), a.typeOf(store.Val.Type())) {
		return "typed store address/value types do not match"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(store.Val.Type())); err != nil {
		return "typed store has unsupported value type: " + err.Error()
	}
	if root == coroPhysicalAddressGlobal &&
		coroTypeContainsGCPointer(a.typeOf(store.Val.Type()), make(map[types.Type]bool)) &&
		!a.coroBarrierFreeGlobalStoreProfile() {
		return "global typed store of a pointer-containing value requires explicit write-barrier lowering"
	}
	// A pointer-containing frame-local, exact managed-heap, or certified global
	// store is accepted only under PhysicalABIV1's current non-moving
	// conservative/non-collecting profile. BDWGC and tinygogc rescan globals;
	// nogc targets retain them for the process lifetime. This is not evidence
	// that precise frame maps, relocation, or write barriers are implemented.
	return a.requireNoRuntimeHelpers(store)
}

func (a *coroPhysicalPureSSAAudit) coroBarrierFreeGlobalStoreProfile() bool {
	if a == nil || emitShadowStackInstrumentation {
		return false
	}
	switch a.frameRetentionABI {
	case CoroFrameRetentionParkABIV2:
		// These are the only active identities backed by the frozen
		// physical-v1.nonmoving-conservative-or-none root profile. A future
		// precise or moving collector must use a new identity and remains
		// rejected above until it supplies real global write barriers.
		return true
	default:
		return false
	}
}

func (a *coroPhysicalPureSSAAudit) validateBuiltin(call *ssa.Call) string {
	if call == nil || call.Call.Value == nil {
		return "unsupported builtin call in pure coroutine body"
	}
	builtin, ok := call.Call.Value.(*ssa.Builtin)
	if !ok {
		return "dynamic/non-builtin call is outside pure SSA lowering"
	}
	switch builtin.Name() {
	case "Sizeof", "Alignof":
		if len(call.Call.Args) != 1 || call.Type() == nil ||
			!types.Identical(a.typeOf(call.Type()), types.Typ[types.Uintptr]) {
			return builtin.Name() + " builtin requires one type operand and a uintptr result"
		}
		operand := a.typeOf(call.Call.Args[0].Type())
		if operand == nil || coroTypeContainsUnresolvedTypeParam(operand, make(map[types.Type]bool)) {
			return builtin.Name() + " builtin has no concrete physical operand type"
		}
		// The operand is deliberately not validated as a live SSA value:
		// collectUnsafeLayoutUnevaluatedSSA removes its type-only producer
		// graph, and compileUnsafeSizeAlignBuiltin emits one target-derived
		// integer constant without a runtime edge.
		return a.requireNoRuntimeHelpers(call)
	case "Offsetof":
		if len(call.Call.Args) != 1 || call.Type() == nil ||
			!types.Identical(a.typeOf(call.Type()), types.Typ[types.Uintptr]) {
			return "Offsetof builtin requires one field operand and a uintptr result"
		}
		if a.ctx == nil {
			return "Offsetof builtin has no exact physical layout context"
		}
		if _, ok := a.ctx.offsetOfBuiltinArg(call.Call.Args[0]); !ok {
			return "Offsetof builtin operand is outside the exact field-layout lowering"
		}
		// collectUnsafeLayoutUnevaluatedSSA erases the selector's complete
		// producer graph. offsetOfBuiltinArg above proves that codegen selects
		// the same target-derived integer constant and no runtime edge.
		return a.requireNoRuntimeHelpers(call)
	case "ssa:wrapnilchk":
		if len(call.Call.Args) != 3 || call.Type() == nil ||
			!types.Identical(a.typeOf(call.Type()), a.typeOf(call.Call.Args[0].Type())) {
			return "ssa:wrapnilchk builtin has an invalid receiver/result shape"
		}
		if _, ok := types.Unalias(a.typeOf(call.Call.Args[0].Type())).Underlying().(*types.Pointer); !ok {
			return "ssa:wrapnilchk receiver is not pointer-shaped"
		}
		for _, index := range []int{1, 2} {
			basic, ok := types.Unalias(a.typeOf(call.Call.Args[index].Type())).Underlying().(*types.Basic)
			if !ok || basic.Kind() != types.String {
				return "ssa:wrapnilchk metadata is not string-shaped"
			}
		}
		if err := validateCoroPhysicalSSAValueType(a.typeOf(call.Type())); err != nil {
			return "builtin result has unsupported type: " + err.Error()
		}
		if a.allowImplicitNilFault {
			// ExplicitStatus codegen owns this exact synthetic guard: it emits an
			// inline pointer test, materializes the source-specific plainError
			// payload through the compiler/runtime ABI, and publishes it through
			// the ordinary structured panic handoff. The legacy
			// PanicWrapNilPointer helper is therefore not called from this
			// physical coroutine body and needs no hidden unwind contract here.
			return ""
		}
		// LLSSA lowers this synthetic wrapper guard to a pointer comparison and
		// the same terminal PanicWrapNilPointer edge used by ordinary checked
		// dereferences. The helper cannot return to the live coroutine frame.
		return a.requireFrozenTerminalRuntimeHelpers(call, "PanicWrapNilPointer")
	case "ssa:deferstack":
		if len(call.Call.Args) != 0 || call.Type() == nil ||
			coroExplicitDeferStackOwner(a.fn) != a.fn ||
			len(coroExplicitCleanupFamilySites(a.fn)) == 0 {
			return "ssa:deferstack builtin has no exact range-over-func cleanup owner"
		}
		if _, ok := types.Unalias(a.typeOf(call.Type())).Underlying().(*types.Pointer); !ok {
			return "ssa:deferstack result is not pointer-shaped"
		}
		return a.requireNoRuntimeHelpers(call)
	case "len":
		return a.validateLenBuiltin(call)
	case "cap":
		return a.validateCapBuiltin(call)
	case "append":
		return a.validateAppendBuiltin(call)
	case "copy":
		return a.validateCopyBuiltin(call)
	case "complex":
		return a.validateComplexBuiltin(call)
	case "real", "imag":
		if reason := a.validateComplexComponentBuiltin(call, builtin.Name()); reason != "" {
			return reason
		}
	case "min", "max":
		return a.validateMinMaxBuiltin(call, builtin.Name())
	case "print", "println":
		return a.validatePrintBuiltin(call, builtin.Name())
	case "delete":
		return a.validateDeleteBuiltin(call)
	case "clear":
		return a.validateClearBuiltin(call)
	case "close":
		return a.validateCloseBuiltin(call)
	case "recover":
		if !a.allowExplicitRecover || len(call.Call.Args) != 0 || call.Type() == nil {
			return "recover builtin requires the explicit-status physical ABI and zero arguments"
		}
		result, ok := types.Unalias(a.typeOf(call.Type())).Underlying().(*types.Interface)
		if !ok || !result.Empty() {
			return "recover builtin result is not one empty interface"
		}
		if err := validateCoroPhysicalSSAValueType(a.typeOf(call.Type())); err != nil {
			return "recover builtin result has unsupported type: " + err.Error()
		}
		return a.requireOnlyCompilerElidedRuntimeHelpers(call, "Recover")
	case "Add":
		if !coroPhysicalUnsafeAddCall(call, a.typeOf) {
			return "unsafe.Add builtin has an invalid frozen pointer/integer shape"
		}
	case "String":
		return a.validateUnsafeStringBuiltin(call)
	case "Slice":
		return a.validateUnsafeSliceBuiltin(call)
	case "StringData", "SliceData":
		return a.validateUnsafeDataBuiltin(call, builtin.Name())
	default:
		return fmt.Sprintf("builtin %q is outside the pure coroutine lowering slice", builtin.Name())
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(call.Type())); err != nil {
		return "builtin result has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(call)
}

type coroPhysicalLenOperandKind uint8

const (
	coroPhysicalLenUnsupported coroPhysicalLenOperandKind = iota
	coroPhysicalLenInline
	coroPhysicalLenMap
	coroPhysicalLenChan
)

// coroPhysicalLenKind accepts only one concrete lowering selected from the
// Go type of the SSA operand. A map whose key or element remains parameterized
// is still exact: len observes only the map header. A bare type parameter or
// interface is deliberately rejected because its type set may require
// different string/slice/map/channel lowerings at different instantiations.
func coroPhysicalLenKind(typ types.Type) coroPhysicalLenOperandKind {
	if typ == nil {
		return coroPhysicalLenUnsupported
	}
	switch operand := types.Unalias(typ).Underlying().(type) {
	case *types.Slice:
		return coroPhysicalLenInline
	case *types.Map:
		return coroPhysicalLenMap
	case *types.Chan:
		return coroPhysicalLenChan
	case *types.Basic:
		if operand.Kind() == types.String {
			return coroPhysicalLenInline
		}
	}
	return coroPhysicalLenUnsupported
}

func (a *coroPhysicalPureSSAAudit) validateLenBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 || call.Type() == nil ||
		!types.Identical(a.typeOf(call.Type()), types.Typ[types.Int]) {
		return "len builtin has an invalid argument/result shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "len" {
		return "len validation requires the exact builtin call"
	}
	argument := call.Common().Args[0]
	if argument == nil {
		return "len builtin has a nil operand"
	}
	switch coroPhysicalLenKind(a.typeOf(argument.Type())) {
	case coroPhysicalLenInline:
		return a.requireNoRuntimeHelpers(call)
	case coroPhysicalLenMap:
		// Builder.BuiltinCall lowers this exact form through MapLen. Bind the
		// occurrence to its owner-scoped helper plan instead of treating the
		// helper name or every generic len operand as intrinsically pure.
		return a.requireFrozenExactRuntimeHelper(call, "MapLen")
	case coroPhysicalLenChan:
		// Channel direction and element type do not alter the header operation.
		// The observable timer-channel view and channel lock still belong to the
		// exact owner-scoped ChanLen helper rather than an inline load.
		return a.requireFrozenExactRuntimeHelper(call, "ChanLen")
	default:
		return "len builtin has no concrete slice, string, map, or channel lowering"
	}
}

type coroPhysicalCapOperandKind uint8

const (
	coroPhysicalCapUnsupported coroPhysicalCapOperandKind = iota
	coroPhysicalCapInline
	coroPhysicalCapChan
)

// coroPhysicalCapKind admits only outer representations whose physical cap
// lowering is fixed without inspecting a type set. A parameterized slice or
// channel remains exact; a bare type parameter or interface does not.
func coroPhysicalCapKind(typ types.Type) coroPhysicalCapOperandKind {
	if typ == nil {
		return coroPhysicalCapUnsupported
	}
	switch types.Unalias(typ).Underlying().(type) {
	case *types.Slice:
		return coroPhysicalCapInline
	case *types.Chan:
		return coroPhysicalCapChan
	default:
		return coroPhysicalCapUnsupported
	}
}

func (a *coroPhysicalPureSSAAudit) validateCapBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 || call.Type() == nil ||
		!types.Identical(a.typeOf(call.Type()), types.Typ[types.Int]) {
		return "cap builtin has an invalid argument/result shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "cap" {
		return "cap validation requires the exact builtin call"
	}
	argument := call.Common().Args[0]
	if argument == nil {
		return "cap builtin has a nil operand"
	}
	switch coroPhysicalCapKind(a.typeOf(argument.Type())) {
	case coroPhysicalCapInline:
		return a.requireNoRuntimeHelpers(call)
	case coroPhysicalCapChan:
		return a.requireFrozenExactRuntimeHelper(call, "ChanCap")
	default:
		return "cap builtin has no concrete slice or channel lowering"
	}
}

// validateClearBuiltin freezes Builder.BuiltinCall's two Go-defined clear
// forms. Slice clearing delegates to SliceClear (including the element-width
// calculation and target memset); map clearing delegates to MapClear. Neither
// form returns a value, and the selected helper must remain in the exact
// owner-scoped lowering plan so future GC/write-barrier work cannot silently
// turn a direct call into an unsafe native-stack suspension.
func (a *coroPhysicalPureSSAAudit) validateClearBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 {
		return "clear builtin has an invalid argument/result shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "clear" {
		return "clear validation requires the exact builtin call"
	}
	if result := call.Type(); result != nil {
		tuple, ok := types.Unalias(a.typeOf(result)).Underlying().(*types.Tuple)
		if !ok || tuple.Len() != 0 {
			return "clear builtin has an invalid argument/result shape"
		}
	}
	argument := call.Common().Args[0]
	if argument == nil {
		return "clear builtin has a nil operand"
	}
	argumentType := a.typeOf(argument.Type())
	var helper string
	switch types.Unalias(argumentType).Underlying().(type) {
	case *types.Slice:
		helper = "SliceClear"
	case *types.Map:
		helper = "MapClear"
	default:
		return "clear builtin operand is neither a slice nor a map"
	}
	if err := validateCoroPhysicalSSAValueType(argumentType); err != nil {
		return "clear builtin operand has unsupported physical type: " + err.Error()
	}
	return a.requireFrozenExactRuntimeHelper(call, helper)
}

// validateCloseBuiltin binds close(ch) to the coroutine runtime's
// non-panicking scalar outcome helper. Nil and already-closed errors are
// published by compiler-owned explicit status, never by unwinding through the
// live LLVM frame.
func (a *coroPhysicalPureSSAAudit) validateCloseBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 {
		return "close builtin has an invalid argument/result shape"
	}
	if result := call.Type(); result != nil {
		tuple, ok := types.Unalias(a.typeOf(result)).Underlying().(*types.Tuple)
		if !ok || tuple.Len() != 0 {
			return "close builtin has an invalid argument/result shape"
		}
	}
	if _, ok := types.Unalias(a.typeOf(call.Common().Args[0].Type())).Underlying().(*types.Chan); !ok {
		return "close builtin argument is not a channel"
	}
	if !a.allowImplicitNilFault {
		return "close builtin requires the explicit-status panic ABI"
	}
	return a.requireFrozenExactRuntimeHelper(call, "CoroChanTryClose")
}

// validateUnsafeDataBuiltin accepts only the two header projection intrinsics.
// LLSSA lowers both to extractvalue of the already-materialized Go
// string/slice header; there is no allocation, bounds check, panic edge, or
// hidden runtime call. Pointer lifetime remains governed by the ordinary
// coroutine value/keepalive analysis of the source aggregate.
func (a *coroPhysicalPureSSAAudit) validateUnsafeDataBuiltin(call *ssa.Call, name string) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 || call.Type() == nil {
		return "unsafe." + name + " builtin has an invalid call shape"
	}
	argument := a.typeOf(call.Common().Args[0].Type())
	result := a.typeOf(call.Type())
	if argument == nil || result == nil {
		return "unsafe." + name + " builtin has no concrete argument/result type"
	}
	pointer, ok := types.Unalias(result).(*types.Pointer)
	if !ok {
		return "unsafe." + name + " result is not pointer-shaped"
	}
	switch name {
	case "StringData":
		basic, ok := types.Unalias(argument).Underlying().(*types.Basic)
		if !ok || basic.Kind() != types.String || !types.Identical(pointer.Elem(), types.Typ[types.Byte]) {
			return "unsafe.StringData requires one string argument and a *byte result"
		}
	case "SliceData":
		slice, ok := types.Unalias(argument).Underlying().(*types.Slice)
		if !ok || !types.Identical(pointer.Elem(), slice.Elem()) {
			return "unsafe.SliceData requires one []T argument and a *T result"
		}
	default:
		return "unsupported unsafe data builtin " + name
	}
	if err := validateCoroPhysicalSSAValueType(result); err != nil {
		return "unsafe." + name + " result has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(call)
}

// validateDeleteBuiltin binds the language builtin to the same owner-scoped
// map-key allocation and MapDelete helpers used by ordinary LLSSA lowering.
// In particular, delete is not assumed non-blocking: each helper must still be
// proven plain/no-unwind or represented as a managed coroutine child.
func (a *coroPhysicalPureSSAAudit) validateDeleteBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil {
		return "delete builtin has an invalid call shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "delete" || len(call.Common().Args) != 2 {
		return "delete validation requires the exact two-argument builtin call"
	}
	mapping, key := call.Common().Args[0], call.Common().Args[1]
	if mapping == nil || key == nil {
		return "delete builtin has a nil map or key operand"
	}
	mapType, ok := types.Unalias(a.typeOf(mapping.Type())).Underlying().(*types.Map)
	if !ok {
		return "delete target is not a map"
	}
	if !types.Identical(a.typeOf(key.Type()), a.typeOf(mapType.Key())) {
		return "delete key does not match the map key type"
	}
	for name, typ := range map[string]types.Type{"map": mapping.Type(), "key": key.Type()} {
		if err := validateCoroPhysicalSSAValueType(a.typeOf(typ)); err != nil {
			return "delete " + name + " has unsupported type: " + err.Error()
		}
	}
	return a.requireFrozenStructuredRuntimeHelpers(call, "AllocU", "MapDelete")
}

// validatePrintBuiltin freezes Builder.PrintEx's exact lowering. Printing is
// not classified as a pure/no-block operation: every emitted Print* helper is
// an ordinary owner-scoped managed edge. Consequently a helper that reaches a
// potentially blocking host output call must itself be represented by a
// coroutine (and awaited here); only a plan-proven NoSuspend/NoUnwind helper
// may remain a direct plain call.
func (a *coroPhysicalPureSSAAudit) validatePrintBuiltin(call *ssa.Call, name string) string {
	if call == nil || call.Common() == nil || name != "print" && name != "println" {
		return "print builtin has an invalid call shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != name {
		return "print validation requires the exact builtin call"
	}
	helperSet := make(map[string]struct{}, len(call.Common().Args)+1)
	for index, argument := range call.Common().Args {
		if argument == nil {
			return fmt.Sprintf("%s builtin argument %d is nil", name, index)
		}
		typ := a.typeOf(argument.Type())
		helper := runtimePrintHelper(typ)
		if helper == "" {
			return fmt.Sprintf("%s builtin argument %d has unsupported type %s", name, index, typ)
		}
		helperSet[helper] = struct{}{}
		if err := validateCoroPhysicalSSAValueType(typ); err != nil {
			return fmt.Sprintf("%s builtin argument %d has unsupported physical type: %v", name, index, err)
		}
	}

	// print() emits nothing. println(), including println(), always emits the
	// trailing newline through PrintByte, so it remains a managed helper edge.
	if name == "print" && len(call.Common().Args) == 0 {
		return ""
	}
	if name == "println" {
		helperSet["PrintByte"] = struct{}{}
	}
	if a == nil || a.ctx == nil || a.universe == nil {
		return "structured runtime helper validation requires a frozen emission universe"
	}
	expected := make([]string, 0, len(helperSet))
	for helper := range helperSet {
		expected = append(expected, helper)
	}
	sort.Strings(expected)
	if len(expected) == 0 {
		return name + " builtin has no exact lowered runtime helper inventory"
	}
	return a.requireFrozenStructuredRuntimeHelpers(call, expected...)
}

// validateMinMaxBuiltin mirrors Builder.compareSelect: ordered scalar values
// are lowered to comparisons plus LLVM selects. String ordering additionally
// uses the owner-scoped runtime.StringLess edge for every comparison.
func (a *coroPhysicalPureSSAAudit) validateMinMaxBuiltin(call *ssa.Call, name string) string {
	if call == nil || call.Common() == nil || call.Type() == nil || name != "min" && name != "max" || len(call.Common().Args) == 0 {
		return name + " builtin has an invalid argument/result shape"
	}
	result := a.typeOf(call.Type())
	basic, ok := types.Unalias(result).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsUntyped != 0 ||
		basic.Info()&(types.IsInteger|types.IsFloat) == 0 && basic.Kind() != types.String {
		return name + " builtin result is not one ordered concrete basic type"
	}
	if err := validateCoroPhysicalSSAValueType(result); err != nil {
		return name + " builtin result has unsupported physical type: " + err.Error()
	}
	for index, argument := range call.Common().Args {
		if argument == nil {
			return fmt.Sprintf("%s builtin argument %d is nil", name, index)
		}
		argumentType := a.typeOf(argument.Type())
		if !types.Identical(argumentType, result) {
			return fmt.Sprintf("%s builtin argument %d type %s differs from result type %s", name, index, argumentType, result)
		}
		if err := validateCoroPhysicalSSAValueType(argumentType); err != nil {
			return fmt.Sprintf("%s builtin argument %d has unsupported physical type: %v", name, index, err)
		}
	}
	if basic.Kind() == types.String && len(call.Common().Args) > 1 {
		return a.requireFrozenExactRuntimeHelper(call, "StringLess")
	}
	return a.requireNoRuntimeHelpers(call)
}

// validateAppendBuiltin freezes the exact x/tools SSA shape consumed by
// Builder.BuiltinCall. Ordinary scalar append operands have already been
// materialized as the second slice argument by x/tools; the only non-slice
// source shape is Go's append([]byte, string...) special case.
func (a *coroPhysicalPureSSAAudit) validateAppendBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 2 || call.Type() == nil {
		return "append builtin has an invalid argument/result shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "append" {
		return "append validation requires the exact builtin call"
	}
	destinationType := a.typeOf(call.Common().Args[0].Type())
	resultType := a.typeOf(call.Type())
	if !types.Identical(destinationType, resultType) {
		return "append destination and result slice types differ"
	}
	destination, ok := types.Unalias(destinationType).Underlying().(*types.Slice)
	if !ok {
		return "append destination is not a slice"
	}
	sourceType := a.typeOf(call.Common().Args[1].Type())
	switch source := types.Unalias(sourceType).Underlying().(type) {
	case *types.Slice:
		if !types.Identical(a.typeOf(destination.Elem()), a.typeOf(source.Elem())) {
			return "append source and destination element types differ"
		}
	case *types.Basic:
		if source.Kind() != types.String ||
			!types.Identical(types.Unalias(a.typeOf(destination.Elem())), types.Typ[types.Byte]) {
			return "append non-slice source is not the []byte/string special case"
		}
	default:
		return "append source is neither a compatible slice nor string"
	}
	for _, typ := range []types.Type{destinationType, sourceType, resultType} {
		if err := validateCoroPhysicalSSAValueType(typ); err != nil {
			return "append has unsupported physical value type: " + err.Error()
		}
	}
	return a.requireFrozenOutcomeRuntimeHelper(call, "SliceAppend")
}

// validateCopyBuiltin freezes Builder.BuiltinCall's two legal forms: copying
// between slices with identical element types, and the []byte <- string
// special case. Both lower through the overlap-safe SliceCopy helper and
// return the built-in int type.
func (a *coroPhysicalPureSSAAudit) validateCopyBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 2 || call.Type() == nil {
		return "copy builtin has an invalid argument/result shape"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "copy" {
		return "copy validation requires the exact builtin call"
	}
	if !types.Identical(a.typeOf(call.Type()), types.Typ[types.Int]) {
		return "copy builtin result is not the built-in int type"
	}
	destinationType := a.typeOf(call.Common().Args[0].Type())
	destination, ok := types.Unalias(destinationType).Underlying().(*types.Slice)
	if !ok {
		return "copy destination is not a slice"
	}
	sourceType := a.typeOf(call.Common().Args[1].Type())
	switch source := types.Unalias(sourceType).Underlying().(type) {
	case *types.Slice:
		if !types.Identical(a.typeOf(destination.Elem()), a.typeOf(source.Elem())) {
			return "copy source and destination element types differ"
		}
	case *types.Basic:
		if source.Kind() != types.String ||
			!types.Identical(types.Unalias(a.typeOf(destination.Elem())), types.Typ[types.Byte]) {
			return "copy non-slice source is not the []byte/string special case"
		}
	default:
		return "copy source is neither a compatible slice nor string"
	}
	for _, typ := range []types.Type{destinationType, sourceType, a.typeOf(call.Type())} {
		if err := validateCoroPhysicalSSAValueType(typ); err != nil {
			return "copy has unsupported physical value type: " + err.Error()
		}
	}
	return a.requireFrozenExactRuntimeHelper(call, "SliceCopy")
}

// validateComplexBuiltin mirrors Builder.BuiltinCall's aggregate construction.
// Both operands have Go's already-converted component type and the result is
// the corresponding two-field complex scalar; no runtime helper is emitted.
func (a *coroPhysicalPureSSAAudit) validateComplexBuiltin(call *ssa.Call) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 2 || call.Type() == nil {
		return "complex builtin requires two floating-point arguments and one result"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "complex" {
		return "complex validation requires the exact complex builtin"
	}
	left := a.typeOf(call.Common().Args[0].Type())
	right := a.typeOf(call.Common().Args[1].Type())
	result := a.typeOf(call.Type())
	leftBasic, leftOK := types.Unalias(left).Underlying().(*types.Basic)
	rightBasic, rightOK := types.Unalias(right).Underlying().(*types.Basic)
	resultBasic, resultOK := types.Unalias(result).Underlying().(*types.Basic)
	if !leftOK || !rightOK || !resultOK || !types.Identical(left, right) {
		return "complex builtin operands do not have one identical concrete floating-point type"
	}
	want := types.Invalid
	switch leftBasic.Kind() {
	case types.Float32:
		want = types.Complex64
	case types.Float64:
		want = types.Complex128
	default:
		return "complex builtin operands are not float32 or float64"
	}
	if rightBasic.Kind() != leftBasic.Kind() || resultBasic.Kind() != want {
		return fmt.Sprintf("complex builtin result kind is %s, want %s", resultBasic, types.Typ[want])
	}
	for _, typ := range []types.Type{left, right, result} {
		if err := validateCoroPhysicalSSAValueType(typ); err != nil {
			return "complex builtin has unsupported physical type: " + err.Error()
		}
	}
	return a.requireNoRuntimeHelpers(call)
}

// validateComplexComponentBuiltin mirrors Builder.BuiltinCall's extractvalue
// lowering. Go fixes the component type: complex64 yields float32 and
// complex128 yields float64, including when the operand has a defined type
// whose underlying type is complex.
func (a *coroPhysicalPureSSAAudit) validateComplexComponentBuiltin(call *ssa.Call, name string) string {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 1 || call.Type() == nil {
		return name + " builtin requires one complex argument and one result"
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != name || name != "real" && name != "imag" {
		return "complex component validation requires the exact real/imag builtin"
	}
	operand, ok := types.Unalias(a.typeOf(call.Common().Args[0].Type())).Underlying().(*types.Basic)
	if !ok {
		return name + " builtin argument is not complex"
	}
	result, ok := types.Unalias(a.typeOf(call.Type())).Underlying().(*types.Basic)
	if !ok {
		return name + " builtin result is not floating point"
	}
	want := types.Invalid
	switch operand.Kind() {
	case types.Complex64:
		want = types.Float32
	case types.Complex128:
		want = types.Float64
	default:
		return name + " builtin argument is not complex"
	}
	if result.Kind() != want {
		return fmt.Sprintf("%s builtin result kind is %s, want %s", name, result, types.Typ[want])
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(call.Common().Args[0].Type())); err != nil {
		return name + " builtin argument has unsupported physical type: " + err.Error()
	}
	return ""
}

// coroPhysicalUnsafeAddCall mirrors LLSSA's inline Advance lowering. It only
// recognizes the exact go/ssa shape of unsafe.Add; the eventual dereference
// still needs its own non-nil/address-retention proof.
func coroPhysicalUnsafeAddCall(call *ssa.Call, patch func(types.Type) types.Type) bool {
	if call == nil || call.Common() == nil || len(call.Common().Args) != 2 {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || builtin.Name() != "Add" {
		return false
	}
	typeOf := func(typ types.Type) types.Type {
		if patch != nil {
			return patch(typ)
		}
		return typ
	}
	if !coroFrameRetentionUnsafePointer(typeOf(call.Common().Args[0].Type())) ||
		!coroFrameRetentionUnsafePointer(typeOf(call.Type())) {
		return false
	}
	offset, ok := types.Unalias(typeOf(call.Common().Args[1].Type())).Underlying().(*types.Basic)
	return ok && offset.Info()&types.IsInteger != 0
}

type coroPhysicalAddressRoot uint8

const (
	coroPhysicalAddressInvalid coroPhysicalAddressRoot = iota
	coroPhysicalAddressLocal
	coroPhysicalAddressManagedHeap
	coroPhysicalAddressGlobal
)

// stableAddress accepts statically non-nil package/current-frame storage plus
// exact parameter/slice-derived address uses present in the immutable frame-
// retention proof. A parameter's pointer-shaped type alone is never evidence:
// each dereference must carry a dominating non-nil/non-empty fact.
func (a *coroPhysicalPureSSAAudit) stableAddress(value ssa.Value, visiting map[ssa.Value]bool) (coroPhysicalAddressRoot, string) {
	return a.stableAddressAt(value, nil, visiting)
}

func (a *coroPhysicalPureSSAAudit) stableAddressAt(value ssa.Value, use ssa.Instruction, visiting map[ssa.Value]bool) (coroPhysicalAddressRoot, string) {
	if value == nil {
		return coroPhysicalAddressInvalid, "nil address"
	}
	if proof := a.currentFrameRetentionProof(); proof != nil && proof.provesDominatedStableAddress(value, use) {
		if root, known := a.provenCoroPhysicalAddressRoot(value, make(map[ssa.Value]bool)); known {
			return root, ""
		}
		return coroPhysicalAddressLocal, ""
	}
	// An address accepted under the explicit-status ABI is dereferenced only on
	// the normal edge of its compiler-inserted nil guard. Transport/root
	// provenance was frozen independently above; this path never treats a
	// pointer-shaped type alone as a lifetime proof.
	if a.allowImplicitNilFault {
		if proof := a.currentFrameRetentionProof(); proof != nil && proof.provesGuardableStableAddress(value, use) {
			if root, known := a.provenCoroPhysicalAddressRoot(value, make(map[ssa.Value]bool)); known {
				return root, ""
			}
			return coroPhysicalAddressLocal, ""
		}
	}
	if visiting[value] {
		return coroPhysicalAddressInvalid, "cyclic address expression"
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.Global:
		if _, ok := types.Unalias(a.typeOf(value.Type())).Underlying().(*types.Pointer); !ok {
			return coroPhysicalAddressInvalid, "global address does not have pointer type"
		}
		return coroPhysicalAddressGlobal, ""
	case *ssa.Alloc:
		if a.frameRetainsManagedHeapAllocation(value) {
			return coroPhysicalAddressManagedHeap, ""
		}
		if value.Heap {
			if !a.frameRetainsAllocation(value) {
				return coroPhysicalAddressInvalid, "heap allocation requires managed allocation/root lowering"
			}
		}
		if a.ctx != nil && (a.ctx.skipSyntheticMakeSliceAlloc(value) || isEmissionVargsAlloc(a.ctx, value)) {
			return coroPhysicalAddressInvalid, "synthetic slice/varargs storage is not a standalone local address"
		}
		return coroPhysicalAddressLocal, ""
	case *ssa.FieldAddr:
		pointer, ok := types.Unalias(a.typeOf(value.X.Type())).Underlying().(*types.Pointer)
		if !ok {
			return coroPhysicalAddressInvalid, "field base is not a pointer"
		}
		structure, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Struct)
		if !ok || value.Field < 0 || value.Field >= structure.NumFields() {
			return coroPhysicalAddressInvalid, "field address is outside its frozen struct shape"
		}
		return a.stableAddressAt(value.X, use, visiting)
	case *ssa.IndexAddr:
		pointer, ok := types.Unalias(a.typeOf(value.X.Type())).Underlying().(*types.Pointer)
		if !ok {
			return coroPhysicalAddressInvalid, "index base is not a fixed-array pointer"
		}
		array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
		if !ok || !coroConstantIndexInBounds(value.Index, array.Len()) {
			return coroPhysicalAddressInvalid, "index may panic; address indexing requires a compile-time in-range fixed-array index"
		}
		return a.stableAddressAt(value.X, use, visiting)
	default:
		return coroPhysicalAddressInvalid, fmt.Sprintf(
			"address root %T has no exact non-nil frame-retention proof (%s)",
			value, coroPhysicalAddressDiagnostic(value, 0, make(map[ssa.Value]bool)),
		)
	}
}

func coroPhysicalAddressDiagnostic(value ssa.Value, depth int, visiting map[ssa.Value]bool) string {
	if value == nil {
		return "nil"
	}
	if depth >= 6 || visiting[value] {
		return fmt.Sprintf("%T:%s", value, value.Name())
	}
	visiting[value] = true
	defer delete(visiting, value)
	next := func(child ssa.Value) string {
		return coroPhysicalAddressDiagnostic(child, depth+1, visiting)
	}
	switch value := value.(type) {
	case *ssa.Convert:
		return fmt.Sprintf("convert[%s](%s)", value.Type(), next(value.X))
	case *ssa.ChangeType:
		return fmt.Sprintf("changetype[%s](%s)", value.Type(), next(value.X))
	case *ssa.Phi:
		edges := make([]string, 0, len(value.Edges))
		for _, edge := range value.Edges {
			edges = append(edges, next(edge))
		}
		return "phi(" + strings.Join(edges, ",") + ")"
	case *ssa.Call:
		callee := "dynamic"
		if value.Common() != nil && value.Common().StaticCallee() != nil {
			callee = value.Common().StaticCallee().String()
		}
		return "call(" + callee + ")"
	case *ssa.FieldAddr:
		return fmt.Sprintf("fieldaddr[%d](%s)", value.Field, next(value.X))
	case *ssa.IndexAddr:
		return "indexaddr(" + next(value.X) + ")"
	default:
		return fmt.Sprintf("%T:%s", value, value.Name())
	}
}

// provenCoroPhysicalAddressRoot classifies an address only after the immutable
// retention proof has authorized that exact value/use pair. It cannot make an
// address stable by itself. Keeping this provenance separate prevents a global
// or managed-heap field address from being mislabeled as a frame-local store
// merely because all three are transport-stable under the current profile.
func (a *coroPhysicalPureSSAAudit) provenCoroPhysicalAddressRoot(value ssa.Value, visiting map[ssa.Value]bool) (coroPhysicalAddressRoot, bool) {
	if value == nil || visiting[value] {
		return coroPhysicalAddressInvalid, false
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.Global:
		return coroPhysicalAddressGlobal, true
	case *ssa.Alloc:
		if a.frameRetainsManagedHeapAllocation(value) {
			return coroPhysicalAddressManagedHeap, true
		}
		if value.Heap {
			if !a.frameRetainsAllocation(value) {
				return coroPhysicalAddressInvalid, false
			}
		}
		return coroPhysicalAddressLocal, true
	case *ssa.FieldAddr:
		return a.provenCoroPhysicalAddressRoot(value.X, visiting)
	case *ssa.IndexAddr:
		return a.provenCoroPhysicalAddressRoot(value.X, visiting)
	case *ssa.ChangeType:
		return a.provenCoroPhysicalAddressRoot(value.X, visiting)
	case *ssa.Convert:
		return a.provenCoroPhysicalAddressRoot(value.X, visiting)
	case *ssa.Call:
		if coroPhysicalUnsafeAddCall(value, a.typeOf) {
			return a.provenCoroPhysicalAddressRoot(value.Common().Args[0], visiting)
		}
	case *ssa.Phi:
		root := coroPhysicalAddressInvalid
		for _, edge := range value.Edges {
			candidate, ok := a.provenCoroPhysicalAddressRoot(edge, visiting)
			if !ok {
				return coroPhysicalAddressInvalid, false
			}
			if root == coroPhysicalAddressInvalid {
				root = candidate
				continue
			}
			if candidate == coroPhysicalAddressGlobal || root == coroPhysicalAddressGlobal {
				root = coroPhysicalAddressGlobal
			} else if candidate == coroPhysicalAddressManagedHeap || root == coroPhysicalAddressManagedHeap {
				root = coroPhysicalAddressManagedHeap
			}
		}
		return root, root != coroPhysicalAddressInvalid
	}
	return coroPhysicalAddressInvalid, false
}

func (a *coroPhysicalPureSSAAudit) plannedRuntimeHelpers(instr ssa.Instruction) ([]string, string) {
	if a == nil || a.ctx == nil || a.universe == nil || instr == nil {
		return nil, "runtime helper validation requires an exact frozen site plan"
	}
	helpers, err := a.universe.coroProgramIR.plannedRuntimeHelpers(a.ctx, instr)
	if err != nil {
		return nil, "load frozen runtime helper site plan: " + err.Error()
	}
	semantic := helpers[:0]
	for _, helper := range helpers {
		if coroLogicalCallerRuntimeHelper(helper) {
			continue
		}
		semantic = append(semantic, helper)
	}
	return semantic, ""
}

func (a *coroPhysicalPureSSAAudit) requireNoRuntimeHelpers(instr ssa.Instruction) string {
	return a.requireNoRuntimeHelpersExcept(instr)
}

// requireOnlyCompilerElidedRuntimeHelpers verifies that the frozen logical
// helper inventory contains no edge beyond the helpers replaced by this
// instruction's structured ExplicitStatus lowering. Unlike
// requireNoRuntimeHelpersExcept, this is not a domination proof: codegen emits
// none of the listed helpers on either branch.
func (a *coroPhysicalPureSSAAudit) requireOnlyCompilerElidedRuntimeHelpers(
	instr ssa.Instruction,
	allowed ...string,
) string {
	if a == nil || a.ctx == nil || a.universe == nil {
		return ""
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, helper := range allowed {
		allowedSet[helper] = struct{}{}
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	var unexpected []string
	for _, helper := range helpers {
		if _, ok := allowedSet[helper]; !ok {
			unexpected = append(unexpected, helper)
		}
	}
	if len(unexpected) != 0 {
		return "operation lowers through non-elided runtime helper(s) " + strings.Join(unexpected, ", ")
	}
	return ""
}

// requireCompilerElidedAndDirectNoSuspendRuntimeHelper accepts one physical
// helper that must execute inline between already-evaluated source values,
// while allowing the remaining named logical helpers to be replaced by the
// instruction's structured ExplicitStatus branches. The direct helper cannot
// be a coroutine child: doing so would spill those values into the frame.
func (a *coroPhysicalPureSSAAudit) requireCompilerElidedAndDirectNoSuspendRuntimeHelper(
	instr ssa.Instruction,
	direct string,
	elided ...string,
) string {
	allowed := append(append([]string(nil), elided...), direct)
	if reason := a.requireOnlyCompilerElidedRuntimeHelpers(instr, allowed...); reason != "" {
		return reason
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	count := 0
	for _, helper := range helpers {
		if helper == direct {
			count++
		}
	}
	if count != 1 {
		return fmt.Sprintf("operation lowers through %d %s helpers, want exactly one", count, direct)
	}
	if a == nil || a.plan == nil || a.fn == nil {
		return "direct no-suspend runtime helper validation requires a whole-build plan"
	}
	call, planned := a.plan.ResolveLoweredCallRecord(a.fn, direct)
	if !planned || call.Target == nil || call.ExplicitStatusElided {
		return "direct no-suspend runtime helper " + direct + " lacks an exact lowered-call fact"
	}
	target, planned := a.plan.FunctionPlan(call.Target)
	if !planned || target.External != coro.Defined || target.Emission != coro.EmitPlain ||
		target.Primary != coro.PrimaryPlain || target.FuncRep != coro.DirectPlain ||
		target.Effect != coro.NoSuspend || target.Demand == coro.NoDemand ||
		target.Exec&(coro.NeedsPreempt|coro.OpaqueExec) != 0 ||
		a.allowImplicitNilFault && target.Exec.Contains(coro.MayUnwind) {
		return "runtime helper " + direct + " is not one demanded direct no-suspend plain body"
	}
	return ""
}

// requireFrozenCoroSafeRuntimeHelpers is the narrow capability gate for an
// operation whose canonical LLGo lowering necessarily calls a known runtime
// helper. It accepts no name outside allowed, and still requires the ordinary
// whole-build lowered-call fact plus one demanded coroutine-safe target plan.
// In particular, this does not make arbitrary allocation helpers legal: the
// captured-closure caller names only AllocU and the frozen emission universe
// must bind that exact logical edge to the runtime allocator body.
func (a *coroPhysicalPureSSAAudit) requireFrozenCoroSafeRuntimeHelpers(instr ssa.Instruction, allowed ...string) string {
	if a == nil || a.ctx == nil || a.universe == nil || a.plan == nil || a.fn == nil {
		return "runtime helper capability validation requires a frozen emission universe"
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	if len(helpers) == 0 {
		return "runtime helper capability validation found no lowered helper"
	}
	accepted := make(map[string]struct{}, len(allowed))
	for _, helper := range allowed {
		accepted[helper] = struct{}{}
	}
	for _, helper := range helpers {
		if _, ok := accepted[helper]; !ok {
			return "operation lowers through unapproved runtime helper " + helper
		}
	}
	if !a.allHelpersHaveCoroSafeLowering(helpers) {
		return "approved runtime helper(s) lack an exact coroutine-safe lowered-call plan: " + strings.Join(helpers, ", ")
	}
	return ""
}

// requireFrozenExactRuntimeHelper is the single-helper form used for a
// non-suspending, non-unwinding runtime operation. It still accepts either a
// proven direct plain target or a managed coroutine target according to the
// shared lowered-call capability gate; it never infers safety from the helper
// name alone.
func (a *coroPhysicalPureSSAAudit) requireFrozenExactRuntimeHelper(instr ssa.Instruction, helper string) string {
	if reason := a.requireFrozenCoroSafeRuntimeHelpers(instr, helper); reason != "" {
		return reason
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	if len(helpers) != 1 || helpers[0] != helper {
		return "operation does not lower through exactly one " + helper + " helper"
	}
	target, planned := a.plan.ResolveLoweredCall(a.fn, helper)
	if !planned || target == nil {
		return "runtime helper " + helper + " lacks an exact lowered-call target"
	}
	return ""
}

// requireFrozenOutcomeRuntimeHelper is the stricter gate for a language
// operation whose runtime implementation can panic on ordinary input. A plain
// helper, even a currently small one, cannot unwind through a live LLVM
// coroutine frame. The exact owner-scoped helper must therefore be an
// ExplicitStatus coroutine whose Return/Panic outcome is consumed by the
// shared child-await lowering.
func (a *coroPhysicalPureSSAAudit) requireFrozenOutcomeRuntimeHelper(instr ssa.Instruction, helper string) string {
	if a == nil {
		return "outcome runtime helper validation requires a physical SSA audit"
	}
	if !a.allowImplicitNilFault {
		return "potentially panicking runtime helper requires the explicit-status panic ABI"
	}
	if reason := a.requireFrozenCoroSafeRuntimeHelpers(instr, helper); reason != "" {
		return reason
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	if len(helpers) != 1 || helpers[0] != helper {
		return "operation does not lower through exactly one " + helper + " helper"
	}
	target, planned := a.plan.ResolveLoweredCall(a.fn, helper)
	if !planned || target == nil {
		return "outcome runtime helper " + helper + " lacks an exact lowered-call target"
	}
	call, planned := a.plan.ResolveLoweredCallRecord(a.fn, helper)
	if !planned || call.RawPlain {
		return "outcome runtime helper " + helper + " cannot use a raw/plain terminal island"
	}
	targetPlan, planned := a.plan.FunctionPlan(target)
	if !planned || targetPlan.External != coro.Defined || targetPlan.Emission != coro.EmitCoroutine ||
		targetPlan.Primary != coro.PrimaryCoroutine ||
		(targetPlan.FuncRep != coro.DirectCoro && targetPlan.FuncRep != coro.Dispatch) ||
		!targetPlan.Demand.Contains(coro.AsyncDemand) || !targetPlan.Effect.Contains(coro.OutcomeStructured) ||
		!targetPlan.Exec.Contains(coro.MayUnwind) {
		return "outcome runtime helper " + helper + " is not one demanded MayUnwind ExplicitStatus coroutine"
	}
	return ""
}

// requireFrozenStructuredRuntimeHelpers is the reusable gate for composite
// language lowerings that issue more than one compiler-owned runtime call.
// It binds the exact helper inventory to owner-scoped lowered-call facts. A
// helper may remain plain only when it is proven non-suspending and
// non-unwinding; a MayUnwind helper must return through an ExplicitStatus
// coroutine child. Thus adding a map, iterator, assertion, or future typed
// lowering cannot silently recreate native-stack unwinding between awaits.
func (a *coroPhysicalPureSSAAudit) requireFrozenStructuredRuntimeHelpers(instr ssa.Instruction, expected ...string) string {
	if a == nil || a.ctx == nil || a.universe == nil {
		return "structured runtime helper validation requires a frozen emission universe"
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	return a.requireFrozenStructuredRuntimeHelperInventory(instr, helpers, expected...)
}

// requireFrozenTypeAssertRuntimeHelpers corrects the logical helper scan with
// the physical callable transport selected by the frontend. The generic
// scanner sees a Go signature and conservatively reports MatchesClosure; raw C
// signatures lower as one direct pointer, so codegen emits no such helper.
func (a *coroPhysicalPureSSAAudit) requireFrozenTypeAssertRuntimeHelpers(assertion *ssa.TypeAssert, expected ...string) string {
	var helpers []string
	if a != nil && a.ctx != nil && a.universe != nil {
		var reason string
		helpers, reason = a.plannedRuntimeHelpers(assertion)
		if reason != "" {
			return reason
		}
		if !coroTypeAssertUsesManagedClosure(a.ctx, assertion) {
			filtered := helpers[:0]
			for _, helper := range helpers {
				if helper != "MatchesClosure" {
					filtered = append(filtered, helper)
				}
			}
			helpers = filtered
		}
	}
	if len(expected) == 0 && len(helpers) == 0 {
		return ""
	}
	return a.requireFrozenStructuredRuntimeHelperInventory(assertion, helpers, expected...)
}

func (a *coroPhysicalPureSSAAudit) requireFrozenStructuredRuntimeHelperInventory(
	instr ssa.Instruction,
	helpers []string,
	expected ...string,
) string {
	if a == nil || a.ctx == nil || a.universe == nil || a.plan == nil || a.fn == nil {
		return "structured runtime helper validation requires a frozen emission universe"
	}
	want := make(map[string]struct{}, len(expected))
	for _, helper := range expected {
		if helper == "" {
			return "structured runtime helper inventory contains an empty helper name"
		}
		want[helper] = struct{}{}
	}
	if len(want) != len(expected) || len(helpers) != len(want) {
		return fmt.Sprintf("structured runtime helper inventory = %v, want exactly %v", helpers, expected)
	}
	for _, helper := range helpers {
		if _, ok := want[helper]; !ok {
			return fmt.Sprintf("structured runtime helper inventory = %v, want exactly %v", helpers, expected)
		}
	}

	lowered := make(map[string]coro.SSALoweredCall)
	for _, call := range a.plan.LoweredCalls(a.fn) {
		lowered[call.LogicalName] = call
	}
	for _, helper := range helpers {
		call, ok := lowered[helper]
		if !ok || call.Target == nil || call.ExplicitStatusElided {
			return "structured runtime helper " + helper + " lacks an exact non-elided lowered-call fact"
		}
		target, planned := a.plan.ResolveLoweredCall(a.fn, helper)
		if !planned || target == nil || target != call.Target {
			return "structured runtime helper " + helper + " lacks one consistent owner-scoped target"
		}
		plan, planned := a.plan.FunctionPlan(target)
		if !planned || plan.External != coro.Defined || plan.Demand == coro.NoDemand {
			return "structured runtime helper " + helper + " does not target one demanded defined body"
		}
		if call.RawPlain {
			if !a.validRawPlainLoweredCall(call, plan) {
				return "structured runtime helper " + helper + " has no validated raw/plain closure"
			}
			continue
		}
		switch plan.Emission {
		case coro.EmitPlain:
			if plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.DirectPlain ||
				plan.Effect != coro.NoSuspend || plan.Exec.Contains(coro.MayUnwind) ||
				plan.Exec&(coro.BlockForeign|coro.NeedsPreempt|coro.OpaqueExec) != 0 {
				return "structured runtime helper " + helper + " is not one non-suspending, non-unwinding direct plain body"
			}
		case coro.EmitCoroutine:
			if plan.Primary != coro.PrimaryCoroutine ||
				(plan.FuncRep != coro.DirectCoro && plan.FuncRep != coro.Dispatch) ||
				!plan.Demand.Contains(coro.AsyncDemand) || !plan.Effect.MaySuspend() {
				return "structured runtime helper " + helper + " is not one demanded coroutine child"
			}
			if plan.Exec.Contains(coro.MayUnwind) &&
				(!a.allowImplicitNilFault || !plan.Effect.Contains(coro.OutcomeStructured)) {
				return "structured runtime helper " + helper + " may unwind without the ExplicitStatus coroutine outcome ABI"
			}
		default:
			return "structured runtime helper " + helper + " has no callable managed emission"
		}
	}
	return ""
}

// requireFrozenTerminalRuntimeHelpers accepts an exact compiler-lowered panic
// edge only when every emitted helper is present in allowed, is frozen as an
// exact lowered call, and has a demanded direct no-suspend plain body. The
// helper may return on the non-panic predicate, but it cannot suspend beneath
// the live coroutine frame.
func (a *coroPhysicalPureSSAAudit) requireFrozenTerminalRuntimeHelpers(instr ssa.Instruction, allowed ...string) string {
	if a == nil || a.ctx == nil || a.universe == nil || a.plan == nil || a.fn == nil {
		return "terminal runtime helper validation requires a frozen emission universe"
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	if len(helpers) == 0 {
		return "terminal runtime helper validation found no lowered helper"
	}
	accepted := make(map[string]struct{}, len(allowed))
	for _, helper := range allowed {
		accepted[helper] = struct{}{}
	}
	lowered := make(map[string]coro.SSALoweredCall)
	for _, call := range a.plan.LoweredCalls(a.fn) {
		lowered[call.LogicalName] = call
	}
	for _, helper := range helpers {
		if _, ok := accepted[helper]; !ok {
			return "operation lowers through unapproved terminal runtime helper " + helper
		}
		call, ok := lowered[helper]
		if !ok || call.Target == nil {
			return "terminal runtime helper " + helper + " lacks an exact lowered-call fact"
		}
		plan, ok := a.plan.FunctionPlan(call.Target)
		if !ok || plan.External != coro.Defined || plan.Emission != coro.EmitPlain ||
			plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.DirectPlain ||
			plan.Effect != coro.NoSuspend || plan.Demand == coro.NoDemand ||
			plan.Exec&(coro.BlockForeign|coro.ThreadAffine|coro.NeedsPreempt|coro.OpaqueExec) != 0 {
			return "terminal runtime helper " + helper + " is not one demanded direct no-suspend plain body"
		}
	}
	return ""
}

// requireNoRuntimeHelpersExcept permits only helpers whose panic predicate is
// made unreachable by the exact address-use dominance fact. It is not a
// general helper allowlist: without that exact proof even these names remain
// rejected, and every other lowered helper always remains rejected.
func (a *coroPhysicalPureSSAAudit) requireNoRuntimeHelpersExcept(instr ssa.Instruction, dominatedOnly ...string) string {
	if a == nil || a.ctx == nil || a.universe == nil {
		return ""
	}
	helpers, reason := a.plannedRuntimeHelpers(instr)
	if reason != "" {
		return reason
	}
	if len(helpers) == 0 {
		return ""
	}
	if a.allHelpersHaveCoroSafeLowering(helpers) {
		return ""
	}
	proof := a.currentFrameRetentionProof()
	if proof != nil {
		allowed := make(map[string]struct{}, len(dominatedOnly))
		for _, helper := range dominatedOnly {
			allowed[helper] = struct{}{}
		}
		if len(allowed) != 0 {
			var address ssa.Value
			switch instruction := instr.(type) {
			case *ssa.FieldAddr:
				address = instruction
			case *ssa.IndexAddr:
				address = instruction
			case *ssa.UnOp:
				address = instruction.X
			}
			if address != nil && proof.provesDominatedStableAddress(address, instr) {
				allDominated := true
				for _, helper := range helpers {
					if _, ok := allowed[helper]; !ok {
						allDominated = false
						break
					}
				}
				if allDominated {
					return ""
				}
			}
		}
	}
	return "operation lowers through managed runtime helper(s) " + strings.Join(helpers, ", ")
}

func (a *coroPhysicalPureSSAAudit) allHelpersHaveCoroSafeLowering(helpers []string) bool {
	if a == nil || a.plan == nil || a.fn == nil || len(helpers) == 0 {
		return false
	}
	lowered := make(map[string]coro.SSALoweredCall)
	for _, call := range a.plan.LoweredCalls(a.fn) {
		lowered[call.LogicalName] = call
	}
	for _, helper := range helpers {
		call, ok := lowered[helper]
		if !ok || call.Target == nil || call.ExplicitStatusElided {
			return false
		}
		plan, ok := a.plan.FunctionPlan(call.Target)
		if !ok || plan.External != coro.Defined || plan.Demand == coro.NoDemand {
			return false
		}
		if call.RawPlain {
			if !a.validRawPlainLoweredCall(call, plan) {
				return false
			}
			continue
		}
		switch plan.Emission {
		case coro.EmitPlain:
			if plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.DirectPlain ||
				plan.Effect != coro.NoSuspend || plan.Exec&(coro.NeedsPreempt|coro.OpaqueExec) != 0 ||
				a.allowImplicitNilFault && plan.Exec.Contains(coro.MayUnwind) {
				return false
			}
		case coro.EmitCoroutine:
			if !a.allowImplicitNilFault || plan.Primary != coro.PrimaryCoroutine ||
				(plan.FuncRep != coro.DirectCoro && plan.FuncRep != coro.Dispatch) ||
				!plan.Demand.Contains(coro.AsyncDemand) || !plan.Effect.MaySuspend() {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// validRawPlainLoweredCall verifies the plan half of a compiler-owned
// raw/plain occurrence. The live-closure validator has already proved every
// reachable Go/C leaf and marks both the callable entry and its exact raw body;
// aggregate managed Effect/Exec facts deliberately remain unchanged because a
// separate managed consumer may still need a coroutine entry.
func (a *coroPhysicalPureSSAAudit) validRawPlainLoweredCall(call coro.SSALoweredCall, plan coro.FunctionPlan) bool {
	return a != nil && a.plan != nil && call.RawPlain && !call.NoUnwind && !call.UnwindOnly && !call.ExplicitStatusElided &&
		call.Target != nil && plan.External == coro.Defined && plan.RawPlainDemand && plan.RawPlainEntry &&
		a.plan.HasRawPlainVariant(call.Target) &&
		(plan.Emission == coro.EmitRawPlain || plan.Emission == coro.EmitPlain || plan.Emission == coro.EmitCoroutine)
}

func (a *coroPhysicalPureSSAAudit) typeOf(typ types.Type) types.Type {
	if typ == nil || a == nil || a.ctx == nil {
		return typ
	}
	return a.ctx.patchType(typ)
}

func validateCoroPhysicalSSAValueType(typ types.Type) error {
	if typ == nil {
		return fmt.Errorf("nil type")
	}
	if tuple, ok := types.Unalias(typ).Underlying().(*types.Tuple); ok {
		for i := 0; i < tuple.Len(); i++ {
			if err := validateCoroPhysicalValueType(tuple.At(i).Type(), make(map[types.Type]bool)); err != nil {
				return fmt.Errorf("tuple field %d: %w", i, err)
			}
		}
		return nil
	}
	return validateCoroPhysicalValueType(typ, make(map[types.Type]bool))
}

func coroConstantIndexInBounds(index ssa.Value, bound int64) bool {
	if index == nil || bound < 0 {
		return false
	}
	value, ok := index.(*ssa.Const)
	if !ok || value.Value == nil {
		return false
	}
	basic, ok := types.Unalias(value.Type()).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 {
		return false
	}
	if basic.Info()&types.IsUnsigned == 0 && constant.Sign(value.Value) < 0 {
		return false
	}
	integer, exact := constant.Uint64Val(value.Value)
	return exact && integer < uint64(bound)
}

func coroPureBasicScalar(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return basic.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

func coroPureConversion(source, target types.Type) bool {
	if source == nil || target == nil {
		return false
	}
	sourceUnderlying := types.Unalias(source).Underlying()
	targetUnderlying := types.Unalias(target).Underlying()
	if types.Identical(sourceUnderlying, targetUnderlying) {
		return true
	}
	sourceBasic, sourceIsBasic := sourceUnderlying.(*types.Basic)
	targetBasic, targetIsBasic := targetUnderlying.(*types.Basic)
	if sourceIsBasic && targetIsBasic {
		if sourceBasic.Kind() == types.String || targetBasic.Kind() == types.String {
			return false
		}
		sourceNumeric := sourceBasic.Info()&(types.IsInteger|types.IsFloat|types.IsComplex) != 0
		targetNumeric := targetBasic.Info()&(types.IsInteger|types.IsFloat|types.IsComplex) != 0
		if sourceNumeric && targetNumeric {
			return true
		}
		return (sourceBasic.Kind() == types.UnsafePointer && targetBasic.Kind() == types.Uintptr) ||
			(sourceBasic.Kind() == types.Uintptr && targetBasic.Kind() == types.UnsafePointer)
	}
	_, sourcePointer := sourceUnderlying.(*types.Pointer)
	_, targetPointer := targetUnderlying.(*types.Pointer)
	if sourcePointer && targetPointer {
		return true
	}
	return (sourcePointer && targetIsBasic && targetBasic.Kind() == types.UnsafePointer) ||
		(targetPointer && sourceIsBasic && sourceBasic.Kind() == types.UnsafePointer)
}

func coroTypeContainsGCPointer(typ types.Type, visiting map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch typ := typ.(type) {
	case *types.Named:
		return coroTypeContainsGCPointer(typ.Underlying(), visiting)
	case *types.Pointer, *types.Map, *types.Chan, *types.Signature, *types.Interface, *types.Slice:
		return true
	case *types.Basic:
		return typ.Kind() == types.String || typ.Kind() == types.UnsafePointer
	case *types.Array:
		return coroTypeContainsGCPointer(typ.Elem(), visiting)
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			if coroTypeContainsGCPointer(typ.Field(i).Type(), visiting) {
				return true
			}
		}
	}
	return false
}

func coroTypeDefinitelyNonZero(typ types.Type, visiting map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch typ := typ.(type) {
	case *types.Named:
		return coroTypeDefinitelyNonZero(typ.Underlying(), visiting)
	case *types.Basic, *types.Pointer, *types.Map, *types.Chan, *types.Signature, *types.Interface, *types.Slice:
		return true
	case *types.Array:
		return typ.Len() > 0 && coroTypeDefinitelyNonZero(typ.Elem(), visiting)
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			if coroTypeDefinitelyNonZero(typ.Field(i).Type(), visiting) {
				return true
			}
		}
	}
	return false
}
