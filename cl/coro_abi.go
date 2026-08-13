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
	"sort"
	"strings"

	"github.com/goplus/llgo/cl/blocks"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroSyntheticSelectNoCaseMessage = "blocking select matched no case"

func coroDemandReferenceTrace(universe *EmissionUniverse, target *ssa.Function) string {
	if universe == nil || target == nil {
		return "unavailable"
	}
	var sources []string
	for _, owner := range universe.Functions() {
		if owner == nil {
			continue
		}
		managed, managedErr := universe.CoroDemandReferences(owner)
		synchronous, syncErr := universe.CoroSyncDemandReferences(owner)
		if managedErr != nil || syncErr != nil {
			continue
		}
		contains := func(functions []*ssa.Function) bool {
			for _, function := range functions {
				if function == target {
					return true
				}
			}
			return false
		}
		if contains(managed) || contains(synchronous) {
			sources = append(sources, fmt.Sprintf(
				"%s(managed-reference=%t sync-reference=%t)",
				owner.String(), contains(managed), contains(synchronous),
			))
		}
	}
	if len(sources) == 0 {
		return "unavailable"
	}
	sort.Strings(sources)
	if len(sources) > 16 {
		sources = append(sources[:16], fmt.Sprintf("... %d more", len(sources)-16))
	}
	return strings.Join(sources, "; ")
}

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

	coroPhysicalABIVersionV1           uint32 = 1
	coroFrameAllocHookV1                      = "__llgo_coro_frame_alloc_v1"
	coroFramePublishHookV1                    = "__llgo_coro_frame_publish_v1"
	coroFramePublishHookV2                    = "__llgo_coro_frame_publish_v2"
	coroFramePublishHookV3                    = "__llgo_coro_frame_publish_v3"
	coroAwaitPrepareHookV1                    = "__llgo_coro_await_prepare_v3"
	coroAwaitInlineHookV1                     = "__llgo_coro_await_inline_v1"
	coroAwaitInlineBeginHookV2                = "__llgo_coro_await_inline_begin_v2"
	coroAwaitInlineFinishHookV2               = "__llgo_coro_await_inline_finish_v2"
	coroAwaitInlineDestroyCommitHookV2        = "__llgo_coro_await_inline_destroy_commit_v2"
	coroFrameDestroyCommitHookV2              = "__llgo_coro_frame_destroy_commit_v2"
	coroAwaitConsumeHookV1                    = "__llgo_coro_await_consume_v1"
	coroPreemptPollHookV1                     = "__llgo_coro_preempt_poll_v1"
	coroYieldPrepareHookV1                    = "__llgo_coro_yield_prepare_v1"
	coroCriticalEnterHookV1                   = "__llgo_coro_critical_enter_v1"
	coroCriticalExitHookV1                    = "__llgo_coro_critical_exit_v1"
	coroKeyedParkHookV2                       = "__llgo_coro_keyed_park_v2"
	coroKeyedResumeHookV2                     = "__llgo_coro_keyed_resume_v2"
	coroRunDecisionTakeHookV1                 = "__llgo_coro_run_decision_take_v1"
	coroRunDecisionTakeZeroHookV1             = "__llgo_coro_run_decision_take_zero_v1"
	coroPanicPrepareHookV1                    = "__llgo_coro_panic_prepare_v1"
	coroPanicTraceReplaceHookV1               = "__llgo_coro_panic_trace_replace_v1"
	coroRecoverTakeHookV1                     = "__llgo_coro_recover_take_v1"
	coroSpawnBeginHookV1                      = "__llgo_coro_spawn_begin_v1"
	coroSpawnCommitHookV1                     = "__llgo_coro_spawn_commit_v1"
	coroCompletePrepareHookV2                 = "__llgo_coro_complete_prepare_v2"
	coroFrameFreeHookV1                       = "__llgo_coro_frame_free_v1"
	coroDescriptorPrefixV1                    = "__llgo_coro_frame_descriptor_v1."
	coroBorrowedFrameMetadataWordsV2          = 20
)

const (
	coroKeyedResumeSuccessV2 uint64 = iota + 1
	coroKeyedResumeTaskAbortV2
	coroKeyedResumeShutdownV2
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
	coroHeaderLine
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

// coroPreemptCheckpointStride is the number of compiler-selected source
// safepoints between full runtime polls. The countdown lives in compiler-local
// storage whose address never escapes; the ordinary SROA/mem2reg pipeline can
// therefore keep it in SSA registers on a non-suspending loop edge. Every
// activation resets it, so it is not live across a scheduler-visible suspend.
// StateID remains exclusively the published resume-state identity.
const coroPreemptCheckpointStride uint64 = 2048

type coroPhysicalABI struct {
	version                 uint32
	hash                    [16]byte
	descriptorName          string
	traceFunction           string
	traceFile               string
	frameAllocHook          string
	frameFreeHook           string
	framePublishHook        string
	awaitPrepareHook        string
	awaitInlineHook         string
	awaitInlineFinishHook   string
	awaitInlineCommitHook   string
	frameDestroyCommitHook  string
	awaitConsumeHook        string
	preemptPollHook         string
	yieldPrepareHook        string
	criticalEnterHook       string
	criticalExitHook        string
	runDecisionTakeHook     string
	runDecisionTakeZeroHook string
	panicPrepareHook        string
	panicTraceReplaceHook   string
	recoverTakeHook         string
	completePrepareHook     string
	physicalSig             *types.Signature
	hasEnv                  bool
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
	externalCleanup        *coroStaticCleanupState
	header                 llssa.Expr
	task                   llssa.Expr
	resultSlot             llssa.Expr
	completion             llssa.BasicBlock
	finalSuspend           llssa.BasicBlock
	preemptPoll            llssa.Expr
	yieldPrepare           llssa.Expr
	criticalEnter          llssa.Expr
	criticalExit           llssa.Expr
	runDecisionTakeZero    llssa.Expr
	runDecisionTrap        llssa.Expr
	unsupportedRunDecision llssa.BasicBlock
	cancelRunDecision      llssa.BasicBlock
	abortRunDecision       llssa.BasicBlock
	shutdownRunDecision    llssa.BasicBlock
	panicPrepare           llssa.Expr
	panicTraceReplace      llssa.Expr
	completePrepare        llssa.Expr
	terminalStatus         llssa.Expr
	preemptCountdown       llssa.Expr
	nextState              uint32
	terminalState          uint32
	needsPreempt           bool
	instructions           int
	frameRetention         *coroFrameRetentionProof
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
	hasEnv := false
	if entry.function != nil {
		hidden := sourceSig.Params().Len() - len(entry.function.Params)
		if hidden < 0 || hidden > 1 {
			panic(fmt.Sprintf(
				"coroutine physical ABI: function %q source parameters=%d disagree with SSA parameters=%d",
				entry.function.String(), sourceSig.Params().Len(), len(entry.function.Params),
			))
		}
		hasEnv = hidden == 1
	}
	version := coroPhysicalABIVersionV1
	frameAllocHook := coroFrameAllocHookV1
	frameFreeHook := coroFrameFreeHookV1
	descriptorPrefix := coroDescriptorPrefixV1
	framePublishHook := coroFramePublishHookV3
	awaitPrepareHook := coroAwaitPrepareHookV1
	awaitInlineHook := coroAwaitInlineBeginHookV2
	awaitInlineFinishHook := coroAwaitInlineFinishHookV2
	awaitInlineCommitHook := coroAwaitInlineDestroyCommitHookV2
	frameDestroyCommitHook := coroFrameDestroyCommitHookV2
	awaitConsumeHook := coroAwaitConsumeHookV1
	preemptPollHook := coroPreemptPollHookV1
	yieldPrepareHook := coroYieldPrepareHookV1
	criticalEnterHook := coroCriticalEnterHookV1
	criticalExitHook := coroCriticalExitHookV1
	runDecisionTakeHook := coroRunDecisionTakeHookV1
	runDecisionTakeZeroHook := coroRunDecisionTakeZeroHookV1
	panicPrepareHook := coroPanicPrepareHookV1
	panicTraceReplaceHook := coroPanicTraceReplaceHookV1
	recoverTakeHook := coroRecoverTakeHookV1
	faultPrepareHook := coroFaultPrepareHookV1
	faultPayloadHook := coroFaultPayloadHookV1
	faultPrepareArgsHook := coroFaultPrepareHookV2
	faultPayloadArgsHook := coroFaultPayloadHookV2
	completePrepareHook := coroCompletePrepareHookV2
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
	traceFunction := ""
	traceFile := ""
	if entry.function != nil {
		if entry.function.Pkg != nil && entry.function.Pkg.Pkg != nil {
			traceFunction = runtimeFrameName(
				funcName(entry.function.Pkg.Pkg, entry.function, false),
			)
		}
		if p.fset != nil {
			traceFile = p.fset.Position(entry.function.Pos()).Filename
		}
	}
	if traceFunction == "" {
		traceFunction = p.runtimeCallerFrameName()
	}
	if traceFunction == "" {
		traceFunction = entry.name
	}
	coroABI := coro.PhysicalABIV1
	schedulerABI := coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	panicABI := coro.PanicExplicitStatusABIV0
	funcRepABI := coro.FuncRepABIV1
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
		"llgo-coro-physical-v%d\x00%s\x00trace-function=%s\x00trace-file=%s\x00coro=%s\x00scheduler=%s\x00panic=%s\x00panic-hook=%s\x00panic-trace-replace=%s\x00recover-take=%s\x00fault-hook=%s\x00fault-payload-hook=%s\x00fault-args-hook=%s\x00fault-args-payload-hook=%s\x00fault-args-abi=x64-yword-v2\x00func-rep=%s\x00frame-publish=%s\x00await-prepare=%s\x00await-inline=%s\x00await-inline-finish=%s\x00await-inline-commit=%s\x00frame-destroy-commit=%s\x00await-consume=%s\x00resume-decision=%s\x00resume-decision-zero=%s\x00critical-enter=%s\x00critical-exit=%s\x00preempt-stride=%d\x00os-thread-lock=%s\x00os-thread-unlock=%s\x00triple=%s\x00cpu=%s\x00features=%s\x00target-abi=%s\x00data-layout=%s\x00ptr=%d\x00sig=%s\x00result=%s",
		version,
		entry.plan.ID,
		traceFunction,
		traceFile,
		coroABI,
		schedulerABI,
		panicABI,
		panicPrepareHook,
		panicTraceReplaceHook,
		recoverTakeHook,
		faultPrepareHook,
		faultPayloadHook,
		faultPrepareArgsHook,
		faultPayloadArgsHook,
		funcRepABI,
		framePublishHook,
		awaitPrepareHook,
		awaitInlineHook,
		awaitInlineFinishHook,
		awaitInlineCommitHook,
		frameDestroyCommitHook,
		awaitConsumeHook,
		runDecisionTakeHook,
		runDecisionTakeZeroHook,
		criticalEnterHook,
		criticalExitHook,
		coroPreemptCheckpointStride,
		coroOSThreadLockHookV1,
		coroOSThreadUnlockHookV1,
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
		traceFunction:           traceFunction,
		traceFile:               traceFile,
		frameAllocHook:          frameAllocHook,
		frameFreeHook:           frameFreeHook,
		framePublishHook:        framePublishHook,
		awaitPrepareHook:        awaitPrepareHook,
		awaitInlineHook:         awaitInlineHook,
		awaitInlineFinishHook:   awaitInlineFinishHook,
		awaitInlineCommitHook:   awaitInlineCommitHook,
		frameDestroyCommitHook:  frameDestroyCommitHook,
		awaitConsumeHook:        awaitConsumeHook,
		preemptPollHook:         preemptPollHook,
		yieldPrepareHook:        yieldPrepareHook,
		criticalEnterHook:       criticalEnterHook,
		criticalExitHook:        criticalExitHook,
		runDecisionTakeHook:     runDecisionTakeHook,
		runDecisionTakeZeroHook: runDecisionTakeZeroHook,
		panicPrepareHook:        panicPrepareHook,
		panicTraceReplaceHook:   panicTraceReplaceHook,
		recoverTakeHook:         recoverTakeHook,
		completePrepareHook:     completePrepareHook,
		physicalSig:             physicalSig,
		hasEnv:                  hasEnv,
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
		prog.Uint32(),  // source line
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
		Version:  abi.version,
		ABIHash:  abi.hash,
		Result:   resultType,
		Function: abi.traceFunction,
		File:     abi.traceFile,
	})
	descriptorPtr := b.Convert(prog.VoidPtr(), descriptor)
	task := p.fn.PhysicalParam(0)
	resultSlot := p.fn.PhysicalParam(1)
	headerType := coroHeaderType(prog)
	header := b.AllocaT(headerType)
	borrowedFrameMetadataType := p.type_(
		types.NewArray(types.Typ[types.Uintptr], coroBorrowedFrameMetadataWordsV2),
		llssa.InGo,
	)
	// Dynamic ramps never consume this fallback storage. Leave it uninitialized
	// here so every ordinary coroutine creation does not pay a 20-word memset;
	// PublishFrameV2 initializes the complete private Frame only when LLVM has
	// actually selected the allocation-elided path (storage == nil).
	borrowedFrameMetadata := b.AllocaT(borrowedFrameMetadataType)
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
				// The fake use is removed after CoroSplit. Until then it makes the
				// compiler-injected scheduler metadata live through final cleanup,
				// so an elided child owns it inside its static parent's LLVM frame.
				b.KeepAlive(borrowedFrameMetadata)
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
		// This address is compiler-private and never reaches a runtime call. It
		// deliberately differs from Header.StateID: that externally visible
		// field aliases runtime validation calls and therefore forces a
		// load/store on every otherwise plain loop edge.
		body.preemptCountdown = b.AllocaT(prog.Uint32())
	}
	if abi.runDecisionTakeZeroHook != "" {
		body.runDecisionTakeZero = p.pkg.NewFunc(
			abi.runDecisionTakeZeroHook, coroRunDecisionTakeZeroSignature(), llssa.InC,
		).Expr
		if p.compilation != nil {
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
	if abi.panicPrepareHook != "" {
		body.panicPrepare = p.pkg.NewFunc(abi.panicPrepareHook, coroPanicPrepareSignature(), llssa.InC).Expr
	}
	if abi.panicTraceReplaceHook != "" {
		body.panicTraceReplace = p.pkg.NewFunc(
			abi.panicTraceReplaceHook, coroPanicTraceReplaceSignature(), llssa.InC,
		).Expr
	}
	if abi.preemptPollHook != "" {
		body.preemptPoll = p.pkg.NewFunc(abi.preemptPollHook, coroPreemptPollSignature(), llssa.InC).Expr
	}
	coroOptions := llssa.CoroOptions{
		Promise: header,
		Frame:   frame,
		BeforeInitialSuspend: func(b llssa.Builder, handle, storage llssa.Expr) {
			if abi.framePublishHook != "" {
				publish := p.pkg.NewFunc(abi.framePublishHook, coroFramePublishSignature(), llssa.InC)
				b.Call(
					publish.Expr,
					task,
					handle,
					b.Convert(prog.VoidPtr(), header),
					storage,
					b.Convert(prog.VoidPtr(), borrowedFrameMetadata),
					descriptorPtr,
					resultSlot,
				)
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
		coroOptions.AfterResumeDispatch = func(b llssa.Builder, normal llssa.BasicBlock) {
			// Publication lets the scheduler retain this opaque address after the
			// ramp returns. Keep one resumed use until CoroSplit has physically
			// placed the storage in the LLVM frame; the post-split cleanup removes
			// the fake use before instruction selection.
			b.KeepAlive(borrowedFrameMetadata)
			body.dispatchZeroRunDecision(b, normal)
		}
	} else {
		coroOptions.AfterResume = func(b llssa.Builder) {
			b.KeepAlive(borrowedFrameMetadata)
		}
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
		types.NewParam(token.NoPos, nil, "metadata", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "descriptor", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "resultSlot", types.Typ[types.UnsafePointer]),
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

func coroAwaitInlineSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "parent", pointer),
		types.NewParam(token.NoPos, nil, "child", pointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "completed", types.Typ[types.Bool]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroAwaitInlineFinishSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "parent", pointer),
		types.NewParam(token.NoPos, nil, "child", pointer),
		types.NewParam(token.NoPos, nil, "done", types.Typ[types.Bool]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "destroy", types.Typ[types.Bool]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroAwaitInlineCommitSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "parent", pointer),
		types.NewParam(token.NoPos, nil, "child", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroFrameDestroyCommitSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
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

func coroKeyedParkSignatureV2() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "state", types.Typ[types.UnsafePointer]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroKeyedResumeSignatureV2() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "state", types.Typ[types.UnsafePointer]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
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

func coroPanicTraceReplaceSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func (c *coroBodyContext) publishState(
	b llssa.Builder,
	reason, lifecycle uint64,
	stateID, line uint32,
) {
	prog := b.Prog
	b.Store(b.FieldAddr(c.header, coroHeaderSuspendReason), prog.IntVal(reason, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderLifecycle), prog.IntVal(lifecycle, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderStateID), prog.IntVal(uint64(stateID), prog.Uint32()))
	b.Store(b.FieldAddr(c.header, coroHeaderLine), prog.IntVal(uint64(line), prog.Uint32()))
}

func (c *coroBodyContext) activate(b llssa.Builder) {
	if c.abi.version < coroPhysicalABIVersionV1 {
		return
	}
	prog := b.Prog
	b.Store(b.FieldAddr(c.header, coroHeaderSuspendReason), prog.IntVal(coroSuspendNone, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderLifecycle), prog.IntVal(coroLifecycleActive, prog.Uint16()))
	if c.preemptCountdown.IsNil() {
		panic("coroutine activation has no private preemption countdown")
	}
	b.Store(c.preemptCountdown, prog.IntVal(coroPreemptCheckpointStride, prog.Uint32()))
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
	// Ordinary execution overwhelmingly has no cancellation request. Besides
	// describing the scheduler's expected path, this keeps a resumed static
	// child call above LLVM 22 CoroAnnotationElide's default 55% block-frequency
	// threshold; an unannotated binary branch would make the normal arm look
	// artificially cold even though cancellation remains fully reachable.
	b.IfWithBranchWeights(isCanceled, canceled, normal, 1, 1000)
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

func (c *coroBodyContext) suspendForChild(b llssa.Builder, line uint32) uint32 {
	if c.abi.version < coroPhysicalABIVersionV1 {
		panic("coroutine child suspension requires PhysicalABIV1")
	}
	stateID := c.nextState
	c.nextState++
	c.instructions = 0
	c.publishState(b, coroSuspendCall, coroLifecycleSuspended, stateID, line)
	return stateID
}

func (c *coroBodyContext) pollAndSuspendForPreempt(b llssa.Builder) uint32 {
	if c.abi.version < coroPhysicalABIVersionV1 || c.preemptPoll.IsNil() || c.yieldPrepare.IsNil() {
		panic("coroutine preemption requires PhysicalABIV1 poll and scheduler handoff hooks")
	}
	if c.preemptCountdown.IsNil() {
		panic("coroutine preemption has no private countdown")
	}
	remaining := b.Load(c.preemptCountdown)
	one := b.Prog.IntVal(1, b.Prog.Uint32())
	b.Store(c.preemptCountdown, b.BinOp(token.SUB, remaining, one))
	due := b.BinOp(token.LEQ, remaining, one)
	stateID := c.nextState
	// Only the stride boundary reaches a potentially suspending block. The hot
	// edge performs one frame-local decrement and no call, so CoroSplit/O2 can
	// retain loop-carried values in registers between full runtime polls.
	b.IfThen(due, func() {
		stateID = c.suspendCurrentFrameIfYieldRequested(b, b.Call(c.preemptPoll, c.task))
	})
	return stateID
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
		c.publishState(suspend, coroSuspendYield, coroLifecycleSuspended, stateID, 0)
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
	c.publishState(b, coroSuspendYield, coroLifecycleSuspended, stateID, 0)
	b.Call(c.yieldPrepare, c.task, c.coro.Handle(), b.Convert(b.Prog.VoidPtr(), c.header))
	c.coro.SuspendCurrentBlock()
	c.activate(b)
	return stateID
}

func (p *context) compileCoroPark(b llssa.Builder, args []llssa.Expr) {
	body := p.requireCoroParkV2Body(b, "keyed wait")
	if b.Func != p.fn || len(args) != 2 {
		panic("llgo.coroPark requires exactly (state, reserved) in the active coroutine function")
	}
	state := b.Convert(b.Prog.VoidPtr(), args[0])
	body.emitCoroParkOperation(p, b, coroParkOperation{
		shouldSuspend: b.Prog.BoolVal(true),
		park: func(suspend llssa.Builder) {
			park := p.pkg.NewFunc(coroKeyedParkHookV2, coroKeyedParkSignatureV2(), llssa.InC)
			suspend.Call(
				park.Expr,
				body.task,
				body.coro.Handle(),
				suspend.Convert(suspend.Prog.VoidPtr(), body.header),
				state,
			)
		},
		resume: func(resume llssa.Builder) llssa.Expr {
			resumeHook := p.pkg.NewFunc(coroKeyedResumeHookV2, coroKeyedResumeSignatureV2(), llssa.InC)
			return resume.Call(resumeHook.Expr, body.task, state)
		},
		normal:   []uint64{coroKeyedResumeSuccessV2},
		abort:    coroKeyedResumeTaskAbortV2,
		shutdown: coroKeyedResumeShutdownV2,
	})
}

func (p *context) compileCoroYield(b llssa.Builder) {
	// A frozen raw/plain variant has no scheduler-owned current G to hand off.
	// Its raw-closure preflight independently proves that the body contains no
	// real park or event wait, so retain the legacy synchronous spin semantics
	// by treating this cooperative managed yield as a no-op.
	if p.rawPlainBody {
		return
	}
	body := p.coroBody()
	if body == nil || p.compilation == nil {
		owner := "<unknown>"
		if p.goFn != nil {
			owner = p.goFn.String()
		}
		panic(fmt.Sprintf("llgo.coroYield in %q (raw-plain=%t) requires an active PhysicalABIV1 coroutine body", owner, p.rawPlainBody))
	}
	if b.Func != p.fn {
		panic("llgo.coroYield requires the active coroutine function")
	}
	body.yieldCurrentFrame(b)
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
	c.publishState(b, coroSuspendFrameComplete, coroLifecycleFinalSuspended, c.terminalStateID(), 0)
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

func (c *coroBodyContext) panic(b llssa.Builder, typeWord, dataWord llssa.Expr, line uint32) {
	c.panicWithLine(
		b,
		typeWord,
		dataWord,
		b.Prog.IntVal(uint64(line), b.Prog.Uint32()),
	)
}

func (c *coroBodyContext) panicWithLine(
	b llssa.Builder,
	typeWord, dataWord, line llssa.Expr,
) {
	if c.abi.version < coroPhysicalABIVersionV1 || c.panicPrepare.IsNil() || c.finalSuspend == nil {
		panic("explicit-status panic requires a PhysicalABIV1 prepare hook and shared final suspend")
	}
	if line.IsNil() || line.Type != b.Prog.Uint32() {
		panic("explicit-status panic requires a uint32 source line")
	}
	prog := b.Prog
	b.Store(b.FieldAddr(c.header, coroHeaderSuspendReason), prog.IntVal(coroSuspendPanic, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderLifecycle), prog.IntVal(coroLifecycleFinalSuspended, prog.Uint16()))
	b.Store(b.FieldAddr(c.header, coroHeaderStateID), prog.IntVal(uint64(c.terminalStateID()), prog.Uint32()))
	b.Store(b.FieldAddr(c.header, coroHeaderLine), line)
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
	// A Go spawn discards source results and therefore passes a nil result
	// sink. Ordinary awaits and explicit roots pass typed storage. Keep one
	// physical coroutine entry for both contexts and branch only at terminal
	// result publication; the child frame and scheduler lifetime never depend
	// on caller-owned discard storage.
	hasResultSink := b.BinOp(token.NEQ, resultSlot, p.prog.Nil(p.prog.VoidPtr()))
	b.IfThen(hasResultSink, func() {
		resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
		typedSlot := b.Convert(p.prog.Pointer(resultType), resultSlot)
		for i, result := range results {
			b.Store(b.FieldAddr(typedSlot, i), result)
		}
	})
}

func (p *context) compileCoroPhysicalBody(b llssa.Builder, fn *ssa.Function, abi coroPhysicalABI, isInit bool) {
	sourceParamBase := 2
	if abi.hasEnv {
		// Environment-bearing entries are (g,out,env,args...). The environment
		// is an explicit physical parameter rather than Function's legacy
		// implicit context, so SSA source parameters begin after all three words.
		sourceParamBase = 3
	}

	if p.emissionUniverse == nil || p.emissionUniverse.coroProgramIR == nil {
		panic("coroutine physical body has no ProgramIR")
	}
	physicalPlan, err := (emissionCanonicalIndex{universe: p.emissionUniverse}).physicalFunctionPlanForEmission(fn, p.emissionOwner)
	if err != nil {
		panic(fmt.Errorf("load frozen coroutine physical plan: %w", err))
	}
	frameRetention := physicalPlan.frameRetention
	critical := physicalPlan.critical
	cleanupPlan := physicalPlan.cleanup
	emission, finishEmission := p.beginCoroManagedPhysicalEmission(
		physicalPlan, sourceParamBase, abi.panicPrepareHook != "",
	)
	defer finishEmission()

	b.SetBlock(p.fn.Block(0))
	preparedCleanup := p.beginCoroStaticCleanup(b, cleanupPlan)
	var cleanup, externalCleanup *coroStaticCleanupState
	if cleanupPlan != nil && cleanupPlan.external {
		externalCleanup = preparedCleanup
	} else {
		cleanup = preparedCleanup
	}
	terminalResultAllocations := []*ssa.Alloc(nil)
	if cleanupPlan != nil {
		terminalResultAllocations = cleanupPlan.terminalResultAllocations
	}
	if !coroTerminalResultAllocationSetMatches(frameRetention, terminalResultAllocations) {
		panic("coroutine cleanup plan and frame-retention proof disagree on terminal-result allocations")
	}
	physical := p.beginCoroBody(b, abi, terminalResultAllocations)
	physical.frameRetention = frameRetention
	physical.critical = critical
	physical.cleanup = cleanup
	physical.externalCleanup = externalCleanup
	if physical.cleanup != nil {
		physical.cleanup.bindBlocks(p.fn)
	}

	// Create source blocks after BeginCoro's canonical ramp/suspend blocks so
	// presplit IR remains in execution order for LLVM diagnostics and ABI tests.
	sourceBlocks := make([]llssa.BasicBlock, len(fn.Blocks))
	for i := range sourceBlocks {
		sourceBlocks[i] = p.fn.MakeBlock()
	}
	physical.completion = p.fn.MakeBlock()
	physical.finalSuspend = p.fn.MakeBlock()
	physical.bindCancellationCompletion(b)
	bodyCapability := newCoroPhysicalBodyCapability(physical)
	emission.bindManagedPhysicalBody(bodyCapability, sourceBlocks)
	b.SetBlock(physical.coro.InitialResumeBlock())
	physical.activate(b)
	b.Jump(sourceBlocks[0])

	cgoFrontend := isCgoExternSymbol(fn)
	off := make([]int, len(fn.Blocks))
	if !cgoFrontend {
		for i, block := range fn.Blocks {
			off[i] = p.compilePhis(b, block)
		}
	}
	p.blkInfos = blocks.Infos(fn.Blocks)
	physical.needsPreempt = physicalPlan.needsPreempt

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
		pollAtEntry := physical.needsPreempt && entryDepth == 0
		if physicalPlan.preempt != nil {
			pollAtEntry = physicalPlan.preempt.pollsAtBlock(block)
		}
		if pollAtEntry {
			physical.instructions = 0
			// Block zero bounds immediately resumed static child chains. The
			// frozen graph plan selects a feedback set plus bounded acyclic
			// path cuts; structured-critical functions retain the conservative
			// depth-aware every-block fallback.
			b.SetBlock(p.sourceBlock(i))
			physical.pollAndSuspendForPreempt(b)
			physical.sourceBlockPollFresh = true
		}
		doModInit := i == 1 && isInit
		p.compileBlock(b, block, off[i], doModInit)
		if cgoFrontend {
			// Generated cgo adapters have a dedicated first-block frontend
			// recipe. Their remaining Go SSA models errno/interface control
			// flow that compileBlock reconstructs explicitly from p.cgoRet and
			// p.cgoErrno, exactly as in the plain compiler.
			for index := 1; index < len(sourceBlocks); index++ {
				b.SetBlock(sourceBlocks[index])
				b.Unreachable()
			}
			break
		}
		if i = p.blkInfos[i].Next; i < 0 {
			break
		}
	}
	for _, phi := range p.phis {
		phi()
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
		physical.panicWithLine(
			b,
			b.Load(physical.cleanup.panicType),
			b.Load(physical.cleanup.panicData),
			b.Load(physical.cleanup.panicLine),
		)
	}
	b.SetBlock(physical.finalSuspend)
	physical.finish(b)
	emission.completeManagedPhysicalBody(bodyCapability)
}

// validateCoroExactSyntheticForwarder proves the complete SSA body shared by
// compiler-owned spawn carriers. The caller remains responsible for proving
// why this exact value may be wrapped; this helper proves only that the
// synthetic function is a context-free, argument-identical forwarder with no
// hidden instructions before its terminal return.
func validateCoroExactSyntheticForwarder(fn *ssa.Function, value ssa.Value) (*ssa.Call, error) {
	if fn == nil || value == nil || fn.Signature == nil || len(fn.Blocks) != 1 || len(fn.Blocks[0].Succs) != 0 {
		return nil, fmt.Errorf("requires one exact single-block forwarding body")
	}
	instructions := fn.Blocks[0].Instrs
	if len(instructions) < 2 {
		return nil, fmt.Errorf("has no forwarding call and terminal return")
	}
	forward, ok := instructions[0].(*ssa.Call)
	if !ok || forward.Common() == nil || forward.Common().Value != value ||
		forward.Common().IsInvoke() || forward.Common().Method != nil ||
		len(forward.Common().Args) != len(fn.Params) {
		return nil, fmt.Errorf("has no exact forwarding call")
	}
	for index, argument := range forward.Common().Args {
		if argument != fn.Params[index] {
			return nil, fmt.Errorf("forwarding argument %d is not its exact parameter", index)
		}
	}
	resultCount := fn.Signature.Results().Len()
	returned := make([]ssa.Value, 0, resultCount)
	cursor := 1
	switch resultCount {
	case 0:
	case 1:
		returned = append(returned, forward)
	default:
		for index := 0; index < resultCount; index++ {
			if cursor >= len(instructions)-1 {
				return nil, fmt.Errorf("omits result extract %d", index)
			}
			extract, ok := instructions[cursor].(*ssa.Extract)
			if !ok || extract.Tuple != forward || extract.Index != index {
				return nil, fmt.Errorf("result extract %d is not exact", index)
			}
			returned = append(returned, extract)
			cursor++
		}
	}
	if cursor != len(instructions)-1 {
		return nil, fmt.Errorf("contains an extra forwarding instruction")
	}
	ret, ok := instructions[cursor].(*ssa.Return)
	if !ok || len(ret.Results) != len(returned) {
		return nil, fmt.Errorf("has no exact terminal return")
	}
	for index := range returned {
		if ret.Results[index] != returned[index] {
			return nil, fmt.Errorf("return value %d is not exact", index)
		}
	}
	return forward, nil
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
		frameRetentionABI, channel, managedDispatch, rawMethodToken,
		nil, nil, nil, nil,
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
	interfacePlain *coroClosedInterfacePlainPlan,
	managedInterface *coroManagedInterfaceDispatchPlan,
	libraryForeign map[*ssa.Function]coro.LibraryEffectForeignCallable,
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
		audit.libraryForeign = libraryForeign
		critical, err := proveCoroCriticalRegions(universe, whole, audit)
		if err != nil {
			return fmt.Errorf("coroutine physical ABI: function %q: leaf critical region: %w", plan.ID, err)
		}
		physical, err := prepareCoroPhysicalFunctionPlan(
			audit, owner, whole, nil, critical, false, coroPhysicalLoweringCapabilities{},
		)
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
		return fail("unfrozen caller instrumentation is incompatible with stackless coroutine suspension")
	}
	managedDispatchTarget := managedDispatch && plan.FuncRep == coro.Dispatch &&
		fn.Signature != nil && fn.Signature.Recv() == nil
	rawMethodDispatchToken := rawMethodToken && plan.FuncRep == coro.Dispatch &&
		fn.Signature != nil && fn.Signature.Recv() != nil
	outcomePlainPrimary := plan.Emission == coro.EmitOutcomePlain && plan.ManagedEntry == coro.ManagedEntryOutcomePlain
	outcomePlainTwin := plan.Emission == coro.EmitCoroutine && plan.ManagedEntry == coro.ManagedEntryCoroutine &&
		plan.HasStaticOutcome()
	outcomePlain := (outcomePlainPrimary || outcomePlainTwin) &&
		plan.HasStaticOutcome() && !plan.Recursive &&
		(plan.AtomicCostProof.ProvesOutcomePlain() && plan.AtomicCost != 0 && plan.Exec&^coro.MayUnwind == 0 ||
			plan.StaticOutcome && plan.Exec&(coro.BlockForeign|coro.ThreadAffine|coro.NeedsCleanupFrame|coro.OpaqueExec) == 0) &&
		(plan.FuncRep == coro.DirectCoro || outcomePlainTwin && plan.FuncRep == coro.Dispatch) &&
		(plan.Effect == coro.OutcomeStructured || outcomePlainTwin &&
			plan.Effect.Contains(coro.OutcomeStructured) &&
			plan.Effect&^(coro.YieldOnly|coro.AwaitStructured|coro.OutcomeStructured) == 0)
	if plan.Emission != coro.EmitCoroutine && !outcomePlain ||
		plan.FuncRep != coro.DirectCoro && !managedDispatchTarget && !rawMethodDispatchToken {
		return fail("requires a direct coroutine/outcome or capability-certified Dispatch emission, got emission=%s representation=%s", plan.Emission, plan.FuncRep)
	}
	if !plan.ManagedDemand.Contains(coro.AsyncDemand) {
		return fail(
			"requires managed async demand, got aggregate=%s managed=%s raw=%t raw-entry=%t representation=%s emission=%s effect=%s; demand-sources: %s; demand-references: %s; effect-trace: %s",
			plan.Demand, plan.ManagedDemand, plan.RawPlainDemand, plan.RawPlainEntry,
			plan.FuncRep, plan.Emission, plan.Effect,
			whole.DemandTrace(fn), coroDemandReferenceTrace(universe, fn), whole.OpaqueEffectTrace(fn),
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
	if outcomePlain && cleanupPlan != nil {
		return fail("outcome-plain body acquired a static cleanup plan")
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
		return fail(
			"execution flags %s require lowering outside the CFG physical ABI; effect-trace: %s",
			unsupported, whole.OpaqueEffectTrace(fn),
		)
	}
	if len(fn.FreeVars) != 0 && !managedDispatchTarget && plan.FuncRep != coro.DirectCoro {
		return fail("captured coroutine bodies require one exact direct or capability-certified descriptor context ABI")
	}
	deadCleanupRecover := cleanupPlan == nil && !plan.Exec.Contains(coro.NeedsCleanupFrame)
	if fn.Recover != nil && (cleanupPlan == nil || cleanupPlan.external) && !deadCleanupRecover {
		return fail("recover blocks require coroutine cleanup/unwind lowering")
	}
	if cleanupPlan != nil && !cleanupPlan.external {
		if err := validateCoroStaticCleanupRecoverBlock(fn); err != nil {
			return fail("static cleanup recover block: %v", err)
		}
	}
	cgoErrnoWorker := isCgoC2func(fn.Name())
	directive, directiveErr := coroRawABIDirective(fn, universe)
	if directiveErr != nil {
		return fail("classify ABI directive: %v", directiveErr)
	}
	if directive != "" && !(plan.RawPlainEntry && rawVariant) &&
		!(cgoErrnoWorker && directive == "//go:cgo_unsafe_args") {
		return fail("ABI directive %q requires a root or foreign adapter", directive)
	}
	if isCgoExternSymbol(fn) && !cgoErrnoWorker {
		return fail("cgo entry requires a foreign adapter")
	}
	programEntry := programRun && isCoroProgramManagedEntry(fn)
	genericInstance := coroMaterializedGenericCallable(fn)
	boundMethodWrapper := false
	if strings.HasPrefix(fn.Synthetic, "bound method wrapper for ") {
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
	rangeYield := false
	if fn.Synthetic == rangeOverFuncYieldSynthetic {
		if err := validateCoroExactRangeYield(fn); err != nil {
			return fail("invalid range-over-func yield: %v", err)
		}
		rangeYield = true
	}
	methodTokenWrapper := rawMethodToken && fn.Signature != nil && fn.Signature.Recv() != nil &&
		strings.Contains(fn.Synthetic, "wrapper for")
	methodWrapper := false
	if !genericInstance && !methodTokenWrapper && strings.HasPrefix(fn.Synthetic, "wrapper for ") {
		if err := validateCoroExactMethodWrapper(fn); err != nil {
			return fail("invalid method wrapper: %v", err)
		}
		methodWrapper = true
	}
	intrinsicSpawnCarrier := false
	if universe != nil && whole != nil {
		if wrapperInfo, compilerWrapper := universe.callWrapInfo[fn]; compilerWrapper {
			spawnUses := make([]*ssa.Go, 0, 1)
			for source, frozen := range universe.coroProgramIR.callPlans {
				if frozen.plan.StaticSpawnTarget != fn {
					continue
				}
				spawn, ok := source.(*ssa.Go)
				if !ok || frozen.failure != "" || frozen.plan.Elision != CoroCallNotElided ||
					!frozen.plan.Intrinsic || frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend ||
					!isCoroAtomicIntrinsic(frozen.opcode) ||
					spawn.Common() == nil ||
					universe.canonicalAlias(spawn.Common().StaticCallee()) != wrapperInfo.intrinsic {
					return fail("compiler intrinsic spawn carrier has an invalid frozen source recipe")
				}
				target, targetPlan, err := whole.ResolveClosedStaticSpawn(spawn)
				if err != nil || target != fn || targetPlan.ID != plan.ID {
					if err == nil {
						err = fmt.Errorf("resolved target=%v plan=%q", target, targetPlan.ID)
					}
					return fail("compiler intrinsic spawn carrier source is not exact: %v", err)
				}
				spawnUses = append(spawnUses, spawn)
			}
			if len(spawnUses) != 0 {
				intrinsicSpawnCarrier = true
				structuralKey, err := universe.intrinsicWrapperStructuralKey(wrapperInfo)
				if err != nil {
					return fail("compiler intrinsic spawn carrier identity: %v", err)
				}
				if fn.Synthetic != "wrapper" || fn.Parent() != nil || len(fn.FreeVars) != 0 ||
					fn.Origin() != nil || len(fn.TypeArgs()) != 0 || fn.Signature == nil ||
					fn.Signature.Recv() != nil || fn.Signature.Variadic() ||
					!types.Identical(fn.Signature, wrapperInfo.intrinsic.Signature) ||
					universe.syntheticKeys[fn] != structuralKey {
					return fail("compiler intrinsic spawn carrier lost its exact wrapper identity or signature")
				}
				forward, err := validateCoroExactSyntheticForwarder(fn, wrapperInfo.intrinsic)
				if err != nil || forward.Common().StaticCallee() != wrapperInfo.intrinsic || !whole.ElidesCall(forward) {
					return fail("compiler intrinsic spawn carrier forwarding body is invalid: %v", err)
				}
				frozenForward, found, err := universe.coroProgramIR.callSitePlan(forward)
				if err != nil || !found || frozenForward.failure != "" ||
					frozenForward.plan.Elision != CoroCallElidedIntrinsic ||
					!frozenForward.plan.Intrinsic ||
					frozenForward.plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend ||
					frozenForward.plan.StaticSpawnTarget != nil ||
					!isCoroAtomicIntrinsic(frozenForward.opcode) {
					if err == nil && !found {
						err = fmt.Errorf("forwarding call is absent from ProgramIR")
					}
					return fail("compiler intrinsic spawn carrier forwarding recipe is invalid: %v", err)
				}
			}
		}
	}
	builtinSpawnCarrier := false
	if universe != nil && whole != nil {
		if wrapperInfo, compilerWrapper := universe.builtinSpawnWrapInfo[fn]; compilerWrapper {
			spawn := wrapperInfo.spawn
			if spawn == nil || spawn.Common() == nil || spawn.Parent() == nil {
				return fail("compiler builtin spawn carrier has no exact source call")
			}
			sourcePlan, found, err := universe.CoroCallSitePlan(spawn)
			builtin, builtinSource := spawn.Common().Value.(*ssa.Builtin)
			if err != nil || !found || !builtinSource || builtin == nil ||
				sourcePlan.Elision != CoroCallNotElided || sourcePlan.Intrinsic ||
				sourcePlan.StaticSpawnTarget != fn || sourcePlan.ManagedStaticTarget != nil ||
				sourcePlan.CgoWorkerTarget != nil || sourcePlan.RawPlain {
				if err == nil && !found {
					err = fmt.Errorf("source spawn is absent from ProgramIR")
				}
				return fail("compiler builtin spawn carrier has an invalid frozen source recipe: %v", err)
			}
			target, targetPlan, err := whole.ResolveClosedStaticSpawn(spawn)
			if err != nil || target != fn || targetPlan.ID != plan.ID {
				if err == nil {
					err = fmt.Errorf("resolved target=%v plan=%q", target, targetPlan.ID)
				}
				return fail("compiler builtin spawn carrier source is not exact: %v", err)
			}
			signature, err := builtinSpawnCarrierSignature(spawn.Common())
			if err != nil {
				return fail("compiler builtin spawn carrier signature: %v", err)
			}
			wrapperOwner := universe.packages[wrapperInfo.owner]
			structuralKey, err := builtinSpawnWrapperStructuralKey(
				universe,
				wrapperInfo,
				wrapperOwner,
				universe.finalIdentity(spawn.Parent()),
				universe.effectiveType(wrapperOwner, spawn.Parent(), signature, false),
			)
			if err != nil {
				return fail("compiler builtin spawn carrier identity: %v", err)
			}
			if fn.Synthetic != "wrapper" || fn.Parent() != nil || len(fn.FreeVars) != 0 ||
				fn.Origin() != nil || len(fn.TypeArgs()) != 0 || fn.Signature == nil ||
				fn.Signature.Recv() != nil || fn.Signature.Variadic() ||
				!types.Identical(fn.Signature, signature) || universe.syntheticKeys[fn] != structuralKey {
				return fail("compiler builtin spawn carrier lost its exact wrapper identity or signature")
			}
			forward, err := validateCoroExactSyntheticForwarder(fn, builtin)
			if err != nil {
				return fail("compiler builtin spawn carrier forwarding body is invalid: %v", err)
			}
			if whole.ElidesCall(forward) {
				return fail("compiler builtin spawn carrier forwarding call was unexpectedly elided")
			}
			if _, planned := whole.CallPlan(forward); planned {
				return fail("compiler builtin spawn carrier forwarding call acquired a callable edge")
			}
			forwardPlan, found, err := universe.CoroCallSitePlan(forward)
			if err != nil || !found || forwardPlan.Elision != CoroCallNotElided || forwardPlan.Intrinsic ||
				forwardPlan.StaticSpawnTarget != nil || forwardPlan.ManagedStaticTarget != nil ||
				forwardPlan.CgoWorkerTarget != nil || forwardPlan.RawPlain {
				if err == nil && !found {
					err = fmt.Errorf("forwarding call is absent from ProgramIR")
				}
				return fail("compiler builtin spawn carrier forwarding recipe is invalid: %v", err)
			}
			builtinSpawnCarrier = true
		}
	}
	managedForeignValueWrapper := false
	if universe != nil {
		if _, compilerWrapper := universe.managedForeignWrapInfo[fn]; compilerWrapper {
			if err := universe.validateManagedForeignFunctionValueWrapper(fn); err != nil {
				return fail("compiler managed foreign function-value adapter: %v", err)
			}
			managedForeignValueWrapper = true
		}
	}
	capturedRawVariant := rawVariant && len(fn.FreeVars) != 0
	if fn.Synthetic != "" && !genericInstance && !boundMethodWrapper && !methodExpressionThunk && !methodWrapper && !methodTokenWrapper &&
		!intrinsicSpawnCarrier && !builtinSpawnCarrier && !managedForeignValueWrapper && !rangeYield && !capturedRawVariant &&
		!(programEntry && fn.Name() == "init" && fn.Synthetic == "package initializer") {
		return fail("synthetic function %q is outside the leaf ABI", fn.Synthetic)
	}
	if list := fn.TypeParams(); list != nil && list.Len() != 0 && !genericInstance && !rangeYield {
		return fail("generic declarations are not materialized coroutine bodies")
	}
	if list := fn.Signature.RecvTypeParams(); list != nil && list.Len() != 0 && !genericInstance && !rangeYield {
		return fail("generic receivers are not materialized coroutine bodies")
	}
	if list := fn.TypeArgs(); len(list) != 0 && !genericInstance && !rangeYield {
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
	pureSSA.libraryForeign = libraryForeign
	if cgoErrnoWorker {
		if err := validateCoroCgoErrnoWorkerOwner(whole, pureSSA.ctx, fn); err != nil {
			return fail("generated C2 worker adapter: %v", err)
		}
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
		coroPhysicalLoweringCapabilities{
			childAwait:       childAwait,
			staticSpawn:      staticSpawn,
			managedDispatch:  managedDispatch,
			explicitPanic:    explicitPanic,
			channel:          channel,
			worker:           universe != nil && universe.CoroWorkerSupported(),
			sameMForeign:     universe != nil && universe.coroCapabilities.NativeFleet(),
			hostOperation:    universe != nil && universe.coroCapabilities.HostOperation(),
			interfacePlain:   interfacePlain,
			managedInterface: managedInterface,
		},
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
	goexits := 0
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
			case coroStaticCleanupCgoWorker:
				if site.cgoWorker == nil {
					return fail("deferred cgo worker cleanup has no frozen typed operation")
				}
				foreignWaits++
			case coroStaticCleanupForeignWorker:
				if site.foreignWorker == nil || site.foreignWorker.mode != coroForeignCallModeWorker {
					return fail("deferred foreign worker cleanup has no frozen typed operation")
				}
				foreignWaits++
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
			instructionPlan, frozen := physical.instructions[instr]
			if !frozen {
				return coroLeafInstructionError(fn, plan, instr, "instruction is absent from the frozen physical plan")
			}
			if !instructionPlan.semantic.evaluated {
				// A dedicated frontend recipe (notably generated cgo) owns and
				// reconstructs this source region. ProgramIR already excluded it
				// from local effects and helper inventory; physical validation
				// must consume the same frozen evaluated bit instead of auditing
				// code that will never be emitted.
				continue
			}
			if instructionPlan.outcomeFailure != "" {
				return coroLeafInstructionError(fn, plan, instr, instructionPlan.outcomeFailure)
			}
			if instructionPlan.operationFailure != "" {
				return coroLeafInstructionError(fn, plan, instr, instructionPlan.operationFailure)
			}
			if instructionPlan.recipe == coroPhysicalInstructionSyntheticSelectNoCaseBox ||
				instructionPlan.outcome == coroPhysicalOutcomeSyntheticSelectTrap {
				continue
			}
			if handled, reason := pureSSA.validate(instr); handled {
				if reason != "" {
					return coroLeafInstructionError(fn, plan, instr, reason)
				}
				if instructionPlan.mayFault() {
					panics++
				}
				if call, ok := instr.(*ssa.Call); ok && isCoroCloseBuiltinCall(call) {
					instructionPlan := physical.instructions[instr]
					if instructionPlan.operation != coroPhysicalOperationChannelClose {
						if instructionPlan.operationFailure != "" {
							return coroLeafInstructionError(fn, plan, instr, instructionPlan.operationFailure)
						}
						return coroLeafInstructionError(fn, plan, instr, "channel close has no frozen operation recipe")
					}
					panics++
				}
				if call, ok := instr.(*ssa.Call); ok && isCoroRecoverBuiltinCall(call) &&
					instructionPlan.outcome != coroPhysicalOutcomeRecover {
					return coroLeafInstructionError(fn, plan, instr, "recover builtin has no frozen outcome recipe")
				}
				continue
			}
			switch instr := instr.(type) {
			case *ssa.DebugRef, *ssa.Jump:
			case *ssa.Return:
				if instructionPlan.outcome != coroPhysicalOutcomeReturn {
					return coroLeafInstructionError(fn, plan, instr, "return has no frozen outcome recipe")
				}
			case *ssa.Defer, *ssa.RunDefers:
				want := coroPhysicalOutcomeDeferRegister
				if _, run := instr.(*ssa.RunDefers); run {
					if cleanupPlan == nil && !plan.Exec.Contains(coro.NeedsCleanupFrame) &&
						instructionPlan.outcome == coroPhysicalOutcomeNone {
						continue
					}
					want = coroPhysicalOutcomeRunDefers
				}
				if instructionPlan.outcome != want {
					return coroLeafInstructionError(fn, plan, instr, "defer instruction has no frozen cleanup outcome recipe")
				}
			case *ssa.Panic:
				if instructionPlan.outcome != coroPhysicalOutcomePanic {
					return coroLeafInstructionError(fn, plan, instr, "panic has no frozen outcome recipe")
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
				instructionPlan, frozen := physical.instructions[instr]
				if !frozen || instructionPlan.operation != coroPhysicalOperationChannelSend {
					if frozen && instructionPlan.operationFailure != "" {
						return coroLeafInstructionError(fn, plan, instr, instructionPlan.operationFailure)
					}
					return coroLeafInstructionError(fn, plan, instr, "channel send has no frozen operation recipe")
				}
				parks++
			case *ssa.Select:
				instructionPlan, frozen := physical.instructions[instr]
				if !frozen || instructionPlan.operation != coroPhysicalOperationChannelSelectPark &&
					instructionPlan.operation != coroPhysicalOperationChannelSelectTry {
					if frozen && instructionPlan.operationFailure != "" {
						return coroLeafInstructionError(fn, plan, instr, instructionPlan.operationFailure)
					}
					return coroLeafInstructionError(fn, plan, instr, "channel select has no frozen operation recipe")
				}
				if instructionPlan.operation == coroPhysicalOperationChannelSelectPark {
					parks++
				}
			case *ssa.UnOp:
				if instr.Op == token.ARROW {
					instructionPlan, frozen := physical.instructions[instr]
					if !frozen || instructionPlan.operation != coroPhysicalOperationChannelReceive {
						if frozen && instructionPlan.operationFailure != "" {
							return coroLeafInstructionError(fn, plan, instr, instructionPlan.operationFailure)
						}
						return coroLeafInstructionError(fn, plan, instr, "channel receive has no frozen operation recipe")
					}
					parks++
					continue
				}
				if (instr.Op != token.SUB && instr.Op != token.XOR && instr.Op != token.NOT) || !coroLeafScalar(instr.Type()) {
					return coroLeafInstructionError(fn, plan, instr, "unsupported unary operation")
				}
			case *ssa.Call:
				instructionPlan, frozen := physical.instructions[instr]
				if !frozen {
					return coroLeafInstructionError(fn, plan, instr, "instruction is absent from the frozen physical plan")
				}
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
						cgoWorker := callPlan.Elision == CoroCallElidedCgoWorker
						pythonOperation := callPlan.Elision == CoroCallElidedPython
						if intrinsic && callPlan.ControlOperation.NativeActivationBound() {
							return coroLeafInstructionError(fn, plan, instr,
								"native setjmp/longjmp control cannot retain a stackless coroutine resume activation; isolate it in a plain native-stack adapter")
						}
						// InlineWithLoweredCalls is cleanup-safe for the same
						// structural reason as an ordinary managed child: its
						// exact helper edges remain in CoroLoweredCalls, and
						// resolveCoroLoweredRuntimeCall routes a suspending or
						// unwinding helper through this owner's cleanup-aware
						// child handoff. Only the erased intrinsic declaration
						// itself disappears.
						if cleanupPlan != nil && callPlan.Elision != CoroCallElidedNoInit &&
							!cgoWorker && !pythonOperation && (!intrinsic || !semantics.ElidesManagedCall()) {
							return coroLeafInstructionError(fn, plan, instr, "elided intrinsic has no cleanup-safe no-unwind contract")
						}
						if cgoWorker {
							if intrinsic {
								return coroLeafInstructionError(fn, plan, instr, "generated cgo worker call is also classified as an intrinsic")
							}
							if instructionPlan.operation != coroPhysicalOperationWorkerCgo || instructionPlan.operationCgo == nil {
								return coroLeafInstructionError(fn, plan, instr, "generated cgo worker call has no frozen typed operation recipe")
							}
							foreignWaits++
							continue
						}
						if pythonOperation {
							if intrinsic || callPlan.ElisionCertificate == "" {
								return coroLeafInstructionError(fn, plan, instr, "Python operation has an invalid frozen elision identity")
							}
							if instructionPlan.operation != coroPhysicalOperationSameMPython {
								return coroLeafInstructionError(fn, plan, instr, "Python operation has no frozen same-M physical recipe")
							}
							if callPlan.PythonTarget != instructionPlan.operationPythonTarget {
								return coroLeafInstructionError(fn, plan, instr, "Python operation physical target differs from ProgramIR")
							}
							if callPlan.PythonTarget == nil &&
								instructionPlan.operationPythonOpcode != frozen.opcode {
								return coroLeafInstructionError(fn, plan, instr, "Python intrinsic physical opcode differs from ProgramIR")
							}
							foreignWaits++
							continue
						}
						if intrinsic && callPlan.ControlOperation != CoroControlNone &&
							(instructionPlan.operation != coroPhysicalOperationControl ||
								instructionPlan.operationControl != callPlan.ControlOperation) {
							return coroLeafInstructionError(fn, plan, instr,
								"typed control intrinsic has no matching frozen physical operation")
						}
						if intrinsic && semantics == CoroIntrinsicCallInlineForeignSuspend {
							if frozen.opcode != llgoCgoCgocall ||
								instructionPlan.operation != coroPhysicalOperationWorkerCgoErrno ||
								instructionPlan.operationCgoErrno == nil {
								return coroLeafInstructionError(fn, plan, instr,
									"generated C2 foreign suspend has no frozen typed errno operation")
							}
							foreignWaits++
						} else if intrinsic && semantics == CoroIntrinsicCallInlineSuspend {
							if isLLGoSyscallIntrinsic(frozen.opcode) {
								if instructionPlan.operation != coroPhysicalOperationWorkerSyscall {
									return coroLeafInstructionError(fn, plan, instr, "worker llgo.syscall has no frozen operation recipe")
								}
							} else if frozen.opcode == llgoCoroHostOperation &&
								instructionPlan.operation != coroPhysicalOperationHostCall {
								return coroLeafInstructionError(fn, plan, instr, "host operation has no frozen operation recipe")
							}
							parks++
						} else if intrinsic && semantics == CoroIntrinsicCallInlineYield {
							yields++
						} else if intrinsic && semantics == CoroIntrinsicCallInlineOutcome {
							if instructionPlan.outcome != coroPhysicalOutcomeGoexit {
								return coroLeafInstructionError(fn, plan, instr,
									"terminal intrinsic has no frozen Goexit outcome recipe")
							}
							goexits++
						}
						if intrinsic {
							if isLLGoSyscallIntrinsic(frozen.opcode) && semantics != CoroIntrinsicCallInlineSuspend {
								return coroLeafInstructionError(fn, plan, instr,
									"elided worker llgo.syscall has no frozen function-word capability")
							}
							if frozen.opcode == llgoAlloca {
								if instructionPlan.recipe != coroPhysicalInstructionFrameAllocaBytes {
									return coroLeafInstructionError(fn, plan, instr,
										"llgo.alloca in a physical coroutine requires one exact constant frame allocation recipe")
								}
							}
							if (frozen.opcode == llgoAllocaCStr || frozen.opcode == llgoAllocaCStrs) &&
								instructionPlan.recipe != coroPhysicalInstructionHeapCStr {
								return coroLeafInstructionError(fn, plan, instr,
									"llgo C string allocation in a physical coroutine requires the frozen heap-backed recipe")
							}
							if frozen.opcode == llgoStackSave {
								return coroLeafInstructionError(fn, plan, instr,
									"llgo.stackSave is valid only in a no-suspend plain island; a stackless physical coroutine cannot retain a native resume-stack pointer")
							}
						}
					}
					// The frozen frontend proved that this declaration call emits no
					// callable edge. A structured park is counted above; ordinary
					// noinit/inline intrinsics need no await/plain entry.
					continue
				}
				if instr.Common().IsInvoke() {
					switch instructionPlan.control {
					case coroPhysicalControlClosedInterfaceAwait, coroPhysicalControlManagedInterfaceAwait,
						coroPhysicalControlExactInterfaceAwait:
						awaits++
						continue
					case coroPhysicalControlExactInterfaceCall:
						continue
					case coroPhysicalControlNone:
						if instructionPlan.controlFailureHard {
							return coroLeafInstructionError(fn, plan, instr, instructionPlan.controlFailure)
						}
						continue
					default:
						return coroLeafInstructionError(fn, plan, instr,
							"interface invoke has mismatched frozen control recipe "+instructionPlan.control.String())
					}
				}
				switch instructionPlan.control {
				case coroPhysicalControlPlainDispatch:
					continue
				case coroPhysicalControlDirectOutcome:
					continue
				case coroPhysicalControlNilDispatchFault:
					continue
				case coroPhysicalControlRawPlainCall:
					continue
				case coroPhysicalControlDispatchAwait:
					awaits++
					continue
				default:
					if instructionPlan.controlFailureHard {
						return coroLeafInstructionError(fn, plan, instr, instructionPlan.controlFailure)
					}
				}
				if instructionPlan.operation == coroPhysicalOperationWorkerForeign ||
					instructionPlan.operation == coroPhysicalOperationSameMForeign {
					if instructionPlan.operationWorker == nil {
						return coroLeafInstructionError(fn, plan, instr, "managed foreign call has no frozen physical shape")
					}
					foreignWaits++
					continue
				}
				if instructionPlan.control == coroPhysicalControlDirectAwait {
					awaits++
					continue
				}
				if instructionPlan.controlFailureHard {
					return coroLeafInstructionError(fn, plan, instr, instructionPlan.controlFailure)
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
					return coroLeafInstructionError(fn, plan, instr, "unsupported child await: "+instructionPlan.controlFailure)
				}
				if _, _, plainErr := resolveCoroStaticPlainCall(whole, instr); plainErr != nil {
					return coroLeafInstructionError(fn, plan, instr, "unsupported call: child await: "+instructionPlan.controlFailure+"; direct plain: "+plainErr.Error())
				}
			case *ssa.Go:
				instructionPlan, frozen := physical.instructions[instr]
				if !frozen {
					return coroLeafInstructionError(fn, plan, instr, "instruction is absent from the frozen physical plan")
				}
				switch instructionPlan.control {
				case coroPhysicalControlDirectSpawn, coroPhysicalControlDispatchSpawn:
				case coroPhysicalControlNone:
					return coroLeafInstructionError(fn, plan, instr, instructionPlan.controlFailure)
				default:
					return coroLeafInstructionError(fn, plan, instr, "goroutine spawn has mismatched frozen control recipe "+instructionPlan.control.String())
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
	if goexits != 0 && (!plan.DeclaredEffect.Contains(coro.OutcomeStructured) ||
		!plan.LocalEffect.Contains(coro.OutcomeStructured) ||
		!plan.Effect.Contains(coro.OutcomeStructured)) {
		return fail("Goexit body lacks outcome-structured owner effect: declared=%s local=%s final=%s", plan.DeclaredEffect, plan.LocalEffect, plan.Effect)
	}
	if plan.DeclaredEffect.Contains(coro.MayPark) && parks == 0 {
		return fail("declared may-park effect has no exact structured park intrinsic")
	}
	if plan.DeclaredEffect.Contains(coro.WaitForeign) && foreignWaits == 0 {
		return fail("declared wait-foreign effect has no exact bounded worker operation")
	}
	// WaitForeign may be inherited from a structured child. An ordinary local
	// foreign edge was counted and shape-checked above; the effect bit alone can
	// never authorize a raw foreign call on this scheduler thread.
	if unsupported := plan.Effect &^ (coro.YieldOnly | coro.AwaitStructured | coro.OutcomeStructured | coro.MayPark | coro.WaitForeign); unsupported != 0 {
		return fail("child-await body has unsupported final effect %s", unsupported)
	}
	if unsupported := plan.DeclaredEffect &^ (coro.YieldOnly | coro.OutcomeStructured | coro.MayPark | coro.WaitForeign); unsupported != 0 {
		return fail("child-await body has unsupported declared effect %s", unsupported)
	}
	if unsupported := plan.LocalEffect &^ (coro.YieldOnly | coro.AwaitStructured | coro.OutcomeStructured | coro.MayPark | coro.WaitForeign); unsupported != 0 {
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

func validateCoroExplicitStatusPanic(
	audit *coroPhysicalPureSSAAudit,
	instruction *ssa.Panic,
	capabilities coroPhysicalLoweringCapabilities,
) string {
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
		if reason := validateCoroExplicitStatusPanicInterfaceValue(
			audit, instruction.X, capabilities, make(map[ssa.Value]bool),
		); reason != "" {
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
// the child frame is destroyed. Interface parameters carry an exact
// frame-retention certificate for the copied pair; arbitrary dynamic producers
// remain fail-closed until they carry their own lifetime certificate.
func validateCoroExplicitStatusPanicInterfaceValue(
	audit *coroPhysicalPureSSAAudit,
	value ssa.Value,
	capabilities coroPhysicalLoweringCapabilities,
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
	case *ssa.Const:
		if value.Value != nil {
			return "interface constant is not nil"
		}
		// An exact nil interface constant lowers to two zero words. It borrows
		// no coroutine-frame storage, and the panic handoff normalizes that
		// pair to the package-rooted Go 1.21+ *PanicNilError payload before
		// publishing it.
		return ""
	case *ssa.Parameter:
		proof := audit.currentFrameRetentionProof()
		if proof == nil || !proof.provesInterfaceParameter(value) {
			return "interface parameter has no exact frame-retention proof"
		}
		return ""
	case *ssa.ChangeInterface:
		if reason := audit.validateChangeInterface(value); reason != "" {
			return "interface conversion has no outcome-safe lowering: " + reason
		}
		return validateCoroExplicitStatusPanicInterfaceValue(audit, value.X, capabilities, visiting)
	case *ssa.ChangeType:
		if reason := audit.validateChangeType(value); reason != "" {
			return "interface change-type has no pure lowering: " + reason
		}
		return validateCoroExplicitStatusPanicInterfaceValue(audit, value.X, capabilities, visiting)
	case *ssa.Phi:
		if len(value.Edges) == 0 {
			return "interface phi has no incoming values"
		}
		for _, edge := range value.Edges {
			if reason := validateCoroExplicitStatusPanicInterfaceValue(audit, edge, capabilities, visiting); reason != "" {
				return "interface phi input: " + reason
			}
		}
		return ""
	case *ssa.UnOp:
		if value.Op == token.ARROW {
			return validateCoroExplicitStatusPanicChannelReceive(
				audit, value, -1, value.Type(), capabilities,
			)
		}
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
	case *ssa.Extract:
		if reason := audit.validateExtract(value); reason != "" {
			return "interface tuple extract has no pure lowering: " + reason
		}
		switch tuple := value.Tuple.(type) {
		case *ssa.UnOp:
			return validateCoroExplicitStatusPanicChannelReceive(
				audit, tuple, value.Index, value.Type(), capabilities,
			)
		case *ssa.Select:
			return validateCoroExplicitStatusPanicSelectReceive(
				audit, tuple, value.Index, value.Type(), capabilities,
			)
		}
		call, ok := value.Tuple.(*ssa.Call)
		if !ok || call == nil || call.Parent() != audit.fn || call.Common() == nil {
			return "interface tuple extract is not produced by one call or channel receive in the current body"
		}
		signature := call.Common().Signature()
		if signature == nil || signature.Results() == nil ||
			value.Index < 0 || value.Index >= signature.Results().Len() ||
			!types.Identical(audit.typeOf(value.Type()), audit.typeOf(signature.Results().At(value.Index).Type())) {
			return "interface tuple extract disagrees with its call result shape"
		}
		return validateCoroExplicitStatusPanicInterfaceCallResult(audit, call, capabilities)
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
		return validateCoroExplicitStatusPanicInterfaceCallResult(audit, value, capabilities)
	default:
		return fmt.Sprintf("interface producer %T has no post-destroy lifetime proof", value)
	}
}

func validateCoroExplicitStatusPanicChannelReceive(
	audit *coroPhysicalPureSSAAudit,
	receive *ssa.UnOp,
	index int,
	resultType types.Type,
	capabilities coroPhysicalLoweringCapabilities,
) string {
	if !capabilities.channel {
		return "interface channel receive requires the channel scheduler capability"
	}
	if audit == nil || receive == nil || receive.Parent() != audit.fn ||
		receive.Op != token.ARROW || receive.X == nil {
		return "interface channel receive is not one exact operation in the current body"
	}
	channel, ok := types.Unalias(audit.typeOf(receive.X.Type())).Underlying().(*types.Chan)
	if !ok {
		return "interface channel receive source is not a channel"
	}
	if receive.CommaOk {
		if index != 0 {
			return "interface channel receive extract is not the received value"
		}
	} else if index >= 0 {
		return "non-tuple interface channel receive has an extract index"
	}
	if !types.Identical(audit.typeOf(resultType), audit.typeOf(channel.Elem())) {
		return "interface channel receive result disagrees with the channel element type"
	}
	return ""
}

func validateCoroExplicitStatusPanicSelectReceive(
	audit *coroPhysicalPureSSAAudit,
	selection *ssa.Select,
	index int,
	resultType types.Type,
	capabilities coroPhysicalLoweringCapabilities,
) string {
	if !capabilities.channel {
		return "interface select receive requires the channel scheduler capability"
	}
	if audit == nil || selection == nil || selection.Parent() != audit.fn {
		return "interface select receive is not one exact operation in the current body"
	}
	resultIndex := 2
	for _, state := range selection.States {
		if state == nil || state.Dir != types.RecvOnly {
			continue
		}
		if resultIndex != index {
			resultIndex++
			continue
		}
		if state.Chan == nil {
			return "interface select receive has no channel"
		}
		channel, ok := types.Unalias(audit.typeOf(state.Chan.Type())).Underlying().(*types.Chan)
		if !ok {
			return "interface select receive source is not a channel"
		}
		if !types.Identical(audit.typeOf(resultType), audit.typeOf(channel.Elem())) {
			return "interface select receive result disagrees with the channel element type"
		}
		return ""
	}
	return "interface select extract is not a received channel value"
}

func validateCoroExplicitStatusPanicInterfaceCallResult(
	audit *coroPhysicalPureSSAAudit,
	call *ssa.Call,
	capabilities coroPhysicalLoweringCapabilities,
) string {
	if audit == nil || audit.plan == nil || audit.fn == nil || call == nil || call.Parent() != audit.fn {
		return "interface call result requires a whole-program call plan in the current body"
	}
	callerPlan, planned := audit.plan.FunctionPlan(audit.fn)
	if !planned {
		return "interface call result owner has no function plan"
	}
	if _, _, _, err := resolveCoroStaticAwait(audit.plan, callerPlan, call, audit.universe); err == nil {
		// The child writes its Go result into parent-owned result storage before
		// the parent resumes and destroys the child. Go escape semantics keep any
		// backing cell referenced by the returned interface alive; copying the
		// two words into the panic completion record is therefore stable.
		return ""
	}
	if _, targetPlan, err := resolveCoroStaticPlainCall(audit.plan, call); err == nil &&
		targetPlan.External == coro.Defined && !targetPlan.Exec.Contains(coro.MayUnwind) {
		// A bounded owned Go callee has completed normally on the same stack; its
		// returned interface obeys the same language-level escape lifetime.
		return ""
	}
	common := call.Common()
	callPlan, callPlanned := audit.plan.CallPlan(call)
	if common != nil && common.IsInvoke() {
		// The universal interface descriptor carrier writes both coroutine and
		// plain results into an owner-frame result slot before this value is
		// observed. Accept it only when the exact compilation-scoped capability
		// owns this occurrence and its immutable CallPlan still validates.
		if capabilities.managedInterface != nil &&
			capabilities.managedInterface.acceptsCall(call) &&
			callPlanned && callPlan.Rep == coro.Dispatch {
			if callPlan.Open {
				if err := validateCoroManagedInterfaceDispatchCall(
					audit.plan, audit.universe, audit.fn, call, callPlan,
				); err == nil {
					return ""
				}
			} else if _, err := resolveCoroInterfaceDispatchPlan(
				audit.plan, audit.universe, call,
			); err == nil {
				return ""
			}
		}
		// A closed receiver-aware await has the same frame-resident result
		// transaction even when this method family does not use the universal
		// descriptor table.
		if dispatch, err := resolveCoroInterfaceDispatchPlan(
			audit.plan, audit.universe, call,
		); err == nil && coroInterfaceDispatchNeedsAwait(dispatch) {
			return ""
		}
		// The bounded all-plain interface island returns an ordinary Go value.
		// Escape semantics make the returned interface pair independent of the
		// callee activation before it is copied into the panic record.
		if capabilities.interfacePlain != nil &&
			capabilities.interfacePlain.acceptsCall(call) {
			if _, err := resolveCoroClosedInterfacePlainCall(audit.plan, call); err == nil {
				return ""
			}
		}
		return "interface invoke result has no exact frame-stable carrier"
	}
	if callPlanned && callPlan.Rep == coro.Dispatch && common != nil &&
		common.StaticCallee() == nil {
		if callPlan.SyncDispatch {
			if err := validateCoroPlainDispatchCall(
				audit.plan, audit.fn, call, callPlan, audit.universe,
			); err == nil {
				return ""
			}
		} else if capabilities.managedDispatch {
			if err := validateCoroManagedDispatchCall(
				audit.plan, audit.fn, call, callPlan, audit.universe,
			); err == nil {
				if err := validateCoroManagedDispatchAwaitShape(
					audit.plan, audit.fn, call, callPlan,
				); err == nil {
					return ""
				}
			}
		}
	}
	return "interface call result is not one exact managed child or non-unwinding owned plain call"
}

func isCoroProgramManagedEntry(fn *ssa.Function) bool {
	// A source method may legally be named init (container/ring has one), but
	// only package-owned top-level functions participate in program bootstrap.
	// Bind this classification to the SSA owner and receiver shape before using
	// the reserved source spelling; name-only classification turns an ordinary
	// library method into a scheduler root when that package is compiled alone.
	if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil || fn.Parent() != nil ||
		fn.Signature == nil || fn.Signature.Recv() != nil {
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

// CoroMaterializedGenericInstance reports whether fn is one exact, fully
// concrete x/tools generic body (including a function literal materialized
// inside such a body). Build-level whole-program proofs use this same
// predicate instead of reconstructing generic-instance identity from Origin
// and TypeArgs independently. The caller supplies only the immutable canonical
// resolver it already owns; this predicate does not acquire direct universe or
// plan authority.
func CoroMaterializedGenericInstance(
	resolve func(*ssa.Function) (*ssa.Function, bool),
	fn *ssa.Function,
) bool {
	if resolve == nil || fn == nil {
		return false
	}
	canonical, ok := resolve(fn)
	return ok && canonical == fn && coroMaterializedGenericInstance(fn)
}

// coroMaterializedGenericCallable includes Pkg-nil method-set wrappers that
// x/tools synthesizes around an instantiated generic receiver method. Such a
// wrapper has no Origin or TypeArgs of its own, but its receiver, SSA
// parameters, and sole callee are fully concrete. This covers both pointer
// adaptation and promotion through a non-generic embedding type. Keep the proof
// separate from ordinary generic instances so an arbitrary synthetic body
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
		case *ssa.Alloc, *ssa.ChangeType, *ssa.Convert, *ssa.Field, *ssa.FieldAddr, *ssa.Store, *ssa.UnOp:
		default:
			return false
		}
		if _, call := instruction.(*ssa.Call); !call {
			for _, operand := range instruction.Operands(nil) {
				if operand == nil || *operand == nil ||
					coroTypeContainsUnresolvedTypeParam((*operand).Type(), make(map[types.Type]bool)) {
					return false
				}
			}
		}
		if value, ok := instruction.(ssa.Value); ok &&
			coroTypeContainsUnresolvedTypeParam(value.Type(), make(map[types.Type]bool)) {
			return false
		}
	}
	if wrapperCall == nil || callee == nil || !coroMaterializedGenericInstance(callee) ||
		callee.Signature == nil || callee.Signature.Recv() == nil || len(wrapperCall.Common().Args) == 0 ||
		len(wrapperCall.Common().Args) != len(callee.Params) {
		return false
	}
	calleeOrigin := callee.Origin()
	if calleeOrigin == nil || calleeOrigin.Name() != fn.Name() {
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
	for index, argument := range wrapperCall.Common().Args {
		if argument == nil || !types.Identical(argument.Type(), calleeSig.Params().At(index).Type()) {
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
		return fail("unfrozen caller instrumentation is incompatible with stackless coroutine suspension")
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
			materialized := coroMaterializedGenericCallable(fn)
			if !materialized && (typeParamCount(sig.RecvTypeParams()) != 0 || typeParamCount(sig.TypeParams()) != 0) {
				return nil, fmt.Errorf(
					"coroutine physical ABI: function %q requires receiver patching before its generic declaration is materialized",
					fn.Name(),
				)
			}
			receiver = types.NewVar(receiver.Pos(), receiver.Pkg(), receiver.Name(), effectiveReceiver)
			// A concrete x/tools receiver instance may retain the origin's
			// RecvTypeParams list even though every callable type is ground. Those
			// TypeParam objects are already bound to sig and cannot legally be
			// rebound into a second go/types Signature. They are source metadata,
			// not part of the physical receiver-first ABI, so the reconstructed
			// concrete signature deliberately clears both parameter lists.
			sig = types.NewSignatureType(
				receiver,
				nil,
				nil,
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

// coroPhysicalEntrySourceSignature adds the one compiler-owned environment
// parameter required by a physical entry. Lexical closures use their exact
// typed capture structure; //llgo:env declarations use unsafe.Pointer. It is
// deliberately separate from coroPhysicalSourceSignature: source call sites
// and lowered helper markers see only explicit Go parameters, while a closure
// or descriptor thunk supplies this context between (g,out) and those params.
func (u *EmissionUniverse) coroPhysicalEntrySourceSignature(fn *ssa.Function) (*types.Signature, error) {
	sig, err := u.coroPhysicalSourceSignature(fn)
	if err != nil || fn == nil {
		return sig, err
	}
	env, hasEnv, err := u.closureEnvironments.entryEnvironment(fn)
	if err != nil || !hasEnv {
		return sig, err
	}
	return llssa.FuncAddCtx(env, sig), nil
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
	offset := effective.Params().Len() - len(fn.Params)
	if offset < 0 || offset > 1 {
		return fail("effective entry parameters=%d have invalid hidden-context offset from SSA parameters=%d", effective.Params().Len(), len(fn.Params))
	}
	wantEnv := len(fn.FreeVars) != 0
	explicitEnv := false
	if universe != nil {
		var envErr error
		_, wantEnv, envErr = universe.closureEnvironments.entryEnvironment(fn)
		if envErr != nil {
			return fail("derive hidden environment: %v", envErr)
		}
		explicitEnv = universe.closureEnvironments.hasExplicitEnvironment(fn)
	}
	if wantEnv != (offset == 1) {
		return fail("effective hidden-context=%t does not match required environment=%t", offset == 1, wantEnv)
	}
	if offset == 1 {
		contextType := effective.Params().At(0).Type()
		switch {
		case explicitEnv:
			if !types.Identical(contextType, types.Typ[types.UnsafePointer]) {
				return fail("effective //llgo:env entry context is %v, want unsafe.Pointer", contextType)
			}
		case !coroPhysicalClosureContextMatches(fn, contextType):
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
		if detachedPackageGoLinknameDirective(fn, decl, text) {
			continue
		}
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
	managedLinkname := false
	if exactManagedSyntax {
		var err error
		managedDefinition, err = universe.exactManagedGoLinknameDefinition(fn)
		if err != nil {
			return "", err
		}
		_, managedLinkname, err = universe.coroManagedGoLinknameCertificate(fn)
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
			if detachedPackageGoLinknameDirective(fn, decl, text) {
				continue
			}
			if exactManagedSyntax && (managedDefinition || managedLinkname) && text == managedDirective {
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

// detachedPackageGoLinknameDirective reports the Go source form whose first
// operand names another exact package-scope function or variable. Directives
// are assigned by that operand even when their comment group is attached to a
// method for documentation, as used by current GOROOT compatibility aliases.
// A missing or ambiguous package object remains attached/fail-closed.
func detachedPackageGoLinknameDirective(
	fn *ssa.Function,
	decl *ast.FuncDecl,
	text string,
) bool {
	if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil || decl == nil {
		return false
	}
	fields := strings.Fields(text)
	if len(fields) != 3 || fields[0] != "//go:linkname" {
		return false
	}
	_, attachedName := astFuncName("", decl)
	if fields[1] == attachedName {
		return false
	}
	object := fn.Pkg.Pkg.Scope().Lookup(fields[1])
	switch object := object.(type) {
	case *types.Func:
		signature, _ := object.Type().(*types.Signature)
		return signature != nil && signature.Recv() == nil
	case *types.Var:
		return true
	default:
		return false
	}
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
		if function.Plan.Emission != coro.EmitNone &&
			(function.Plan.ManagedEntry == coro.ManagedEntryCoroutine ||
				function.Plan.ManagedEntry == coro.ManagedEntryOutcomePlain) {
			coroutineIDs[function.Plan.ID] = struct{}{}
		}
	}
	for _, function := range plan.Functions() {
		if function.Plan.Emission != coro.EmitPlain && function.Plan.Emission != coro.EmitCoroutine {
			continue
		}
		fn := function.Function
		unevaluated, _ := universe.frozenUnsafeLayoutUnevaluatedSSA(fn)
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				var managedStaticCalleeOperand *ssa.Value
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
						if _, err := plan.ResolveManagedSpawn(spawn); err != nil {
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
							if deferred, cleanup := call.(*ssa.Defer); cleanup {
								callPlan, found := plan.CallPlan(deferred)
								if !found {
									return coroLeafInstructionError(fn, function.Plan, instr,
										"managed interface defer has no compilation CallPlan")
								}
								if !managedDispatch || !function.Plan.Exec.Contains(coro.NeedsCleanupFrame) {
									return coroLeafInstructionError(fn, function.Plan, instr,
										"managed interface defer requires a coroutine cleanup owner and universal descriptor capability")
								}
								if callPlan.Open {
									if err := validateCoroManagedInterfaceDispatchCall(
										plan, universe, fn, deferred, callPlan,
									); err != nil {
										return coroLeafInstructionError(fn, function.Plan, instr,
											"invalid managed interface defer: "+err.Error())
									}
								} else if _, err := resolveCoroInterfaceDispatchPlan(plan, universe, deferred); err != nil {
									return coroLeafInstructionError(fn, function.Plan, instr,
										"invalid managed interface cleanup: "+err.Error())
								}
								continue
							}
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
					if universe != nil {
						site, frozen, err := universe.CoroCallSitePlan(call)
						if err != nil {
							return coroLeafInstructionError(fn, function.Plan, instr,
								"managed static-call SitePlan: "+err.Error())
						}
						if !frozen {
							return coroLeafInstructionError(fn, function.Plan, instr,
								"managed static-call source is absent from the frozen ProgramIR")
						}
						if target := site.ManagedStaticTarget; target != nil {
							common := call.Common()
							targetPlan, planned := plan.FunctionPlan(target)
							raw, exact := common.Value.(*ssa.Function)
							if site.ManagedStaticTargetCertificate == "" || !exact || raw == nil ||
								common.StaticCallee() != raw || common.IsInvoke() || common.Method != nil ||
								!planned || len(callPlan.Targets) != 1 ||
								callPlan.Targets[0] != targetPlan.ID {
								return coroLeafInstructionError(fn, function.Plan, instr,
									"managed static-call redirect disagrees with its frozen CallPlan")
							}
							managedStaticCalleeOperand = &common.Value
						}
					}
					if callPlan.RawPlain {
						direct, ordinary := call.(*ssa.Call)
						if !ordinary {
							return coroLeafInstructionError(fn, function.Plan, instr, "raw/plain invocation is not an ordinary call")
						}
						if _, _, err := validateCoroRawPlainSourceCall(plan, universe.Resolve, direct); err != nil {
							return coroLeafInstructionError(fn, function.Plan, instr, "invalid raw/plain invocation: "+err.Error())
						}
						continue
					}
					if callPlan.Transport == coro.RawCCodePointer {
						common := call.Common()
						if function.Plan.Emission == coro.EmitCoroutine &&
							common != nil && common.StaticCallee() == nil &&
							!common.IsInvoke() && common.Method == nil &&
							callPlan.Kind == coro.CallForeign &&
							callPlan.Rep == coro.DirectPlain &&
							callPlan.Open && callPlan.Unresolved == coro.UnknownForeign &&
							!callPlan.SyncDispatch {
							// The target-dependent physical planner owns the exact
							// typed-record, nil-fault, and bounded-worker proof.
							// This consumer pass establishes only that the raw C
							// pointer cannot fall through to ordinary managed
							// emission.
							continue
						}
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
							if _, _, _, err := resolveCoroStaticAwait(plan, function.Plan, direct, universe); err == nil {
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
				// DebugRef operands describe source-level debugger state only. They do
				// not materialize a function value in emitted IR, so they must not
				// participate in physical-representation validation.
				if _, debug := instr.(*ssa.DebugRef); debug {
					continue
				}
				if boxed, ok := instr.(*ssa.MakeInterface); ok &&
					coroCompilerElidedFunctionBox(plan, universe, fn, boxed) {
					// funcAddr/funcPCABI0 consume the exact static function
					// operand as a code address. No interface or callable value
					// reaches physical emission, so the target's coroutine
					// primary needs no descriptor conversion at this site.
					continue
				}
				for _, operand := range instr.Operands(nil) {
					if operand == nil || *operand == nil {
						continue
					}
					if operand == managedStaticCalleeOperand {
						// This exact source C declaration is identity-only for
						// the managed occurrence. Analysis and lowering consume
						// the frozen Go target; raw/plain bodies keep the source
						// declaration and never take this branch.
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
						if change, exactRetag := instr.(*ssa.ChangeType); exactRetag && change.X == target {
							audit := &coroPhysicalPureSSAAudit{
								plan: plan, universe: universe, fn: fn,
							}
							resolved, err := resolveCoroCompilerElidedStaticAwaitRetag(
								audit, function.Plan, change,
							)
							if err == nil && resolved == target {
								// The physical direct-await recipe consumes the
								// canonical target without materializing this
								// source-level function-type name.
								continue
							}
						}
						if closure, exactClosure := instr.(*ssa.MakeClosure); exactClosure && closure.Fn == target &&
							childAwait && function.Plan.Emission == coro.EmitCoroutine && len(target.FreeVars) != 0 &&
							targetPlan.Primary == coro.PrimaryCoroutine {
							if err := validateCoroCapturedClosureProducer(plan, closure, targetPlan); err == nil {
								// DirectCoro producers retag the physical
								// (g,out,ctx,args) entry as a (ctx,args) carrier.
								// Escaping producers instead publish a Dispatch
								// descriptor. Exact static await/defer sites consume
								// only the common environment word and frozen target.
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
	// Complex is aggregate-shaped in LLVM but remains a closed by-value leaf in
	// the coroutine ABI. The leaf instruction allowlist separately restricts it
	// to Go-valid helper-free operations.
	return info&(types.IsBoolean|types.IsInteger|types.IsFloat|types.IsComplex) != 0
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
	return fmt.Errorf(
		"coroutine physical ABI: function %q (%s, effect=%s, exec=%s): %T%s at %s: %s",
		plan.ID, name, plan.Effect, plan.Exec, instr, operation, where, reason,
	)
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
