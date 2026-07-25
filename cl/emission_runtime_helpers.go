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

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// materializeLoweredRuntimeHelpers freezes the runtime calls that LLGo's
// instruction lowering inserts without an x/tools SSA CallInstruction. An
// explicitly complete runtime ABI is required. Report-only/unit-test
// universes retain legacy symbol resolution and intentionally freeze no such
// edges; whole-program active builds enable the contract and fail closed.
type coroEmissionFunctionShape struct {
	dynamicCleanup           bool
	terminalResultAllocation map[*ssa.Alloc]none
}

func prepareCoroEmissionFunctionShape(fn *ssa.Function) (coroEmissionFunctionShape, error) {
	shape := coroEmissionFunctionShape{
		dynamicCleanup:           coroFunctionRequiresDynamicCleanup(fn),
		terminalResultAllocation: make(map[*ssa.Alloc]none),
	}
	allocations, err := coroStaticTerminalReconstructionAllocations(fn)
	if err != nil {
		return coroEmissionFunctionShape{}, err
	}
	for _, allocation := range allocations {
		shape.terminalResultAllocation[allocation] = none{}
	}
	return shape, nil
}

func (shape coroEmissionFunctionShape) helperPlacement(instr ssa.Instruction, helper string) coroRuntimeHelperPlacement {
	if allocation, ok := instr.(*ssa.Alloc); ok && helper == "AllocZ" {
		if _, relocated := shape.terminalResultAllocation[allocation]; relocated {
			return coroRuntimeHelperAtPrologue
		}
	}
	if deferred, ok := instr.(*ssa.Defer); ok {
		if shape.dynamicCleanup && helper == "FreeDeferNode" {
			return coroRuntimeHelperAtCleanup
		}
		// Interface method selection is part of evaluating the deferred
		// function value, so IfacePtrData runs at registration. Builtin helpers
		// implement the deferred operation itself and therefore move with that
		// operation into the frame cleanup drainer.
		if helper != "IfacePtrData" {
			if _, builtin := deferred.Call.Value.(*ssa.Builtin); builtin {
				// A dynamic cleanup record has its own registration-time AllocU.
				// A deferred builtin that also needs AllocU is rejected by the
				// cleanup planner until helper occurrences become a multiset.
				if !(shape.dynamicCleanup && helper == "AllocU") {
					return coroRuntimeHelperAtCleanup
				}
			}
		}
	}
	return coroRuntimeHelperAtSource
}

func (u *EmissionUniverse) materializeLoweredRuntimeHelpers(ctx *context, ownerFn *ssa.Function, ownerPkg *preparedEmissionPackage, state emissionFunctionState, shape coroEmissionFunctionShape, instr ssa.Instruction) error {
	if u == nil {
		return nil
	}
	if ctx == nil || ownerFn == nil || ownerPkg == nil || instr == nil || instr.Parent() != ownerFn || u.coroProgramIR == nil {
		return fmt.Errorf("prepare emission universe: runtime helper SitePlan requires one exact builder, owner, context, and source instruction")
	}
	sitePlan := coroEmissionSitePlan{}
	if u.prog != nil {
		managed := u.classifyCoroRuntimeHelpers(ctx, shape, instr)
		for _, helper := range managed {
			sitePlan.managedRuntimeHelpers = append(sitePlan.managedRuntimeHelpers, coroPlannedRuntimeHelper{
				name:      helper,
				placement: shape.helperPlacement(instr, helper),
			})
		}
		sitePlan.plainRuntimeHelpers = u.classifyPlainRuntimeHelpers(ctx, instr, managed)
	}
	if err := u.coroProgramIR.freezeSite(ownerFn, ownerPkg, instr, sitePlan); err != nil {
		return fmt.Errorf("prepare emission universe: function %q site plan: %w", ownerFn.Name(), err)
	}
	if !u.completeRuntimeABI {
		return nil
	}
	if u.prog == nil {
		return fmt.Errorf("prepare emission universe: complete runtime ABI requires an LLVM SSA program")
	}
	runtimePkg := u.byPath[llssa.PkgRuntime]
	if runtimePkg == nil {
		return fmt.Errorf("prepare emission universe: complete runtime ABI requires package %q", llssa.PkgRuntime)
	}
	if u.pathDup[llssa.PkgRuntime] {
		return fmt.Errorf("prepare emission universe: runtime helper resolution has ambiguous package path %q", llssa.PkgRuntime)
	}
	for _, plannedHelper := range sitePlan.managedRuntimeHelpers {
		helper := plannedHelper.name
		if coroCompilerElidesImplicitFaultRuntimeHelper(instr, helper) {
			// The physical Index/IndexAddr recipe emits a current-frame terminal
			// guard before its unchecked access.  The legacy-stack representation
			// is retained separately below; importing this helper as a managed
			// child would manufacture an await on the successful access path.
			continue
		}
		target := runtimePkg.ssa.Func(helper)
		if target == nil {
			return fmt.Errorf("prepare emission universe: function %q lowers to missing runtime helper %q", ownerFn.Name(), helper)
		}
		canonical, err := u.addResolvedRequired(target, ownerPkg, ownerFn, state)
		if err != nil {
			return fmt.Errorf("prepare emission universe: function %q runtime helper %q: %w", ownerFn.Name(), helper, err)
		}
		if coroCompilerRawPlainLoweredRuntimeHelper(u, helper) {
			if err := u.recordCoroRawPlainLoweredCall(ownerFn, helper, canonical); err != nil {
				return err
			}
			// The exact raw occurrence is an executable code reference, not a
			// managed call edge. Feed it through the existing frozen raw-demand
			// projection so the closure builder must prove every reachable leaf.
			if err := u.recordABIMethodReferences(ownerFn, []*ssa.Function{canonical}); err != nil {
				return err
			}
			if err := u.recordABISyncReferences(ownerFn, []*ssa.Function{canonical}); err != nil {
				return err
			}
			continue
		}
		unwindOnly := u.loweredCallUnwindOnly(ownerFn, instr)
		if err := u.recordCoroLoweredCallSite(
			ownerFn, helper, canonical, unwindOnly,
			unwindOnly && coroLoweredCallExplicitStatusElided(instr, helper),
			false,
		); err != nil {
			return err
		}
	}
	// One source function may need both a plain and a physical coroutine
	// representation. Channel instructions in the physical representation use
	// the nonblocking CoroChanTry* edge above, while the plain representation
	// still lowers to the synchronous ChanSend/ChanRecv helper. Retain that
	// helper without recording a second physical lowered-call edge: the source
	// channel instruction already contributes MayPark to coroutine analysis.
	managed := make(map[string]none, len(sitePlan.managedRuntimeHelpers))
	for _, helper := range sitePlan.managedRuntimeHelpers {
		managed[helper.name] = none{}
	}
	for _, helper := range sitePlan.plainRuntimeHelpers {
		if _, shared := managed[helper]; shared && !coroCompilerElidesImplicitFaultRuntimeHelper(instr, helper) {
			continue
		}
		target := runtimePkg.ssa.Func(helper)
		if target == nil {
			return fmt.Errorf("prepare emission universe: function %q lowers its plain representation to missing runtime helper %q", ownerFn.Name(), helper)
		}
		canonical, err := u.addResolvedRequired(target, ownerPkg, ownerFn, state)
		if err != nil {
			return fmt.Errorf("prepare emission universe: function %q plain-representation runtime helper %q: %w", ownerFn.Name(), helper, err)
		}
		if err := u.recordCoroPlainLoweredCall(ownerFn, helper, canonical); err != nil {
			return err
		}
		// This helper is called only by the legacy-stack representation. Feed
		// it through the existing exact synchronous-reference classifier so the
		// second fixed point gives its closure RawPlainDemand without leaking
		// its effects into the managed physical body.
		if err := u.recordABIMethodReferences(ownerFn, []*ssa.Function{canonical}); err != nil {
			return err
		}
		if err := u.recordABISyncReferences(ownerFn, []*ssa.Function{canonical}); err != nil {
			return err
		}
	}
	return nil
}

// coroCompilerRawPlainLoweredRuntimeHelper classifies the typed channel
// primitives whose call occurrences are owned completely by coroutine
// lowering. They execute as bounded try/park/resume transactions on the
// current executor stack. In particular park is between state publication and
// llvm.coro.suspend, and resume is inside a terminating resume gate, so neither
// occurrence may grow a managed child-await edge. The exact target is still
// frozen per owner by recordCoroRawPlainLoweredCall and its complete raw
// closure is validated by the whole-program plan.
func coroCompilerRawPlainLoweredRuntimeHelper(u *EmissionUniverse, helper string) bool {
	if u == nil {
		return false
	}
	switch helper {
	case "CoroChanTrySend", "CoroChanTryRecv", "CoroChanTryClose",
		"CoroChanSelectTry", "CoroChanSelectPark", "CoroChanSelectResume":
		return true
	default:
		return false
	}
}

// coroLoweredCallExplicitStatusElided identifies an exact frontend recipe,
// not a runtime symbol-name exception.  compileInstr owns a non-synthetic
// source Panic in a physical ExplicitStatus body and publishes its already
// evaluated interface words directly; a plain primary still emits the frozen
// runtime.Panic helper, so the helper remains in the universe and plan.
func coroLoweredCallExplicitStatusElided(instr ssa.Instruction, helper string) bool {
	panicInstruction, ok := instr.(*ssa.Panic)
	return ok && !coroSyntheticSelectNoCasePanic(panicInstruction) && helper == "Panic"
}

// coroCompilerElidesImplicitFaultRuntimeHelper identifies the exact source
// recipes whose physical coroutine lowering owns the nil/bounds branch in the
// current frame.  It is intentionally instruction-shaped: the same runtime
// helper name emitted by any other lowering remains an ordinary managed edge.
func coroCompilerElidesImplicitFaultRuntimeHelper(instr ssa.Instruction, helper string) bool {
	switch instr.(type) {
	case *ssa.Index, *ssa.IndexAddr:
		return helper == "CheckIndexRange" || helper == "AssertNilDeref"
	default:
		return false
	}
}

func (u *EmissionUniverse) classifyPlainRuntimeHelpers(ctx *context, instr ssa.Instruction, managed []string) []string {
	if u == nil {
		return nil
	}
	set := make(map[string]struct{})
	add := func(names ...string) {
		for _, name := range names {
			if name != "" {
				set[name] = struct{}{}
			}
		}
	}
	plainStackCStr := false
	if call, ok := instr.(*ssa.Call); ok && u.prog != nil {
		opcode, intrinsic := emissionCallIntrinsicInstruction(ctx, &call.Call)
		plainStackCStr = intrinsic && (opcode == llgoAllocaCStr || opcode == llgoAllocaCStrs)
	}
	for _, helper := range managed {
		if plainStackCStr && helper == "AllocU" {
			continue
		}
		if !coroCompilerRawPlainLoweredRuntimeHelper(u, helper) {
			add(helper)
		}
	}
	switch instruction := instr.(type) {
	case *ssa.Send:
		add("ChanSend")
	case *ssa.UnOp:
		if instruction.Op == token.ARROW {
			add("ChanRecv")
		}
	case *ssa.Select:
		if instruction.Blocking {
			add("Select")
		} else {
			add("TrySelect")
		}
	case *ssa.Call:
		if builtin, ok := instruction.Common().Value.(*ssa.Builtin); ok && builtin.Name() == "close" {
			add("ChanClose")
		}
	case *ssa.Defer:
		if builtin, ok := instruction.Common().Value.(*ssa.Builtin); ok {
			switch builtin.Name() {
			case "close":
				add("ChanClose")
			case "panic":
				add("Panic")
			case "recover":
				add("Recover")
			}
		}
	}
	if call, ok := instr.(*ssa.Call); ok && !call.Call.IsInvoke() &&
		call.Call.StaticCallee() == nil && !emissionCallIsBuiltin(&call.Call) {
		// The representation choice is made after this universe freezes. Every
		// real dynamic function call may therefore become a descriptor load whose
		// plain implementation needs a recoverable nil-call check. Its physical
		// coroutine implementation owns the equivalent explicit-status edge.
		add("AssertNilDeref")
	}
	// The physical select lowering turns x/tools' unreachable no-case panic
	// into a trap. A dual plain representation still emits the original box and
	// panic instructions, so retain only those plain-only helper edges here.
	switch instruction := instr.(type) {
	case *ssa.MakeInterface:
		if coroSyntheticSelectNoCaseBox(instruction) {
			u.makeInterfaceRuntimeHelpers(ctx, instruction, add)
		}
	case *ssa.Panic:
		if coroSyntheticSelectNoCasePanic(instruction) {
			add("Panic")
		}
	}
	helpers := make([]string, 0, len(set))
	for helper := range set {
		helpers = append(helpers, helper)
	}
	sort.Strings(helpers)
	return helpers
}

// loweredCallUnwindOnly reports a structural CFG proof: the instruction's
// block cannot reach any normal Return in owner. It deliberately does not use
// helper names, runtime package policy, dominance guesses, or panic text.
//
// The result is cached per immutable SSA body. recordCoroLoweredCallSite merges
// all occurrences of one logical helper with AND, so any normal-return-reachable
// physical use makes the frozen edge ordinary.
func (u *EmissionUniverse) loweredCallUnwindOnly(owner *ssa.Function, instr ssa.Instruction) bool {
	if u == nil || owner == nil || instr == nil || instr.Parent() != owner || instr.Block() == nil {
		return false
	}
	if u.normalReturnBlocks == nil {
		u.normalReturnBlocks = make(map[*ssa.Function]map[*ssa.BasicBlock]none)
	}
	reachable, ok := u.normalReturnBlocks[owner]
	if !ok {
		reachable = make(map[*ssa.BasicBlock]none)
		queue := make([]*ssa.BasicBlock, 0, len(owner.Blocks))
		for _, block := range owner.Blocks {
			for _, blockInstr := range block.Instrs {
				if _, normalReturn := blockInstr.(*ssa.Return); normalReturn {
					reachable[block] = none{}
					queue = append(queue, block)
					break
				}
			}
		}
		for head := 0; head < len(queue); head++ {
			for _, predecessor := range queue[head].Preds {
				if _, seen := reachable[predecessor]; seen {
					continue
				}
				reachable[predecessor] = none{}
				queue = append(queue, predecessor)
			}
		}
		u.normalReturnBlocks[owner] = reachable
	}
	_, reachesReturn := reachable[instr.Block()]
	return !reachesReturn
}

func (u *EmissionUniverse) materializeRuntimeHelperReference(ownerFn *ssa.Function, ownerPkg *preparedEmissionPackage, state emissionFunctionState, helper string) (*ssa.Function, bool, error) {
	if u == nil || !u.completeRuntimeABI {
		return nil, false, nil
	}
	if u.prog == nil {
		return nil, false, fmt.Errorf("complete runtime ABI requires an LLVM SSA program")
	}
	if u.byPath[llssa.PkgRuntime] == nil {
		return nil, false, fmt.Errorf("complete runtime ABI requires package %q", llssa.PkgRuntime)
	}
	if u.pathDup[llssa.PkgRuntime] {
		return nil, false, fmt.Errorf("runtime helper resolution has ambiguous package path %q", llssa.PkgRuntime)
	}
	target := u.byPath[llssa.PkgRuntime].ssa.Func(helper)
	if target == nil {
		return nil, false, fmt.Errorf("missing runtime helper %q", helper)
	}
	canonical, err := u.addResolvedRequired(target, ownerPkg, ownerFn, state)
	if err != nil {
		return nil, false, err
	}
	return canonical, true, nil
}

// classifyCoroRuntimeHelpers is the sole raw-SSA helper classifier. Only the
// ProgramModelBuilder path above may call it; analysis, preflight and emission
// consume the frozen coroEmissionSitePlan instead.
func (u *EmissionUniverse) classifyCoroRuntimeHelpers(ctx *context, shape coroEmissionFunctionShape, instr ssa.Instruction) []string {
	set := make(map[string]struct{})
	add := func(names ...string) {
		for _, name := range names {
			if name != "" {
				set[name] = struct{}{}
			}
		}
	}

	switch v := instr.(type) {
	case *ssa.BinOp:
		u.binOpRuntimeHelpers(ctx, v, add)
	case *ssa.UnOp:
		switch v.Op {
		case token.ARROW:
			add("CoroChanTryRecv")
		case token.MUL:
			if _, checkedReceiver := ctx.methodNilDerefChecks[v]; checkedReceiver {
				// compileCheckedDeref preserves the checked pointer through the
				// value-receiver call and therefore uses the pointer-returning ABI.
				if !ssaValueProvenNonNilAt(v.X, v) {
					emissionCheckedDerefBaseRuntimeHelpers(v.X, add)
					add("AssertNilDerefPtr")
				}
			} else if shouldAssertDirectNilDeref(v) && !ssaValueProvenNonNilAt(v.X, v) {
				add("AssertNilDeref")
			}
			// Builder.Load must still perform the source-language nil check
			// when the loaded value has zero physical size: there is no LLVM
			// load whose fault could preserve that edge. This applies to
			// aggregate values as well as the scalar subset handled by
			// shouldAssertDirectNilDeref, and follows an AssertNilDerefPtr for a
			// zero-sized value-receiver wrapper.
			if emissionUniversallyZeroSizedType(ctx.patchType(v.Type())) &&
				!isKnownNonNilAddr(v.X) && !ssaValueProvenNonNilAt(v.X, v) {
				emissionAssertNilDerefBaseRuntimeHelpers(v.X, add)
				add("AssertNilDeref")
			}
			if isInterfaceCompareDeref(v) {
				emissionAssertNilDerefBaseRuntimeHelpers(v.X, add)
				add("AssertNilDeref")
			}
		}
	case *ssa.Convert:
		u.convertRuntimeHelpers(ctx, v, add)
	case *ssa.Alloc:
		bitcast, scalarBitcast := coro.ProveSSAExactScalarBitcast(v.Parent())
		scalarBitcast = scalarBitcast && bitcast.Allocation == v
		if v.Heap && !scalarBitcast && !ctx.skipSyntheticMakeSliceAlloc(v) && !isEmissionVargsAlloc(ctx, v) {
			elem := types.Unalias(v.Type()).(*types.Pointer).Elem()
			if !emissionZeroSizedType(ctx.patchType(elem), ctx.prog.PointerSize()) {
				add("AllocZ")
			}
		}
	case *ssa.Defer:
		if shape.dynamicCleanup {
			add("AllocU", "FreeDeferNode")
		}
		if v.Call.IsInvoke() {
			add("IfacePtrData")
		}
		if builtin, ok := v.Call.Value.(*ssa.Builtin); !ok ||
			builtin.Name() != "panic" && builtin.Name() != "recover" {
			u.builtinRuntimeHelpers(ctx, &v.Call, add)
		}
	case *ssa.FieldAddr:
		if ctx.isAddressOfFieldAddr(v) && !ssaAddressValueProvenNonNilAt(v.X, v) {
			add("AssertNilDeref")
		}
	case *ssa.Index:
		if emissionIndexNeedsRangeCheck(ctx, v.X, v.Index, v) {
			add("CheckIndexRange")
		}
	case *ssa.IndexAddr:
		// compileValue consumes varargs IndexAddr nodes in the enclosing varargs
		// lowering and emits neither an address nor bounds/nil helpers here.
		if emissionIsVargsAlloc(ctx, v.X) {
			break
		}
		safeBounds := !emissionIndexNeedsRangeCheck(ctx, v.X, v.Index, v)
		if !safeBounds {
			add("CheckIndexRange")
		}
		if _, pointer := types.Unalias(ctx.patchType(v.X.Type())).Underlying().(*types.Pointer); pointer &&
			!emissionKnownNonNilArrayBase(v.X) && !(safeBounds && ssaValueProvenNonNilAt(v.X, v)) {
			add("AssertNilDeref")
		}
	case *ssa.Slice:
		if _, synthetic := ctx.syntheticMakeSliceCap(v); synthetic {
			add("MakeSlice")
			break
		}
		if emissionIsVargsAlloc(ctx, v.X) {
			break
		}
		switch types.Unalias(ctx.patchType(v.X.Type())).Underlying().(type) {
		case *types.Basic:
			add("StringSlice2")
		case *types.Slice:
			if v.Max == nil {
				add("NewSlice2")
			} else {
				add("NewSlice3Bounds")
			}
		case *types.Pointer:
			// Builder.Slice returns unsafeSlice directly for the complete p[:]
			// view of a pointer-to-array. No bounds helper is emitted.
			if v.Low == nil && v.High == nil && v.Max == nil {
				break
			}
			if v.Max == nil {
				add("NewSlice2")
			} else {
				add("NewSlice3Bounds")
			}
		}
	case *ssa.MakeInterface:
		if !coroSyntheticSelectNoCaseBox(v) {
			u.makeInterfaceRuntimeHelpers(ctx, v, add)
		}
	case *ssa.MakeSlice:
		add("MakeSlice")
	case *ssa.MakeMap:
		add("MakeMap")
	case *ssa.MakeClosure:
		if len(v.Bindings) != 0 {
			add("AllocU")
		}
	case *ssa.Lookup:
		// Builder.Lookup always materializes the map key through mapKeyPtr
		// before calling MapAccess1/MapAccess2. mapKeyPtr owns an AllocU call;
		// it is not represented by an x/tools SSA instruction.
		add("AllocU")
		if v.CommaOk {
			add("MapAccess2")
		} else {
			add("MapAccess1")
		}
	case *ssa.TypeAssert:
		u.typeAssertRuntimeHelpers(ctx, v, add)
	case *ssa.Range:
		switch types.Unalias(ctx.patchType(v.X.Type())).Underlying().(type) {
		case *types.Basic:
			add("NewStringIter")
		case *types.Map:
			add("NewMapIter")
		}
	case *ssa.Next:
		if v.IsString {
			add("StringIterNext")
		} else {
			add("MapIterNext")
		}
		add(u.nextAssignmentRuntimeHelperNames(ctx, v)...)
	case *ssa.ChangeInterface:
		if interfaceIsNonEmpty(ctx.patchType(v.X.Type())) {
			add("IfaceType")
		}
		if interfaceIsNonEmpty(ctx.patchType(v.Type())) {
			add("NewItab")
		}
	case *ssa.MakeChan:
		add("NewChan")
	case *ssa.Select:
		add("CoroChanSelectTry")
		if v.Blocking {
			add("CoroChanSelectPark", "CoroChanSelectResume")
		}
	case *ssa.SliceToArrayPointer:
		if length, exact := coroSliceToArrayPointerLen(v, ctx.patchType); !exact || length != 0 {
			add("PanicSliceConvert")
		}
	case *ssa.MapUpdate:
		// Builder.MapUpdate uses the same mapKeyPtr lowering as Lookup.
		add("AllocU", "MapAssign")
	case *ssa.Panic:
		if !coroSyntheticSelectNoCasePanic(v) {
			add("Panic")
		}
	case *ssa.Send:
		add("CoroChanTrySend")
	case *ssa.Call:
		if v.Call.IsInvoke() {
			// Builder.Imethod extracts the receiver through this runtime helper
			// before issuing the physical closure call.
			add("IfacePtrData")
		}
		// Exact intrinsic opcodes are frozen by the LLSSA link table. Pure
		// frontend/report universes intentionally have no such table and do not
		// materialize physical runtime-helper edges.
		if u.prog != nil {
			opcode, intrinsic := emissionCallIntrinsicInstruction(ctx, &v.Call)
			switch {
			case intrinsic && (opcode == llgoAllocaCStr || opcode == llgoAllocaCStrs):
				// A plain body emits the stack-backed C-string recipe, while a
				// physical body consumes the frozen heap recipe. The latter emits
				// AllocU followed by CStrCopy, so both managed edges must be known
				// before whole-program analysis.
				add("AllocU", "CStrCopy")
			case intrinsic && opcode == llgoCgoCgocall &&
				v.Parent() != nil && isCgoC2func(v.Parent().Name()):
				// The exact generated C2 worker transaction resumes in the Go
				// wrapper and constructs its (result, error) pair there. Attach
				// that synthetic interface construction to the cgocall source
				// site so helper closure, physical emission, and observation
				// share one immutable recipe.
				add("AllocU", "NewItab")
			case intrinsic && opcode == llgoDeferData:
				// Builder.DeferData replaces the compiler declaration with an
				// ordinary runtime.GetThreadDefer call.
				add("GetThreadDefer")
			case intrinsic && opcode == llgoString:
				// Builder.MakeString selects exactly one runtime helper from the
				// already-lowered varargs shape. Invalid shapes are rejected later
				// by CoroIntrinsicCallSiteSemantics.
				if helper, err := emissionStringIntrinsicHelper(ctx, v); err == nil {
					add(helper)
				}
			case intrinsic && (opcode == llgoCgoCString || opcode == llgoCgoCBytes ||
				opcode == llgoCgoGoString || opcode == llgoCgoGoStringN || opcode == llgoCgoGoBytes):
				// The five cgo conversion intrinsics are compiler declarations,
				// not callable Go bodies. Their physical lowering inserts one
				// exact runtime helper call; retain that hidden edge just like
				// llgo.string so its complete allocation/foreign-call effects
				// remain visible to the whole-program plan.
				if helper, ok := emissionCgoConversionRuntimeHelper(opcode); ok {
					add(helper)
				}
			case intrinsic && opcode == llgoSigsetjmp && u.coroUsesRuntimeSigjmpHelpers():
				add("Sigsetjmp")
			case intrinsic && opcode == llgoSiglongjmp && u.coroUsesRuntimeSigjmpHelpers():
				add("Siglongjmp")
			}
		}
		u.builtinRuntimeHelpers(ctx, &v.Call, add)
	}

	ret := make([]string, 0, len(set))
	for name := range set {
		ret = append(ret, name)
	}
	sort.Strings(ret)
	return ret
}

// nextAssignmentRuntimeHelperNames mirrors compileRangeNext's explicit
// assignment conversions. x/tools records the assignment-target key/value
// types in Next.Type, while Builder.Next first materializes the source
// map/string element types. Converting a source value to an interface can
// therefore add hidden ABI allocation/itab edges at this instruction.
func (u *EmissionUniverse) nextAssignmentRuntimeHelperNames(ctx *context, next *ssa.Next) []string {
	if u == nil || ctx == nil || next == nil || next.Iter == nil || next.Type() == nil {
		return nil
	}
	rng, ok := next.Iter.(*ssa.Range)
	if !ok || rng.X == nil {
		return nil
	}
	result, ok := types.Unalias(ctx.patchType(next.Type())).Underlying().(*types.Tuple)
	if !ok || result.Len() != 3 {
		return nil
	}

	var sourceFields [2]types.Type
	sourceType := ctx.patchType(rng.X.Type())
	if _, stringSource := coroPhysicalRangeStringType(sourceType); stringSource {
		sourceFields = [2]types.Type{types.Typ[types.Int], types.Typ[types.Rune]}
	} else {
		sourceMap, ok := types.Unalias(sourceType).Underlying().(*types.Map)
		if !ok {
			return nil
		}
		sourceFields = [2]types.Type{sourceMap.Key(), sourceMap.Elem()}
	}

	set := make(map[string]struct{})
	add := func(name string) {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	for index, source := range sourceFields {
		target := result.At(index + 1).Type()
		if coroPhysicalInvalidType(target) || types.Identical(source, target) ||
			!types.AssignableTo(source, target) {
			continue
		}
		targetInterface, targetIsInterface := types.Unalias(target).Underlying().(*types.Interface)
		if !targetIsInterface {
			continue
		}
		targetInterface.Complete()
		sourceInterface, sourceIsInterface := types.Unalias(source).Underlying().(*types.Interface)
		if sourceIsInterface {
			sourceInterface.Complete()
			if !sourceInterface.Empty() {
				add("IfaceType")
			}
			if !targetInterface.Empty() {
				add("NewItab")
			}
			continue
		}
		if !targetInterface.Empty() {
			add("NewItab")
		}
		physicalSource := u.physicalFunctionABIType(ctx, source)
		if !emissionDirectIfaceType(physicalSource) {
			add("AllocU")
		}
	}

	ret := make([]string, 0, len(set))
	for name := range set {
		ret = append(ret, name)
	}
	sort.Strings(ret)
	return ret
}

func emissionCgoConversionRuntimeHelper(opcode int) (string, bool) {
	switch opcode {
	case llgoCgoCString:
		return "CString", true
	case llgoCgoCBytes:
		return "CBytes", true
	case llgoCgoGoString:
		return "GoString", true
	case llgoCgoGoStringN:
		return "GoStringN", true
	case llgoCgoGoBytes:
		return "GoBytes", true
	default:
		return "", false
	}
}

// emissionCheckedDerefBaseRuntimeHelpers mirrors emitNilDerefBaseCheck. A
// value-receiver check may first traverse a retained * or field-address chain
// before NilDerefCheck validates the final receiver pointer. Those nested
// checks are emitted while compiling the outer UnOp, so recording only the
// pointer-returning helper leaves the owner-scoped AssertNilDeref edge hidden
// from both managed and raw/plain emission planning.
func emissionCheckedDerefBaseRuntimeHelpers(addr ssa.Value, add func(...string)) {
	switch addr := addr.(type) {
	case *ssa.UnOp:
		if addr.Op != token.MUL || isKnownNonNilAddr(addr.X) || isWrapNilCheckCall(addr.X) {
			return
		}
		emissionCheckedDerefBaseRuntimeHelpers(addr.X, add)
		add("AssertNilDeref")
	case *ssa.FieldAddr:
		if isKnownNonNilAddr(addr.X) || isWrapNilCheckCall(addr.X) {
			return
		}
		emissionCheckedDerefBaseRuntimeHelpers(addr.X, add)
		if isPointerGoType(addr.X.Type()) {
			add("AssertNilDeref")
		}
	}
}

// emissionAssertNilDerefBaseRuntimeHelpers mirrors assertNilDerefBase. Unlike
// emitNilDerefBaseCheck, that path retains each checked intermediate pointer
// by using compileCheckedDeref/NilDerefCheck, whose physical helper is
// AssertNilDerefPtr. Zero-sized loads and interface-comparison dereferences
// call it while emitting the outer UnOp, so those edges belong to that outer
// instruction's frozen SitePlan.
func emissionAssertNilDerefBaseRuntimeHelpers(addr ssa.Value, add func(...string)) {
	switch addr := addr.(type) {
	case *ssa.UnOp:
		if addr.Op != token.MUL || isKnownNonNilAddr(addr.X) || isWrapNilCheckCall(addr.X) {
			return
		}
		emissionCheckedDerefBaseRuntimeHelpers(addr.X, add)
		add("AssertNilDerefPtr")
	case *ssa.FieldAddr:
		if isKnownNonNilAddr(addr.X) || isWrapNilCheckCall(addr.X) {
			return
		}
		emissionAssertNilDerefBaseRuntimeHelpers(addr.X, add)
		if isPointerGoType(addr.X.Type()) {
			add("AssertNilDerefPtr")
		}
	}
}

func emissionCallIsBuiltin(call *ssa.CallCommon) bool {
	if call == nil {
		return false
	}
	_, ok := call.Value.(*ssa.Builtin)
	return ok
}

// emissionStringIntrinsicHelper mirrors context.string, compileVArg, and
// Builder.MakeString closely enough to select the one physical runtime call.
// The trailing x/tools SSA argument is always the materialized variadic slice:
// nil/empty means StringFromCStr, while one or more values selects StringFrom.
func emissionStringIntrinsicHelper(ctx *context, call *ssa.Call) (string, error) {
	if ctx == nil || call == nil || call.Common() == nil || call.Common().IsInvoke() {
		return "", fmt.Errorf("llgo.string must be an exact direct call")
	}
	common := call.Common()
	if len(common.Args) != 2 {
		return "", fmt.Errorf("llgo.string call %q requires a C string pointer and one variadic slice operand", call.String())
	}
	signature := common.Signature()
	if signature == nil || signature.Recv() != nil || !signature.Variadic() || signature.Params() == nil || signature.Params().Len() != 2 {
		return "", fmt.Errorf("llgo.string call %q requires the exact func(*int8, ...any) string shape", call.String())
	}
	first, ok := types.Unalias(signature.Params().At(0).Type()).Underlying().(*types.Pointer)
	if !ok {
		return "", fmt.Errorf("llgo.string call %q requires the exact func(*int8, ...any) string shape", call.String())
	}
	firstElem, ok := types.Unalias(first.Elem()).Underlying().(*types.Basic)
	if !ok || firstElem.Kind() != types.Int8 {
		return "", fmt.Errorf("llgo.string call %q requires the exact func(*int8, ...any) string shape", call.String())
	}
	variadic, ok := types.Unalias(signature.Params().At(1).Type()).Underlying().(*types.Slice)
	if !ok || !isAny(variadic.Elem()) {
		return "", fmt.Errorf("llgo.string call %q requires the exact func(*int8, ...any) string shape", call.String())
	}
	results := signature.Results()
	if results == nil || results.Len() != 1 {
		return "", fmt.Errorf("llgo.string call %q requires the exact func(*int8, ...any) string shape", call.String())
	}
	result, ok := types.Unalias(results.At(0).Type()).Underlying().(*types.Basic)
	if !ok || result.Kind() != types.String {
		return "", fmt.Errorf("llgo.string call %q requires the exact func(*int8, ...any) string shape", call.String())
	}
	actualPointer, ok := types.Unalias(common.Args[0].Type()).Underlying().(*types.Pointer)
	if !ok {
		return "", fmt.Errorf("llgo.string call %q has a non-pointer C string operand", call.String())
	}
	actualElem, ok := types.Unalias(actualPointer.Elem()).Underlying().(*types.Basic)
	if !ok || actualElem.Kind() != types.Int8 {
		return "", fmt.Errorf("llgo.string call %q has a non-*int8 C string operand", call.String())
	}

	switch varargs := common.Args[1].(type) {
	case *ssa.Const:
		if varargs.Value == nil {
			return "StringFromCStr", nil
		}
	case *ssa.Parameter:
		if varargs.Parent() != nil && llssa.HasNameValist(varargs.Parent().Signature) {
			// compileVArg intentionally treats a named va-list parameter as an
			// empty frontend-owned list.
			return "StringFromCStr", nil
		}
	case *ssa.Slice:
		if !emissionIsVargsAlloc(ctx, varargs.X) {
			break
		}
		alloc := varargs.X.(*ssa.Alloc)
		pointer := types.Unalias(alloc.Type()).(*types.Pointer)
		array := types.Unalias(pointer.Elem()).(*types.Array)
		if array.Len() == 0 {
			return "StringFromCStr", nil
		}
		return "StringFrom", nil
	}
	return "", fmt.Errorf("llgo.string call %q has an unsupported variadic lowering shape %T", call.String(), common.Args[1])
}

// emissionIndexNeedsRangeCheck mirrors ssa.Builder.checkRange for the source
// operands available before LLVM construction. Slice and string lengths are
// dynamic, while arrays and pointers to arrays have a frozen constant bound.
func emissionIndexNeedsRangeCheck(ctx *context, collection, index ssa.Value, use ssa.Instruction) bool {
	if ctx == nil || collection == nil || index == nil || use == nil || use.Parent() == nil {
		return true
	}
	bound, fixed := emissionFixedArrayBound(ctx, collection)
	if !fixed {
		return true
	}
	return !coro.ProveSSAExactSafeFixedArrayIndex(use.Parent(), index, bound, use)
}

func emissionFixedArrayBound(ctx *context, collection ssa.Value) (int64, bool) {
	if ctx == nil || collection == nil || collection.Type() == nil {
		return 0, false
	}
	switch typ := types.Unalias(ctx.patchType(collection.Type())).Underlying().(type) {
	case *types.Array:
		return typ.Len(), true
	case *types.Pointer:
		if array, ok := types.Unalias(typ.Elem()).Underlying().(*types.Array); ok {
			return array.Len(), true
		}
	}
	return 0, false
}

// emissionKnownNonNilArrayBase deliberately matches the narrow LLVM-side
// isKnownNonNilArrayBase predicate: direct globals, stack allocas, and the
// AllocU/AllocZ calls produced for an SSA Alloc. Recursive field/index address
// reasoning would incorrectly suppress a physical AssertNilDeref call.
func emissionKnownNonNilArrayBase(value ssa.Value) bool {
	switch value.(type) {
	case *ssa.Global, *ssa.Alloc:
		return true
	default:
		return false
	}
}

func isEmissionVargsAlloc(ctx *context, alloc *ssa.Alloc) bool {
	if alloc == nil || alloc.Comment != "varargs" {
		return false
	}
	ptr, ok := types.Unalias(alloc.Type()).(*types.Pointer)
	if !ok {
		return false
	}
	arr, ok := types.Unalias(ptr.Elem()).(*types.Array)
	return ok && isAny(arr.Elem()) && isAllocVargs(ctx, alloc)
}

func (u *EmissionUniverse) binOpRuntimeHelpers(ctx *context, op *ssa.BinOp, add func(...string)) {
	typ := types.Unalias(ctx.patchType(op.X.Type())).Underlying()
	switch typ := typ.(type) {
	case *types.Basic:
		switch {
		case typ.Kind() == types.String:
			switch op.Op {
			case token.ADD:
				add("StringCat")
			case token.EQL, token.NEQ:
				add("StringEqual")
			case token.LSS, token.LEQ, token.GTR, token.GEQ:
				add("StringLess")
			}
		case typ.Info()&types.IsComplex != 0 && op.Op == token.QUO:
			add("Complex128Div")
		case typ.Info()&types.IsInteger != 0 && (op.Op == token.QUO || op.Op == token.REM):
			if !ssaIntegerValueProvenNonZeroAt(op.Y, op) {
				add("AssertDivideByZero")
			}
		}
		if (op.Op == token.SHL || op.Op == token.SHR) && signedIntegerMayBeNegative(op.Y) {
			add("AssertNegativeShift")
		}
	case *types.Interface:
		if op.Op == token.EQL || op.Op == token.NEQ {
			add("EfaceEqual")
			if !typ.Empty() {
				add("IfaceType")
			}
			if interfaceIsNonEmpty(ctx.patchType(op.Y.Type())) {
				add("IfaceType")
			}
		}
	case *types.Array:
		if op.Op == token.EQL || op.Op == token.NEQ {
			u.compositeCompareRuntimeHelpers(ctx, typ.Elem(), add)
		}
	case *types.Struct:
		if op.Op == token.EQL || op.Op == token.NEQ {
			for i := 0; i < typ.NumFields(); i++ {
				if typ.Field(i).Name() != "_" {
					u.compositeCompareRuntimeHelpers(ctx, typ.Field(i).Type(), add)
				}
			}
		}
	}
}

func (u *EmissionUniverse) compositeCompareRuntimeHelpers(ctx *context, typ types.Type, add func(...string)) {
	typ = types.Unalias(ctx.patchType(typ)).Underlying()
	switch typ := typ.(type) {
	case *types.Basic:
		if typ.Kind() == types.String {
			add("StringEqual")
		}
	case *types.Interface:
		add("EfaceEqual")
		if !typ.Empty() {
			add("IfaceType")
		}
	case *types.Array:
		u.compositeCompareRuntimeHelpers(ctx, typ.Elem(), add)
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			if typ.Field(i).Name() != "_" {
				u.compositeCompareRuntimeHelpers(ctx, typ.Field(i).Type(), add)
			}
		}
	}
}

func constantIntegerKnownNonZero(value ssa.Value) bool {
	c, ok := value.(*ssa.Const)
	return ok && c.Value != nil && constant.Sign(c.Value) != 0
}

func signedIntegerMayBeNegative(value ssa.Value) bool {
	basic, ok := types.Unalias(value.Type()).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 || basic.Info()&types.IsUnsigned != 0 {
		return false
	}
	if c, ok := value.(*ssa.Const); ok && c.Value != nil {
		return constant.Sign(c.Value) < 0
	}
	return true
}

func (u *EmissionUniverse) convertRuntimeHelpers(ctx *context, convert *ssa.Convert, add func(...string)) {
	dst := types.Unalias(ctx.patchType(convert.Type())).Underlying()
	src := types.Unalias(ctx.patchType(convert.X.Type())).Underlying()
	if basic, ok := dst.(*types.Basic); ok && basic.Kind() == types.String {
		switch src := src.(type) {
		case *types.Slice:
			if elem, ok := types.Unalias(src.Elem()).Underlying().(*types.Basic); ok {
				switch elem.Kind() {
				case types.Byte:
					add("StringFromBytes")
				case types.Rune:
					add("StringFromRunes")
				}
			}
		case *types.Basic:
			if src.Info()&types.IsInteger != 0 {
				if src.Info()&types.IsUnsigned != 0 {
					add("StringFromUint64")
				} else {
					add("StringFromInt64")
				}
			}
		}
	}
	if slice, ok := dst.(*types.Slice); ok {
		if basic, ok := src.(*types.Basic); ok && basic.Kind() == types.String {
			if elem, ok := types.Unalias(slice.Elem()).Underlying().(*types.Basic); ok {
				switch elem.Kind() {
				case types.Byte:
					add("StringToBytes")
				case types.Rune:
					add("StringToRunes")
				}
			}
		}
	}
}

func (u *EmissionUniverse) makeInterfaceRuntimeHelpers(ctx *context, makeInterface *ssa.MakeInterface, add func(...string)) {
	// compileValue deliberately consumes these nodes without calling
	// Builder.MakeInterface: untyped nil becomes a constant, varargs stores
	// are lowered by their consumer, and funcAddr/funcPCABI0 inspect the SSA
	// operand directly.
	if !u.makeInterfaceEmitsABIType(makeInterface, ctx) {
		return
	}
	if interfaceIsNonEmpty(ctx.patchType(makeInterface.Type())) {
		add("NewItab")
	}
	// Helper planning must remain valid for report/identity universes whose
	// LLSSA program intentionally has no runtime package. The interface data
	// representation and the large/zero dereference rules depend only on the
	// patched Go type and target pointer size; materializing an LLSSA type here
	// would incorrectly require runtime.String and other runtime ABI types.
	// Builder.MakeInterface consumes the Go-to-raw physical payload, not the
	// patched source shape. In particular, every Go func value becomes the
	// two-word closure aggregate before the direct-interface decision. Looking
	// only at the source *types.Signature would incorrectly classify it as one
	// direct pointer and omit the AllocU call that code generation emits.
	physical := u.physicalFunctionABIType(ctx, makeInterface.X.Type())
	if !emissionDirectIfaceType(physical) {
		add("AllocU")
	}
	if unop, ok := makeInterface.X.(*ssa.UnOp); ok && unop.Op == token.MUL &&
		emissionLargeOrZeroInterfaceDeref(physical, ctx.prog.PointerSize()) {
		if !isKnownNonNilAddr(unop.X) && !ssaValueProvenNonNilAt(unop.X, makeInterface) {
			add("AssertNilDeref")
		}
		// MakeInterfaceFromPtr uses the indirect representation for both large
		// and zero-sized values and therefore always copies through AllocU.
		add("AllocU", "Typedmemmove")
	}
}

func emissionLargeOrZeroInterfaceDeref(typ types.Type, pointerSize int) bool {
	raw := types.Unalias(typ)
	if raw == nil {
		return false
	}
	if _, pointer := raw.Underlying().(*types.Pointer); pointer {
		return false
	}
	word := int64(pointerSize)
	if word <= 0 {
		return false
	}
	size := emissionTargetTypeSize(raw, word)
	return size == 0 || size > maxDirectDerefSize
}

type coroInterfaceDerefFusion uint8

const (
	coroInterfaceDerefNotFused coroInterfaceDerefFusion = iota
	coroInterfaceDerefZero
	coroInterfaceDerefLarge
)

// coroInterfaceDerefConsumer recognizes the exact frontend/codegen fusion
// where MakeInterfaceFromPtr performs the typed copy. Large values deliberately
// emit no producer load and leave the nil edge at MakeInterface. Zero-sized
// values retain the producer dereference's nil edge even though no physical
// load remains.
func coroInterfaceDerefConsumerForPhysicalType(
	deref *ssa.UnOp,
	physical types.Type,
	pointerSize int,
) (*ssa.MakeInterface, coroInterfaceDerefFusion) {
	if deref == nil || deref.Op != token.MUL || physical == nil || pointerSize <= 0 {
		return nil, coroInterfaceDerefNotFused
	}
	refs, ok := nonDebugReferrers(deref)
	if !ok || len(refs) != 1 {
		return nil, coroInterfaceDerefNotFused
	}
	box, ok := refs[0].(*ssa.MakeInterface)
	if !ok || box.X != deref {
		return nil, coroInterfaceDerefNotFused
	}
	size := emissionTargetTypeSize(types.Unalias(physical), int64(pointerSize))
	switch {
	case size == 0:
		return box, coroInterfaceDerefZero
	case size > maxDirectDerefSize:
		return box, coroInterfaceDerefLarge
	default:
		return nil, coroInterfaceDerefNotFused
	}
}

func emissionZeroSizedType(typ types.Type, pointerSize int) bool {
	if typ == nil || pointerSize <= 0 {
		return false
	}
	return emissionTargetTypeSize(types.Unalias(typ), int64(pointerSize)) == 0
}

// emissionUniversallyZeroSizedType classifies the aggregate shapes whose size
// is zero independently of a 32- or 64-bit target. It is used during early
// helper discovery, including report-only universes that intentionally have no
// LLVM target yet.
func emissionUniversallyZeroSizedType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	switch typ := types.Unalias(typ).Underlying().(type) {
	case *types.Array:
		return typ.Len() == 0 || emissionUniversallyZeroSizedType(typ.Elem())
	case *types.Struct:
		for index := 0; index < typ.NumFields(); index++ {
			if !emissionUniversallyZeroSizedType(typ.Field(index).Type()) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func emissionTargetTypeSize(typ types.Type, word int64) int64 {
	if typ == nil || word <= 0 {
		return -1
	}
	return (&types.StdSizes{WordSize: word, MaxAlign: word}).Sizeof(typ)
}

func emissionDirectIfaceType(typ types.Type) bool {
	switch typ := types.Unalias(typ).(type) {
	case *types.Named:
		return emissionDirectIfaceType(typ.Underlying())
	case *types.Pointer, *types.Chan, *types.Map, *types.Signature:
		return true
	case *types.Basic:
		return typ.Kind() == types.UnsafePointer
	case *types.Array:
		return typ.Len() == 1 && emissionDirectIfaceType(typ.Elem())
	case *types.Struct:
		return typ.NumFields() == 1 && emissionDirectIfaceType(typ.Field(0).Type())
	}
	return false
}

func (u *EmissionUniverse) typeAssertRuntimeHelpers(ctx *context, assertion *ssa.TypeAssert, add func(...string)) {
	asserted := ctx.patchType(assertion.AssertedType)
	if !types.Identical(ctx.patchType(assertion.X.Type()), asserted) {
		if _, ok := types.Unalias(asserted).Underlying().(*types.Interface); ok {
			add("Implements")
			if interfaceIsNonEmpty(asserted) {
				add("NewItab")
			}
		} else if _, ok := types.Unalias(asserted).Underlying().(*types.Signature); ok {
			add("MatchesClosure")
		}
	}
	if interfaceIsNonEmpty(ctx.patchType(assertion.X.Type())) {
		add("IfaceType")
	}
	if !assertion.CommaOk {
		add("PanicTypeAssert")
	}
}

func interfaceIsNonEmpty(typ types.Type) bool {
	iface, ok := types.Unalias(typ).Underlying().(*types.Interface)
	if !ok {
		return false
	}
	iface.Complete()
	return !iface.Empty()
}

func (u *EmissionUniverse) builtinRuntimeHelpers(ctx *context, call *ssa.CallCommon, add func(...string)) {
	builtin, ok := call.Value.(*ssa.Builtin)
	if !ok {
		return
	}
	args := call.Args
	switch builtin.Name() {
	case "ssa:wrapnilchk":
		add("PanicWrapNilPointer")
	case "len":
		if len(args) == 1 {
			switch types.Unalias(ctx.patchType(args[0].Type())).Underlying().(type) {
			case *types.Chan:
				add("ChanLen")
			case *types.Map:
				add("MapLen")
			}
		}
	case "cap":
		if len(args) == 1 {
			if _, ok := types.Unalias(ctx.patchType(args[0].Type())).Underlying().(*types.Chan); ok {
				add("ChanCap")
			}
		}
	case "append":
		add("SliceAppend")
	case "copy":
		add("SliceCopy")
	case "close":
		add("CoroChanTryClose")
	case "recover":
		add("Recover")
	case "panic":
		add("Panic")
	case "delete":
		// The delete builtin also lowers its key through Builder.mapKeyPtr.
		add("AllocU", "MapDelete")
	case "clear":
		if len(args) == 1 {
			switch types.Unalias(ctx.patchType(args[0].Type())).Underlying().(type) {
			case *types.Map:
				add("MapClear")
			case *types.Slice:
				add("SliceClear")
			}
		}
	case "min", "max":
		if len(args) > 1 {
			if basic, ok := types.Unalias(ctx.patchType(args[0].Type())).Underlying().(*types.Basic); ok && basic.Kind() == types.String {
				add("StringLess")
			}
		}
	case "print", "println":
		for _, arg := range args {
			add(runtimePrintHelper(ctx.patchType(arg.Type())))
		}
		if builtin.Name() == "println" {
			add("PrintByte")
		}
	case "String", "Slice":
		add("AssertRuntimeError")
	}
}

func runtimePrintHelper(typ types.Type) string {
	switch typ := types.Unalias(typ).Underlying().(type) {
	case *types.Basic:
		switch {
		case typ.Kind() == types.Bool:
			return "PrintBool"
		case typ.Info()&types.IsInteger != 0 && typ.Info()&types.IsUnsigned == 0:
			return "PrintInt"
		case typ.Info()&types.IsInteger != 0:
			return "PrintUint"
		case typ.Info()&types.IsFloat != 0:
			return "PrintFloat"
		case typ.Kind() == types.String:
			return "PrintString"
		case typ.Info()&types.IsComplex != 0:
			return "PrintComplex"
		case typ.Kind() == types.UnsafePointer:
			return "PrintPointer"
		}
	case *types.Pointer, *types.Signature, *types.Chan, *types.Map:
		return "PrintPointer"
	case *types.Slice:
		return "PrintSlice"
	case *types.Interface:
		if typ.Empty() {
			return "PrintEface"
		}
		return "PrintIface"
	}
	return ""
}
