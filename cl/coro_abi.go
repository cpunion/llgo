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
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"github.com/goplus/llgo/cl/blocks"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroSyntheticSelectNoCaseMessage = "blocking select matched no case"

// x/tools emits one unreachable panic block after every blocking select to
// guard its synthetic case-index dispatch. Physical channel lowering proves
// that a completed runtime decision is either a real state index or a
// compiler-owned cancellation edge, so this block is an internal invariant
// trap rather than a user panic requiring managed interface allocation.
func coroSyntheticSelectNoCasePanic(instruction *ssa.Panic) bool {
	if instruction == nil || instruction.Pos() != token.NoPos {
		return false
	}
	boxed, ok := instruction.X.(*ssa.MakeInterface)
	if !ok {
		return false
	}
	value, ok := boxed.X.(*ssa.Const)
	if !ok || value.Value == nil || value.Value.Kind() != constant.String ||
		constant.StringVal(value.Value) != coroSyntheticSelectNoCaseMessage {
		return false
	}
	for _, block := range instruction.Parent().Blocks {
		for _, candidate := range block.Instrs {
			if selected, ok := candidate.(*ssa.Select); ok && selected.Blocking {
				return true
			}
		}
	}
	return false
}

func coroSyntheticSelectNoCaseBox(instruction *ssa.MakeInterface) bool {
	if instruction == nil {
		return false
	}
	refs := instruction.Referrers()
	if refs == nil || len(*refs) != 1 {
		return false
	}
	panicInstruction, ok := (*refs)[0].(*ssa.Panic)
	return ok && panicInstruction.X == instruction && coroSyntheticSelectNoCasePanic(panicInstruction)
}

const (
	// Version zero is intentionally experimental: the complete CoroHeader and
	// FrameDescriptor ABI is not frozen until scheduler/root lowering lands.
	coroPhysicalABIVersion uint32 = 0
	coroFrameAllocHook            = "__llgo_coro_frame_alloc_v0"
	coroFrameFreeHook             = "__llgo_coro_frame_free_v0"
	coroDescriptorPrefix          = "__llgo_coro_frame_descriptor_v0."

	coroPhysicalABIVersionV1      uint32 = 1
	coroFrameAllocHookV1                 = "__llgo_coro_frame_alloc_v1"
	coroFramePublishHookV1               = "__llgo_coro_frame_publish_v1"
	coroAwaitPrepareHookV1               = "__llgo_coro_await_prepare_v3"
	coroAwaitConsumeHookV1               = "__llgo_coro_await_consume_v1"
	coroPreemptPollHookV1                = "__llgo_coro_preempt_poll_v1"
	coroYieldPrepareHookV1               = "__llgo_coro_yield_prepare_v1"
	coroCriticalEnterHookV1              = "__llgo_coro_critical_enter_v1"
	coroCriticalExitHookV1               = "__llgo_coro_critical_exit_v1"
	coroParkPrepareHookV1                = "__llgo_coro_park_prepare_v1"
	coroRunDecisionTakeHookV1            = "__llgo_coro_run_decision_take_v1"
	coroRunDecisionTakeZeroHookV1        = "__llgo_coro_run_decision_take_zero_v1"
	coroPanicPrepareHookV1               = "__llgo_coro_panic_prepare_v1"
	coroRecoverTakeHookV1                = "__llgo_coro_recover_take_v1"
	coroSpawnBeginHookV1                 = "__llgo_coro_spawn_begin_v1"
	coroSpawnCommitHookV1                = "__llgo_coro_spawn_commit_v1"
	coroCompletePrepareHookV2            = "__llgo_coro_complete_prepare_v2"
	coroFrameFreeHookV1                  = "__llgo_coro_frame_free_v1"
	coroDescriptorPrefixV1               = "__llgo_coro_frame_descriptor_v1."
)

const (
	coroHeaderTask = iota
	coroHeaderParent
	coroHeaderDescriptor
	coroHeaderAllocationBase
	coroHeaderResultSlot
	coroHeaderSuspendReason
	coroHeaderLifecycle
	coroHeaderStateID
	coroHeaderFlags
)

const (
	coroSuspendNone uint64 = iota
	coroSuspendCall
	coroSuspendFrameComplete
	coroSuspendYield
	coroSuspendPark
	coroSuspendPanic
)

const (
	coroLifecycleAllocated uint64 = iota
	coroLifecycleInitialSuspended
	coroLifecycleActive
	coroLifecycleSuspended
	coroLifecycleFinalSuspended
	coroLifecycleDestroyPending
	coroLifecycleDestroyed
)

// coroPreemptInstructionBudget bounds straight-line source work between
// compiler-inserted scheduler handoffs. Loop SCC entries are separate
// safepoints, so even a tiny loop cannot run forever without a cut.
const coroPreemptInstructionBudget = 64

type coroPhysicalABI struct {
	version                 uint32
	hash                    [16]byte
	descriptorName          string
	frameAllocHook          string
	frameFreeHook           string
	framePublishHook        string
	awaitPrepareHook        string
	awaitConsumeHook        string
	preemptPollHook         string
	yieldPrepareHook        string
	criticalEnterHook       string
	criticalExitHook        string
	parkPrepareHook         string
	runDecisionTakeHook     string
	runDecisionTakeZeroHook string
	panicPrepareHook        string
	recoverTakeHook         string
	completePrepareHook     string
	physicalSig             *types.Signature
	resultSlotType          types.Type
	resultCount             int
}

// coroBodyContext exists only while emitting one physical coroutine body. It
// carries the current handle/header explicitly so call lowering never guesses a
// frame layout from a raw handle.
type coroBodyContext struct {
	coro                   *llssa.CoroBuilder
	abi                    coroPhysicalABI
	cleanup                *coroStaticCleanupState
	header                 llssa.Expr
	task                   llssa.Expr
	resultSlot             llssa.Expr
	completion             llssa.BasicBlock
	finalSuspend           llssa.BasicBlock
	preemptPoll            llssa.Expr
	yieldPrepare           llssa.Expr
	criticalEnter          llssa.Expr
	criticalExit           llssa.Expr
	parkPrepare            llssa.Expr
	runDecisionTakeZero    llssa.Expr
	runDecisionTrap        llssa.Expr
	unsupportedRunDecision llssa.BasicBlock
	cancelRunDecision      llssa.BasicBlock
	abortRunDecision       llssa.BasicBlock
	shutdownRunDecision    llssa.BasicBlock
	panicPrepare           llssa.Expr
	completePrepare        llssa.Expr
	terminalStatus         llssa.Expr
	nextState              uint32
	terminalState          uint32
	needsPreempt           bool
	instructions           int
	frameRetention         *coroFrameRetentionProof
	frameRetaining         bool
	critical               *coroCriticalProof
	terminalResultAllocs   map[*ssa.Alloc]llssa.Expr
	sourceBlockPollFresh   bool
}

func newCoroPhysicalABI(p *context, entry plannedFunctionSymbol, sourceSig *types.Signature) coroPhysicalABI {
	// Declared methods use x/tools' receiver-as-Params[0] SSA convention. Keep
	// one receiver-free callable signature everywhere below so the descriptor
	// hash, ramp parameters, result slot, and child-await call all see the same
	// physical source ABI.
	sourceSig = coroPhysicalNormalizeSourceSignature(sourceSig)
	version := coroPhysicalABIVersion
	frameAllocHook := coroFrameAllocHook
	frameFreeHook := coroFrameFreeHook
	descriptorPrefix := coroDescriptorPrefix
	framePublishHook := ""
	awaitPrepareHook := ""
	awaitConsumeHook := ""
	preemptPollHook := ""
	yieldPrepareHook := ""
	criticalEnterHook := ""
	criticalExitHook := ""
	parkPrepareHook := ""
	runDecisionTakeHook := ""
	runDecisionTakeZeroHook := ""
	panicPrepareHook := ""
	recoverTakeHook := ""
	faultPrepareHook := ""
	faultPayloadHook := ""
	completePrepareHook := ""
	if p.compilation != nil && p.compilation.EnableCoroChildAwait {
		version = coroPhysicalABIVersionV1
		frameAllocHook = coroFrameAllocHookV1
		frameFreeHook = coroFrameFreeHookV1
		descriptorPrefix = coroDescriptorPrefixV1
		framePublishHook = coroFramePublishHookV1
		awaitPrepareHook = coroAwaitPrepareHookV1
		awaitConsumeHook = coroAwaitConsumeHookV1
		preemptPollHook = coroPreemptPollHookV1
		yieldPrepareHook = coroYieldPrepareHookV1
		parkPrepareHook = coroParkPrepareHookV1
		runDecisionTakeHook = coroRunDecisionTakeHookV1
		runDecisionTakeZeroHook = coroRunDecisionTakeZeroHookV1
		completePrepareHook = coroCompletePrepareHookV2
		if p.compilation.EnableCoroProgramBootstrapRun {
			criticalEnterHook = coroCriticalEnterHookV1
			criticalExitHook = coroCriticalExitHookV1
		}
	}
	if p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
		panicPrepareHook = coroPanicPrepareHookV1
		recoverTakeHook = coroRecoverTakeHookV1
		faultPrepareHook = coroFaultPrepareHookV1
		faultPayloadHook = coroFaultPayloadHookV1
	}
	resultFields := make([]*types.Var, sourceSig.Results().Len())
	for i := range resultFields {
		resultFields[i] = types.NewField(token.NoPos, nil, fmt.Sprintf("r%d", i), sourceSig.Results().At(i).Type(), false)
	}
	resultSlotType := types.NewStruct(resultFields, nil)
	physicalParams := make([]*types.Var, 0, sourceSig.Params().Len()+2)
	physicalParams = append(physicalParams,
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
	)
	for i := 0; i < sourceSig.Params().Len(); i++ {
		physicalParams = append(physicalParams, sourceSig.Params().At(i))
	}
	physicalResults := types.NewTuple(types.NewParam(token.NoPos, nil, "__llgo_handle", types.Typ[types.UnsafePointer]))
	physicalSig := types.NewSignatureType(nil, nil, nil, types.NewTuple(physicalParams...), physicalResults, false)

	qualified := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return llssa.PathOf(pkg)
	}
	target := p.prog.TargetSpec()
	coroABI := coro.PhysicalABIV0
	schedulerABI := coro.SchedulerNoneABIV0
	if p.compilation != nil && p.compilation.EnableCoroChildAwait {
		coroABI = coro.PhysicalABIV1
		schedulerABI = coro.SchedulerChildAwaitABIV0
	}
	panicABI := coro.PanicLegacyABIV0
	funcRepABI := coro.FuncRepABIV0
	if p.compilation != nil {
		if p.compilation.CoroABI != "" {
			coroABI = p.compilation.CoroABI
		}
		if p.compilation.SchedulerABI != "" {
			schedulerABI = p.compilation.SchedulerABI
		}
		if p.compilation.PanicABI != "" {
			panicABI = p.compilation.PanicABI
		}
		if p.compilation.FuncRepABI != "" {
			funcRepABI = p.compilation.FuncRepABI
		}
	}
	key := fmt.Sprintf(
		"llgo-coro-physical-v%d\x00%s\x00coro=%s\x00scheduler=%s\x00panic=%s\x00panic-hook=%s\x00recover-take=%s\x00fault-hook=%s\x00fault-payload-hook=%s\x00func-rep=%s\x00await-prepare=%s\x00await-consume=%s\x00resume-decision=%s\x00resume-decision-zero=%s\x00critical-enter=%s\x00critical-exit=%s\x00triple=%s\x00cpu=%s\x00features=%s\x00target-abi=%s\x00data-layout=%s\x00ptr=%d\x00sig=%s\x00result=%s",
		version,
		entry.plan.ID,
		coroABI,
		schedulerABI,
		panicABI,
		panicPrepareHook,
		recoverTakeHook,
		faultPrepareHook,
		faultPayloadHook,
		funcRepABI,
		awaitPrepareHook,
		awaitConsumeHook,
		runDecisionTakeHook,
		runDecisionTakeZeroHook,
		criticalEnterHook,
		criticalExitHook,
		target.Triple,
		target.CPU,
		target.Features,
		target.TargetABI,
		p.prog.DataLayout(),
		p.prog.PointerSize(),
		types.TypeString(sourceSig, qualified),
		types.TypeString(resultSlotType, qualified),
	)
	sum := sha256.Sum256([]byte(key))
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return coroPhysicalABI{
		version:                 version,
		hash:                    hash,
		descriptorName:          descriptorPrefix + hex.EncodeToString(hash[:]),
		frameAllocHook:          frameAllocHook,
		frameFreeHook:           frameFreeHook,
		framePublishHook:        framePublishHook,
		awaitPrepareHook:        awaitPrepareHook,
		awaitConsumeHook:        awaitConsumeHook,
		preemptPollHook:         preemptPollHook,
		yieldPrepareHook:        yieldPrepareHook,
		criticalEnterHook:       criticalEnterHook,
		criticalExitHook:        criticalExitHook,
		parkPrepareHook:         parkPrepareHook,
		runDecisionTakeHook:     runDecisionTakeHook,
		runDecisionTakeZeroHook: runDecisionTakeZeroHook,
		panicPrepareHook:        panicPrepareHook,
		recoverTakeHook:         recoverTakeHook,
		completePrepareHook:     completePrepareHook,
		physicalSig:             physicalSig,
		resultSlotType:          resultSlotType,
		resultCount:             sourceSig.Results().Len(),
	}
}

func coroHeaderType(prog llssa.Program) llssa.Type {
	return prog.Struct(
		prog.VoidPtr(), // g
		prog.VoidPtr(), // parent
		prog.VoidPtr(), // descriptor
		prog.VoidPtr(), // allocation base (published by the future runtime)
		prog.VoidPtr(), // result slot
		prog.Uint16(),  // suspend reason
		prog.Uint16(),  // lifecycle state
		prog.Uint32(),  // state ID
		prog.Uint32(),  // flags
	)
}

func (p *context) beginCoroBody(
	b llssa.Builder,
	abi coroPhysicalABI,
	terminalResultAllocations []*ssa.Alloc,
) *coroBodyContext {
	prog := p.prog
	resultType := prog.Type(abi.resultSlotType, llssa.InGo)
	descriptor := p.pkg.NewCoroFrameDescriptor(abi.descriptorName, llssa.CoroFrameDescriptorOptions{
		Version: abi.version,
		ABIHash: abi.hash,
		Result:  resultType,
	})
	descriptorPtr := b.Convert(prog.VoidPtr(), descriptor)
	task := p.fn.PhysicalParam(0)
	resultSlot := p.fn.PhysicalParam(1)
	null := prog.Nil(prog.VoidPtr())
	headerType := coroHeaderType(prog)
	header := b.AllocaT(headerType)
	initialLifecycle := uint64(coroLifecycleAllocated)
	if abi.version >= coroPhysicalABIVersionV1 {
		initialLifecycle = coroLifecycleInitialSuspended
	}
	headerValues := []llssa.Expr{
		task,
		null,
		descriptorPtr,
		null,
		resultSlot,
		prog.IntVal(coroSuspendNone, prog.Uint16()),
		prog.IntVal(initialLifecycle, prog.Uint16()),
		prog.IntVal(0, prog.Uint32()),
		prog.IntVal(0, prog.Uint32()),
	}
	allocSig := coroFrameAllocSignature(abi.version)
	freeSig := coroFrameFreeSignature(abi.version)
	alloc := p.pkg.NewFunc(abi.frameAllocHook, allocSig, llssa.InC)
	free := p.pkg.NewFunc(abi.frameFreeHook, freeSig, llssa.InC)
	frame := llssa.CoroFrameOps{
		Alloc: func(b llssa.Builder, size, align llssa.Expr) llssa.Expr {
			if abi.version >= coroPhysicalABIVersionV1 {
				return b.Call(alloc.Expr, task, size, align, descriptorPtr)
			}
			return b.Call(alloc.Expr, size, align, descriptorPtr)
		},
		Free: func(b llssa.Builder, storage, size, align llssa.Expr) {
			if abi.version >= coroPhysicalABIVersionV1 {
				b.Call(free.Expr, task, storage, size, align, descriptorPtr)
				return
			}
			b.Call(free.Expr, storage, size, align, descriptorPtr)
		},
	}
	body := &coroBodyContext{
		abi:                  abi,
		header:               header,
		task:                 task,
		resultSlot:           resultSlot,
		nextState:            1,
		terminalResultAllocs: make(map[*ssa.Alloc]llssa.Expr, len(terminalResultAllocations)),
	}
	if abi.version >= coroPhysicalABIVersionV1 {
		// The cleanup base is frame-local rather than G-local: deferred code
		// invoked while a task is canceling must still be able to make ordinary
		// managed calls and receive their ordinary Return outcomes.
		body.terminalStatus = b.AllocaT(prog.Uint32())
		b.Store(body.terminalStatus, prog.IntVal(coroAwaitCompletionReturn, prog.Uint32()))
	}
	if abi.runDecisionTakeZeroHook != "" {
		body.runDecisionTakeZero = p.pkg.NewFunc(
			abi.runDecisionTakeZeroHook, coroRunDecisionTakeZeroSignature(), llssa.InC,
		).Expr
		if p.compilation != nil && (p.compilation.EnableCoroChannel || p.compilation.EnableCoroWorker ||
			p.compilation.CoroFrameRetentionABI == CoroFrameRetentionParkABIV2) {
			body.unsupportedRunDecision = p.fn.MakeBlock()
			body.runDecisionTrap = p.pkg.NewFunc(
				"llvm.trap", types.NewSignatureType(nil, nil, nil, nil, nil, false), llssa.InC,
			).Expr
		}
	}
	if abi.completePrepareHook != "" {
		body.completePrepare = p.pkg.NewFunc(abi.completePrepareHook, coroCompletePrepareSignature(), llssa.InC).Expr
	}
	if abi.yieldPrepareHook != "" {
		body.yieldPrepare = p.pkg.NewFunc(abi.yieldPrepareHook, coroYieldPrepareSignature(), llssa.InC).Expr
	}
	if abi.criticalEnterHook != "" {
		body.criticalEnter = p.pkg.NewFunc(abi.criticalEnterHook, coroCriticalEnterSignature(), llssa.InC).Expr
	}
	if abi.criticalExitHook != "" {
		body.criticalExit = p.pkg.NewFunc(abi.criticalExitHook, coroCriticalExitSignature(), llssa.InC).Expr
	}
	if abi.parkPrepareHook != "" {
		body.parkPrepare = p.pkg.NewFunc(abi.parkPrepareHook, coroParkPrepareSignature(), llssa.InC).Expr
	}
	if abi.panicPrepareHook != "" {
		body.panicPrepare = p.pkg.NewFunc(abi.panicPrepareHook, coroPanicPrepareSignature(), llssa.InC).Expr
	}
	if abi.preemptPollHook != "" {
		body.preemptPoll = p.pkg.NewFunc(abi.preemptPollHook, coroPreemptPollSignature(), llssa.InC).Expr
	}
	coroOptions := llssa.CoroOptions{
		Promise: header,
		Frame:   frame,
		BeforeInitialSuspend: func(b llssa.Builder, handle, storage llssa.Expr) {
			for i, value := range headerValues {
				b.Store(b.FieldAddr(header, i), value)
			}
			if abi.framePublishHook != "" {
				publish := p.pkg.NewFunc(abi.framePublishHook, coroFramePublishSignature(), llssa.InC)
				b.Call(publish.Expr, task, handle, b.Convert(prog.VoidPtr(), header), storage)
			}
			// A named result captured by a defer is an ordinary Go heap object,
			// but x/tools reloads it from compiler-owned RunDefers continuations.
			// Define only that structurally certified subset after frame/header
			// publication and before the initial suspend. Its pointer then dominates
			// every normal, cancellation, and cleanup continuation, and CoroSplit
			// retains it exactly when live without changing heap identity.
			for _, allocation := range terminalResultAllocations {
				if allocation == nil || !allocation.Heap || allocation.Parent() != p.goFn ||
					allocation.Block() == nil || allocation.Block().Index != 0 {
					panic("coroutine terminal-result allocation lost its exact source-entry heap proof")
				}
				if _, duplicate := body.terminalResultAllocs[allocation]; duplicate {
					panic("duplicate coroutine terminal-result allocation")
				}
				pointer, ok := types.Unalias(allocation.Type()).Underlying().(*types.Pointer)
				if !ok {
					panic("coroutine terminal-result allocation is not pointer typed")
				}
				value := func() llssa.Expr {
					finishSite := p.beginCoroRelocatedSiteEmission(allocation, coroRuntimeHelperAtPrologue)
					defer finishSite()
					return b.Alloc(p.type_(pointer.Elem(), llssa.InGo), true)
				}()
				body.terminalResultAllocs[allocation] = value
				p.bvals[allocation] = value
			}
		},
	}
	if !body.runDecisionTakeZero.IsNil() {
		coroOptions.AfterResumeDispatch = body.dispatchZeroRunDecision
	}
	body.coro = b.BeginCoro(coroOptions)
	if body.unsupportedRunDecision != nil {
		// Every zero-ticket gate in this physical body shares one fail-closed
		// destination. Restore the compiler-owned initial normal continuation
		// before source lowering starts.
		initialResume := body.coro.InitialResumeBlock()
		b.SetBlock(body.unsupportedRunDecision)
		b.Call(body.runDecisionTrap)
		b.Unreachable()
		b.SetBlock(initialResume)
	}
	return body
}

func coroFrameAllocSignature(version uint32) *types.Signature {
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "size", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "align", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "descriptor", types.Typ[types.UnsafePointer]),
	}
	if version >= coroPhysicalABIVersionV1 {
		params = append([]*types.Var{types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer])}, params...)
	}
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "frame", types.Typ[types.UnsafePointer]))
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), results, false)
}

func coroFrameFreeSignature(version uint32) *types.Signature {
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "frame", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "size", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "align", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "descriptor", types.Typ[types.UnsafePointer]),
	}
	if version >= coroPhysicalABIVersionV1 {
		params = append([]*types.Var{types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer])}, params...)
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), nil, false)
}

func coroFramePublishSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "storage", types.Typ[types.UnsafePointer]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroAwaitPrepareSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "parent", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "child", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "recoverMode", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "recoverType", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "recoverData", types.Typ[types.UnsafePointer]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroAwaitConsumeSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "parent", pointer),
		types.NewParam(token.NoPos, nil, "typeOut", pointer),
		types.NewParam(token.NoPos, nil, "dataOut", pointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroCompletePrepareSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroYieldPrepareSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroParkPrepareSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "token", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "ticket", types.Typ[types.Uint32]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroRunDecisionTakeZeroSignature() *types.Signature {
	params := types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]))
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "taskKind", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroPreemptPollSignature() *types.Signature {
	params := types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]))
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "requested", types.Typ[types.Bool]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroCriticalEnterSignature() *types.Signature {
	params := types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]))
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroCriticalExitSignature() *types.Signature {
	params := types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]))
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "requested", types.Typ[types.Bool]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroPanicPrepareSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "typeWord", pointer),
		types.NewParam(token.NoPos, nil, "dataWord", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func (c *coroBodyContext) publishState(b llssa.Builder, reason, lifecycle uint64, stateID uint32) {
	prog := b.Prog
	b.Store(b.FieldAddr(c.header, coroHeaderSuspendReason), prog.IntVal(reason, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderLifecycle), prog.IntVal(lifecycle, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderStateID), prog.IntVal(uint64(stateID), prog.Uint32()))
}

func (c *coroBodyContext) activate(b llssa.Builder) {
	if c.abi.version < coroPhysicalABIVersionV1 {
		return
	}
	prog := b.Prog
	b.Store(b.FieldAddr(c.header, coroHeaderSuspendReason), prog.IntVal(coroSuspendNone, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderLifecycle), prog.IntVal(coroLifecycleActive, prog.Uint16()))
}

// dispatchZeroRunDecision emits the exactly-once compiler resume gate for a
// non-park continuation. The runtime scalar ABI validates the complete
// zero-ticket decision and returns only None/Abort/Shutdown. No output address
// exists for CoroSplit to retain in the stackless coroutine frame.
func (c *coroBodyContext) dispatchZeroRunDecision(b llssa.Builder, normal llssa.BasicBlock) {
	if c.cancelRunDecision == nil {
		c.cancelRunDecision = b.Func.MakeBlock()
	}
	c.dispatchZeroRunDecisionTo(b, normal, c.cancelRunDecision)
}

func (c *coroBodyContext) dispatchZeroRunDecisionTo(
	b llssa.Builder, normal, canceled llssa.BasicBlock,
) {
	if c.abi.version < coroPhysicalABIVersionV1 || c.runDecisionTakeZero.IsNil() {
		panic("coroutine resume requires PhysicalABIV1 zero-ticket run-decision hook")
	}
	if canceled == nil {
		panic("coroutine resume decision has no cancellation destination")
	}
	if c.terminalStatus.IsNil() {
		panic("coroutine resume cancellation requires frame-local terminal status")
	}
	zero := b.Prog.IntVal(0, b.Prog.Uint32())
	taskKind := b.Call(c.runDecisionTakeZero, c.task)
	// The runtime ABI validates the complete decision and aborts before return
	// for every value other than None/Abort/Shutdown. Any nonzero value reaching
	// generated IR is therefore an exact task-cancellation cleanup request.
	isCanceled := b.BinOp(token.NEQ, taskKind, zero)
	// Runtime validation restricts nonzero taskKind to Abort=1/Shutdown=2;
	// CompletionAbort/Shutdown are exactly those values plus two. Preserve the
	// existing base on normal resumes so safepoints inside cleanup are masked.
	mapped := b.BinOp(token.ADD, taskKind, b.Prog.IntVal(2, b.Prog.Uint32()))
	current := b.Load(c.terminalStatus)
	b.Store(c.terminalStatus, b.SelectValue(isCanceled, mapped, current))
	b.If(isCanceled, canceled, normal)
}

func (c *coroBodyContext) bindCancellationCompletion(b llssa.Builder) {
	if c.cancelRunDecision == nil && c.runDecisionTakeZero.IsNil() {
		return
	}
	if c.cancelRunDecision == nil || c.completion == nil {
		panic("coroutine cancellation resume gate requires a completion block")
	}
	b.SetBlock(c.cancelRunDecision)
	if c.cleanup == nil {
		b.Jump(c.completion)
	} else {
		c.cleanup.enterCancellation(b)
	}
}

// cancellationRunDecisionTargets adapts operation-specific resume statuses to
// the same frame-local terminal base used by the scalar zero-ticket gate. The
// operation resume hook has already consumed/discarded its result ownership;
// these tiny blocks only retain Abort versus Shutdown before shared cleanup.
func (c *coroBodyContext) cancellationRunDecisionTargets(
	b llssa.Builder,
) (abort, shutdown llssa.BasicBlock) {
	if b.Func == nil || c.cancelRunDecision == nil || c.terminalStatus.IsNil() {
		panic("coroutine operation cancellation requires a bound cleanup destination")
	}
	makeTarget := func(status uint64) llssa.BasicBlock {
		target := b.Func.MakeBlock()
		builder := b.Func.NewBuilder()
		defer builder.Dispose()
		builder.SetBlock(target)
		builder.Store(c.terminalStatus, builder.Prog.IntVal(status, builder.Prog.Uint32()))
		builder.Jump(c.cancelRunDecision)
		return target
	}
	if c.abortRunDecision == nil {
		c.abortRunDecision = makeTarget(coroAwaitCompletionAbort)
	}
	if c.shutdownRunDecision == nil {
		c.shutdownRunDecision = makeTarget(coroAwaitCompletionShutdown)
	}
	return c.abortRunDecision, c.shutdownRunDecision
}

func (c *coroBodyContext) enterCancellation(b llssa.Builder, status uint64) {
	if c.terminalStatus.IsNil() || c.completion == nil ||
		(status != coroAwaitCompletionAbort && status != coroAwaitCompletionShutdown) {
		panic("coroutine cancellation has an invalid terminal status")
	}
	b.Store(c.terminalStatus, b.Prog.IntVal(status, b.Prog.Uint32()))
	if c.cleanup == nil {
		b.Jump(c.completion)
	} else {
		c.cleanup.enterCancellation(b)
	}
}

func (c *coroBodyContext) suspendForChild(b llssa.Builder) uint32 {
	if c.abi.version < coroPhysicalABIVersionV1 {
		panic("coroutine child suspension requires PhysicalABIV1")
	}
	stateID := c.nextState
	c.nextState++
	c.instructions = 0
	c.publishState(b, coroSuspendCall, coroLifecycleSuspended, stateID)
	return stateID
}

func (c *coroBodyContext) pollAndSuspendForPreempt(b llssa.Builder) uint32 {
	if c.abi.version < coroPhysicalABIVersionV1 || c.preemptPoll.IsNil() || c.yieldPrepare.IsNil() {
		panic("coroutine preemption requires PhysicalABIV1 poll and scheduler handoff hooks")
	}
	return c.suspendCurrentFrameIfYieldRequested(b, b.Call(c.preemptPoll, c.task))
}

// suspendCurrentFrameIfYieldRequested is the shared conditional runnable
// handoff used by an ordinary poll and by the outermost critical-region exit.
// The runtime has already consumed the exact request before requested=true is
// returned; only the true edge publishes and suspends this physical frame.
func (c *coroBodyContext) suspendCurrentFrameIfYieldRequested(b llssa.Builder, requested llssa.Expr) uint32 {
	if c.abi.version < coroPhysicalABIVersionV1 || c.yieldPrepare.IsNil() {
		panic("coroutine conditional preemption requires the PhysicalABIV1 scheduler handoff hook")
	}
	stateID := c.nextState
	c.nextState++
	c.instructions = 0
	c.coro.SuspendCurrentBlockIf(requested, func(suspend llssa.Builder) {
		c.publishState(suspend, coroSuspendYield, coroLifecycleSuspended, stateID)
		suspend.Call(c.yieldPrepare, c.task, c.coro.Handle(), suspend.Convert(suspend.Prog.VoidPtr(), c.header))
	})
	// The false edge is already active. The CoroBuilder AfterResume callback
	// consumes a decision only on the resumed true edge before this join.
	c.activate(b)
	return stateID
}

func (c *coroBodyContext) yieldCurrentFrame(b llssa.Builder) uint32 {
	if c.abi.version < coroPhysicalABIVersionV1 || c.yieldPrepare.IsNil() {
		panic("coroutine yield requires PhysicalABIV1 scheduler handoff hooks")
	}
	stateID := c.nextState
	c.nextState++
	c.instructions = 0
	c.publishState(b, coroSuspendYield, coroLifecycleSuspended, stateID)
	b.Call(c.yieldPrepare, c.task, c.coro.Handle(), b.Convert(b.Prog.VoidPtr(), c.header))
	c.coro.SuspendCurrentBlock()
	c.activate(b)
	return stateID
}

// parkCurrentFrame is the exact stack-cut primitive used by future channel,
// timer, syscall, and platform adapters. The suspend must remain here in the
// caller's physical coroutine body; a normal synchronous helper cannot retain
// the caller's native activation across llvm.coro.suspend.
func (c *coroBodyContext) parkCurrentFrame(b llssa.Builder, token, ticket llssa.Expr) uint32 {
	if c.abi.version < coroPhysicalABIVersionV1 || c.parkPrepare.IsNil() {
		panic("coroutine park requires PhysicalABIV1 scheduler handoff hook")
	}
	stateID := c.nextState
	c.nextState++
	c.instructions = 0
	c.publishState(b, coroSuspendPark, coroLifecycleSuspended, stateID)
	b.Call(
		c.parkPrepare,
		c.task,
		c.coro.Handle(),
		b.Convert(b.Prog.VoidPtr(), c.header),
		b.Convert(b.Prog.VoidPtr(), token),
		b.Convert(b.Prog.Uint32(), ticket),
	)
	c.coro.SuspendCurrentBlock()
	c.activate(b)
	return stateID
}

func (p *context) compileCoroPark(b llssa.Builder, args []llssa.Expr) {
	if p.currentCoro == nil || p.compilation == nil || !p.compilation.EnableCoroChildAwait {
		panic("llgo.coroPark requires an active PhysicalABIV1 coroutine body")
	}
	if b.Func != p.fn || len(args) != 2 {
		panic("llgo.coroPark requires exactly (token, ticket) in the active coroutine function")
	}
	p.currentCoro.parkCurrentFrame(b, args[0], args[1])
}

func (p *context) compileCoroYield(b llssa.Builder) {
	if p.currentCoro == nil || p.compilation == nil || !p.compilation.EnableCoroChildAwait {
		panic("llgo.coroYield requires an active PhysicalABIV1 coroutine body")
	}
	if b.Func != p.fn {
		panic("llgo.coroYield requires the active coroutine function")
	}
	p.currentCoro.yieldCurrentFrame(b)
}

func (c *coroBodyContext) countInstructionAndMaybeYield(b llssa.Builder) {
	if !c.needsPreempt {
		return
	}
	if c.instructions >= coroPreemptInstructionBudget {
		c.pollAndSuspendForPreempt(b)
	}
	c.instructions++
}

func (c *coroBodyContext) terminalStateID() uint32 {
	if c.terminalState == 0 {
		c.terminalState = c.nextState
		c.nextState++
	}
	return c.terminalState
}

func (c *coroBodyContext) complete(b llssa.Builder) {
	if c.abi.version < coroPhysicalABIVersionV1 {
		b.Jump(c.finalSuspend)
		return
	}
	c.publishState(b, coroSuspendFrameComplete, coroLifecycleFinalSuspended, c.terminalStateID())
	if !c.completePrepare.IsNil() {
		if c.terminalStatus.IsNil() {
			panic("coroutine completion has no frame-local terminal status")
		}
		b.Call(
			c.completePrepare,
			c.task,
			c.coro.Handle(),
			b.Convert(b.Prog.VoidPtr(), c.header),
			b.Load(c.terminalStatus),
		)
	}
	b.Jump(c.finalSuspend)
}

func (c *coroBodyContext) panic(b llssa.Builder, typeWord, dataWord llssa.Expr) {
	if c.abi.version < coroPhysicalABIVersionV1 || c.panicPrepare.IsNil() || c.finalSuspend == nil {
		panic("explicit-status panic requires a PhysicalABIV1 prepare hook and shared final suspend")
	}
	c.publishState(b, coroSuspendPanic, coroLifecycleFinalSuspended, c.terminalStateID())
	b.Call(
		c.panicPrepare,
		c.task,
		c.coro.Handle(),
		b.Convert(b.Prog.VoidPtr(), c.header),
		b.Convert(b.Prog.VoidPtr(), typeWord),
		b.Convert(b.Prog.VoidPtr(), dataWord),
	)
	b.Jump(c.finalSuspend)
}

func (c *coroBodyContext) finish(b llssa.Builder) {
	c.coro.Finish()
}

func (p *context) storeCoroLeafResult(b llssa.Builder, abi coroPhysicalABI, resultSlot llssa.Expr, results []llssa.Expr) {
	if len(results) != abi.resultCount {
		panic(fmt.Sprintf("coroutine result count %d does not match ABI count %d", len(results), abi.resultCount))
	}
	if len(results) == 0 {
		return
	}
	resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
	typedSlot := b.Convert(p.prog.Pointer(resultType), resultSlot)
	for i, result := range results {
		b.Store(b.FieldAddr(typedSlot, i), result)
	}
}

func (p *context) compileCoroPhysicalBody(b llssa.Builder, fn *ssa.Function, abi coroPhysicalABI, isInit bool) {
	oldBase := p.sourceParamBase
	oldCoro := p.currentCoro
	oldSourceBlocks := p.coroSourceBlocks
	oldPhysicalPlan := p.coroPhysicalPlan
	oldPhysicalEmission := p.coroPhysicalEmission
	oldExplicitStatus := p.coroExplicitStatus
	p.sourceParamBase = 2
	if len(fn.FreeVars) != 0 {
		// Captured descriptor entries are (g,out,ctx,args...). The context is an
		// explicit physical parameter rather than aFunction's legacy implicit
		// closure parameter, so SSA source parameters begin after all three words.
		p.sourceParamBase = 3
	}
	defer func() {
		p.sourceParamBase = oldBase
		p.currentCoro = oldCoro
		p.coroSourceBlocks = oldSourceBlocks
		p.coroPhysicalPlan = oldPhysicalPlan
		p.coroPhysicalEmission = oldPhysicalEmission
		p.coroExplicitStatus = oldExplicitStatus
	}()

	if p.emissionUniverse == nil || p.emissionUniverse.coroProgramIR == nil {
		panic("coroutine physical body has no ProgramIR")
	}
	physicalPlan, err := p.emissionUniverse.coroProgramIR.physicalFunctionPlan(fn, p.emissionOwner)
	if err != nil {
		panic(fmt.Errorf("load frozen coroutine physical plan: %w", err))
	}
	frameRetention := physicalPlan.frameRetention
	critical := physicalPlan.critical
	cleanupPlan := physicalPlan.cleanup
	p.coroPhysicalPlan = physicalPlan

	b.SetBlock(p.fn.Block(0))
	cleanup := p.beginCoroStaticCleanup(b, cleanupPlan)
	terminalResultAllocations := []*ssa.Alloc(nil)
	if cleanupPlan != nil {
		terminalResultAllocations = cleanupPlan.terminalResultAllocations
	}
	if !coroTerminalResultAllocationSetMatches(frameRetention, terminalResultAllocations) {
		panic("coroutine cleanup plan and frame-retention proof disagree on terminal-result allocations")
	}
	p.coroPhysicalEmission = true
	p.coroExplicitStatus = abi.panicPrepareHook != ""
	physical := p.beginCoroBody(b, abi, terminalResultAllocations)
	physical.frameRetention = frameRetention
	physical.critical = critical
	physical.cleanup = cleanup
	if physical.cleanup != nil {
		physical.cleanup.bindBlocks(p.fn)
	}
	p.currentCoro = physical

	// Create source blocks after BeginCoro's canonical ramp/suspend blocks so
	// presplit IR remains in execution order for LLVM diagnostics and ABI tests.
	sourceBlocks := make([]llssa.BasicBlock, len(fn.Blocks))
	for i := range sourceBlocks {
		sourceBlocks[i] = p.fn.MakeBlock()
	}
	p.coroSourceBlocks = sourceBlocks
	physical.completion = p.fn.MakeBlock()
	physical.finalSuspend = p.fn.MakeBlock()
	physical.bindCancellationCompletion(b)
	b.SetBlock(physical.coro.InitialResumeBlock())
	physical.activate(b)
	b.Jump(sourceBlocks[0])

	off := make([]int, len(fn.Blocks))
	for i, block := range fn.Blocks {
		off[i] = p.compilePhis(b, block)
	}
	p.blkInfos = blocks.Infos(fn.Blocks)
	plan, ok := p.compilation.CoroPlan.FunctionPlan(fn)
	if !ok {
		panic("coroutine physical body has no compilation plan")
	}
	physical.needsPreempt = plan.Exec.Contains(coro.NeedsPreempt)

	i := 0
	for {
		block := fn.Blocks[i]
		physical.sourceBlockPollFresh = false
		entryDepth := uint32(0)
		if physical.critical != nil {
			var proven bool
			entryDepth, proven = physical.critical.entryDepth[block]
			if !proven {
				panic("coroutine critical proof has no source-block entry depth")
			}
		}
		if physical.needsPreempt && entryDepth == 0 {
			physical.instructions = 0
			// Every source block, including block zero, begins with a poll. A
			// child initial suspend is a scheduler boundary but not necessarily
			// a fairness boundary: pendingAwait can immediately resume a long
			// static child chain on the same G without returning to ready-queue
			// selection. Polling block zero therefore bounds that chain as well
			// as ordinary CFG paths and block-zero backedges.
			b.SetBlock(p.sourceBlock(i))
			physical.pollAndSuspendForPreempt(b)
			physical.sourceBlockPollFresh = true
		}
		doModInit := i == 1 && isInit
		p.compileBlock(b, block, off[i], doModInit)
		if i = p.blkInfos[i].Next; i < 0 {
			break
		}
	}
	for _, phi := range p.phis {
		phi()
	}
	if physical.frameRetaining {
		panic("coroutine frame-retention critical span escaped its certified source block")
	}

	b.SetBlock(physical.completion)
	if physical.cleanup == nil {
		physical.complete(b)
	} else {
		physical.cleanup.enterCompletion(b)
		physical.cleanup.emit(p, b)
		b.SetBlock(physical.cleanup.complete)
		physical.complete(b)
		b.SetBlock(physical.cleanup.panic)
		physical.panic(
			b,
			b.Load(physical.cleanup.panicType),
			b.Load(physical.cleanup.panicData),
		)
	}
	b.SetBlock(physical.finalSuspend)
	physical.finish(b)
}

func validateCoroPhysicalABI(fn *ssa.Function, plan coro.FunctionPlan, whole *coro.SSAPlan, childAwait, programRun bool) error {
	return validateCoroPhysicalABIWithUniverseCapabilities(fn, plan, whole, nil, childAwait, programRun, false, false)
}

// validateCoroPhysicalABIWithUniverse is the production preflight. The
// prepared emission universe supplies the exact frontend lowering context used
// to prove that an accepted pure SSA instruction emits no hidden runtime call.
// The wrapper above is retained for narrow structural unit tests; active
// Compilation paths always call this form with their frozen universe.
func validateCoroPhysicalABIWithUniverse(fn *ssa.Function, plan coro.FunctionPlan, whole *coro.SSAPlan, universe *EmissionUniverse, childAwait, programRun bool) error {
	return validateCoroPhysicalABIWithUniverseCapabilities(fn, plan, whole, universe, childAwait, programRun, false, false)
}

func validateCoroPhysicalABIWithUniverseCapabilities(fn *ssa.Function, plan coro.FunctionPlan, whole *coro.SSAPlan, universe *EmissionUniverse, childAwait, programRun, staticSpawn, explicitPanic bool) error {
	return validateCoroPhysicalABIWithUniverseCapabilitiesAndFrameRetention(
		fn, plan, whole, universe, childAwait, programRun, staticSpawn, explicitPanic, "",
	)
}

func validateCoroPhysicalABIWithUniverseCapabilitiesAndFrameRetention(fn *ssa.Function, plan coro.FunctionPlan, whole *coro.SSAPlan, universe *EmissionUniverse, childAwait, programRun, staticSpawn, explicitPanic bool, frameRetentionABI string) error {
	return validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		fn, plan, whole, universe, childAwait, programRun, staticSpawn, explicitPanic, frameRetentionABI, false, false, false,
	)
}

func validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(fn *ssa.Function, plan coro.FunctionPlan, whole *coro.SSAPlan, universe *EmissionUniverse, childAwait, programRun, staticSpawn, explicitPanic bool, frameRetentionABI string, channel, managedDispatch, rawMethodToken bool) error {
	return validateCoroPhysicalABIForOwner(
		fn, plan, whole, universe, nil, childAwait, programRun, staticSpawn, explicitPanic,
		frameRetentionABI, channel, managedDispatch, rawMethodToken, nil,
	)
}

func validateCoroPhysicalABIForOwner(
	fn *ssa.Function,
	plan coro.FunctionPlan,
	whole *coro.SSAPlan,
	universe *EmissionUniverse,
	owner *preparedEmissionPackage,
	childAwait, programRun, staticSpawn, explicitPanic bool,
	frameRetentionABI string,
	channel, managedDispatch, rawMethodToken bool,
	accept func(*coroPhysicalFunctionPlan) error,
) error {
	if !childAwait {
		if explicitPanic {
			return fmt.Errorf("coroutine physical ABI: function %q: explicit-status panic requires PhysicalABIV1 child-await lowering", plan.ID)
		}
		if err := validateCoroLeafPhysicalABI(fn, plan); err != nil {
			return err
		}
		if accept == nil {
			return nil
		}
		audit, err := newCoroPhysicalPureSSAAuditForOwner(universe, whole, fn, owner, frameRetentionABI)
		if err != nil {
			return fmt.Errorf("coroutine physical ABI: function %q: cannot freeze leaf physical proof: %w", plan.ID, err)
		}
		critical, err := proveCoroCriticalRegions(universe, whole, audit)
		if err != nil {
			return fmt.Errorf("coroutine physical ABI: function %q: leaf critical region: %w", plan.ID, err)
		}
		physical, err := prepareCoroPhysicalFunctionPlan(audit, owner, whole, nil, critical, false)
		if err != nil {
			return fmt.Errorf("coroutine physical ABI: function %q: leaf physical plan: %w", plan.ID, err)
		}
		return accept(physical)
	}

	fail := func(format string, args ...any) error {
		name := "<nil>"
		if fn != nil {
			name = fn.String()
		}
		return fmt.Errorf("coroutine physical ABI: function %q (%s): %s", plan.ID, name, fmt.Sprintf(format, args...))
	}
	if fn == nil || plan.External != coro.Defined || len(fn.Blocks) == 0 {
		return fail("requires one defined SSA body")
	}
	if emitShadowStackInstrumentation {
		return fail("legacy thread-local shadow-stack instrumentation is incompatible with stackless coroutine suspension")
	}
	managedDispatchTarget := managedDispatch && plan.FuncRep == coro.Dispatch &&
		fn.Signature != nil && fn.Signature.Recv() == nil
	rawMethodDispatchToken := rawMethodToken && plan.FuncRep == coro.Dispatch &&
		fn.Signature != nil && fn.Signature.Recv() != nil
	if plan.Emission != coro.EmitCoroutine ||
		plan.FuncRep != coro.DirectCoro && !managedDispatchTarget && !rawMethodDispatchToken {
		return fail("requires a direct coroutine or capability-certified Dispatch emission, got emission=%s representation=%s", plan.Emission, plan.FuncRep)
	}
	if !plan.ManagedDemand.Contains(coro.AsyncDemand) {
		return fail(
			"requires managed async demand, got aggregate=%s managed=%s raw=%t raw-entry=%t",
			plan.Demand, plan.ManagedDemand, plan.RawPlainDemand, plan.RawPlainEntry,
		)
	}
	rawVariant := whole != nil && whole.HasRawPlainVariant(fn)
	// A recursive edge uses the same structured child-frame transaction as any
	// other exact coroutine call. Recursive SCCs also carry NeedsPreempt, whose
	// runnable-scheduler gate below guarantees bounded execution between polls.
	// PhysicalABIV0 remains leaf-only and retains its separate rejection.
	cleanupPlan, cleanupErr := prepareCoroStaticCleanupPlan(
		fn, whole, universe, frameRetentionABI, explicitPanic,
	)
	if cleanupErr != nil {
		return fail("static cleanup: %v", cleanupErr)
	}
	if err := validateCoroDynamicCleanupHelpers(cleanupPlan, whole); err != nil {
		return fail("dynamic cleanup: %v", err)
	}
	if plan.Exec.Contains(coro.NeedsPreempt) && !programRun {
		return fail("needs-preempt execution requires the runnable scheduler ABI")
	}
	// IRQUnsafe constrains interrupt roots; an ordinary scheduler-managed G is
	// not an IRQ context. Preserve the bit in the plan/digest while allowing the
	// CFG lowering to execute it. Thread affinity and opaque execution still
	// require scheduler protocols that this ABI does not provide.
	allowedExec := coro.MayUnwind | coro.NeedsPreempt | coro.IRQUnsafe
	if cleanupPlan != nil {
		allowedExec |= coro.NeedsCleanupFrame
	}
	if unsupported := plan.Exec &^ allowedExec; unsupported != 0 {
		return fail("execution flags %s require lowering outside the CFG physical ABI", unsupported)
	}
	if len(fn.FreeVars) != 0 && !managedDispatchTarget && plan.FuncRep != coro.DirectCoro {
		return fail("captured coroutine bodies require one exact direct or capability-certified descriptor context ABI")
	}
	if fn.Recover != nil && cleanupPlan == nil {
		return fail("recover blocks require coroutine cleanup/unwind lowering")
	}
	if cleanupPlan != nil {
		if err := validateCoroStaticCleanupRecoverBlock(fn); err != nil {
			return fail("static cleanup recover block: %v", err)
		}
	}
	directive, directiveErr := coroRawABIDirective(fn, universe)
	if directiveErr != nil {
		return fail("classify ABI directive: %v", directiveErr)
	}
	if directive != "" && !(plan.RawPlainEntry && rawVariant) {
		return fail("ABI directive %q requires a root or foreign adapter", directive)
	}
	if isCgoExternSymbol(fn) {
		return fail("cgo entry requires a foreign adapter")
	}
	programEntry := programRun && isCoroProgramManagedEntry(fn)
	genericInstance := coroMaterializedGenericCallable(fn)
	boundMethodWrapper := false
	if managedDispatchTarget && strings.HasPrefix(fn.Synthetic, "bound method wrapper for ") {
		if err := validateCoroExactBoundMethodWrapper(fn); err != nil {
			return fail("invalid bound method wrapper: %v", err)
		}
		boundMethodWrapper = true
	}
	methodExpressionThunk := false
	if strings.HasPrefix(fn.Synthetic, "thunk for ") {
		if err := validateCoroExactMethodExpressionThunk(fn); err != nil {
			return fail("invalid method-expression thunk: %v", err)
		}
		methodExpressionThunk = true
	}
	methodTokenWrapper := rawMethodToken && fn.Signature != nil && fn.Signature.Recv() != nil &&
		strings.Contains(fn.Synthetic, "wrapper for")
	capturedRawVariant := rawVariant && len(fn.FreeVars) != 0
	if fn.Synthetic != "" && !genericInstance && !boundMethodWrapper && !methodExpressionThunk && !methodTokenWrapper &&
		!capturedRawVariant &&
		!(programEntry && fn.Name() == "init" && fn.Synthetic == "package initializer") {
		return fail("synthetic function %q is outside the leaf ABI", fn.Synthetic)
	}
	if list := fn.TypeParams(); list != nil && list.Len() != 0 && !genericInstance {
		return fail("generic declarations are not materialized coroutine bodies")
	}
	if list := fn.Signature.RecvTypeParams(); list != nil && list.Len() != 0 && !genericInstance {
		return fail("generic receivers are not materialized coroutine bodies")
	}
	if list := fn.TypeArgs(); len(list) != 0 && !genericInstance {
		return fail("generic instances require a frozen instantiated ABI")
	}
	if isCoroProgramManagedEntry(fn) && !programEntry {
		return fail("program roots require scheduler bootstrap lowering")
	}
	physicalSourceSig := coroPhysicalNormalizeSourceSignature(fn.Signature)
	if universe != nil {
		var signatureErr error
		physicalSourceSig, signatureErr = universe.coroPhysicalEntrySourceSignature(fn)
		if signatureErr != nil {
			return fail("derive effective source signature: %v", signatureErr)
		}
	}
	if err := validateCoroPhysicalSSAParameterShape(plan, fn, physicalSourceSig, universe); err != nil {
		return err
	}
	if err := validateCoroLeafPhysicalSignature(plan, physicalSourceSig); err != nil {
		return err
	}
	pureSSA, err := newCoroPhysicalPureSSAAuditForOwner(universe, whole, fn, owner, frameRetentionABI)
	if err != nil {
		return fail("cannot audit pure SSA lowering: %v", err)
	}
	// Nullable FieldAddr values are accepted only under the target-wide
	// explicit-status identity. Codegen then replaces the host signal/legacy
	// AssertNilDeref behavior with a compiler-owned terminal coroutine edge.
	pureSSA.allowImplicitNilFault = explicitPanic
	pureSSA.allowExplicitRecover = explicitPanic
	terminalResultAllocations := []*ssa.Alloc(nil)
	if cleanupPlan != nil {
		terminalResultAllocations = cleanupPlan.terminalResultAllocations
	}
	if !coroTerminalResultAllocationSetMatches(pureSSA.currentFrameRetentionProof(), terminalResultAllocations) {
		return fail("static cleanup and frame-retention proofs disagree on terminal-result allocations")
	}
	critical, criticalErr := proveCoroCriticalRegions(universe, whole, pureSSA)
	if criticalErr != nil {
		return fail("critical region: %v", criticalErr)
	}
	if critical != nil && !programRun {
		return fail("critical regions require the runnable scheduler ABI")
	}
	physical, physicalErr := prepareCoroPhysicalFunctionPlan(
		pureSSA, owner, whole, cleanupPlan, critical, explicitPanic,
	)
	if physicalErr != nil {
		return fail("cannot freeze physical instruction plan: %v", physicalErr)
	}

	panics := 0
	awaits := 0
	parks := 0
	foreignWaits := 0
	yields := 0
	spawns := 0
	if cleanupPlan != nil {
		for _, site := range cleanupPlan.sites {
			switch site.kind {
			case coroStaticCleanupCoroutine:
				awaits++
			case coroStaticCleanupDispatch:
				if !managedDispatch {
					return fail("managed descriptor defer requires the v1 descriptor dispatch capability")
				}
				if site.callPlan.Open || coroDispatchCallHasCoroutineTarget(whole, site.callPlan) {
					awaits++
				}
			}
		}
	}
	infos := blocks.Infos(fn.Blocks)
	hasCyclicBlock := false
	for _, info := range infos {
		hasCyclicBlock = hasCyclicBlock || info.InLoop
	}
	// A RawPlainVariant with a cyclic body can lack NeedsPreempt only when the
	// frontend's exact compiler-runtime island policy suppressed the scanner
	// seed. Its raw execution is an intentionally atomic/bounded scheduler
	// transaction; ordinary source callbacks are not given that policy and keep
	// NeedsPreempt. The managed primary otherwise requires normal poll lowering.
	if hasCyclicBlock && !plan.Exec.Contains(coro.NeedsPreempt) && !rawVariant {
		return fail("cyclic CFG requires needs-preempt execution classification")
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if boxed, ok := instr.(*ssa.MakeInterface); ok && coroSyntheticSelectNoCaseBox(boxed) {
				continue
			}
			if panicInstruction, ok := instr.(*ssa.Panic); ok && coroSyntheticSelectNoCasePanic(panicInstruction) {
				continue
			}
			if handled, reason := pureSSA.validate(instr); handled {
				if reason != "" {
					return coroLeafInstructionError(fn, plan, instr, reason)
				}
				if instructionPlan, ok := physical.instructions[instr]; !ok {
					return coroLeafInstructionError(fn, plan, instr, "instruction is absent from the frozen physical plan")
				} else if instructionPlan.mayFault() {
					panics++
				}
				if call, ok := instr.(*ssa.Call); ok && explicitPanic && isCoroCloseBuiltinCall(call) {
					panics++
				}
				continue
			}
			switch instr := instr.(type) {
			case *ssa.DebugRef, *ssa.Jump:
			case *ssa.Return:
			case *ssa.Defer, *ssa.RunDefers:
				if cleanupPlan == nil {
					return coroLeafInstructionError(fn, plan, instr, "defer instruction has no certified static cleanup plan")
				}
			case *ssa.Panic:
				if !explicitPanic {
					return coroLeafInstructionError(fn, plan, instr, "explicit panic requires the explicit-status panic ABI")
				}
				if reason := validateCoroExplicitStatusPanic(pureSSA, instr); reason != "" {
					return coroLeafInstructionError(fn, plan, instr, reason)
				}
				panics++
			case *ssa.If:
				if !coroLeafScalar(instr.Cond.Type()) {
					return coroLeafInstructionError(fn, plan, instr, "non-scalar branch condition")
				}
			case *ssa.BinOp:
				if instr.Op == token.QUO || instr.Op == token.REM || instr.Op == token.SHL || instr.Op == token.SHR ||
					!coroLeafScalar(instr.Type()) ||
					!coroLeafScalar(instr.X.Type()) || !coroLeafScalar(instr.Y.Type()) {
					return coroLeafInstructionError(fn, plan, instr, "potentially panicking or non-scalar binary operation")
				}
			case *ssa.Send:
				if !channel {
					return coroLeafInstructionError(fn, plan, instr, "blocking channel send requires the channel scheduler capability")
				}
				if err := validateCoroPhysicalChannelType(instr.Chan.Type()); err != nil {
					return coroLeafInstructionError(fn, plan, instr, "channel send type: "+err.Error())
				}
				parks++
			case *ssa.Select:
				if !channel {
					return coroLeafInstructionError(fn, plan, instr, "channel select requires the channel scheduler capability")
				}
				for index, state := range instr.States {
					if state == nil {
						return coroLeafInstructionError(fn, plan, instr, fmt.Sprintf("channel select case %d is nil", index))
					}
					if state.Chan == nil {
						return coroLeafInstructionError(fn, plan, instr, fmt.Sprintf("channel select case %d channel is nil", index))
					}
					if err := validateCoroPhysicalChannelType(state.Chan.Type()); err != nil {
						return coroLeafInstructionError(fn, plan, instr, fmt.Sprintf("channel select case %d type: %v", index, err))
					}
				}
				if instr.Blocking {
					parks++
				}
			case *ssa.UnOp:
				if instr.Op == token.ARROW {
					if !channel {
						return coroLeafInstructionError(fn, plan, instr, "blocking channel receive requires the channel scheduler capability")
					}
					if err := validateCoroPhysicalChannelType(instr.X.Type()); err != nil {
						return coroLeafInstructionError(fn, plan, instr, "channel receive type: "+err.Error())
					}
					parks++
					continue
				}
				if (instr.Op != token.SUB && instr.Op != token.XOR && instr.Op != token.NOT) || !coroLeafScalar(instr.Type()) {
					return coroLeafInstructionError(fn, plan, instr, "unsupported unary operation")
				}
			case *ssa.Call:
				if whole != nil && whole.ElidesCall(instr) {
					if universe != nil {
						frozen, found, err := universe.coroProgramIR.callSitePlan(instr)
						if err != nil || !found {
							if err == nil {
								err = fmt.Errorf("call is absent from the frozen ProgramIR")
							}
							return coroLeafInstructionError(fn, plan, instr, err.Error())
						}
						if frozen.failure != "" {
							return coroLeafInstructionError(fn, plan, instr, "invalid frozen intrinsic: "+frozen.failure)
						}
						callPlan := frozen.plan
						semantics, intrinsic := callPlan.IntrinsicSemantics, callPlan.Intrinsic
						if cleanupPlan != nil && callPlan.Elision != CoroCallElidedNoInit && (!intrinsic ||
							(semantics != CoroIntrinsicCallInlineNoSuspend && semantics != CoroIntrinsicCallInlineSuspend &&
								semantics != CoroIntrinsicCallInlineYield)) {
							return coroLeafInstructionError(fn, plan, instr, "elided intrinsic has no cleanup-safe no-unwind contract")
						}
						if intrinsic && semantics == CoroIntrinsicCallInlineSuspend {
							if isLLGoSyscallIntrinsic(frozen.opcode) {
								if err := validateCoroWorkerSyscallCall(whole, universe, instr); err != nil {
									return coroLeafInstructionError(fn, plan, instr, "invalid worker llgo.syscall capability: "+err.Error())
								}
							}
							parks++
						} else if intrinsic && semantics == CoroIntrinsicCallInlineYield {
							yields++
						}
						if intrinsic {
							if isLLGoSyscallIntrinsic(frozen.opcode) && semantics != CoroIntrinsicCallInlineSuspend {
								return coroLeafInstructionError(fn, plan, instr,
									"elided worker llgo.syscall has no frozen function-word capability")
							}
							if frozen.opcode == llgoAlloca {
								return coroLeafInstructionError(fn, plan, instr,
									"dynamic llgo.alloca is valid only in a no-suspend plain island; a physical coroutine requires an exact resume-local lifetime proof")
							}
						}
					}
					// The frozen frontend proved that this declaration call emits no
					// callable edge. A structured park is counted above; ordinary
					// noinit/inline intrinsics need no await/plain entry.
					continue
				}
				if instr.Common().IsInvoke() {
					if !explicitPanic {
						if _, invokeErr := resolveCoroClosedInterfacePlainCall(whole, instr); invokeErr == nil {
							continue
						}
					}
					if callPlan, found := whole.CallPlan(instr); found && callPlan.Open &&
						callPlan.Unresolved == coro.UnknownManagedInterfaceDispatch {
						if !managedDispatch {
							return coroLeafInstructionError(fn, plan, instr,
								"managed interface invoke requires the v1 descriptor dispatch capability")
						}
						if err := validateCoroManagedInterfaceDispatchCall(whole, universe, fn, instr, callPlan); err != nil {
							return coroLeafInstructionError(fn, plan, instr, "invalid managed interface await: "+err.Error())
						}
						awaits++
						continue
					}
					dispatch, invokeErr := resolveCoroInterfaceDispatchPlan(whole, universe, instr)
					if invokeErr != nil || !coroInterfaceDispatchNeedsAwait(dispatch) {
						if invokeErr == nil {
							invokeErr = fmt.Errorf("closed interface dispatch has no coroutine target")
						}
						return coroLeafInstructionError(fn, plan, instr, "unsupported interface invoke: "+invokeErr.Error())
					}
					awaits++
					continue
				}
				if callPlan, found := whole.CallPlan(instr); found && callPlan.Rep == coro.Dispatch &&
					instr.Common().StaticCallee() == nil {
					if callPlan.SyncDispatch {
						if !managedDispatch {
							return coroLeafInstructionError(fn, plan, instr, "synchronous descriptor call requires the v1 plain dispatch capability")
						}
						if err := validateCoroPlainDispatchCall(whole, fn, instr, callPlan, universe); err != nil {
							return coroLeafInstructionError(fn, plan, instr, "invalid synchronous descriptor call: "+err.Error())
						}
						continue
					}
					if callPlan.Open && callPlan.Unresolved != coro.UnknownManagedDispatch {
						return coroLeafInstructionError(fn, plan, instr, fmt.Sprintf(
							"open descriptor call has uncertified execution domain %v", callPlan.Unresolved,
						))
					}
					if !managedDispatch {
						return coroLeafInstructionError(fn, plan, instr, "open managed descriptor call requires the v1 descriptor dispatch capability")
					}
					mayAwait := callPlan.Open || coroDispatchCallHasCoroutineTarget(whole, callPlan)
					if err := validateCoroManagedDispatchCall(whole, fn, instr, callPlan, universe); err != nil {
						return coroLeafInstructionError(fn, plan, instr, "invalid managed descriptor await: "+err.Error())
					}
					if mayAwait {
						awaits++
					}
					continue
				}
				if _, recognized, foreignErr := validateCoroWorkerForeignCall(
					whole, universe, instr, coroWorkerTargetPointerSize(universe),
				); recognized {
					if universe == nil || !universe.CoroWorkerEnabled() {
						return coroLeafInstructionError(fn, plan, instr, "blocking foreign call requires the bounded worker capability")
					}
					if foreignErr != nil {
						return coroLeafInstructionError(fn, plan, instr, "invalid bounded worker foreign call: "+foreignErr.Error())
					}
					foreignWaits++
					continue
				}
				callee, calleePlan, err := resolveCoroStaticAwait(whole, plan, instr, universe)
				if err == nil {
					calleeSignature := coroPhysicalNormalizeSourceSignature(callee.Signature)
					if universe != nil {
						calleeSignature, err = universe.coroPhysicalSourceSignature(callee)
						if err != nil {
							return coroLeafInstructionError(fn, plan, instr, "child await signature: "+err.Error())
						}
					}
					if err := validateCoroLeafPhysicalSignature(calleePlan, calleeSignature); err != nil {
						return coroLeafInstructionError(fn, plan, instr, "child await signature: "+err.Error())
					}
					awaits++
					continue
				}
				if explicitPanic {
					if _, targetPlan, plainErr := resolveCoroStaticPlainCall(whole, instr); plainErr == nil {
						// A direct plain call needs no hidden outcome slot when the
						// whole-program SSA plan has proved that its exact target cannot
						// initiate a Go unwind. resolveCoroStaticPlainCall already proves
						// that the call is closed, non-suspending, and has one exact plain
						// entry. Keep MayUnwind fail-closed: a merely synchronous function
						// may still panic and therefore must use the managed explicit-status
						// ABI rather than silently unwinding through this coroutine frame.
						if !targetPlan.Exec.Contains(coro.MayUnwind) {
							continue
						}
						return coroLeafInstructionError(fn, plan, instr, fmt.Sprintf(
							"direct plain target %q (exec=%s) has no certified explicit-status hidden-outcome/unwind contract",
							targetPlan.ID, targetPlan.Exec,
						))
					}
				}
				if !programRun {
					return coroLeafInstructionError(fn, plan, instr, "unsupported child await: "+err.Error())
				}
				if _, _, plainErr := resolveCoroStaticPlainCall(whole, instr); plainErr != nil {
					return coroLeafInstructionError(fn, plan, instr, "unsupported call: child await: "+err.Error()+"; direct plain: "+plainErr.Error())
				}
			case *ssa.Go:
				if !staticSpawn {
					return coroLeafInstructionError(fn, plan, instr, "goroutine spawn requires the closed-static scheduler capability")
				}
				callPlan, found := whole.CallPlan(instr)
				if !found {
					return coroLeafInstructionError(fn, plan, instr, "goroutine spawn has no compilation CallPlan")
				}
				switch callPlan.Rep {
				case coro.DirectCoro:
					target, targetPlan, err := resolveCoroDirectStaticSpawn(whole, instr, managedDispatch)
					if err != nil {
						return coroLeafInstructionError(fn, plan, instr, "unsupported closed static spawn: "+err.Error())
					}
					targetSignature := coroPhysicalNormalizeSourceSignature(target.Signature)
					if universe != nil {
						targetSignature, err = universe.coroPhysicalSourceSignature(target)
						if err != nil {
							return coroLeafInstructionError(fn, plan, instr, "spawn target signature: "+err.Error())
						}
					}
					if err := validateCoroLeafPhysicalSignature(targetPlan, targetSignature); err != nil {
						return coroLeafInstructionError(fn, plan, instr, "spawn target signature: "+err.Error())
					}
				case coro.Dispatch:
					if !managedDispatch {
						return coroLeafInstructionError(fn, plan, instr, "managed descriptor spawn requires the v1 descriptor dispatch capability")
					}
					if _, err := whole.ResolveManagedDispatchSpawn(instr); err != nil {
						return coroLeafInstructionError(fn, plan, instr, "unsupported managed descriptor spawn: "+err.Error())
					}
					if err := validateCoroManagedDispatchSignatureShape(instr.Common().Signature()); err != nil {
						return coroLeafInstructionError(fn, plan, instr, "managed descriptor spawn signature: "+err.Error())
					}
				default:
					return coroLeafInstructionError(fn, plan, instr, "goroutine spawn has unsupported representation "+callPlan.Rep.String())
				}
				spawns++
			default:
				return coroLeafInstructionError(fn, plan, instr, "instruction is outside the CFG physical ABI allowlist")
			}
		}
	}
	// A Go function may deliberately never return (for example select{} or an
	// infinite scheduler-polled loop). Cancellation still reaches the compiler-
	// owned completion block, so a source Return is not an ABI prerequisite.
	if panics != 0 && !plan.Exec.Contains(coro.MayUnwind) {
		return fail("explicit panic body lacks may-unwind execution classification: %s", plan.Exec)
	}
	if !plan.Effect.MaySuspend() {
		return fail("CFG physical body lacks a suspension-capable final effect: %s", plan.Effect)
	}
	if awaits != 0 && !plan.Effect.Contains(coro.AwaitStructured) {
		return fail("child-await body lacks await-structured final effect: %s", plan.Effect)
	}
	if parks != 0 && !plan.Effect.Contains(coro.MayPark) {
		return fail("structured-park body lacks may-park final effect: %s", plan.Effect)
	}
	if foreignWaits != 0 && !plan.Effect.Contains(coro.WaitForeign) {
		return fail("bounded worker body lacks wait-foreign final effect: %s", plan.Effect)
	}
	if yields != 0 && (!plan.DeclaredEffect.Contains(coro.YieldOnly) || !plan.LocalEffect.Contains(coro.YieldOnly) || !plan.Effect.Contains(coro.YieldOnly)) {
		return fail("structured-yield body lacks yield-only owner effect: declared=%s local=%s final=%s", plan.DeclaredEffect, plan.LocalEffect, plan.Effect)
	}
	if spawns != 0 && (!plan.DeclaredEffect.Contains(coro.YieldOnly) || !plan.LocalEffect.Contains(coro.YieldOnly) || !plan.Effect.Contains(coro.YieldOnly)) {
		return fail("coroutine spawn body lacks its exact yield-only owner seed: declared=%s local=%s final=%s", plan.DeclaredEffect, plan.LocalEffect, plan.Effect)
	}
	if plan.DeclaredEffect.Contains(coro.MayPark) && parks == 0 {
		return fail("declared may-park effect has no exact structured park intrinsic")
	}
	// WaitForeign may be inherited from a structured child. An ordinary local
	// foreign edge was counted and shape-checked above; the effect bit alone can
	// never authorize a raw foreign call on this scheduler thread.
	if unsupported := plan.Effect &^ (coro.YieldOnly | coro.AwaitStructured | coro.OutcomeStructured | coro.MayPark | coro.WaitForeign); unsupported != 0 {
		return fail("child-await body has unsupported final effect %s", unsupported)
	}
	if unsupported := plan.DeclaredEffect &^ (coro.YieldOnly | coro.MayPark); unsupported != 0 {
		return fail("child-await body has unsupported declared effect %s", unsupported)
	}
	if unsupported := plan.LocalEffect &^ (coro.YieldOnly | coro.AwaitStructured | coro.OutcomeStructured | coro.MayPark); unsupported != 0 {
		return fail("child-await body has unsupported local effect %s", unsupported)
	}
	if accept != nil {
		if err := accept(physical); err != nil {
			return fail("freeze physical plan: %v", err)
		}
	}
	return nil
}

func validateCoroPhysicalChannelType(typ types.Type) error {
	channel, ok := types.Unalias(typ).Underlying().(*types.Chan)
	if !ok {
		return fmt.Errorf("operand is not a channel")
	}
	if err := validateCoroPhysicalValueType(channel.Elem(), make(map[types.Type]bool)); err != nil {
		return fmt.Errorf("element type: %w", err)
	}
	return nil
}

func validateCoroExplicitStatusPanic(audit *coroPhysicalPureSSAAudit, instruction *ssa.Panic) string {
	if instruction == nil || instruction.X == nil {
		return "explicit-status panic requires a non-nil operand"
	}
	if audit == nil {
		return "explicit-status panic requires a prepared pure-SSA audit"
	}
	boxed, ok := instruction.X.(*ssa.MakeInterface)
	if !ok {
		target, interfaceValue := types.Unalias(audit.typeOf(instruction.X.Type())).Underlying().(*types.Interface)
		if !interfaceValue || !target.Empty() {
			return "explicit-status panic requires one empty-interface operand"
		}
		if reason := validateCoroExplicitStatusPanicInterfaceValue(audit, instruction.X, make(map[ssa.Value]bool)); reason != "" {
			return "explicit-status panic interface payload is not frame-stable: " + reason
		}
		return ""
	}
	if boxed.X == nil {
		return "explicit-status panic requires a complete concrete MakeInterface operand"
	}
	if boxed.Parent() != instruction.Parent() {
		return "explicit-status panic MakeInterface belongs to a different SSA body"
	}
	refs := boxed.Referrers()
	if refs == nil || len(*refs) != 1 || (*refs)[0] != instruction {
		return "explicit-status panic requires its MakeInterface to have the panic site as its sole consumer"
	}
	target, ok := types.Unalias(boxed.Type()).Underlying().(*types.Interface)
	if !ok || !target.Empty() {
		return "explicit-status panic requires an empty-interface MakeInterface result"
	}
	if isUntypedNilConst(boxed.X) {
		return "explicit-status panic does not yet support an untyped nil value"
	}
	source := boxed.X.Type()
	if audit != nil {
		source = audit.typeOf(source)
	}
	if source == nil {
		return "explicit-status panic MakeInterface has no concrete source type"
	}
	if reason := audit.validateMakeInterface(boxed); reason != "" {
		return "explicit-status panic MakeInterface has no outcome-safe lowering: " + reason
	}
	// Non-direct interface representations live in the managed backing cell
	// created by the exact MakeInterface helper. Under ExplicitStatus that helper
	// is an awaited coroutine child, so the allocation has completed before its
	// stable data word is published to the parent CompletionRecord.
	if !emissionDirectIfaceType(source) {
		return ""
	}
	if constant, ok := boxed.X.(*ssa.Const); ok && constant.Value == nil {
		// A typed nil pointer still produces a non-nil interface type word and
		// carries no frame-owned storage in its data word.
		return ""
	}
	switch types.Unalias(source).Underlying().(type) {
	case *types.Map, *types.Chan:
		// These direct interface words identify managed heap objects; publishing
		// the word itself retains the object independently of the child frame.
		return ""
	case *types.Pointer:
		// A frozen AllocZ result is a real managed-heap object, not storage
		// owned by the LLVM frame. The scheduler publishes this exact data word
		// into its parent CompletionRecord (or root PanicRecord) before destroying
		// the frame, so the non-moving-conservative/no-GC root profile retains it
		// just like a package-global pointer.
		root, reason := audit.stableAddress(boxed.X, make(map[ssa.Value]bool))
		if reason != "" || root != coroPhysicalAddressGlobal && root != coroPhysicalAddressManagedHeap {
			if reason == "" {
				reason = "payload is not rooted in package-global or managed-heap storage"
			}
			return "explicit-status panic data word may outlive its coroutine frame: " + reason
		}
		return ""
	default:
		// unsafe.Pointer, direct one-field wrappers, and function values can
		// borrow frame-owned storage. They need a dedicated payload-lifetime
		// certificate before the child may be destroyed.
		return "explicit-status direct panic payload has no post-destroy lifetime proof"
	}
}

// validateCoroExplicitStatusPanicInterfaceValue accepts an already-built
// interface only when its two words can be copied without borrowing storage
// that disappears before publication. For a typed load, the address therefore
// needs to be stable only through the load itself: an interface value is the
// type/data pair, and neither word points back at the interface cell. The pair
// is copied immediately into parent-owned completion storage, whose current
// nonmoving-conservative-or-none profile retains the dynamic data word after
// the child frame is destroyed. Parameters and arbitrary dynamic producers
// still stay fail-closed until they carry their own lifetime certificate.
func validateCoroExplicitStatusPanicInterfaceValue(
	audit *coroPhysicalPureSSAAudit,
	value ssa.Value,
	visiting map[ssa.Value]bool,
) string {
	if audit == nil || value == nil {
		return "missing pure-SSA audit or interface value"
	}
	if visiting[value] {
		return "cyclic interface value"
	}
	visiting[value] = true
	defer delete(visiting, value)
	if instruction, ok := value.(ssa.Instruction); ok && instruction.Parent() != audit.fn {
		return "interface producer belongs to a different SSA body"
	}
	interfaceType, ok := types.Unalias(audit.typeOf(value.Type())).Underlying().(*types.Interface)
	if !ok {
		return "value is not an interface"
	}
	interfaceType.Complete()
	switch value := value.(type) {
	case *ssa.ChangeInterface:
		if reason := audit.validateChangeInterface(value); reason != "" {
			return "interface conversion has no outcome-safe lowering: " + reason
		}
		return validateCoroExplicitStatusPanicInterfaceValue(audit, value.X, visiting)
	case *ssa.ChangeType:
		if reason := audit.validateChangeType(value); reason != "" {
			return "interface change-type has no pure lowering: " + reason
		}
		return validateCoroExplicitStatusPanicInterfaceValue(audit, value.X, visiting)
	case *ssa.Phi:
		if len(value.Edges) == 0 {
			return "interface phi has no incoming values"
		}
		for _, edge := range value.Edges {
			if reason := validateCoroExplicitStatusPanicInterfaceValue(audit, edge, visiting); reason != "" {
				return "interface phi input: " + reason
			}
		}
		return ""
	case *ssa.UnOp:
		if value.Op != token.MUL {
			return "interface producer is not a typed load"
		}
		if reason := audit.validateUnOp(value); reason != "" {
			return "interface load has no pure lowering: " + reason
		}
		root, reason := audit.stableAddressAt(value.X, value, make(map[ssa.Value]bool))
		if reason != "" {
			return "interface load address: " + reason
		}
		if root == coroPhysicalAddressInvalid {
			return "interface load address has no stable root"
		}
		return ""
	case *ssa.Call:
		if builtin, ok := value.Call.Value.(*ssa.Builtin); ok && builtin.Name() == "recover" {
			if reason := audit.validateBuiltin(value); reason != "" {
				return "recover result has no explicit-status lowering: " + reason
			}
			// The direct deferred-child hook copied these words from the
			// parent-owned CompletionRecord, which retains them until this child has
			// published its terminal CompletionRecord and been destroyed. A
			// repanic therefore transfers the same stable pair without borrowing
			// child-frame storage.
			return ""
		}
		if audit.plan == nil || audit.fn == nil {
			return "interface call result requires a whole-program call plan"
		}
		callerPlan, planned := audit.plan.FunctionPlan(audit.fn)
		if !planned {
			return "interface call result owner has no function plan"
		}
		if _, _, err := resolveCoroStaticAwait(audit.plan, callerPlan, value, audit.universe); err == nil {
			// The child writes its Go result into parent-owned result storage before
			// the parent resumes and destroys the child. Go escape semantics keep
			// any backing cell referenced by the returned interface alive; copying
			// the two words into the panic completion record is therefore stable.
			return ""
		}
		if _, targetPlan, err := resolveCoroStaticPlainCall(audit.plan, value); err == nil &&
			targetPlan.External == coro.Defined && !targetPlan.Exec.Contains(coro.MayUnwind) {
			// A bounded owned Go callee has completed normally on the same stack;
			// its returned interface obeys the same language-level escape lifetime.
			return ""
		}
		return "interface call result is not one exact managed child or non-unwinding owned plain call"
	default:
		return fmt.Sprintf("interface producer %T has no post-destroy lifetime proof", value)
	}
}

func isCoroProgramManagedEntry(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	name := fn.Name()
	if name == "init" || strings.HasPrefix(name, "init#") {
		return true
	}
	return name == "main" && fn.Pkg != nil && fn.Pkg.Pkg != nil && fn.Pkg.Pkg.Name() == "main"
}

func coroMaterializedGenericInstance(fn *ssa.Function) bool {
	if fn == nil || fn.Origin() == nil || fn.Origin() == fn || len(fn.TypeArgs()) == 0 ||
		!hasGenericInstantiation(fn) || !coroGroundGenericTypeArgs(fn.TypeArgs()) {
		return false
	}
	if parent := fn.Parent(); parent != nil {
		// x/tools materializes a function literal inside each instantiated
		// generic body. The child keeps the origin's TypeParams metadata, but its
		// signature, parameters, free variables, and TypeArgs are concrete. Bind
		// the exception to that exact parent/Origin/AnonFuncs graph; an arbitrary
		// nested synthetic function cannot acquire a dispatch ABI merely by
		// carrying TypeArgs.
		if !coroMaterializedGenericInstance(parent) || fn.Synthetic != "" || fn.Object() != nil {
			return false
		}
		if _, ok := fn.Syntax().(*ast.FuncLit); !ok {
			return false
		}
		originParent := fn.Origin().Parent()
		if originParent == nil || originParent != parent.Origin() {
			return false
		}
		found := false
		for _, child := range parent.AnonFuncs {
			if child == fn {
				if found {
					return false
				}
				found = true
			}
		}
		if !found || len(fn.TypeArgs()) != len(parent.TypeArgs()) {
			return false
		}
		for index, argument := range fn.TypeArgs() {
			if !types.Identical(argument, parent.TypeArgs()[index]) {
				return false
			}
		}
	} else if !strings.HasPrefix(fn.Synthetic, "instance of ") {
		return false
	}
	// x/tools erases ordinary declaration type parameters from an instantiated
	// callable signature. For an instantiated generic receiver method, and for
	// its parentless method body only, it keeps the origin's RecvTypeParams
	// metadata even though Recv and every physical parameter/result are already
	// concrete. Judge materialization from that callable value shape, not from
	// the stale declaration metadata alone.
	if params := fn.Signature.TypeParams(); params != nil && params.Len() != 0 {
		return false
	}
	if params := fn.Signature.RecvTypeParams(); params != nil && params.Len() != 0 &&
		(fn.Parent() != nil || fn.Signature.Recv() == nil) {
		return false
	}
	normalized := coroPhysicalNormalizeSourceSignature(fn.Signature)
	if normalized == nil || normalized.Params().Len() != len(fn.Params) {
		return false
	}
	for index, parameter := range fn.Params {
		if parameter == nil || !types.Identical(parameter.Type(), normalized.Params().At(index).Type()) {
			return false
		}
	}
	for _, tuple := range []*types.Tuple{normalized.Params(), normalized.Results()} {
		for index := 0; index < tuple.Len(); index++ {
			if validateCoroPhysicalValueType(tuple.At(index).Type(), make(map[types.Type]bool)) != nil {
				return false
			}
		}
	}
	for _, free := range fn.FreeVars {
		if free == nil || coroTypeContainsUnresolvedTypeParam(free.Type(), make(map[types.Type]bool)) ||
			validateCoroPhysicalValueType(free.Type(), make(map[types.Type]bool)) != nil {
			return false
		}
	}
	return true
}

// coroMaterializedGenericCallable includes the one Pkg-nil method-set wrapper
// shape that x/tools synthesizes when a pointer invokes an instantiated generic
// value-receiver method. Such a wrapper has no Origin or TypeArgs of its own,
// but its receiver, SSA parameters, and sole callee are fully concrete. Keep
// this separate from ordinary generic instances so arbitrary synthetic bodies
// cannot acquire a physical ABI from stale RecvTypeParams metadata.
func coroMaterializedGenericCallable(fn *ssa.Function) bool {
	return coroMaterializedGenericInstance(fn) || coroMaterializedGenericMethodWrapper(fn)
}

func coroMaterializedGenericMethodWrapper(fn *ssa.Function) bool {
	if fn == nil || fn.Pkg != nil || fn.Parent() != nil || len(fn.FreeVars) != 0 ||
		fn.Signature == nil || fn.Signature.Recv() == nil ||
		!strings.HasPrefix(fn.Synthetic, "wrapper for ") || !hasGenericInstantiation(fn) ||
		typeParamCount(fn.Signature.TypeParams()) != 0 ||
		typeParamCount(fn.Signature.RecvTypeParams()) == 0 || len(fn.Blocks) != 1 {
		return false
	}
	var nilCheck *ssa.Call
	var receiverLoad *ssa.UnOp
	var wrapperCall *ssa.Call
	var callee *ssa.Function
	for _, instruction := range fn.Blocks[0].Instrs {
		switch instruction := instruction.(type) {
		case *ssa.DebugRef, *ssa.Return:
		case *ssa.Call:
			if builtin, ok := instruction.Common().Value.(*ssa.Builtin); ok {
				if nilCheck != nil || builtin.Name() != "ssa:wrapnilchk" || len(instruction.Common().Args) != 3 ||
					instruction.Common().Args[0] != fn.Params[0] ||
					!types.Identical(instruction.Type(), fn.Params[0].Type()) {
					return false
				}
				nilCheck = instruction
				continue
			}
			if wrapperCall != nil || instruction.Common() == nil || instruction.Common().IsInvoke() {
				return false
			}
			callee = instruction.Common().StaticCallee()
			if callee == nil {
				return false
			}
			wrapperCall = instruction
		case *ssa.UnOp:
			if receiverLoad != nil || instruction.Op != token.MUL {
				return false
			}
			receiverLoad = instruction
		default:
			return false
		}
	}
	if nilCheck == nil || receiverLoad == nil || receiverLoad.X != nilCheck || wrapperCall == nil ||
		callee == nil || !coroMaterializedGenericInstance(callee) ||
		callee.Signature == nil || callee.Signature.Recv() == nil || len(wrapperCall.Common().Args) == 0 ||
		wrapperCall.Common().Args[0] != receiverLoad {
		return false
	}
	calleeOrigin := callee.Origin()
	if calleeOrigin == nil || calleeOrigin.Name() != fn.Name() {
		return false
	}
	wrapperReceiver, pointerReceiver := types.Unalias(fn.Signature.Recv().Type()).Underlying().(*types.Pointer)
	if !pointerReceiver || !types.Identical(wrapperReceiver.Elem(), callee.Signature.Recv().Type()) ||
		!types.Identical(receiverLoad.Type(), callee.Signature.Recv().Type()) {
		return false
	}
	expectedSyntheticPrefix := "wrapper for func (" + callee.Signature.Recv().Type().String() + ")." + calleeOrigin.Name() + "("
	if !strings.HasPrefix(fn.Synthetic, expectedSyntheticPrefix) {
		return false
	}
	wrapperSig := coroPhysicalNormalizeSourceSignature(fn.Signature)
	calleeSig := coroPhysicalNormalizeSourceSignature(callee.Signature)
	if wrapperSig == nil || calleeSig == nil || wrapperSig.Params().Len() != len(fn.Params) ||
		wrapperSig.Params().Len() != calleeSig.Params().Len() ||
		wrapperSig.Results().Len() != calleeSig.Results().Len() {
		return false
	}
	for index, parameter := range fn.Params {
		if parameter == nil || !types.Identical(parameter.Type(), wrapperSig.Params().At(index).Type()) ||
			(index != 0 && !types.Identical(wrapperSig.Params().At(index).Type(), calleeSig.Params().At(index).Type())) {
			return false
		}
	}
	for index := 0; index < wrapperSig.Results().Len(); index++ {
		if !types.Identical(wrapperSig.Results().At(index).Type(), calleeSig.Results().At(index).Type()) {
			return false
		}
	}

	return true
}

func coroGroundGenericTypeArgs(arguments []types.Type) bool {
	if len(arguments) == 0 {
		return false
	}
	for _, argument := range arguments {
		if coroTypeContainsUnresolvedTypeParam(argument, make(map[types.Type]bool)) {
			return false
		}
	}
	return true
}

// coroTypeContainsUnresolvedTypeParam is deliberately deeper than the
// physical-value validator: a pointer has a fixed transport width, but
// *Box[T] is still not a materialized generic identity. This proof is used
// only for instantiation identity and therefore follows referents and named
// underlying types all the way to a TypeParam.
func coroTypeContainsUnresolvedTypeParam(typ types.Type, visiting map[types.Type]bool) bool {
	if typ == nil {
		return true
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)

	switch value := typ.(type) {
	case *types.TypeParam:
		return true
	case *types.Named:
		if arguments := value.TypeArgs(); arguments != nil {
			for index := 0; index < arguments.Len(); index++ {
				if coroTypeContainsUnresolvedTypeParam(arguments.At(index), visiting) {
					return true
				}
			}
		}
		return coroTypeContainsUnresolvedTypeParam(value.Underlying(), visiting)
	case *types.Pointer:
		return coroTypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Array:
		return coroTypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Slice:
		return coroTypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Chan:
		return coroTypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Map:
		return coroTypeContainsUnresolvedTypeParam(value.Key(), visiting) ||
			coroTypeContainsUnresolvedTypeParam(value.Elem(), visiting)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if coroTypeContainsUnresolvedTypeParam(value.Field(index).Type(), visiting) {
				return true
			}
		}
	case *types.Signature:
		if typeParamCount(value.TypeParams()) != 0 || typeParamCount(value.RecvTypeParams()) != 0 {
			return true
		}
		if value.Recv() != nil && coroTypeContainsUnresolvedTypeParam(value.Recv().Type(), visiting) {
			return true
		}
		for _, tuple := range []*types.Tuple{value.Params(), value.Results()} {
			for index := 0; index < tuple.Len(); index++ {
				if coroTypeContainsUnresolvedTypeParam(tuple.At(index).Type(), visiting) {
					return true
				}
			}
		}
	case *types.Tuple:
		for index := 0; index < value.Len(); index++ {
			if coroTypeContainsUnresolvedTypeParam(value.At(index).Type(), visiting) {
				return true
			}
		}
	case *types.Interface:
		value.Complete()
		for index := 0; index < value.NumMethods(); index++ {
			if coroTypeContainsUnresolvedTypeParam(value.Method(index).Type(), visiting) {
				return true
			}
		}
		for index := 0; index < value.NumEmbeddeds(); index++ {
			if coroTypeContainsUnresolvedTypeParam(value.EmbeddedType(index), visiting) {
				return true
			}
		}
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if coroTypeContainsUnresolvedTypeParam(value.Term(index).Type(), visiting) {
				return true
			}
		}
	}
	return false
}

// resolveCoroStaticPlainCall proves the synchronous island allowed inside a
// runnable physical coroutine. The exact CallPlan must select either one
// defined primary plain body, one frozen known external plain entry, or one
// exact TrustedInline invocation of a conservatively unknown foreign entry.
// The last form is an edge capability: it suppresses BlockForeign only for
// that call and never upgrades the target's default policy. A
// missing/open/dynamic edge may not fall back to the legacy source symbol.
func resolveCoroStaticPlainCall(plan *coro.SSAPlan, call ssa.CallInstruction) (*ssa.Function, coro.FunctionPlan, error) {
	if plan == nil || call == nil || call.Common() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	common := call.Common()
	if common.IsInvoke() {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a non-invoke call")
	}
	callPlan, ok := plan.CallPlan(call)
	if !ok {
		return nil, coro.FunctionPlan{}, fmt.Errorf("call has no compilation CallPlan")
	}
	trustedInline := callPlan.Kind == coro.CallTrustedInline
	ordinaryDirect := callPlan.Kind == coro.CallDirect
	if (!ordinaryDirect && !trustedInline) || callPlan.Rep != coro.DirectPlain || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"requires one closed non-nil direct plain target, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	if trustedInline {
		if callPlan.InvocationPolicy != coro.InvocationTrustedInline || callPlan.InvocationContract == "" ||
			callPlan.InvocationABI == "" || callPlan.InvocationCertificate == "" {
			return nil, coro.FunctionPlan{}, fmt.Errorf("trusted-inline direct call has incomplete frozen invocation metadata")
		}
	} else if callPlan.InvocationPolicy != "" || callPlan.InvocationContract != "" ||
		callPlan.InvocationABI != "" || callPlan.InvocationCertificate != "" {
		return nil, coro.FunctionPlan{}, fmt.Errorf("ordinary direct call unexpectedly carries invocation capability metadata")
	}
	target, ok := plan.Function(callPlan.Targets[0])
	if !ok || target == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("direct plain target %q is absent from the compilation plan", callPlan.Targets[0])
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return nil, coro.FunctionPlan{}, fmt.Errorf("direct plain target %q has no canonical function plan", callPlan.Targets[0])
	}
	if common.StaticCallee() == nil && (len(target.FreeVars) != 0 || target.Signature == nil || target.Signature.Recv() != nil) {
		return nil, coro.FunctionPlan{}, fmt.Errorf("closed direct plain target requires a non-capturing non-method callable")
	}
	validBody := targetPlan.External == coro.Defined && targetPlan.Emission == coro.EmitPlain && targetPlan.Primary == coro.PrimaryPlain
	validExternal := targetPlan.External == coro.ExternalKnown && targetPlan.Emission == coro.EmitExternal && targetPlan.Primary == coro.PrimaryExternal
	validTrustedExternal := trustedInline && targetPlan.External == coro.ExternalUnknownForeign &&
		targetPlan.Emission == coro.EmitExternal && targetPlan.Primary == coro.PrimaryExternal
	effectiveExec := targetPlan.Exec
	allowedExec := coro.MayUnwind | coro.IRQUnsafe
	if trustedInline {
		targetCertificate, certified := plan.CallableContractCertificate(target)
		if !certified {
			return nil, coro.FunctionPlan{}, fmt.Errorf("trusted-inline target has no frozen callable contract certificate")
		}
		if err := targetCertificate.Validate(); err != nil {
			return nil, coro.FunctionPlan{}, fmt.Errorf("trusted-inline target has invalid callable contract certificate: %w", err)
		}
		if targetCertificate.Scope != coro.CallableContractScopeDeclaration || !targetCertificate.HasTrustedInlineContract {
			return nil, coro.FunctionPlan{}, fmt.Errorf("trusted-inline target does not own one declaration refinement")
		}
		if callPlan.InvocationContract != targetCertificate.TrustedInlineContract.ID {
			return nil, coro.FunctionPlan{}, fmt.Errorf(
				"trusted-inline invocation contract %q is not owned by target %q (want %q)",
				callPlan.InvocationContract, targetPlan.ID, targetCertificate.TrustedInlineContract.ID,
			)
		}
		if callPlan.InvocationABI != targetCertificate.CallableABI {
			return nil, coro.FunctionPlan{}, fmt.Errorf(
				"trusted-inline invocation ABI %q differs from target %q ABI %q",
				callPlan.InvocationABI, targetPlan.ID, targetCertificate.CallableABI,
			)
		}
		if err := coro.ValidateTrustedInlineCallableContractRefinement(
			targetCertificate.TrustedInlineContract, targetCertificate.Contract,
		); err != nil {
			return nil, coro.FunctionPlan{}, fmt.Errorf("trusted-inline target refinement is invalid: %w", err)
		}
		defaultExec := coro.CallableContractExecConstraints(targetCertificate.Contract)
		selectedExec := coro.CallableContractExecConstraints(targetCertificate.TrustedInlineContract)
		const contractExec = coro.ThreadAffine | coro.OpaqueExec
		if unsupported := (defaultExec | selectedExec) &^ contractExec; unsupported != 0 {
			return nil, coro.FunctionPlan{}, fmt.Errorf("trusted-inline target projected non-contract execution flags %s", unsupported)
		}
		if widening := selectedExec &^ defaultExec; widening != 0 {
			return nil, coro.FunctionPlan{}, fmt.Errorf("trusted-inline selected execution projection widens default by %s", widening)
		}
		declared := targetPlan.DeclaredExec & contractExec
		localLane := targetPlan.LocalExec & contractExec
		finalLane := targetPlan.Exec & contractExec
		if declared != defaultExec || localLane != defaultExec || finalLane != defaultExec {
			return nil, coro.FunctionPlan{}, fmt.Errorf(
				"trusted-inline target default contract execution projection is %s, lanes are declared=%s local=%s final=%s",
				defaultExec, declared, localLane, finalLane,
			)
		}
		// ProgressExecutorSafe removes this exact edge's default stack cut. The
		// selected contract replaces only its own projected lane; IRQUnsafe,
		// MayUnwind, and unrelated constraints remain in effectiveExec.
		effectiveExec &^= coro.BlockForeign
		effectiveExec &^= defaultExec
		effectiveExec |= selectedExec
	}
	unsupportedExec := effectiveExec &^ allowedExec
	directEntry := targetPlan.FuncRep == coro.DirectPlain ||
		(common.StaticCallee() != nil && targetPlan.FuncRep == coro.Dispatch && validBody)
	if (!validBody && !validExternal && !validTrustedExternal) || !directEntry || targetPlan.Effect != coro.NoSuspend ||
		targetPlan.Demand == coro.NoDemand || unsupportedExec != 0 {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"target %q is not one demanded defined, known-external, or exact trusted-inline foreign bounded no-suspend plain entry (external=%s emission=%s primary=%s representation=%s effect=%s exec=%s effective-exec=%s demand=%s)",
			targetPlan.ID, targetPlan.External, targetPlan.Emission, targetPlan.Primary, targetPlan.FuncRep, targetPlan.Effect, targetPlan.Exec, effectiveExec, targetPlan.Demand,
		)
	}
	return target, targetPlan, nil
}

// validateCoroLeafPhysicalABI preserves the v0 leaf-only acceptance boundary
// and diagnostics. Enabling later physical ABI capabilities must not silently
// change an archive still identified as PhysicalABIV0/SchedulerNoneABIV0.
func validateCoroLeafPhysicalABI(fn *ssa.Function, plan coro.FunctionPlan) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine physical ABI: function %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if fn == nil || plan.External != coro.Defined || len(fn.Blocks) == 0 {
		return fail("requires one defined SSA body")
	}
	if emitShadowStackInstrumentation {
		return fail("legacy thread-local shadow-stack instrumentation is incompatible with stackless coroutine suspension")
	}
	if plan.Emission != coro.EmitCoroutine || plan.FuncRep != coro.DirectCoro {
		return fail("requires a direct coroutine emission, got emission=%s representation=%s", plan.Emission, plan.FuncRep)
	}
	if !plan.ManagedDemand.Contains(coro.AsyncDemand) {
		return fail(
			"requires managed async demand, got aggregate=%s managed=%s raw=%t raw-entry=%t",
			plan.Demand, plan.ManagedDemand, plan.RawPlainDemand, plan.RawPlainEntry,
		)
	}
	if plan.Recursive {
		return fail("recursive coroutine lowering requires child frames and preemption polls")
	}
	if plan.DeclaredEffect != coro.YieldOnly || plan.LocalEffect != coro.YieldOnly || plan.Effect != coro.YieldOnly {
		return fail("requires an explicit, isolated yield-only effect, got declared=%s local=%s final=%s", plan.DeclaredEffect, plan.LocalEffect, plan.Effect)
	}
	if unsupported := plan.Exec &^ coro.MayUnwind; unsupported != 0 {
		return fail("execution flags %s require lowering outside the leaf ABI", unsupported)
	}
	if len(fn.FreeVars) != 0 {
		return fail("closures require the coroutine context ABI")
	}
	for _, nested := range fn.AnonFuncs {
		if nested != nil && len(nested.FreeVars) != 0 {
			return fail("nested function literals require closure body lowering")
		}
	}
	if fn.Signature.Variadic() {
		return fail("variadic coroutine ABI is not implemented")
	}
	if directive := coroLeafABIDirective(fn); directive != "" {
		return fail("ABI directive %q requires a root or foreign adapter", directive)
	}
	if isCgoExternSymbol(fn) {
		return fail("cgo entry requires a foreign adapter")
	}
	genericInstance := coroMaterializedGenericCallable(fn)
	if fn.Synthetic != "" && !genericInstance {
		return fail("synthetic function %q is outside the leaf ABI", fn.Synthetic)
	}
	if list := fn.TypeParams(); list != nil && list.Len() != 0 && !genericInstance {
		return fail("generic declarations are not materialized coroutine bodies")
	}
	if list := fn.Signature.RecvTypeParams(); list != nil && list.Len() != 0 && !genericInstance {
		return fail("generic receivers are not materialized coroutine bodies")
	}
	if list := fn.TypeArgs(); len(list) != 0 && !genericInstance {
		return fail("generic instances require a frozen instantiated ABI")
	}
	if isCoroProgramManagedEntry(fn) {
		return fail("program roots require scheduler bootstrap lowering")
	}
	if len(fn.Blocks) != 1 {
		return fail("requires exactly one basic block, got %d", len(fn.Blocks))
	}
	physicalSourceSig := coroPhysicalNormalizeSourceSignature(fn.Signature)
	if err := validateCoroPhysicalSSAParameterShape(plan, fn, physicalSourceSig, nil); err != nil {
		return err
	}
	if err := validateCoroLeafPhysicalSignature(plan, physicalSourceSig); err != nil {
		return err
	}

	returns := 0
	for _, instr := range fn.Blocks[0].Instrs {
		switch instr := instr.(type) {
		case *ssa.DebugRef:
		case *ssa.Return:
			returns++
		case *ssa.BinOp:
			if instr.Op == token.QUO || instr.Op == token.REM || instr.Op == token.SHL || instr.Op == token.SHR ||
				!coroLeafScalar(instr.Type()) ||
				!coroLeafScalar(instr.X.Type()) || !coroLeafScalar(instr.Y.Type()) {
				return coroLeafInstructionError(fn, plan, instr, "potentially panicking or non-scalar binary operation")
			}
		case *ssa.UnOp:
			if (instr.Op != token.SUB && instr.Op != token.XOR && instr.Op != token.NOT) || !coroLeafScalar(instr.Type()) {
				return coroLeafInstructionError(fn, plan, instr, "unsupported unary operation")
			}
		case *ssa.Convert, *ssa.ChangeType:
			value, ok := instr.(ssa.Value)
			if !ok || !coroLeafScalar(value.Type()) {
				return coroLeafInstructionError(fn, plan, instr, "non-scalar conversion")
			}
		default:
			return coroLeafInstructionError(fn, plan, instr, "instruction is outside the ABI-only leaf allowlist")
		}
	}
	if returns != 1 {
		return fail("requires exactly one return instruction, got %d", returns)
	}
	return nil
}

func (u *EmissionUniverse) coroPhysicalSourceSignature(fn *ssa.Function) (*types.Signature, error) {
	owner := u.ownerOf(fn)
	ctx, err := u.functionABIContext(fn, owner)
	if err != nil {
		return nil, fmt.Errorf("coroutine physical ABI: function %q: derive effective signature: %w", fn.Name(), err)
	}
	sig, ok := ctx.patchType(fn.Signature).(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("coroutine physical ABI: function %q: effective type is not a signature", fn.Name())
	}
	if receiver := fn.Signature.Recv(); receiver != nil {
		effectiveReceiver := ctx.patchType(receiver.Type())
		if !types.Identical(effectiveReceiver, sig.Recv().Type()) {
			receiver = types.NewVar(receiver.Pos(), receiver.Pkg(), receiver.Name(), effectiveReceiver)
			sig = types.NewSignatureType(
				receiver,
				coroPhysicalTypeParamSlice(sig.RecvTypeParams()),
				coroPhysicalTypeParamSlice(sig.TypeParams()),
				sig.Params(), sig.Results(), sig.Variadic(),
			)
		}
	}
	if params := sig.RecvTypeParams(); params != nil && params.Len() != 0 && !coroMaterializedGenericCallable(fn) {
		pkgPath := "<nil>"
		if fn.Pkg != nil && fn.Pkg.Pkg != nil {
			pkgPath = fn.Pkg.Pkg.Path()
		}
		origin, originName := fn.Origin(), "<nil>"
		if origin != nil {
			originName = origin.String()
		}
		return nil, fmt.Errorf(
			"coroutine physical ABI: function %q (%s, package=%q, synthetic=%q, origin=%s, type-args=%d): effective generic receiver has %d type parameters",
			fn.Name(), fn.String(), pkgPath, fn.Synthetic, originName, len(fn.TypeArgs()), params.Len(),
		)
	}
	return coroPhysicalNormalizeSourceSignature(sig), nil
}

func coroPhysicalTypeParamSlice(list *types.TypeParamList) []*types.TypeParam {
	if list == nil || list.Len() == 0 {
		return nil
	}
	params := make([]*types.TypeParam, list.Len())
	for index := range params {
		params[index] = list.At(index)
	}
	return params
}

// coroPhysicalEntrySourceSignature adds the one typed closure environment that
// belongs to a captured physical entry. It is deliberately separate from
// coroPhysicalSourceSignature: source call sites and lowered helper markers see
// only explicit Go parameters, while the descriptor thunk supplies this
// compiler-owned context between (g,out) and those parameters.
func (u *EmissionUniverse) coroPhysicalEntrySourceSignature(fn *ssa.Function) (*types.Signature, error) {
	sig, err := u.coroPhysicalSourceSignature(fn)
	if err != nil || fn == nil || len(fn.FreeVars) == 0 {
		return sig, err
	}
	if fn.Signature == nil || fn.Signature.Recv() != nil {
		return nil, fmt.Errorf("coroutine physical ABI: captured function %q must be a receiver-free closure body", fn.Name())
	}
	owner := u.ownerOf(fn)
	if owner == nil || owner.pkgTypes == nil {
		return nil, fmt.Errorf("coroutine physical ABI: captured function %q has no emission owner", fn.Name())
	}
	return llssa.FuncAddCtx(makeClosureCtx(owner.pkgTypes, fn.FreeVars), sig), nil
}

// coroPhysicalNormalizeSourceSignature maps a declared receiver to the exact
// leading ordinary parameter used by x/tools SSA and LLGo's existing Go method
// declaration ABI. It also clears the source-only variadic marker: x/tools SSA
// has already packed a variadic call into its final []T argument, and every
// LLVM coroutine entry/call transports that ordinary slice value. Thus no C
// varargs convention or second coroutine ABI is involved. It is idempotent.
func coroPhysicalNormalizeSourceSignature(sig *types.Signature) *types.Signature {
	if sig == nil {
		return sig
	}
	if sig.Recv() != nil {
		sig = llssa.FuncAddCtx(sig.Recv(), sig)
	}
	if !sig.Variadic() {
		return sig
	}
	return types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), false)
}

func validateCoroPhysicalSSAParameterShape(plan coro.FunctionPlan, fn *ssa.Function, effective *types.Signature, universe *EmissionUniverse) error {
	fail := func(format string, args ...any) error {
		name := "<nil>"
		if fn != nil {
			name = fn.String()
		}
		return fmt.Errorf("coroutine physical ABI: function %q (%s): %s", plan.ID, name, fmt.Sprintf(format, args...))
	}
	if fn == nil || fn.Signature == nil || effective == nil {
		return fail("requires an SSA function and effective source signature")
	}
	source := coroPhysicalNormalizeSourceSignature(fn.Signature)
	if source.Params().Len() != len(fn.Params) {
		return fail("normalized source parameters=%d do not match SSA parameters=%d", source.Params().Len(), len(fn.Params))
	}
	offset := 0
	if len(fn.FreeVars) != 0 {
		offset = 1
		if effective.Params().Len() == 0 || !coroPhysicalClosureContextMatches(fn, effective.Params().At(0).Type()) {
			return fail("effective captured entry has no exact typed closure context")
		}
	}
	if effective.Params().Len() != len(fn.Params)+offset {
		return fail("effective entry parameters=%d do not match SSA parameters=%d plus hidden-context=%d", effective.Params().Len(), len(fn.Params), offset)
	}
	for index, parameter := range fn.Params {
		if parameter == nil || !types.Identical(parameter.Type(), source.Params().At(index).Type()) {
			return fail("SSA parameter %d type %v does not match normalized source parameter %v", index, parameterType(parameter), source.Params().At(index).Type())
		}
		effectiveType := effective.Params().At(index + offset).Type()
		sourceType := source.Params().At(index).Type()
		if !types.Identical(effectiveType, sourceType) &&
			(universe == nil || coroPhysicalTransportTypeKey(universe, effectiveType) != coroPhysicalTransportTypeKey(universe, sourceType)) {
			return fail("effective parameter %d type %v is not ABI-compatible with normalized source parameter %v", index, effectiveType, sourceType)
		}
	}
	return nil
}

// coroPhysicalTransportTypeKey describes only the value transported by the
// coroutine entry ABI. It deliberately erases pointee and logical descriptor
// identity: LLVM opaque pointers do not carry a referent type, while LLGo's
// map, channel, interface, and slice values each have one frozen aggregate
// shape independent of their source element or method types. Function values
// are different: an exact //llgo:type C function is one opaque code pointer,
// whereas a managed Go function is a two-word descriptor. Inline arrays and
// structs remain recursive and exact. This is used only after the emission
// universe has proved an exact source -> canonical patch alias.
func coroPhysicalTransportTypeKey(universe *EmissionUniverse, typ types.Type) string {
	var key func(types.Type) string
	key = func(typ types.Type) string {
		typ = types.Unalias(typ)
		if _, signature := typ.Underlying().(*types.Signature); signature {
			transport, err := coroCallableLeafTransport(universe, typ)
			if err == nil && transport == coro.RawCCodePointer {
				// Raw C function values use the same one-word LLVM transport as
				// every other opaque pointer. Keeping this physical equivalence is
				// required for exact frontend patch aliases to pointer types.
				return framedEmissionKey("opaque-pointer")
			}
			// Fail closed on absent/invalid raw-C metadata: only an exact
			// frontend classification can select the one-word transport.
			return framedEmissionKey("managed-function-descriptor")
		}
		switch value := typ.(type) {
		case *types.Named:
			return key(value.Underlying())
		case *types.Basic:
			if value.Kind() == types.UnsafePointer {
				return framedEmissionKey("opaque-pointer")
			}
			return framedEmissionKey("basic", fmt.Sprint(int(value.Kind())))
		case *types.Pointer:
			return framedEmissionKey("opaque-pointer")
		case *types.Map:
			return framedEmissionKey("map")
		case *types.Chan:
			return framedEmissionKey("chan")
		case *types.Interface:
			return framedEmissionKey("interface")
		case *types.Slice:
			return framedEmissionKey("slice")
		case *types.Array:
			return framedEmissionKey("array", fmt.Sprint(value.Len()), key(value.Elem()))
		case *types.Struct:
			fields := make([]string, 0, value.NumFields()+1)
			fields = append(fields, "struct")
			for index := 0; index < value.NumFields(); index++ {
				fields = append(fields, key(value.Field(index).Type()))
			}
			return framedEmissionKey(fields...)
		case *types.Tuple:
			fields := make([]string, 0, value.Len()+1)
			fields = append(fields, "tuple")
			for index := 0; index < value.Len(); index++ {
				fields = append(fields, key(value.At(index).Type()))
			}
			return framedEmissionKey(fields...)
		default:
			return framedEmissionKey("unsupported", fmt.Sprintf("%T", typ))
		}
	}
	return key(typ)
}

func parameterType(parameter *ssa.Parameter) types.Type {
	if parameter == nil {
		return nil
	}
	return parameter.Type()
}

func coroPhysicalClosureContextMatches(fn *ssa.Function, typ types.Type) bool {
	if fn == nil || len(fn.FreeVars) == 0 || typ == nil {
		return false
	}
	pointer, ok := types.Unalias(typ).Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	fields, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Struct)
	if !ok || fields.NumFields() != len(fn.FreeVars) {
		return false
	}
	for index, free := range fn.FreeVars {
		if free == nil || !types.Identical(fields.Field(index).Type(), free.Type()) {
			return false
		}
	}
	return true
}

func validateCoroLeafPhysicalSignature(plan coro.FunctionPlan, sig *types.Signature) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine physical ABI: function %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if sig == nil {
		return fail("requires a physical source signature")
	}
	if sig.Variadic() {
		return fail("effective variadic coroutine ABI is not implemented")
	}
	if params := sig.TypeParams(); params != nil && params.Len() != 0 {
		return fail("effective generic declaration has %d type parameters", params.Len())
	}
	if params := sig.RecvTypeParams(); params != nil && params.Len() != 0 {
		return fail("effective generic receiver has %d type parameters", params.Len())
	}
	sig = coroPhysicalNormalizeSourceSignature(sig)
	for i := 0; i < sig.Params().Len(); i++ {
		if err := validateCoroPhysicalValueType(sig.Params().At(i).Type(), make(map[types.Type]bool)); err != nil {
			return fail("parameter %d has unsupported type %s: %v", i, sig.Params().At(i).Type(), err)
		}
	}
	for i := 0; i < sig.Results().Len(); i++ {
		if err := validateCoroPhysicalValueType(sig.Results().At(i).Type(), make(map[types.Type]bool)); err != nil {
			return fail("result %d has unsupported type %s: %v", i, sig.Results().At(i).Type(), err)
		}
	}
	return nil
}

// validateCoroPhysicalFunctionValueABI keeps function-valued transport on the
// one compilation-wide representation path. The generic LLGo type converter
// supplies the canonical two-pointer closure layout, while FuncRepABIV1's
// ValuePlan validation decides whether the first word is a direct entry or a
// descriptor. Accepting the width here must not create a second, unplanned
// function representation at a coroutine boundary.
func validateCoroPhysicalFunctionValueABI(plan coro.FunctionPlan, sig *types.Signature, plainDispatch bool) error {
	if sig == nil || !coroPhysicalSignatureContainsFunctionValue(sig) || plainDispatch {
		return nil
	}
	return fmt.Errorf(
		"coroutine physical ABI: function %q: function-valued parameters/results require canonical ValuePlan validation and the descriptor/closure ABI",
		plan.ID,
	)
}

func coroPhysicalSignatureContainsFunctionValue(sig *types.Signature) bool {
	for _, tuple := range []*types.Tuple{sig.Params(), sig.Results()} {
		if tuple == nil {
			continue
		}
		for i := 0; i < tuple.Len(); i++ {
			if coroPhysicalTypeContainsFunctionValue(tuple.At(i).Type(), make(map[types.Type]bool)) {
				return true
			}
		}
	}
	return false
}

func coroPhysicalTypeContainsFunctionValue(typ types.Type, visiting map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch value := typ.(type) {
	case *types.Signature:
		return true
	case *types.Named:
		return coroPhysicalTypeContainsFunctionValue(value.Underlying(), visiting)
	case *types.Struct:
		for i := 0; i < value.NumFields(); i++ {
			if coroPhysicalTypeContainsFunctionValue(value.Field(i).Type(), visiting) {
				return true
			}
		}
	case *types.Array, *types.Slice, *types.Chan:
		var elem types.Type
		switch container := value.(type) {
		case *types.Array:
			elem = container.Elem()
		case *types.Slice:
			elem = container.Elem()
		case *types.Chan:
			elem = container.Elem()
		}
		return coroPhysicalTypeContainsFunctionValue(elem, visiting)
	case *types.Map:
		return coroPhysicalTypeContainsFunctionValue(value.Key(), visiting) ||
			coroPhysicalTypeContainsFunctionValue(value.Elem(), visiting)
	case *types.Tuple:
		for i := 0; i < value.Len(); i++ {
			if coroPhysicalTypeContainsFunctionValue(value.At(i).Type(), visiting) {
				return true
			}
		}
	}
	return false
}

// validateCoroPhysicalValueType proves only that a source value has a stable
// LLGo by-value representation that can be copied through the typed coroutine
// result slot. It does not authorize any SSA producer/consumer instruction:
// those remain governed by the physical-body allowlist and ValuePlan checks.
func validateCoroPhysicalValueType(typ types.Type, visiting map[types.Type]bool) error {
	if typ == nil {
		return fmt.Errorf("nil type")
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return nil
	}
	visiting[typ] = true
	defer delete(visiting, typ)

	switch value := typ.(type) {
	case *types.Named:
		return validateCoroPhysicalValueType(value.Underlying(), visiting)
	case *types.Basic:
		if value.Kind() == types.Invalid || value.Info()&types.IsUntyped != 0 {
			return fmt.Errorf("invalid or untyped basic kind %s", value)
		}
		return nil
	case *types.Pointer, *types.Map, *types.Chan, *types.Interface, *types.Slice, *types.Signature:
		// These are target-width opaque pointers or LLGo's stable descriptor /
		// closure aggregates. Their referent/method/call signature is logical
		// identity, not an inline extension of the transported value layout.
		return nil
	case *types.Struct:
		for i := 0; i < value.NumFields(); i++ {
			if err := validateCoroPhysicalValueType(value.Field(i).Type(), visiting); err != nil {
				return fmt.Errorf("field %d: %w", i, err)
			}
		}
		return nil
	case *types.Array:
		if value.Len() < 0 {
			return fmt.Errorf("negative array length %d", value.Len())
		}
		return validateCoroPhysicalValueType(value.Elem(), visiting)
	case *types.TypeParam:
		return fmt.Errorf("uninstantiated type parameter")
	case *types.Tuple:
		return fmt.Errorf("tuple is valid only as the outer result list")
	case *types.Union:
		return fmt.Errorf("union has no runtime value representation")
	default:
		return fmt.Errorf("unsupported type class %T", typ)
	}
}

func coroLeafABIDirective(fn *ssa.Function) string {
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return ""
	}
	for _, comment := range decl.Doc.List {
		text := strings.TrimSpace(comment.Text)
		for _, prefix := range []string{
			"//go:linkname", "//llgo:link", "// llgo:link", "//export", "//go:wasmexport", "//go:wasmimport",
		} {
			if text == prefix || strings.HasPrefix(text, prefix+" ") {
				return text
			}
		}
		if strings.HasPrefix(text, "//go:cgo_") {
			return text
		}
	}
	return ""
}

// coroRawABIDirective separates an exact source-level Go symbol alias or
// visibility declaration from a physical ABI crossing. A bodyful
// //go:linkname definition is managed when the prepared emission universe has
// either activated an exact bodyless Go declaration -> definition alias for
// the same final symbol and structural signature, retained such an exact
// pending pair from an ordinary non-metadata input, or frozen a strict
// visibility-only certificate for an unredirected two-field directive. In
// each case every in-program Go call resolves to the managed primary
// (including its $coro spelling), so publishing an unrelated legacy
// RawPlainEntry would be both unnecessary and incorrect.
//
// All exports, cgo/wasm/custom links, malformed or duplicate go:linkname text,
// and unpaired redirecting/two-argument go:linkname definitions remain
// raw/unproven boundaries. This is deliberately fail-closed for assembly or
// out-of-universe consumers.
func coroRawABIDirective(fn *ssa.Function, universe *EmissionUniverse) (string, error) {
	decl, _ := fn.Syntax().(*ast.FuncDecl)
	if decl == nil || decl.Doc == nil {
		return "", nil
	}
	managedDirective, exactManagedSyntax := attachedManagedGoLinknameDirective(decl)
	managedDefinition := false
	managedVisibility := false
	if exactManagedSyntax {
		var err error
		managedDefinition, err = universe.exactManagedGoLinknameDefinition(fn)
		if err != nil {
			return "", err
		}
		_, managedVisibility, err = universe.coroGoLinknameVisibilityCertificate(fn)
		if err != nil {
			return "", err
		}
	}
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		text := strings.TrimSpace(comment.Text)
		if text == "//go:linkname" || strings.HasPrefix(text, "//go:linkname ") {
			if exactManagedSyntax && (managedDefinition || managedVisibility) && text == managedDirective {
				continue
			}
			return text, nil
		}
		for _, prefix := range []string{
			"//llgo:link", "// llgo:link", "//export", "//go:wasmexport", "//go:wasmimport",
		} {
			if text == prefix || strings.HasPrefix(text, prefix+" ") {
				return text, nil
			}
		}
		if strings.HasPrefix(text, "//go:cgo_") {
			return text, nil
		}
	}
	return "", nil
}

func attachedManagedGoLinknameDirective(decl *ast.FuncDecl) (string, bool) {
	if decl == nil || decl.Body == nil || decl.Doc == nil || decl.Name == nil {
		return "", false
	}
	_, localName := astFuncName("", decl)
	var found string
	for _, comment := range decl.Doc.List {
		if comment == nil {
			continue
		}
		fields := strings.Fields(comment.Text)
		if len(fields) == 0 || fields[0] != "//go:linkname" {
			continue
		}
		if found != "" || len(fields) != 2 && len(fields) != 3 || fields[1] != localName {
			return "", false
		}
		found = strings.TrimSpace(comment.Text)
	}
	return found, found != ""
}

func validateCoroPhysicalConsumers(plan *coro.SSAPlan, childAwait bool) error {
	return validateCoroPhysicalConsumersCapabilities(plan, nil, childAwait, false, false)
}

func validateCoroPhysicalConsumersCapabilities(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	childAwait, staticSpawn, managedDispatch bool,
) error {
	coroutineIDs := make(map[coro.FunctionID]struct{})
	for _, function := range plan.Functions() {
		if function.Plan.Emission == coro.EmitCoroutine {
			coroutineIDs[function.Plan.ID] = struct{}{}
		}
	}
	for _, function := range plan.Functions() {
		if function.Plan.Emission != coro.EmitPlain && function.Plan.Emission != coro.EmitCoroutine {
			continue
		}
		fn := function.Function
		unevaluated, _ := universe.frozenUnsafeSizeAlignUnevaluatedSSA(fn)
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if _, omitted := unevaluated[instr]; omitted {
					continue
				}
				if store, ok := instr.(*ssa.Store); ok && plan.ElidesConditionalManagedStore(store) {
					// This occurrence is a frozen closed-cell publication whose
					// target has no live consumer. Code generation omits it before
					// resolving the otherwise non-emitted function operand.
					continue
				}
				if spawn, ok := instr.(*ssa.Go); ok {
					if !staticSpawn {
						return coroLeafInstructionError(fn, function.Plan, instr, "goroutine spawn requires scheduler root lowering")
					}
					callPlan, found := plan.CallPlan(spawn)
					if !found {
						return coroLeafInstructionError(fn, function.Plan, instr, "goroutine spawn has no compilation CallPlan")
					}
					switch callPlan.Rep {
					case coro.DirectCoro:
						if _, _, err := resolveCoroDirectStaticSpawn(plan, spawn, managedDispatch); err != nil {
							return coroLeafInstructionError(fn, function.Plan, instr, "unsupported closed static spawn: "+err.Error())
						}
					case coro.Dispatch:
						if !managedDispatch {
							return coroLeafInstructionError(fn, function.Plan, instr, "managed descriptor spawn requires the v1 descriptor dispatch capability")
						}
						if _, err := plan.ResolveManagedDispatchSpawn(spawn); err != nil {
							return coroLeafInstructionError(fn, function.Plan, instr, "unsupported managed descriptor spawn: "+err.Error())
						}
					default:
						return coroLeafInstructionError(fn, function.Plan, instr, "goroutine spawn has unsupported representation "+callPlan.Rep.String())
					}
					continue
				}
				if call, ok := instr.(ssa.CallInstruction); ok {
					if plan.ElidesCall(call) {
						continue
					}
					// SSA builtins are compiler-lowered operations, not managed
					// function consumers. AnalyzeSSA deliberately does not create
					// CallPlans for them, so keep the physical-ABI check focused on
					// every non-builtin call instruction.
					if common := call.Common(); common != nil {
						if _, builtin := common.Value.(*ssa.Builtin); builtin {
							continue
						}
						if function.Plan.Emission == coro.EmitCoroutine && common.IsInvoke() {
							if _, err := resolveCoroClosedInterfacePlainCall(plan, call); err != nil {
								if !childAwait {
									return coroLeafInstructionError(fn, function.Plan, instr, "unsupported interface invoke: "+err.Error())
								}
								if callPlan, found := plan.CallPlan(call); found && callPlan.Open &&
									callPlan.Unresolved == coro.UnknownManagedInterfaceDispatch {
									if !managedDispatch {
										return coroLeafInstructionError(fn, function.Plan, instr,
											"managed interface invoke requires the v1 descriptor dispatch capability")
									}
									if err := validateCoroManagedInterfaceDispatchCall(plan, universe, fn, call, callPlan); err != nil {
										return coroLeafInstructionError(fn, function.Plan, instr,
											"invalid managed interface call: "+err.Error())
									}
									continue
								}
								direct, ordinary := call.(*ssa.Call)
								if !ordinary {
									return coroLeafInstructionError(fn, function.Plan, instr, "coroutine interface dispatch requires an ordinary call")
								}
								dispatch, awaitErr := resolveCoroInterfaceDispatchPlan(plan, universe, direct)
								if awaitErr != nil || !coroInterfaceDispatchNeedsAwait(dispatch) {
									if awaitErr == nil {
										awaitErr = fmt.Errorf("closed interface dispatch has no coroutine target")
									}
									return coroLeafInstructionError(fn, function.Plan, instr, "unsupported interface invoke: "+awaitErr.Error())
								}
							}
						}
					}
					callPlan, found := plan.CallPlan(call)
					if !found {
						return coroLeafInstructionError(fn, function.Plan, instr, "call has no compilation CallPlan")
					}
					if callPlan.Transport == coro.RawCCodePointer {
						return coroLeafInstructionError(fn, function.Plan, instr,
							"managed coroutine raw C code-pointer call requires an explicit event, worker, or trusted inline recipe")
					}
					hasCoroutineTarget := false
					for _, target := range callPlan.Targets {
						targetFn, found := plan.Function(target)
						if !found || targetFn == nil {
							return coroLeafInstructionError(fn, function.Plan, instr, fmt.Sprintf("call target %q is absent from the compilation plan", target))
						}
						targetPlan, found := plan.FunctionPlan(targetFn)
						if !found || targetPlan.ID != target {
							return coroLeafInstructionError(fn, function.Plan, instr, fmt.Sprintf("call target %q has no canonical function plan", target))
						}
						if targetPlan.Emission == coro.EmitNone {
							return coroLeafInstructionError(fn, function.Plan, instr, fmt.Sprintf("emitted body references non-emitted call target %q", target))
						}
						if targetPlan.Emission == coro.EmitRawPlain {
							return coroLeafInstructionError(
								fn, function.Plan, instr,
								fmt.Sprintf("managed body calls raw-plain-only target %q without a managed entry", target),
							)
						}
						if _, isCoroutine := coroutineIDs[target]; isCoroutine {
							hasCoroutineTarget = true
							break
						}
					}
					if callPlan.Rep == coro.Dispatch && call.Common() != nil &&
						call.Common().StaticCallee() == nil && !call.Common().IsInvoke() {
						if deferred, cleanup := call.(*ssa.Defer); cleanup {
							if !managedDispatch {
								return coroLeafInstructionError(fn, function.Plan, instr, "managed descriptor defer requires the v1 descriptor dispatch capability")
							}
							if !childAwait || function.Plan.Emission != coro.EmitCoroutine {
								return coroLeafInstructionError(fn, function.Plan, instr, "managed descriptor defer requires coroutine child-await lowering")
							}
							if err := validateCoroManagedDispatchDefer(plan, fn, deferred, callPlan, universe); err != nil {
								return coroLeafInstructionError(fn, function.Plan, instr, "invalid managed descriptor defer: "+err.Error())
							}
							if _, _, kind, err := resolveCoroStaticCleanupTarget(plan, function.Plan, deferred, universe); err != nil || kind != coroStaticCleanupDispatch {
								if err == nil {
									err = fmt.Errorf("resolved cleanup kind is %d", kind)
								}
								return coroLeafInstructionError(fn, function.Plan, instr, "invalid managed descriptor cleanup plan: "+err.Error())
							}
							continue
						}
						if callPlan.SyncDispatch {
							if !managedDispatch {
								return coroLeafInstructionError(fn, function.Plan, instr, "synchronous descriptor call requires the v1 plain dispatch capability")
							}
							if err := validateCoroPlainDispatchCall(plan, fn, call, callPlan, universe); err != nil {
								return coroLeafInstructionError(fn, function.Plan, instr, "invalid synchronous descriptor call: "+err.Error())
							}
							continue
						}
						if callPlan.Open && callPlan.Unresolved != coro.UnknownManagedDispatch {
							return coroLeafInstructionError(fn, function.Plan, instr, fmt.Sprintf(
								"open descriptor call has uncertified execution domain %v", callPlan.Unresolved,
							))
						}
						if !managedDispatch {
							return coroLeafInstructionError(fn, function.Plan, instr, "open managed descriptor call requires the v1 descriptor dispatch capability")
						}
						if !childAwait || function.Plan.Emission != coro.EmitCoroutine {
							return coroLeafInstructionError(fn, function.Plan, instr, "open managed descriptor call requires coroutine child-await lowering")
						}
						if err := validateCoroManagedDispatchCall(plan, fn, call, callPlan, universe); err != nil {
							return coroLeafInstructionError(fn, function.Plan, instr, "invalid managed descriptor call: "+err.Error())
						}
						continue
					}
					if hasCoroutineTarget {
						if childAwait && function.Plan.Emission == coro.EmitCoroutine && call.Common() != nil && call.Common().IsInvoke() {
							if direct, ok := call.(*ssa.Call); ok {
								if dispatch, err := resolveCoroInterfaceDispatchPlan(plan, universe, direct); err == nil && coroInterfaceDispatchNeedsAwait(dispatch) {
									continue
								}
							}
						}
						direct, ordinary := call.(*ssa.Call)
						if childAwait && ordinary && function.Plan.Emission == coro.EmitCoroutine {
							if _, _, err := resolveCoroStaticAwait(plan, function.Plan, direct, universe); err == nil {
								// The static callee operand is represented by this exact
								// CallPlan and is not an escaped function value.
								continue
							}
						}
						deferred, cleanup := call.(*ssa.Defer)
						if childAwait && cleanup && function.Plan.Emission == coro.EmitCoroutine {
							if _, _, kind, err := resolveCoroStaticCleanupTarget(plan, function.Plan, deferred, universe); err == nil && kind == coroStaticCleanupCoroutine {
								// The physical-body preflight separately proves the
								// frame-resident record and child no-unwind contract.
								continue
							}
						}
						return coroLeafInstructionError(fn, function.Plan, instr, "coroutine target requires a supported static child await or root lowering")
					}
				}
				for _, operand := range instr.Operands(nil) {
					if operand == nil || *operand == nil {
						continue
					}
					target, ok := (*operand).(*ssa.Function)
					if !ok {
						continue
					}
					targetPlan, planned := plan.FunctionPlan(target)
					if planned && targetPlan.Emission == coro.EmitNone {
						return coroLeafInstructionError(fn, function.Plan, instr, fmt.Sprintf("emitted body references non-emitted function value %q", targetPlan.ID))
					}
					if planned && targetPlan.Emission == coro.EmitCoroutine {
						if managedDispatch && targetPlan.FuncRep == coro.Dispatch {
							// The universal descriptor producer converts this exact
							// function reference. Value/consumer validation below owns
							// the rest of the two-pointer transport proof.
							continue
						}
						if closure, exactClosure := instr.(*ssa.MakeClosure); exactClosure && closure.Fn == target &&
							childAwait && function.Plan.Emission == coro.EmitCoroutine && len(target.FreeVars) != 0 &&
							targetPlan.Primary == coro.PrimaryCoroutine && targetPlan.FuncRep == coro.DirectCoro {
							value, exactValue := plan.ValuePlan(closure)
							if exactValue && len(value.Funcs) == 1 && len(value.Funcs[0].Path) == 0 &&
								value.Funcs[0].Rep == coro.DirectCoro && !value.Funcs[0].MayBeNil &&
								len(value.Funcs[0].Targets) == 1 && value.Funcs[0].Targets[0] == targetPlan.ID {
								// compileValue retags the physical (g,out,ctx,args)
								// entry solely as a canonical (ctx,args) closure carrier;
								// the exact static await consumes its env word and never
								// calls through that temporary code word.
								continue
							}
						}
						return coroLeafInstructionError(fn, function.Plan, instr, "coroutine function value requires physical representation conversion")
					}
				}
			}
		}
	}
	return nil
}

func coroDispatchCallHasCoroutineTarget(plan *coro.SSAPlan, call coro.SSACallPlan) bool {
	if plan == nil {
		return false
	}
	for _, id := range call.Targets {
		target, ok := plan.Function(id)
		if !ok || target == nil {
			continue
		}
		targetPlan, ok := plan.FunctionPlan(target)
		if ok && targetPlan.Emission == coro.EmitCoroutine {
			return true
		}
	}
	return false
}

func coroLeafScalar(typ types.Type) bool {
	basic, ok := typ.Underlying().(*types.Basic)
	if !ok || basic.Kind() == types.Uintptr {
		return false
	}
	info := basic.Info()
	return info&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

func coroLeafInstructionError(fn *ssa.Function, plan coro.FunctionPlan, instr ssa.Instruction, reason string) error {
	pos := fn.Prog.Fset.Position(instr.Pos())
	where := "unknown position"
	if pos.IsValid() {
		where = fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column)
	}
	name := "<nil>"
	if fn != nil {
		name = fn.String()
	}
	operation := coroInstructionOperation(instr)
	return fmt.Errorf("coroutine physical ABI: function %q (%s): %T%s at %s: %s", plan.ID, name, instr, operation, where, reason)
}

func coroInstructionOperation(instr ssa.Instruction) (operation string) {
	// Tests and validation adapters may construct partially attached SSA
	// instructions. x/tools String methods assume both a complete parent and
	// complete operands, so diagnostics must tolerate either being absent.
	if instr == nil || instr.Block() == nil || instr.Block().Parent() == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			operation = ""
		}
	}()
	return fmt.Sprintf(" %q", instr.String())
}
