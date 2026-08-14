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
	"math"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
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
	coroPhysicalInstructionStaticArrayRangeDerefElided
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
	coroPhysicalInstructionIntegerDivideByZeroGuard
	coroPhysicalInstructionTerminalResultAllocation
	coroPhysicalInstructionFrameAllocation
	coroPhysicalInstructionBorrowedAllocation
	coroPhysicalInstructionFrameBitcastAllocation
	coroPhysicalInstructionFrameAllocaBytes
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
	case coroPhysicalInstructionStaticArrayRangeDerefElided:
		return "static-array-range-deref-elided"
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
	case coroPhysicalInstructionIntegerDivideByZeroGuard:
		return "integer-divide-by-zero-guard"
	case coroPhysicalInstructionTerminalResultAllocation:
		return "terminal-result-allocation"
	case coroPhysicalInstructionFrameAllocation:
		return "frame-allocation"
	case coroPhysicalInstructionBorrowedAllocation:
		return "borrowed-allocation"
	case coroPhysicalInstructionFrameBitcastAllocation:
		return "frame-bitcast-allocation"
	case coroPhysicalInstructionFrameAllocaBytes:
		return "frame-alloca-bytes"
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
	coroPhysicalControlExactInterfaceCall
	coroPhysicalControlExactInterfaceAwait
	coroPhysicalControlPlainDispatch
	coroPhysicalControlNilDispatchFault
	coroPhysicalControlRawPlainCall
	coroPhysicalControlDirectSpawn
	coroPhysicalControlDispatchSpawn
	coroPhysicalControlDirectOutcome
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
	case coroPhysicalControlExactInterfaceCall:
		return "exact-interface-call"
	case coroPhysicalControlExactInterfaceAwait:
		return "exact-interface-await"
	case coroPhysicalControlPlainDispatch:
		return "plain-dispatch"
	case coroPhysicalControlNilDispatchFault:
		return "nil-dispatch-fault"
	case coroPhysicalControlRawPlainCall:
		return "raw-plain-call"
	case coroPhysicalControlDirectSpawn:
		return "direct-spawn"
	case coroPhysicalControlDispatchSpawn:
		return "dispatch-spawn"
	case coroPhysicalControlDirectOutcome:
		return "direct-outcome"
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
	coroPhysicalOperationSameMForeign
	coroPhysicalOperationSameMPython
	coroPhysicalOperationWorkerCgo
	coroPhysicalOperationWorkerCgoErrno
	coroPhysicalOperationHostCall
	coroPhysicalOperationControl
	// NativeSyscall executes the compiler-certified word call synchronously on
	// the current M. It owns no park/resume transaction and must not request the
	// program worker fleet.
	coroPhysicalOperationNativeSyscall
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
	case coroPhysicalOperationSameMForeign:
		return "same-m-foreign"
	case coroPhysicalOperationSameMPython:
		return "same-m-python"
	case coroPhysicalOperationWorkerCgo:
		return "worker-cgo"
	case coroPhysicalOperationWorkerCgoErrno:
		return "worker-cgo-errno"
	case coroPhysicalOperationHostCall:
		return "host-operation"
	case coroPhysicalOperationControl:
		return "control-operation"
	case coroPhysicalOperationNativeSyscall:
		return "native-syscall"
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
	sameMForeign     bool
	hostOperation    bool
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
	controlReceiver    ssa.Value
	controlClosure     ssa.Value
	controlInterface   *coroInterfaceDispatchPlan
	controlSignature   *types.Signature
	controlFailure     string
	controlFailureHard bool
	// directOutcomeNativeResult proves that this exact target's result-slot
	// struct fits the target's native-stack single-object limit. Full LLVM
	// coroutine callers may use managed frame storage instead; an outcome-plain
	// DAG caller has no suspension-capable allocator fallback and must require
	// this frozen target-layout fact.
	directOutcomeNativeResult bool
	operation                 coroPhysicalOperationRecipe
	operationFailure          string
	operationWorker           *coroWorkerForeignCallShape
	operationPythonTarget     *ssa.Function
	operationPythonOpcode     int
	operationCgo              *coroWorkerCgoCallShape
	operationCgoErrno         *coroWorkerCgoErrnoCallShape
	operationHost             coroHostOperationCallShape
	operationControl          CoroControlOperation
	outcome                   coroPhysicalOutcomeRecipe
	outcomeFailure            string
	elideValue                bool
	reuseValueAddress         bool
	valueOperand              ssa.Value
	container                 coroPhysicalContainerKind
	bound                     int64
	nilGuard                  bool
	boundsGuard               bool
	boundsDisabled            bool
	rawInterfaceReceiver      bool
}

func (plan coroPhysicalInstructionPlan) mayFault() bool {
	return plan.nilGuard || plan.boundsGuard ||
		plan.recipe == coroPhysicalInstructionFieldAddr ||
		plan.recipe == coroPhysicalInstructionDeref ||
		plan.recipe == coroPhysicalInstructionBuiltinNilGuard ||
		plan.recipe == coroPhysicalInstructionIntegerDivideByZeroGuard
}

// elidesRuntimeHelper reports whether this frozen physical recipe replaces one
// logical LLSSA helper with compiler-owned structured control flow. Emission
// must use this projection rather than reclassifying the raw SSA instruction:
// the helper inventory remains part of effect/closure planning, while the
// selected recipe is the sole authority for whether a call is physically
// emitted in the live coroutine frame.
func (plan coroPhysicalInstructionPlan) elidesRuntimeHelper(helper string) bool {
	if plan.elideValue {
		return true
	}
	if plan.rawInterfaceReceiver && helper == "IfacePtrData" {
		return true
	}
	if helper == coroManagedFrameSlotAllocZCall {
		switch plan.control {
		case coroPhysicalControlDirectAwait, coroPhysicalControlDirectOutcome, coroPhysicalControlDispatchAwait,
			coroPhysicalControlClosedInterfaceAwait, coroPhysicalControlManagedInterfaceAwait,
			coroPhysicalControlExactInterfaceAwait:
			return false
		default:
			// The conservative pre-analysis inventory cannot yet know whether
			// a large-result call becomes an await. Every non-await recipe
			// returns directly and therefore owns no managed result slot.
			return true
		}
	}
	switch plan.recipe {
	case coroPhysicalInstructionFieldAddr, coroPhysicalInstructionDeref:
		if helper == "AssertNilDeref" || helper == "AssertNilDerefPtr" {
			return true
		}
	case coroPhysicalInstructionIndexAddr, coroPhysicalInstructionIndex:
		if helper == "CheckIndexRange" || helper == "AssertNilDeref" || helper == "AssertNilDerefPtr" {
			return true
		}
		if helper == "AllocZ" && plan.reuseValueAddress {
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
	case coroPhysicalInstructionIntegerDivideByZeroGuard:
		if helper == "AssertDivideByZero" {
			return true
		}
	case coroPhysicalInstructionFrameAllocation, coroPhysicalInstructionBorrowedAllocation:
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
	preempt           *coroPhysicalPreemptPlan
	atomicCost        uint64
	atomicCostProof   coro.AtomicCostProof
	atomicCertificate string
	staticOutcome     bool
	reachableBlocks   map[*ssa.BasicBlock]bool
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
		reachableBlocks:   make(map[*ssa.BasicBlock]bool, len(audit.reachableBlocks)),
		instructions:      make(map[ssa.Instruction]coroPhysicalInstructionPlan),
	}
	for block, reachable := range audit.reachableBlocks {
		plan.reachableBlocks[block] = reachable
	}
	var logical coro.FunctionPlan
	if whole != nil {
		function, planned := whole.FunctionPlan(audit.fn)
		if !planned {
			return nil, fmt.Errorf("physical function planning requires a frozen function plan")
		}
		logical = function
		plan.needsPreempt = function.Exec.Contains(coro.NeedsPreempt)
		plan.staticOutcome = function.StaticOutcome
	}
	preempt, err := planCoroPhysicalPreemption(
		audit, critical, plan.needsPreempt,
	)
	if err != nil {
		return nil, err
	}
	plan.preempt = preempt
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
	for instruction, instructionPlan := range plan.instructions {
		index, ok := instruction.(*ssa.Index)
		if !ok {
			continue
		}
		call, ok := index.X.(*ssa.Call)
		if !ok {
			continue
		}
		producer, ok := plan.instructions[call]
		if !ok {
			continue
		}
		switch producer.control {
		case coroPhysicalControlDirectAwait, coroPhysicalControlDirectOutcome, coroPhysicalControlDispatchAwait,
			coroPhysicalControlClosedInterfaceAwait, coroPhysicalControlManagedInterfaceAwait,
			coroPhysicalControlExactInterfaceAwait:
			instructionPlan.reuseValueAddress = true
			plan.instructions[instruction] = instructionPlan
		}
	}
	if whole != nil {
		for instruction, instructionPlan := range plan.instructions {
			if instruction == nil || instruction.Block() == nil ||
				!plan.reachableBlocks[instruction.Block()] ||
				!coroPhysicalInstructionNeedsRuntimeContext(instructionPlan) {
				continue
			}
			if !logical.Exec.Contains(coro.NeedsRuntimeContext) {
				return nil, fmt.Errorf(
					"physical %s operation at %q requires runtime context but logical exec is %s",
					instructionPlan.control, instruction.String(), logical.Exec,
				)
			}
		}
	}
	if logical.AtomicCostProof.ProvesOutcomePlain() {
		if audit.universe == nil || audit.universe.coroProgramIR == nil {
			return nil, fmt.Errorf("atomic-cost physical proof requires one frozen ProgramIR")
		}
		facts, err := audit.universe.coroProgramIR.functionLocalBodyFacts(audit.fn)
		if err != nil {
			return nil, fmt.Errorf("atomic-cost physical proof: %w", err)
		}
		callees := make(map[ssa.CallInstruction]coro.SSAAtomicCalleeCertificate)
		for instruction, instructionPlan := range plan.instructions {
			if instructionPlan.control != coroPhysicalControlDirectOutcome {
				continue
			}
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || instructionPlan.controlTarget == nil || instructionPlan.controlTargetID == "" {
				return nil, fmt.Errorf("atomic-cost physical proof contains an incomplete direct outcome edge")
			}
			targetPlan, planned := whole.FunctionPlan(instructionPlan.controlTarget)
			if !planned || targetPlan.ID != instructionPlan.controlTargetID ||
				!targetPlan.AtomicCostProof.ProvesOutcomePlain() {
				return nil, fmt.Errorf("atomic-cost physical proof target %q has no exact outcome capability", instructionPlan.controlTargetID)
			}
			callees[call] = coro.SSAAtomicCalleeCertificate{
				Function: targetPlan.ID, Cost: targetPlan.AtomicCost, Certificate: targetPlan.AtomicCostCertificate,
			}
		}
		if err := coro.VerifySSAAtomicCostCertificate(
			logical.ID, logical.AtomicCostProof, logical.AtomicCost, logical.AtomicCostCertificate,
			facts.AtomicPath, callees,
		); err != nil {
			return nil, fmt.Errorf("atomic-cost physical proof: %w", err)
		}
		plan.atomicCost = logical.AtomicCost
		plan.atomicCostProof = logical.AtomicCostProof
		plan.atomicCertificate = logical.AtomicCostCertificate
	}
	return plan, nil
}

// coroPhysicalInstructionNeedsRuntimeContext is the emission-side closure of
// compiler-injected helpers whose requirement is not represented by an
// ordinary Go call edge. ProgramIR seeds the corresponding source operation;
// this independent gate prevents a stale/custom analyzer from issuing an
// unsafe frame descriptor when those two projections disagree.
func coroPhysicalInstructionNeedsRuntimeContext(plan coroPhysicalInstructionPlan) bool {
	switch plan.operation {
	case coroPhysicalOperationChannelSelectPark, coroPhysicalOperationChannelSelectTry:
		return true
	default:
		return false
	}
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

// programCapabilities projects optional target-service demand only after the
// complete physical plan transaction has committed. Logical WaitForeign or a
// target's ability to host workers is insufficient: same-M episodes and dead
// source blocks must not start a worker pool. Conversely every reachable
// worker transaction below is the exact recipe codegen will emit.
func (ir *coroProgramIR) programCapabilities() (coro.ProgramCapabilities, error) {
	if ir == nil || !ir.physicalPlansSealed {
		return 0, fmt.Errorf("coroutine program capabilities require sealed physical plans")
	}
	worker := false
	for key, function := range ir.physicalPlans {
		if key.function == nil || key.owner == nil || function == nil ||
			function.function != key.function || function.owner != key.owner {
			return 0, fmt.Errorf("coroutine program capabilities found an incomplete physical owner")
		}
		for instruction, plan := range function.instructions {
			if instruction == nil || instruction.Parent() != function.function || instruction.Block() == nil {
				return 0, fmt.Errorf("coroutine program capabilities found an incomplete physical instruction")
			}
			if !function.reachableBlocks[instruction.Block()] {
				continue
			}
			switch plan.operation {
			case coroPhysicalOperationWorkerSyscall,
				coroPhysicalOperationWorkerForeign,
				coroPhysicalOperationWorkerCgo,
				coroPhysicalOperationWorkerCgoErrno:
				worker = true
			}
		}
	}
	capabilities := coro.NewProgramCapabilities(worker)
	if !capabilities.Valid() {
		return 0, fmt.Errorf("coroutine program capabilities are invalid")
	}
	return capabilities, nil
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
	boundsDisabled := audit.ctx != nil && audit.ctx.prog != nil &&
		audit.ctx.prog.BoundsChecksDisabled()
	semantic, err := planCoroSemanticInstruction(instruction)
	if audit.universe != nil && audit.universe.coroProgramIR != nil && owner != nil {
		semantic, err = audit.universe.coroProgramIR.semanticInstructionPlan(audit.fn, owner, instruction)
	}
	if err != nil {
		return result, fmt.Errorf("load semantic instruction recipe: %w", err)
	}
	result.semantic = semantic
	if !semantic.evaluated {
		return result, nil
	}
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
		case audit.frameRetainsManagedHeapAllocation(instruction):
			// Preserve AllocZ for semantic escapes and target-layout promotion
			// of oversized locals. CoroSplit retains the resulting pointer.
		case audit.frameRetainsBorrowedAllocation(instruction):
			result.recipe = coroPhysicalInstructionBorrowedAllocation
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
			// A single-index range over an array or *array uses only the
			// statically known length. When its operand contains no call or
			// receive, Go does not evaluate the dereference at all. Freeze that
			// source-language decision before the frame-retention proof can
			// conservatively turn every nullable pointer load into a faulting
			// physical Deref.
			if skipUnusedArrayDeref(instruction) &&
				!isEffectfulArrayPointerDeref(instruction) {
				result.recipe = coroPhysicalInstructionStaticArrayRangeDerefElided
				break
			}
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
		// -B still needs a frozen unchecked aggregate recipe even when this
		// exact function had no panic edge before lowering. Otherwise ordinary
		// LLSSA would rediscover a pointer-to-array nil helper after the
		// emission universe was frozen.
		if !explicitPanic && !boundsDisabled {
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
		result.boundsDisabled = boundsDisabled
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			emissionArrayPointerNeedsNilCheck(instruction.X, instruction)
		safe, err := coroPhysicalSafeFixedArrayIndex(audit, whole, instruction, instruction.X, instruction.Index)
		if err != nil {
			return result, err
		}
		result.boundsGuard = !safe && !boundsDisabled
		if result.nilGuard && !explicitPanic {
			return result, fmt.Errorf("unchecked IndexAddr requires a nil guard but the physical ABI has no explicit panic outcome")
		}
	case *ssa.Index:
		if !explicitPanic && !boundsDisabled {
			break
		}
		result.recipe = coroPhysicalInstructionIndex
		container, bound, err := coroPhysicalContainerPlan(audit, instruction.X)
		if err != nil {
			return result, err
		}
		result.container, result.bound = container, bound
		result.boundsDisabled = boundsDisabled
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			emissionArrayPointerNeedsNilCheck(instruction.X, instruction)
		safe, err := coroPhysicalSafeFixedArrayIndex(audit, whole, instruction, instruction.X, instruction.Index)
		if err != nil {
			return result, err
		}
		result.boundsGuard = !safe && !boundsDisabled
		if result.nilGuard && !explicitPanic {
			return result, fmt.Errorf("unchecked Index requires a nil guard but the physical ABI has no explicit panic outcome")
		}
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
		if !explicitPanic && !boundsDisabled {
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
		result.boundsDisabled = boundsDisabled
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			emissionArrayPointerNeedsNilCheck(instruction.X, instruction)
		result.boundsGuard = !boundsDisabled
		if result.nilGuard && !explicitPanic {
			return result, fmt.Errorf("unchecked Slice requires a nil guard but the physical ABI has no explicit panic outcome")
		}
	case *ssa.SliceToArrayPointer:
		length, exact := coroSliceToArrayPointerLen(instruction, audit.typeOf)
		if !exact || length < 0 {
			return result, fmt.Errorf("slice-to-array-pointer has no exact physical length")
		}
		result.recipe = coroPhysicalInstructionSliceToArrayPointer
		result.bound = length
		result.boundsDisabled = boundsDisabled
		result.boundsGuard = length != 0 && !boundsDisabled
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
			if found && frozen.failure == "" && frozen.plan.Intrinsic {
				switch {
				case frozen.opcode == llgoAlloca &&
					frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineNoSuspend:
					if len(instruction.Common().Args) != 1 {
						return result, fmt.Errorf("physical llgo.alloca has no exact size operand")
					}
					size, exact := coroConstantAllocaSize(instruction.Common().Args[0])
					if !exact {
						return result, fmt.Errorf(
							"dynamic llgo.alloca is valid only in a no-suspend plain island; a physical coroutine requires an exact constant frame size",
						)
					}
					result.recipe = coroPhysicalInstructionFrameAllocaBytes
					result.bound = size
				case (frozen.opcode == llgoAllocaCStr || frozen.opcode == llgoAllocaCStrs) &&
					frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineWithLoweredCalls:
					if reason := audit.requireFrozenStructuredRuntimeHelpers(instruction, "AllocU", "CStrCopy"); reason != "" {
						return result, fmt.Errorf("physical llgo C string heap storage: %s", reason)
					}
					result.recipe = coroPhysicalInstructionHeapCStr
				}
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
		elided, err := coroPlannedExactInterfaceMakeElision(whole, instruction)
		if err != nil {
			return result, fmt.Errorf("exact interface construction elision: %w", err)
		}
		if elided {
			result.elideValue = true
			break
		}
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
		operand, _ := types.Unalias(audit.typeOf(instruction.X.Type())).Underlying().(*types.Basic)
		if explicitPanic && operand != nil && operand.Info()&types.IsInteger != 0 &&
			(instruction.Op == token.QUO || instruction.Op == token.REM) &&
			!ssaIntegerValueProvenNonZeroAt(instruction.Y, instruction) {
			result.recipe = coroPhysicalInstructionIntegerDivideByZeroGuard
			break
		}
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

func coroConstantAllocaSize(value ssa.Value) (int64, bool) {
	switch value := value.(type) {
	case *ssa.ChangeType:
		return coroConstantAllocaSize(value.X)
	case *ssa.Convert:
		return coroConstantAllocaSize(value.X)
	case *ssa.Const:
		if value.Value == nil {
			return 0, false
		}
		basic, ok := types.Unalias(value.Type()).Underlying().(*types.Basic)
		if !ok || basic.Info()&types.IsInteger == 0 || constant.Sign(value.Value) < 0 {
			return 0, false
		}
		size, exact := constant.Uint64Val(value.Value)
		if !exact || size > math.MaxInt64 {
			return 0, false
		}
		return int64(size), true
	default:
		return 0, false
	}
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
			if audit != nil && audit.plan != nil {
				if logical, planned := audit.plan.FunctionPlan(audit.fn); planned &&
					!logical.Exec.Contains(coro.NeedsCleanupFrame) {
					// ProgramIR removed NeedsCleanupFrame only after proving that no
					// reachable Defer registration remains. Leave this synthetic
					// RunDefers as an explicit no-op physical recipe.
					return
				}
			}
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
			if found && frozen.plan.ControlOperation != CoroControlNone {
				if frozen.failure != "" {
					result.operationFailure = "invalid typed control operation: " + frozen.failure
					return
				}
				if !frozen.plan.Intrinsic ||
					frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend {
					result.operationFailure = "typed control operation has an incompatible frozen intrinsic recipe"
					return
				}
				result.operation = coroPhysicalOperationControl
				result.operationControl = frozen.plan.ControlOperation
				return
			}
			if found && frozen.plan.Intrinsic && frozen.opcode == llgoCoroHostOperation &&
				frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineSuspend {
				if !capabilities.hostOperation {
					result.operationFailure = "host operation requires the host-pull operation capability"
					return
				}
				if !frozen.hostOperation.valid() {
					result.operationFailure = "host operation has no frozen ProgramIR call shape"
					return
				}
				result.operation = coroPhysicalOperationHostCall
				result.operationHost = frozen.hostOperation
				return
			}
			if found && frozen.plan.Elision == CoroCallElidedPython {
				if frozen.failure != "" {
					result.operationFailure = "invalid Python operation: " + frozen.failure
					return
				}
				if frozen.plan.ElisionCertificate == "" {
					result.operationFailure = "Python operation has no frozen call-site certificate"
					return
				}
				if !capabilities.sameMForeign {
					result.operationFailure = "Python operation requires the native same-M foreign-episode capability"
					return
				}
				if !isCoroProgramManagedEntry(audit.fn) {
					result.operationFailure = "Python operation has no compiler-owned program-root owner realm"
					return
				}
				if target := frozen.plan.PythonTarget; target != nil {
					resolved, required := audit.universe.Resolve(instruction.Common().StaticCallee())
					if !required || resolved != target {
						result.operationFailure = "Python operation target differs from its frozen frontend declaration"
						return
					}
					background, classified, err := audit.universe.FunctionBackground(target)
					if err != nil || !classified || background != llssa.InPython || frozen.plan.Intrinsic {
						result.operationFailure = "Python operation target has no exact InPython frontend identity"
						return
					}
					result.operationPythonTarget = target
				} else {
					if frozen.plan.Intrinsic || !isCoroPythonIntrinsicOpcode(frozen.opcode) {
						result.operationFailure = "Python construction operation has no exact compiler-owned opcode"
						return
					}
					result.operationPythonOpcode = frozen.opcode
				}
				result.operation = coroPhysicalOperationSameMPython
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
				(frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineSuspend ||
					frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineNativeBlock) {
				if !capabilities.worker {
					result.operationFailure = "worker llgo.syscall requires the bounded worker capability"
					return
				}
				if err := validateCoroWorkerSyscallCall(whole, audit.universe, instruction); err != nil {
					result.operationFailure = "invalid worker llgo.syscall capability: " + err.Error()
					return
				}
				if frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineNativeBlock {
					result.operation = coroPhysicalOperationNativeSyscall
				} else {
					result.operation = coroPhysicalOperationWorkerSyscall
				}
				return
			}
		}
		pointerSize := 0
		if audit.universe != nil && audit.universe.prog != nil {
			pointerSize = audit.universe.prog.PointerSize()
		}
		shape, recognized, err := validateCoroWorkerForeignCallWithAuthority(
			audit.foreignCallAuthority(),
			instruction, pointerSize,
		)
		if !recognized {
			return
		}
		if err != nil {
			result.operationFailure = "invalid managed foreign call: " + err.Error()
			return
		}
		if shape.nilGuard && !capabilities.explicitPanic {
			result.operationFailure = "nullable dynamic raw C worker call requires the explicit-status panic ABI"
			return
		}
		switch shape.mode {
		case coroForeignCallModeWorker:
			if !capabilities.worker {
				result.operationFailure = "blocking foreign call requires the bounded worker capability"
				return
			}
			result.operation = coroPhysicalOperationWorkerForeign
		case coroForeignCallModeSameM:
			if !capabilities.sameMForeign {
				result.operationFailure = "same-M foreign call requires the native foreign-episode capability"
				return
			}
			result.operation = coroPhysicalOperationSameMForeign
		default:
			result.operationFailure = "managed foreign call selected an unknown execution mode"
			return
		}
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
			receiver, target, targetPlan, exact, err :=
				whole.ResolveExactInterfaceCall(instruction)
			if err != nil {
				result.controlFailure = "invalid exact interface call: " + err.Error()
				return
			}
			if exact && coroExactInterfaceTargetDirectPlain(targetPlan) {
				result.control = coroPhysicalControlExactInterfaceCall
				result.controlTarget = target
				result.controlTargetID = targetPlan.ID
				result.controlReceiver = receiver
				// The direct occurrence consumes the concrete SSA value and never
				// emits the logical IfacePtrData normalization helper.
				result.rawInterfaceReceiver = true
				return
			}
			if exact && coroExactInterfaceTargetDirectAwait(targetPlan) {
				result.control = coroPhysicalControlExactInterfaceAwait
				result.controlTarget = target
				result.controlTargetID = targetPlan.ID
				result.controlReceiver = receiver
				result.rawInterfaceReceiver = true
				return
			}
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
				result.rawInterfaceReceiver =
					capabilities.managedInterface.acceptsRawReceiverCall(instruction)
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
			// A closed empty target set is an exact nil-only function value, not
			// a child capability. Keep it in the current physical frame and
			// route the inevitable nil call through the explicit-status fault
			// edge. Inventing a child await here would disagree with the effect
			// graph, which deliberately contributes no AwaitStructured bit for
			// a nonexistent target.
			nilOnly := !callPlan.Open && callPlan.MayBeNil && len(callPlan.Targets) == 0
			if nilOnly {
				if err := validateCoroPlainDispatchCall(whole, audit.fn, instruction, callPlan, audit.universe); err != nil {
					result.controlFailure = "invalid nil-only descriptor call: " + err.Error()
					return
				}
				result.control = coroPhysicalControlNilDispatchFault
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
		callee, targetPlan, closureValue, err := resolveCoroStaticAwait(whole, callerPlan, instruction, audit.universe)
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
		if targetPlan.HasStaticOutcome() {
			result.control = coroPhysicalControlDirectOutcome
			if audit.ctx != nil && audit.ctx.prog != nil {
				result.directOutcomeNativeResult = !audit.ctx.prog.LocalGoTypeExceedsNativeStack(
					newOutcomePlainPhysicalABI(calleeSignature).resultSlotType,
				)
			}
		} else {
			result.control = coroPhysicalControlDirectAwait
		}
		result.controlTarget = callee
		result.controlTargetID = targetPlan.ID
		result.controlClosure = closureValue
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
			if instruction.Common() != nil && instruction.Common().IsInvoke() &&
				(capabilities.managedInterface == nil ||
					!capabilities.managedInterface.acceptsCall(instruction)) {
				result.controlFailure = "managed interface spawn requires the receiver-aware descriptor capability"
				return
			}
			if _, err := whole.ResolveManagedSpawn(instruction); err != nil {
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

func coroExactInterfaceTargetDirectPlain(plan coro.FunctionPlan) bool {
	return plan.External == coro.Defined &&
		plan.Emission == coro.EmitPlain &&
		plan.Primary == coro.PrimaryPlain &&
		plan.Demand != coro.NoDemand &&
		plan.Effect == coro.NoSuspend &&
		!plan.Exec.Contains(coro.MayUnwind|coro.NeedsPreempt|coro.OpaqueExec|coro.BlockForeign|coro.ThreadAffine)
}

func coroExactInterfaceTargetDirectAwait(plan coro.FunctionPlan) bool {
	return plan.External == coro.Defined &&
		plan.Emission == coro.EmitCoroutine &&
		plan.Primary == coro.PrimaryCoroutine &&
		plan.ManagedDemand.Contains(coro.AsyncDemand) &&
		(plan.FuncRep == coro.DirectCoro || plan.FuncRep == coro.Dispatch) &&
		plan.Effect.MaySuspend() &&
		!plan.Effect.IsOpaque() &&
		!plan.Exec.IsOpaque()
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
