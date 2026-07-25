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
	"go/token"
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// coroPhysicalInstructionRecipe is the closed code-generation choice for one
// source SSA instruction. Ordinary means that physical preflight accepted the
// legacy value recipe without a coroutine-specific fault branch. Every other
// value is selected and observed through the physical SitePlan; codegen may
// not rediscover it from SSA, target types, or frame-retention maps.
type coroPhysicalInstructionRecipe uint8

const (
	coroPhysicalInstructionOrdinary coroPhysicalInstructionRecipe = iota
	coroPhysicalInstructionFieldAddr
	coroPhysicalInstructionDeref
	coroPhysicalInstructionStore
	coroPhysicalInstructionIndexAddr
	coroPhysicalInstructionIndex
	coroPhysicalInstructionSlice
	coroPhysicalInstructionSliceToArrayPointer
	coroPhysicalInstructionBuiltinNilGuard
	coroPhysicalInstructionSyntheticSelectNoCaseBox
	coroPhysicalInstructionInterfaceFromCheckedPtr
	coroPhysicalInstructionUnsafeString
	coroPhysicalInstructionUnsafeSlice
	coroPhysicalInstructionInterfaceNilCompare
	coroPhysicalInstructionTerminalResultAllocation
	coroPhysicalInstructionFrameAllocation
	coroPhysicalInstructionFrameBitcastAllocation
	coroPhysicalInstructionHeapCStr
)

func (recipe coroPhysicalInstructionRecipe) String() string {
	switch recipe {
	case coroPhysicalInstructionOrdinary:
		return "ordinary"
	case coroPhysicalInstructionFieldAddr:
		return "fieldaddr"
	case coroPhysicalInstructionDeref:
		return "deref"
	case coroPhysicalInstructionStore:
		return "store"
	case coroPhysicalInstructionIndexAddr:
		return "indexaddr"
	case coroPhysicalInstructionIndex:
		return "index"
	case coroPhysicalInstructionSlice:
		return "slice"
	case coroPhysicalInstructionSliceToArrayPointer:
		return "slice-to-array-pointer"
	case coroPhysicalInstructionBuiltinNilGuard:
		return "builtin-nil-guard"
	case coroPhysicalInstructionSyntheticSelectNoCaseBox:
		return "synthetic-select-no-case-box"
	case coroPhysicalInstructionInterfaceFromCheckedPtr:
		return "interface-from-checked-ptr"
	case coroPhysicalInstructionUnsafeString:
		return "unsafe-string"
	case coroPhysicalInstructionUnsafeSlice:
		return "unsafe-slice"
	case coroPhysicalInstructionInterfaceNilCompare:
		return "interface-nil-compare"
	case coroPhysicalInstructionTerminalResultAllocation:
		return "terminal-result-allocation"
	case coroPhysicalInstructionFrameAllocation:
		return "frame-allocation"
	case coroPhysicalInstructionFrameBitcastAllocation:
		return "frame-bitcast-allocation"
	case coroPhysicalInstructionHeapCStr:
		return "heap-cstr"
	default:
		return fmt.Sprintf("physical-recipe(%d)", uint8(recipe))
	}
}

type coroPhysicalContainerKind uint8

const (
	coroPhysicalContainerNone coroPhysicalContainerKind = iota
	coroPhysicalContainerString
	coroPhysicalContainerArray
	coroPhysicalContainerSlice
	coroPhysicalContainerArrayPointer
)

// coroPhysicalControlRecipe is the frozen coroutine control operation selected
// for one source instruction. It is deliberately orthogonal to the value/fault
// recipe above: an awaited call can still carry an implicit guard plan, while
// ordinary calls and values keep the zero control recipe.
type coroPhysicalControlRecipe uint8

const (
	coroPhysicalControlNone coroPhysicalControlRecipe = iota
	coroPhysicalControlDirectAwait
	coroPhysicalControlDispatchAwait
	coroPhysicalControlClosedInterfaceAwait
	coroPhysicalControlManagedInterfaceAwait
	coroPhysicalControlPlainDispatch
	coroPhysicalControlRawPlainCall
	coroPhysicalControlDirectSpawn
	coroPhysicalControlDispatchSpawn
)

func (recipe coroPhysicalControlRecipe) String() string {
	switch recipe {
	case coroPhysicalControlNone:
		return "none"
	case coroPhysicalControlDirectAwait:
		return "direct-await"
	case coroPhysicalControlDispatchAwait:
		return "dispatch-await"
	case coroPhysicalControlClosedInterfaceAwait:
		return "closed-interface-await"
	case coroPhysicalControlManagedInterfaceAwait:
		return "managed-interface-await"
	case coroPhysicalControlPlainDispatch:
		return "plain-dispatch"
	case coroPhysicalControlRawPlainCall:
		return "raw-plain-call"
	case coroPhysicalControlDirectSpawn:
		return "direct-spawn"
	case coroPhysicalControlDispatchSpawn:
		return "dispatch-spawn"
	default:
		return fmt.Sprintf("physical-control-recipe(%d)", uint8(recipe))
	}
}

type coroPhysicalOperationRecipe uint8

const (
	coroPhysicalOperationNone coroPhysicalOperationRecipe = iota
	coroPhysicalOperationChannelSend
	coroPhysicalOperationChannelReceive
	coroPhysicalOperationChannelClose
	coroPhysicalOperationChannelSelectPark
	coroPhysicalOperationChannelSelectTry
	coroPhysicalOperationWorkerSyscall
	coroPhysicalOperationWorkerForeign
	coroPhysicalOperationWorkerCgo
	coroPhysicalOperationWorkerCgoErrno
)

func (recipe coroPhysicalOperationRecipe) String() string {
	switch recipe {
	case coroPhysicalOperationNone:
		return "none"
	case coroPhysicalOperationChannelSend:
		return "channel-send"
	case coroPhysicalOperationChannelReceive:
		return "channel-receive"
	case coroPhysicalOperationChannelClose:
		return "channel-close"
	case coroPhysicalOperationChannelSelectPark:
		return "channel-select-park"
	case coroPhysicalOperationChannelSelectTry:
		return "channel-select-try"
	case coroPhysicalOperationWorkerSyscall:
		return "worker-syscall"
	case coroPhysicalOperationWorkerForeign:
		return "worker-foreign"
	case coroPhysicalOperationWorkerCgo:
		return "worker-cgo"
	case coroPhysicalOperationWorkerCgoErrno:
		return "worker-cgo-errno"
	default:
		return fmt.Sprintf("physical-operation-recipe(%d)", uint8(recipe))
	}
}

// coroPhysicalOutcomeRecipe freezes source instructions that enter or inspect
// the Go completion/cleanup protocol. It is orthogonal to value/fault,
// call/spawn control, and blocking operation recipes: no outcome site may be
// rediscovered from a compilation-wide feature flag during emission.
type coroPhysicalOutcomeRecipe uint8

const (
	coroPhysicalOutcomeNone coroPhysicalOutcomeRecipe = iota
	coroPhysicalOutcomeReturn
	coroPhysicalOutcomeDeferRegister
	coroPhysicalOutcomeRunDefers
	coroPhysicalOutcomePanic
	coroPhysicalOutcomeRecover
	coroPhysicalOutcomeGoexit
	coroPhysicalOutcomeSyntheticSelectTrap
)

func (recipe coroPhysicalOutcomeRecipe) String() string {
	switch recipe {
	case coroPhysicalOutcomeNone:
		return "none"
	case coroPhysicalOutcomeReturn:
		return "return"
	case coroPhysicalOutcomeDeferRegister:
		return "defer-register"
	case coroPhysicalOutcomeRunDefers:
		return "run-defers"
	case coroPhysicalOutcomePanic:
		return "panic"
	case coroPhysicalOutcomeRecover:
		return "recover"
	case coroPhysicalOutcomeGoexit:
		return "goexit"
	case coroPhysicalOutcomeSyntheticSelectTrap:
		return "synthetic-select-trap"
	default:
		return fmt.Sprintf("physical-outcome-recipe(%d)", uint8(recipe))
	}
}

type coroPhysicalLoweringCapabilities struct {
	childAwait       bool
	staticSpawn      bool
	managedDispatch  bool
	explicitPanic    bool
	channel          bool
	worker           bool
	interfacePlain   *coroClosedInterfacePlainPlan
	managedInterface *coroManagedInterfaceDispatchPlan
}

// coroPhysicalInstructionPlan contains only information that changes emitted
// control flow. container and bound make the guarded Index/Slice recipes
// target-independent; nilGuard and boundsGuard are independent because a
// frozen safe array index removes only the range edge, never a nullable
// pointer-to-array dereference.
type coroPhysicalInstructionPlan struct {
	semantic           coroSemanticInstructionPlan
	recipe             coroPhysicalInstructionRecipe
	control            coroPhysicalControlRecipe
	controlTarget      *ssa.Function
	controlTargetID    coro.FunctionID
	controlInterface   *coroInterfaceDispatchPlan
	controlSignature   *types.Signature
	controlFailure     string
	controlFailureHard bool
	operation          coroPhysicalOperationRecipe
	operationFailure   string
	operationWorker    *coroWorkerForeignCallShape
	operationCgo       *coroWorkerCgoCallShape
	operationCgoErrno  *coroWorkerCgoErrnoCallShape
	outcome            coroPhysicalOutcomeRecipe
	outcomeFailure     string
	elideValue         bool
	valueOperand       ssa.Value
	container          coroPhysicalContainerKind
	bound              int64
	nilGuard           bool
	boundsGuard        bool
}

func (plan coroPhysicalInstructionPlan) mayFault() bool {
	return plan.nilGuard || plan.boundsGuard ||
		plan.recipe == coroPhysicalInstructionFieldAddr ||
		plan.recipe == coroPhysicalInstructionDeref ||
		plan.recipe == coroPhysicalInstructionBuiltinNilGuard
}

// elidesRuntimeHelper reports whether this frozen physical recipe replaces one
// logical LLSSA helper with compiler-owned structured control flow. Emission
// must use this projection rather than reclassifying the raw SSA instruction:
// the helper inventory remains part of effect/closure planning, while the
// selected recipe is the sole authority for whether a call is physically
// emitted in the live coroutine frame.
func (plan coroPhysicalInstructionPlan) elidesRuntimeHelper(helper string) bool {
	switch plan.recipe {
	case coroPhysicalInstructionFieldAddr, coroPhysicalInstructionDeref:
		if helper == "AssertNilDeref" || helper == "AssertNilDerefPtr" {
			return true
		}
	case coroPhysicalInstructionIndexAddr, coroPhysicalInstructionIndex:
		if helper == "CheckIndexRange" || helper == "AssertNilDeref" || helper == "AssertNilDerefPtr" {
			return true
		}
	case coroPhysicalInstructionSlice:
		if helper == "StringSlice2" || helper == "NewSlice2" || helper == "NewSlice3Bounds" ||
			helper == "AssertNilDeref" {
			return true
		}
	case coroPhysicalInstructionSliceToArrayPointer:
		if helper == "PanicSliceConvert" {
			return true
		}
	case coroPhysicalInstructionBuiltinNilGuard:
		if helper == "PanicWrapNilPointer" {
			return true
		}
	case coroPhysicalInstructionInterfaceFromCheckedPtr:
		if helper == "AssertNilDeref" {
			return true
		}
	case coroPhysicalInstructionUnsafeString, coroPhysicalInstructionUnsafeSlice:
		if helper == "AssertRuntimeError" {
			return true
		}
	case coroPhysicalInstructionInterfaceNilCompare:
		if helper == "EfaceEqual" || helper == "IfaceType" {
			return true
		}
	case coroPhysicalInstructionFrameAllocation:
		if helper == "AllocZ" {
			return true
		}
	}
	switch plan.outcome {
	case coroPhysicalOutcomePanic:
		return helper == "Panic"
	case coroPhysicalOutcomeRecover:
		return helper == "Recover"
	default:
		return false
	}
}

// coroPhysicalFunctionPlan is the post-analysis physical projection of one
// exact function emission. It owns every proof and per-instruction choice used
// after preflight. The pointed-to proofs are built to completion before this
// object is frozen and are read-only thereafter.
type coroPhysicalFunctionPlan struct {
	function          *ssa.Function
	owner             *preparedEmissionPackage
	frameRetention    *coroFrameRetentionProof
	critical          *coroCriticalProof
	cleanup           *coroStaticCleanupPlan
	frameRetentionABI string
	needsPreempt      bool
	instructions      map[ssa.Instruction]coroPhysicalInstructionPlan
}

func prepareCoroPhysicalFunctionPlan(
	audit *coroPhysicalPureSSAAudit,
	owner *preparedEmissionPackage,
	whole *coro.SSAPlan,
	cleanup *coroStaticCleanupPlan,
	critical *coroCriticalProof,
	explicitPanic bool,
	capabilities coroPhysicalLoweringCapabilities,
) (*coroPhysicalFunctionPlan, error) {
	if audit == nil || audit.fn == nil {
		return nil, fmt.Errorf("physical function planning requires one exact pure-SSA audit")
	}
	plan := &coroPhysicalFunctionPlan{
		function:          audit.fn,
		owner:             owner,
		frameRetention:    audit.currentFrameRetentionProof(),
		critical:          critical,
		cleanup:           cleanup,
		frameRetentionABI: audit.frameRetentionABI,
		instructions:      make(map[ssa.Instruction]coroPhysicalInstructionPlan),
	}
	if whole != nil {
		function, planned := whole.FunctionPlan(audit.fn)
		if !planned {
			return nil, fmt.Errorf("physical function planning requires a frozen function plan")
		}
		plan.needsPreempt = function.Exec.Contains(coro.NeedsPreempt)
	}
	var exactBitcastAllocation *ssa.Alloc
	if proof, exact := coro.ProveSSAExactScalarBitcast(audit.fn); exact {
		exactBitcastAllocation = proof.Allocation
	}
	for _, block := range audit.fn.Blocks {
		for _, instruction := range block.Instrs {
			instructionPlan, err := planCoroPhysicalInstruction(
				audit, owner, whole, cleanup, exactBitcastAllocation, instruction, explicitPanic, capabilities,
			)
			if err != nil {
				return nil, fmt.Errorf("block %d instruction %T: %w", block.Index, instruction, err)
			}
			plan.instructions[instruction] = instructionPlan
		}
	}
	return plan, nil
}

func (plan *coroPhysicalFunctionPlan) instructionPlan(instruction ssa.Instruction) (coroPhysicalInstructionPlan, error) {
	if plan == nil || plan.function == nil || plan.owner == nil || instruction == nil || instruction.Parent() != plan.function {
		return coroPhysicalInstructionPlan{}, fmt.Errorf("physical instruction plan requires one exact frozen function owner and source instruction")
	}
	physical, ok := plan.instructions[instruction]
	if !ok {
		return coroPhysicalInstructionPlan{}, fmt.Errorf("source instruction %q is absent from its frozen physical plan", instruction.String())
	}
	return physical, nil
}

type coroPhysicalPlanStage struct {
	plans map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan
}

func newCoroPhysicalPlanStage() *coroPhysicalPlanStage {
	return &coroPhysicalPlanStage{plans: make(map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan)}
}

func (stage *coroPhysicalPlanStage) freezePhysicalFunctionPlan(plan *coroPhysicalFunctionPlan) error {
	if stage == nil || plan == nil || plan.function == nil || plan.owner == nil || plan.instructions == nil {
		return fmt.Errorf("physical plan freeze requires one complete function-owner projection")
	}
	key := emissionFunctionOwnerKey{function: plan.function, owner: plan.owner}
	if _, exists := stage.plans[key]; exists {
		return fmt.Errorf("physical plan for function %q owner %q was frozen more than once", plan.function.Name(), plan.owner.identity)
	}
	for _, block := range plan.function.Blocks {
		for _, instruction := range block.Instrs {
			if _, ok := plan.instructions[instruction]; !ok {
				return fmt.Errorf("physical plan for function %q omitted source instruction %q", plan.function.Name(), instruction.String())
			}
		}
	}
	stage.plans[key] = plan
	return nil
}

func (ir *coroProgramIR) commitPhysicalFunctionPlans(stage *coroPhysicalPlanStage, expected map[emissionFunctionOwnerKey]none) error {
	if ir == nil || !ir.callsFrozen {
		return fmt.Errorf("physical plan commit requires the call SitePlan stage")
	}
	if ir.physicalPlansSealed {
		return fmt.Errorf("physical plans were committed more than once")
	}
	if stage == nil {
		return fmt.Errorf("physical plan commit requires one complete staging transaction")
	}
	if len(stage.plans) != len(expected) {
		return fmt.Errorf("physical plan stage has %d function owners, want %d", len(stage.plans), len(expected))
	}
	for key := range expected {
		if stage.plans[key] == nil {
			name, owner := "<nil>", "<nil>"
			if key.function != nil {
				name = key.function.Name()
			}
			if key.owner != nil {
				owner = key.owner.identity
			}
			return fmt.Errorf("physical plan stage omitted function %q owner %q", name, owner)
		}
	}
	for key := range stage.plans {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("physical plan stage retained an unexpected function %q owner %q", key.function.Name(), key.owner.identity)
		}
	}
	committed := make(map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan, len(stage.plans))
	for key, plan := range stage.plans {
		committed[key] = plan
	}
	ir.physicalPlans = committed
	ir.physicalPlansSealed = true
	return nil
}

func (ir *coroProgramIR) physicalFunctionPlan(function *ssa.Function, owner *preparedEmissionPackage) (*coroPhysicalFunctionPlan, error) {
	if ir == nil || !ir.physicalPlansSealed {
		return nil, fmt.Errorf("coroutine physical plans are not sealed")
	}
	if function == nil || owner == nil {
		return nil, fmt.Errorf("physical plan lookup requires one exact function and owner")
	}
	plan := ir.physicalPlans[emissionFunctionOwnerKey{function: function, owner: owner}]
	if plan == nil {
		return nil, fmt.Errorf("function %q owner %q has no frozen physical plan", function.Name(), owner.identity)
	}
	return plan, nil
}

// physicalFunctionPlanForEmission resolves the frozen definition projection
// used by one physical emission. Ordinary functions require the exact current
// package owner. A syntax-free generated wrapper is the sole exception: ABI
// method tables may materialize its linkonce body from another package, so the
// already-validated shared symbol may borrow the corresponding frozen plan.
func (index emissionCanonicalIndex) physicalFunctionPlanForEmission(
	function *ssa.Function,
	requestedOwner *preparedEmissionPackage,
) (*coroPhysicalFunctionPlan, error) {
	u := index.universe
	if u == nil || u.coroProgramIR == nil || !u.coroProgramIR.physicalPlansSealed {
		return nil, fmt.Errorf("coroutine physical plans are not sealed")
	}
	if function == nil || requestedOwner == nil {
		return nil, fmt.Errorf("physical emission plan lookup requires one exact function and requested owner")
	}
	function = u.canonicalAlias(function)
	if function == nil {
		return nil, fmt.Errorf("physical emission plan lookup found cyclic function aliases")
	}
	if plan := u.coroProgramIR.physicalPlans[emissionFunctionOwnerKey{
		function: function,
		owner:    requestedOwner,
	}]; plan != nil {
		return plan, nil
	}
	if !isEmissionGeneratedWrapper(function) {
		return nil, fmt.Errorf("function %q owner %q has no frozen physical plan", function.Name(), requestedOwner.identity)
	}

	shared, available := index.sharedGeneratedWrapperPhysicalName(function)
	if shared == "" {
		return nil, fmt.Errorf(
			"generated wrapper %q owner %q has no unambiguous frozen physical symbol; frozen owners: %v",
			function.Name(), requestedOwner.identity, available,
		)
	}
	for _, owner := range u.sortedUseOwners(function) {
		key := emissionFunctionOwnerKey{function: function, owner: owner}
		if u.physicalNames[key] != shared {
			continue
		}
		if plan := u.coroProgramIR.physicalPlans[key]; plan != nil {
			return plan, nil
		}
	}
	return nil, fmt.Errorf(
		"generated wrapper %q shared physical symbol %q has no corresponding frozen physical plan; frozen owners: %v",
		function.Name(), shared, available,
	)
}

func planCoroPhysicalInstruction(
	audit *coroPhysicalPureSSAAudit,
	owner *preparedEmissionPackage,
	whole *coro.SSAPlan,
	cleanup *coroStaticCleanupPlan,
	exactBitcastAllocation *ssa.Alloc,
	instruction ssa.Instruction,
	explicitPanic bool,
	capabilities coroPhysicalLoweringCapabilities,
) (coroPhysicalInstructionPlan, error) {
	result := coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionOrdinary}
	if audit == nil || instruction == nil || instruction.Parent() != audit.fn {
		return result, fmt.Errorf("physical instruction planning requires one exact audit and source instruction")
	}
	semantic, err := planCoroSemanticInstruction(instruction)
	if audit.universe != nil && audit.universe.coroProgramIR != nil && owner != nil {
		semantic, err = audit.universe.coroProgramIR.semanticInstructionPlan(audit.fn, owner, instruction)
	}
	if err != nil {
		return result, fmt.Errorf("load semantic instruction recipe: %w", err)
	}
	result.semantic = semantic
	planCoroPhysicalControlInstruction(audit, whole, instruction, capabilities, &result)
	planCoroPhysicalOperationInstruction(audit, whole, instruction, capabilities, &result)
	planCoroPhysicalOutcomeInstruction(audit, cleanup, instruction, capabilities, &result)
	switch instruction := instruction.(type) {
	case *ssa.Alloc:
		switch {
		case coroPhysicalCleanupContainsTerminalAllocation(cleanup, instruction):
			result.recipe = coroPhysicalInstructionTerminalResultAllocation
		case instruction == exactBitcastAllocation:
			result.recipe = coroPhysicalInstructionFrameBitcastAllocation
		case !instruction.Heap || audit.frameRetainsAllocation(instruction):
			result.recipe = coroPhysicalInstructionFrameAllocation
		}
	case *ssa.FieldAddr:
		requiresGuard, reason := audit.fieldAddrRequiresImplicitNilFault(instruction)
		if reason != "" {
			return result, fmt.Errorf("FieldAddr frozen helper plan: %s", reason)
		}
		if requiresGuard {
			result.recipe = coroPhysicalInstructionFieldAddr
			result.nilGuard = true
		}
	case *ssa.UnOp:
		if instruction.Op == token.MUL {
			if audit.derefRequiresImplicitNilFault(instruction) {
				result.recipe = coroPhysicalInstructionDeref
				result.nilGuard = true
				break
			}
			producerOwnsFault, reason := audit.derefAddressProducerOwnsImplicitFault(instruction)
			if reason != "" {
				return result, fmt.Errorf("Deref checked address producer: %s", reason)
			}
			if !producerOwnsFault {
				break
			}
			helpers, reason := audit.plannedRuntimeHelpers(instruction)
			if reason != "" {
				return result, fmt.Errorf("Deref frozen helper plan: %s", reason)
			}
			for _, helper := range helpers {
				if helper == "AssertNilDeref" || helper == "AssertNilDerefPtr" {
					// The address instruction emits the actual guard. Keep a
					// no-guard deref recipe here so the outer logical helper is
					// explicitly elided rather than silently disappearing.
					result.recipe = coroPhysicalInstructionDeref
					break
				}
			}
		}
	case *ssa.Store:
		if audit.ctx != nil {
			if index, ok := instruction.Addr.(*ssa.IndexAddr); ok &&
				emissionIsVargsAlloc(audit.ctx, index.X) {
				break
			}
		}
		requiresGuard, reason := audit.storeRequiresImplicitNilFault(instruction)
		if reason != "" {
			return result, fmt.Errorf("Store frozen helper plan: %s", reason)
		}
		if explicitPanic && requiresGuard {
			result.recipe = coroPhysicalInstructionStore
			result.nilGuard = true
		}
	case *ssa.IndexAddr:
		if audit.ctx != nil && emissionIsVargsAlloc(audit.ctx, instruction.X) {
			break
		}
		if !explicitPanic {
			break
		}
		result.recipe = coroPhysicalInstructionIndexAddr
		container, bound, err := coroPhysicalContainerPlan(audit, instruction.X)
		if err != nil {
			return result, err
		}
		if container != coroPhysicalContainerSlice && container != coroPhysicalContainerArrayPointer {
			return result, fmt.Errorf("IndexAddr has unsupported physical container %d", container)
		}
		result.container, result.bound = container, bound
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			!emissionKnownNonNilArrayBase(instruction.X) && !ssaValueProvenNonNilAt(instruction.X, instruction)
		safe, err := coroPhysicalSafeFixedArrayIndex(audit, whole, instruction, instruction.X, instruction.Index)
		if err != nil {
			return result, err
		}
		result.boundsGuard = !safe
	case *ssa.Index:
		if !explicitPanic {
			break
		}
		result.recipe = coroPhysicalInstructionIndex
		container, bound, err := coroPhysicalContainerPlan(audit, instruction.X)
		if err != nil {
			return result, err
		}
		result.container, result.bound = container, bound
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			!emissionKnownNonNilArrayBase(instruction.X) && !ssaValueProvenNonNilAt(instruction.X, instruction)
		safe, err := coroPhysicalSafeFixedArrayIndex(audit, whole, instruction, instruction.X, instruction.Index)
		if err != nil {
			return result, err
		}
		result.boundsGuard = !safe
	case *ssa.Slice:
		if audit.ctx != nil && emissionIsVargsAlloc(audit.ctx, instruction.X) {
			break
		}
		if audit.ctx != nil {
			if _, synthetic := audit.ctx.syntheticMakeSliceCap(instruction); synthetic {
				// The ordinary value recipe is the exact frontend lowering for
				// this x/tools pair: the synthetic Alloc emits nothing and this
				// Slice emits runtime.MakeSlice. Its frozen lowered-call record,
				// not a slice-bounds recipe, owns suspension and unwind.
				break
			}
		}
		if !explicitPanic {
			break
		}
		result.recipe = coroPhysicalInstructionSlice
		container, bound, err := coroPhysicalContainerPlan(audit, instruction.X)
		if err != nil {
			return result, err
		}
		if container != coroPhysicalContainerString && container != coroPhysicalContainerSlice &&
			container != coroPhysicalContainerArrayPointer {
			return result, fmt.Errorf("Slice has unsupported physical container %d", container)
		}
		result.container, result.bound = container, bound
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			!isKnownNonNilAddr(instruction.X) && !ssaValueProvenNonNilAt(instruction.X, instruction)
		result.boundsGuard = true
	case *ssa.SliceToArrayPointer:
		length, exact := coroSliceToArrayPointerLen(instruction, audit.typeOf)
		if !exact || length < 0 {
			return result, fmt.Errorf("slice-to-array-pointer has no exact physical length")
		}
		result.recipe = coroPhysicalInstructionSliceToArrayPointer
		result.bound = length
		result.boundsGuard = length != 0
	case *ssa.Call:
		if explicitPanic && isWrapNilCheckCall(instruction) {
			result.recipe = coroPhysicalInstructionBuiltinNilGuard
			result.nilGuard = true
		}
		if builtin, ok := instruction.Common().Value.(*ssa.Builtin); ok && explicitPanic {
			switch builtin.Name() {
			case "String":
				result.recipe = coroPhysicalInstructionUnsafeString
			case "Slice":
				result.recipe = coroPhysicalInstructionUnsafeSlice
			}
		}
		if audit.universe != nil && audit.universe.coroProgramIR != nil {
			frozen, found, err := audit.universe.coroProgramIR.callSitePlan(instruction)
			if err != nil {
				return result, fmt.Errorf("load intrinsic physical SitePlan: %w", err)
			}
			if found && frozen.failure == "" && frozen.plan.Intrinsic &&
				(frozen.opcode == llgoAllocaCStr || frozen.opcode == llgoAllocaCStrs) &&
				frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineWithLoweredCalls {
				if reason := audit.requireFrozenStructuredRuntimeHelpers(instruction, "AllocU", "CStrCopy"); reason != "" {
					return result, fmt.Errorf("physical llgo C string heap storage: %s", reason)
				}
				result.recipe = coroPhysicalInstructionHeapCStr
			}
		}
	case *ssa.ChangeType:
		if whole == nil {
			break
		}
		caller, planned := whole.FunctionPlan(audit.fn)
		if !planned {
			break
		}
		if target, err := resolveCoroCompilerElidedStaticAwaitRetag(
			audit, caller, instruction,
		); err == nil && target != nil {
			result.elideValue = true
		}
	case *ssa.MakeInterface:
		if coroSyntheticSelectNoCaseBox(instruction) {
			result.recipe = coroPhysicalInstructionSyntheticSelectNoCaseBox
			break
		}
		if unop, ok := instruction.X.(*ssa.UnOp); ok {
			_, fusion := coroInterfaceDerefConsumer(audit.ctx, unop)
			if fusion == coroInterfaceDerefNotFused {
				break
			}
			if fusion == coroInterfaceDerefZero && audit.derefRequiresImplicitNilFault(unop) {
				// The zero-sized dereference retains the source nil edge even
				// though it has no physical load. Its structured guard makes
				// the pointer safe for the following boxing copy.
				result.recipe = coroPhysicalInstructionInterfaceFromCheckedPtr
				break
			}
			switch address := unop.X.(type) {
			case *ssa.FieldAddr:
				ownsFault, reason := audit.fieldAddrRequiresImplicitNilFault(address)
				if reason != "" {
					return result, fmt.Errorf("MakeInterface checked FieldAddr producer: %s", reason)
				}
				if ownsFault {
					result.recipe = coroPhysicalInstructionInterfaceFromCheckedPtr
				}
			case *ssa.IndexAddr:
				if explicitPanic && (audit.ctx == nil || !emissionIsVargsAlloc(audit.ctx, address.X)) {
					result.recipe = coroPhysicalInstructionInterfaceFromCheckedPtr
				}
			}
		}
	case *ssa.BinOp:
		if (instruction.Op == token.EQL || instruction.Op == token.NEQ) &&
			(isUntypedNilConst(instruction.X) || isUntypedNilConst(instruction.Y)) {
			value := instruction.X
			if isUntypedNilConst(value) {
				value = instruction.Y
			}
			if _, ok := types.Unalias(audit.typeOf(value.Type())).Underlying().(*types.Interface); ok {
				result.recipe = coroPhysicalInstructionInterfaceNilCompare
				result.valueOperand = value
			}
		}
	}
	return result, nil
}

func planCoroPhysicalOutcomeInstruction(
	audit *coroPhysicalPureSSAAudit,
	cleanup *coroStaticCleanupPlan,
	instruction ssa.Instruction,
	capabilities coroPhysicalLoweringCapabilities,
	result *coroPhysicalInstructionPlan,
) {
	if audit == nil || result == nil || instruction == nil || instruction.Parent() != audit.fn {
		panic("physical outcome planning requires one exact instruction plan")
	}
	switch instruction := instruction.(type) {
	case *ssa.Return:
		result.outcome = coroPhysicalOutcomeReturn
	case *ssa.Defer:
		if !coroPhysicalCleanupContainsDefer(cleanup, instruction) {
			result.outcomeFailure = "defer registration is absent from the frozen cleanup plan"
			return
		}
		result.outcome = coroPhysicalOutcomeDeferRegister
	case *ssa.RunDefers:
		if cleanup == nil || len(cleanup.sites) == 0 {
			result.outcomeFailure = "RunDefers has no frozen cleanup plan"
			return
		}
		result.outcome = coroPhysicalOutcomeRunDefers
	case *ssa.Panic:
		if coroSyntheticSelectNoCasePanic(instruction) {
			result.outcome = coroPhysicalOutcomeSyntheticSelectTrap
			return
		}
		if !capabilities.explicitPanic {
			result.outcomeFailure = "explicit panic requires the explicit-status panic ABI"
			return
		}
		if reason := validateCoroExplicitStatusPanic(audit, instruction, capabilities); reason != "" {
			result.outcomeFailure = reason
			return
		}
		result.outcome = coroPhysicalOutcomePanic
	case *ssa.Call:
		if isCoroRecoverBuiltinCall(instruction) {
			if !capabilities.explicitPanic {
				result.outcomeFailure = "recover builtin requires the explicit-status panic ABI"
				return
			}
			result.outcome = coroPhysicalOutcomeRecover
			return
		}
		if audit.universe == nil || audit.universe.coroProgramIR == nil {
			return
		}
		frozen, found, err := audit.universe.coroProgramIR.callSitePlan(instruction)
		if err != nil {
			result.outcomeFailure = "load terminal intrinsic SitePlan: " + err.Error()
			return
		}
		if !found || frozen.failure != "" || !frozen.plan.Intrinsic ||
			frozen.opcode != llgoCoroGoexit ||
			frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineOutcome {
			return
		}
		if !capabilities.explicitPanic {
			result.outcomeFailure = "Goexit requires the explicit-status outcome ABI"
			return
		}
		result.outcome = coroPhysicalOutcomeGoexit
	}
}

func coroPhysicalCleanupContainsDefer(cleanup *coroStaticCleanupPlan, instruction *ssa.Defer) bool {
	if cleanup == nil || instruction == nil {
		return false
	}
	for _, site := range cleanup.sites {
		if site != nil && site.instruction == instruction {
			return true
		}
	}
	return false
}

func coroPhysicalCleanupContainsTerminalAllocation(cleanup *coroStaticCleanupPlan, allocation *ssa.Alloc) bool {
	if cleanup == nil || allocation == nil {
		return false
	}
	for _, candidate := range cleanup.terminalResultAllocations {
		if candidate == allocation {
			return true
		}
	}
	return false
}

func planCoroPhysicalOperationInstruction(
	audit *coroPhysicalPureSSAAudit,
	whole *coro.SSAPlan,
	instruction ssa.Instruction,
	capabilities coroPhysicalLoweringCapabilities,
	result *coroPhysicalInstructionPlan,
) {
	if audit == nil || result == nil || instruction == nil || instruction.Parent() != audit.fn {
		panic("physical operation planning requires one exact instruction plan")
	}
	failChannel := func(operation string) bool {
		if capabilities.channel {
			return false
		}
		result.operationFailure = operation + " requires the channel scheduler capability"
		return true
	}
	switch instruction := instruction.(type) {
	case *ssa.Send:
		if failChannel("channel send") {
			return
		}
		if err := validateCoroPhysicalChannelType(instruction.Chan.Type()); err != nil {
			result.operationFailure = "channel send type: " + err.Error()
			return
		}
		result.operation = coroPhysicalOperationChannelSend
	case *ssa.UnOp:
		if instruction.Op != token.ARROW {
			return
		}
		if failChannel("channel receive") {
			return
		}
		if err := validateCoroPhysicalChannelType(instruction.X.Type()); err != nil {
			result.operationFailure = "channel receive type: " + err.Error()
			return
		}
		result.operation = coroPhysicalOperationChannelReceive
	case *ssa.Select:
		if failChannel("channel select") {
			return
		}
		for index, state := range instruction.States {
			if state == nil {
				result.operationFailure = fmt.Sprintf("channel select case %d is nil", index)
				return
			}
			if state.Chan == nil {
				result.operationFailure = fmt.Sprintf("channel select case %d channel is nil", index)
				return
			}
			if err := validateCoroPhysicalChannelType(state.Chan.Type()); err != nil {
				result.operationFailure = fmt.Sprintf("channel select case %d type: %v", index, err)
				return
			}
		}
		if instruction.Blocking {
			result.operation = coroPhysicalOperationChannelSelectPark
		} else {
			result.operation = coroPhysicalOperationChannelSelectTry
		}
	case *ssa.Call:
		if isCoroCloseBuiltinCall(instruction) {
			if failChannel("channel close") {
				return
			}
			if !capabilities.explicitPanic {
				result.operationFailure = "channel close requires the explicit-status panic ABI"
				return
			}
			if len(instruction.Common().Args) != 1 {
				result.operationFailure = "channel close requires one exact channel operand"
				return
			}
			if err := validateCoroPhysicalChannelType(instruction.Common().Args[0].Type()); err != nil {
				result.operationFailure = "channel close type: " + err.Error()
				return
			}
			result.operation = coroPhysicalOperationChannelClose
			return
		}
		if audit.universe != nil && audit.universe.coroProgramIR != nil {
			frozen, found, err := audit.universe.coroProgramIR.callSitePlan(instruction)
			if err != nil {
				result.operationFailure = "load worker operation SitePlan: " + err.Error()
				return
			}
			if found && frozen.plan.Elision == CoroCallElidedCgoWorker {
				if !capabilities.worker {
					result.operationFailure = "generated cgo call requires the bounded worker capability"
					return
				}
				shape, recognized, err := validateCoroWorkerCgoCall(whole, audit.universe, instruction)
				if !recognized {
					result.operationFailure = "generated cgo worker elision has no exact typed call shape"
					return
				}
				if err != nil {
					result.operationFailure = "invalid generated cgo worker call: " + err.Error()
					return
				}
				result.operation = coroPhysicalOperationWorkerCgo
				result.operationCgo = &shape
				return
			}
			if found && frozen.plan.Intrinsic && frozen.opcode == llgoCgoCgocall &&
				frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineForeignSuspend {
				if !capabilities.worker {
					result.operationFailure = "generated C2 call requires the bounded worker capability"
					return
				}
				shape, recognized, err := validateCoroWorkerCgoErrnoCall(
					whole, audit.ctx, instruction,
				)
				if !recognized {
					result.operationFailure = "generated C2 worker intrinsic has no exact typed errno shape"
					return
				}
				if err != nil {
					result.operationFailure = "invalid generated C2 worker call: " + err.Error()
					return
				}
				result.operation = coroPhysicalOperationWorkerCgoErrno
				result.operationCgoErrno = &shape
				return
			}
			if found && frozen.plan.Intrinsic && isLLGoSyscallIntrinsic(frozen.opcode) &&
				frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineSuspend {
				if !capabilities.worker {
					result.operationFailure = "worker llgo.syscall requires the bounded worker capability"
					return
				}
				if err := validateCoroWorkerSyscallCall(whole, audit.universe, instruction); err != nil {
					result.operationFailure = "invalid worker llgo.syscall capability: " + err.Error()
					return
				}
				result.operation = coroPhysicalOperationWorkerSyscall
				return
			}
		}
		pointerSize := 0
		if audit.universe != nil && audit.universe.prog != nil {
			pointerSize = audit.universe.prog.PointerSize()
		}
		shape, recognized, err := validateCoroWorkerForeignCall(
			whole, audit.universe, instruction, pointerSize,
		)
		if !recognized {
			return
		}
		if !capabilities.worker {
			result.operationFailure = "blocking foreign call requires the bounded worker capability"
			return
		}
		if err != nil {
			result.operationFailure = "invalid bounded worker foreign call: " + err.Error()
			return
		}
		if shape.nilGuard && !capabilities.explicitPanic {
			result.operationFailure = "nullable dynamic raw C worker call requires the explicit-status panic ABI"
			return
		}
		result.operation = coroPhysicalOperationWorkerForeign
		result.operationWorker = &shape
	}
}

// planCoroPhysicalControlInstruction is the sole post-analysis selector for
// direct child-await and source goroutine-spawn control recipes. It records a
// non-hard await mismatch so the validator can still consider the established
// direct-plain path; once an await target was recognized, ABI failures are hard
// and cannot silently fall back. Every spawn mismatch is hard because a source
// Go instruction has no ordinary synchronous lowering.
func planCoroPhysicalControlInstruction(
	audit *coroPhysicalPureSSAAudit,
	whole *coro.SSAPlan,
	instruction ssa.Instruction,
	capabilities coroPhysicalLoweringCapabilities,
	result *coroPhysicalInstructionPlan,
) {
	if audit == nil || result == nil || instruction == nil || instruction.Parent() != audit.fn {
		panic("physical control planning requires one exact instruction plan")
	}
	switch instruction := instruction.(type) {
	case *ssa.Call:
		if whole == nil {
			return
		}
		common := instruction.Common()
		callPlan, callPlanned := whole.CallPlan(instruction)
		if callPlanned && callPlan.RawPlain {
			result.controlFailureHard = true
			if audit.universe == nil {
				result.controlFailure = "raw/plain invocation requires one frozen emission universe"
				return
			}
			target, targetPlan, err := validateCoroRawPlainSourceCall(
				whole, audit.universe.Resolve, instruction,
			)
			if err != nil {
				result.controlFailure = "invalid raw/plain invocation: " + err.Error()
				return
			}
			result.control = coroPhysicalControlRawPlainCall
			result.controlTarget = target
			result.controlTargetID = targetPlan.ID
			return
		}
		if !capabilities.childAwait {
			return
		}
		if common != nil && common.IsInvoke() {
			result.controlFailureHard = true
			if capabilities.managedInterface.acceptsCall(instruction) {
				if !callPlanned || callPlan.Rep != coro.Dispatch {
					result.controlFailure = "managed interface descriptor call lost its frozen Dispatch CallPlan"
					return
				}
				if callPlan.Open {
					if err := validateCoroManagedInterfaceDispatchCall(
						whole, audit.universe, audit.fn, instruction, callPlan,
					); err != nil {
						result.controlFailure = "invalid managed interface await: " + err.Error()
						return
					}
				}
				signature, err := coroInterfaceDispatchSourceSignature(common)
				if err != nil {
					result.controlFailure = "managed interface await signature: " + err.Error()
					return
				}
				result.control = coroPhysicalControlManagedInterfaceAwait
				result.controlSignature = signature
				return
			}
			if !capabilities.explicitPanic {
				if _, err := resolveCoroClosedInterfacePlainCall(whole, instruction); err == nil {
					result.controlFailureHard = false
					return
				}
			}
			if capabilities.interfacePlain.acceptsCall(instruction) {
				result.controlFailureHard = false
				return
			}
			dispatch, err := resolveCoroInterfaceDispatchPlan(whole, audit.universe, instruction)
			if err != nil {
				result.controlFailure = "unsupported interface invoke: " + err.Error()
				return
			}
			if !coroInterfaceDispatchNeedsAwait(dispatch) {
				result.controlFailure = "unsupported interface invoke: closed interface dispatch has no coroutine target"
				return
			}
			result.control = coroPhysicalControlClosedInterfaceAwait
			result.controlInterface = dispatch
			return
		}
		if callPlanned && callPlan.Rep == coro.Dispatch && common != nil && common.StaticCallee() == nil {
			result.controlFailureHard = true
			if !capabilities.managedDispatch {
				result.controlFailure = "managed descriptor call requires the v1 descriptor dispatch capability"
				return
			}
			if callPlan.SyncDispatch {
				if err := validateCoroPlainDispatchCall(whole, audit.fn, instruction, callPlan, audit.universe); err != nil {
					result.controlFailure = "invalid synchronous descriptor call: " + err.Error()
					return
				}
				result.control = coroPhysicalControlPlainDispatch
				return
			}
			if err := validateCoroManagedDispatchCall(whole, audit.fn, instruction, callPlan, audit.universe); err != nil {
				result.controlFailure = "invalid managed descriptor await: " + err.Error()
				return
			}
			if err := validateCoroManagedDispatchAwaitShape(whole, audit.fn, instruction, callPlan); err != nil {
				result.controlFailure = "invalid managed descriptor await: " + err.Error()
				return
			}
			result.control = coroPhysicalControlDispatchAwait
			return
		}
		callerPlan, found := whole.FunctionPlan(audit.fn)
		if !found {
			result.controlFailure = "current function has no compilation plan"
			return
		}
		callee, targetPlan, err := resolveCoroStaticAwait(whole, callerPlan, instruction, audit.universe)
		if err != nil {
			result.controlFailure = err.Error()
			return
		}
		calleeSignature := coroPhysicalNormalizeSourceSignature(callee.Signature)
		if audit.universe != nil {
			calleeSignature, err = audit.universe.coroPhysicalSourceSignature(callee)
		}
		if err == nil {
			err = validateCoroLeafPhysicalSignature(targetPlan, calleeSignature)
		}
		if err != nil {
			result.controlFailure = "child await signature: " + err.Error()
			result.controlFailureHard = true
			return
		}
		result.control = coroPhysicalControlDirectAwait
		result.controlTarget = callee
		result.controlTargetID = targetPlan.ID
	case *ssa.Go:
		result.controlFailureHard = true
		if !capabilities.staticSpawn {
			result.controlFailure = "goroutine spawn requires the closed-static scheduler capability"
			return
		}
		if whole == nil {
			result.controlFailure = "goroutine spawn requires one compilation plan"
			return
		}
		callPlan, found := whole.CallPlan(instruction)
		if !found {
			result.controlFailure = "goroutine spawn has no compilation CallPlan"
			return
		}
		switch callPlan.Rep {
		case coro.DirectCoro:
			target, targetPlan, err := resolveCoroDirectStaticSpawn(whole, instruction, capabilities.managedDispatch)
			if err != nil {
				result.controlFailure = "unsupported closed static spawn: " + err.Error()
				return
			}
			targetSignature := coroPhysicalNormalizeSourceSignature(target.Signature)
			if audit.universe != nil {
				targetSignature, err = audit.universe.coroPhysicalSourceSignature(target)
			}
			if err == nil {
				err = validateCoroLeafPhysicalSignature(targetPlan, targetSignature)
			}
			if err != nil {
				result.controlFailure = "spawn target signature: " + err.Error()
				return
			}
			result.control = coroPhysicalControlDirectSpawn
			result.controlTarget = target
			result.controlTargetID = targetPlan.ID
		case coro.Dispatch:
			if !capabilities.managedDispatch {
				result.controlFailure = "managed descriptor spawn requires the v1 descriptor dispatch capability"
				return
			}
			if _, err := whole.ResolveManagedDispatchSpawn(instruction); err != nil {
				result.controlFailure = "unsupported managed descriptor spawn: " + err.Error()
				return
			}
			if err := validateCoroManagedDispatchSignatureShape(instruction.Common().Signature()); err != nil {
				result.controlFailure = "managed descriptor spawn signature: " + err.Error()
				return
			}
			result.control = coroPhysicalControlDispatchSpawn
		default:
			result.controlFailure = "goroutine spawn has unsupported representation " + callPlan.Rep.String()
		}
	}
}

func coroPhysicalContainerPlan(audit *coroPhysicalPureSSAAudit, value ssa.Value) (coroPhysicalContainerKind, int64, error) {
	if audit == nil || value == nil || value.Type() == nil {
		return coroPhysicalContainerNone, 0, fmt.Errorf("physical container has no exact type")
	}
	switch container := types.Unalias(audit.typeOf(value.Type())).Underlying().(type) {
	case *types.Basic:
		if coroPhysicalStringBasic(container) {
			return coroPhysicalContainerString, 0, nil
		}
	case *types.Array:
		return coroPhysicalContainerArray, container.Len(), nil
	case *types.Slice:
		return coroPhysicalContainerSlice, 0, nil
	case *types.Pointer:
		if array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array); ok {
			return coroPhysicalContainerArrayPointer, array.Len(), nil
		}
	}
	return coroPhysicalContainerNone, 0, fmt.Errorf("unsupported physical container type %s", audit.typeOf(value.Type()))
}

func coroPhysicalSafeFixedArrayIndex(
	audit *coroPhysicalPureSSAAudit,
	whole *coro.SSAPlan,
	operation ssa.Instruction,
	collection, index ssa.Value,
) (bool, error) {
	if audit == nil || operation == nil || collection == nil || index == nil || operation.Parent() != audit.fn {
		return false, fmt.Errorf("safe fixed-array plan requires exact source operands")
	}
	if whole == nil {
		// Structural validators have no frozen optimization fact. Preserve the
		// checked recipe; active Compilation paths always provide the SSAPlan.
		return false, nil
	}
	bound, fixed := coroPhysicalFixedArrayBound(audit, collection)
	recomputed := fixed && coro.ProveSSAExactSafeFixedArrayIndex(operation.Parent(), index, bound, operation)
	plannedBound, planned := whole.ExactSafeFixedArrayIndex(operation)
	if planned != recomputed || planned && plannedBound != bound {
		return false, fmt.Errorf(
			"safe fixed-array index disagrees between CoroPlan and physical projection (planned=%t bound=%d recomputed=%t bound=%d)",
			planned, plannedBound, recomputed, bound,
		)
	}
	return planned, nil
}

func coroPhysicalFixedArrayBound(audit *coroPhysicalPureSSAAudit, collection ssa.Value) (int64, bool) {
	if audit == nil || collection == nil || collection.Type() == nil {
		return 0, false
	}
	switch container := types.Unalias(audit.typeOf(collection.Type())).Underlying().(type) {
	case *types.Array:
		return container.Len(), true
	case *types.Pointer:
		if array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array); ok {
			return array.Len(), true
		}
	}
	return 0, false
}
