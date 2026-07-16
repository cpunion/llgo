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
	"go/token"
	"go/types"
	"strings"

	"github.com/goplus/llgo/cl/blocks"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	// Version zero is intentionally experimental: the complete CoroHeader and
	// FrameDescriptor ABI is not frozen until scheduler/root lowering lands.
	coroPhysicalABIVersion uint32 = 0
	coroFrameAllocHook            = "__llgo_coro_frame_alloc_v0"
	coroFrameFreeHook             = "__llgo_coro_frame_free_v0"
	coroDescriptorPrefix          = "__llgo_coro_frame_descriptor_v0."

	coroPhysicalABIVersionV1  uint32 = 1
	coroFrameAllocHookV1             = "__llgo_coro_frame_alloc_v1"
	coroFramePublishHookV1           = "__llgo_coro_frame_publish_v1"
	coroAwaitPrepareHookV1           = "__llgo_coro_await_prepare_v1"
	coroPreemptPollHookV1            = "__llgo_coro_preempt_poll_v1"
	coroYieldPrepareHookV1           = "__llgo_coro_yield_prepare_v1"
	coroParkPrepareHookV1            = "__llgo_coro_park_prepare_v1"
	coroPanicPrepareHookV1           = "__llgo_coro_panic_prepare_v1"
	coroSpawnBeginHookV1             = "__llgo_coro_spawn_begin_v1"
	coroSpawnCommitHookV1            = "__llgo_coro_spawn_commit_v1"
	coroCompletePrepareHookV1        = "__llgo_coro_complete_prepare_v1"
	coroFrameFreeHookV1              = "__llgo_coro_frame_free_v1"
	coroDescriptorPrefixV1           = "__llgo_coro_frame_descriptor_v1."
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
	version             uint32
	hash                [16]byte
	descriptorName      string
	frameAllocHook      string
	frameFreeHook       string
	framePublishHook    string
	awaitPrepareHook    string
	preemptPollHook     string
	yieldPrepareHook    string
	parkPrepareHook     string
	panicPrepareHook    string
	completePrepareHook string
	physicalSig         *types.Signature
	resultSlotType      types.Type
	resultCount         int
}

// coroBodyContext exists only while emitting one physical coroutine body. It
// carries the current handle/header explicitly so call lowering never guesses a
// frame layout from a raw handle.
type coroBodyContext struct {
	coro            *llssa.CoroBuilder
	abi             coroPhysicalABI
	header          llssa.Expr
	task            llssa.Expr
	resultSlot      llssa.Expr
	completion      llssa.BasicBlock
	finalSuspend    llssa.BasicBlock
	preemptPoll     llssa.Expr
	yieldPrepare    llssa.Expr
	parkPrepare     llssa.Expr
	panicPrepare    llssa.Expr
	completePrepare llssa.Expr
	nextState       uint32
	terminalState   uint32
	needsPreempt    bool
	instructions    int
	frameRetention  *coroFrameRetentionProof
	frameRetaining  bool
}

func newCoroPhysicalABI(p *context, entry plannedFunctionSymbol, sourceSig *types.Signature) coroPhysicalABI {
	version := coroPhysicalABIVersion
	frameAllocHook := coroFrameAllocHook
	frameFreeHook := coroFrameFreeHook
	descriptorPrefix := coroDescriptorPrefix
	framePublishHook := ""
	awaitPrepareHook := ""
	preemptPollHook := ""
	yieldPrepareHook := ""
	parkPrepareHook := ""
	panicPrepareHook := ""
	completePrepareHook := ""
	if p.compilation != nil && p.compilation.EnableCoroChildAwait {
		version = coroPhysicalABIVersionV1
		frameAllocHook = coroFrameAllocHookV1
		frameFreeHook = coroFrameFreeHookV1
		descriptorPrefix = coroDescriptorPrefixV1
		framePublishHook = coroFramePublishHookV1
		awaitPrepareHook = coroAwaitPrepareHookV1
		preemptPollHook = coroPreemptPollHookV1
		yieldPrepareHook = coroYieldPrepareHookV1
		parkPrepareHook = coroParkPrepareHookV1
		completePrepareHook = coroCompletePrepareHookV1
	}
	if p.compilation != nil && p.compilation.EnableCoroExplicitStatusPanicABI {
		panicPrepareHook = coroPanicPrepareHookV1
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
		"llgo-coro-physical-v%d\x00%s\x00coro=%s\x00scheduler=%s\x00panic=%s\x00func-rep=%s\x00triple=%s\x00cpu=%s\x00features=%s\x00target-abi=%s\x00data-layout=%s\x00ptr=%d\x00sig=%s\x00result=%s",
		version,
		entry.plan.ID,
		coroABI,
		schedulerABI,
		panicABI,
		funcRepABI,
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
		version:             version,
		hash:                hash,
		descriptorName:      descriptorPrefix + hex.EncodeToString(hash[:]),
		frameAllocHook:      frameAllocHook,
		frameFreeHook:       frameFreeHook,
		framePublishHook:    framePublishHook,
		awaitPrepareHook:    awaitPrepareHook,
		preemptPollHook:     preemptPollHook,
		yieldPrepareHook:    yieldPrepareHook,
		parkPrepareHook:     parkPrepareHook,
		panicPrepareHook:    panicPrepareHook,
		completePrepareHook: completePrepareHook,
		physicalSig:         physicalSig,
		resultSlotType:      resultSlotType,
		resultCount:         sourceSig.Results().Len(),
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

func (p *context) beginCoroBody(b llssa.Builder, abi coroPhysicalABI) *coroBodyContext {
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
		abi:        abi,
		header:     header,
		task:       task,
		resultSlot: resultSlot,
		nextState:  1,
	}
	if abi.completePrepareHook != "" {
		body.completePrepare = p.pkg.NewFunc(abi.completePrepareHook, coroCompletePrepareSignature(), llssa.InC).Expr
	}
	if abi.yieldPrepareHook != "" {
		body.yieldPrepare = p.pkg.NewFunc(abi.yieldPrepareHook, coroYieldPrepareSignature(), llssa.InC).Expr
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
	body.coro = b.BeginCoro(llssa.CoroOptions{
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
		},
	})
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
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroCompletePrepareSignature() *types.Signature {
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroYieldPrepareSignature() *types.Signature {
	return coroCompletePrepareSignature()
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

func coroPreemptPollSignature() *types.Signature {
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
	stateID := c.nextState
	c.nextState++
	c.instructions = 0
	requested := b.Call(c.preemptPoll, c.task)
	c.coro.SuspendCurrentBlockIf(requested, func(suspend llssa.Builder) {
		c.publishState(suspend, coroSuspendYield, coroLifecycleSuspended, stateID)
		suspend.Call(c.yieldPrepare, c.task, c.coro.Handle(), suspend.Convert(suspend.Prog.VoidPtr(), c.header))
	})
	// The false poll edge is already active; repeating these stores there keeps
	// the joined continuation state-independent while the resumed true edge
	// clears its published yield state before executing source instructions.
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
		b.Call(c.completePrepare, c.task, c.coro.Handle(), b.Convert(b.Prog.VoidPtr(), c.header))
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
	p.sourceParamBase = 2
	defer func() {
		p.sourceParamBase = oldBase
		p.currentCoro = oldCoro
		p.coroSourceBlocks = oldSourceBlocks
	}()

	audit, err := newCoroPhysicalPureSSAAudit(p.emissionUniverse, fn, p.compilation.CoroFrameRetentionABI)
	if err != nil {
		panic(fmt.Errorf("rebuild coroutine frame-retention proof: %w", err))
	}
	frameRetention := audit.currentFrameRetentionProof()

	b.SetBlock(p.fn.Block(0))
	physical := p.beginCoroBody(b, abi)
	physical.frameRetention = frameRetention
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
		if physical.needsPreempt {
			physical.instructions = 0
			// Every source block, including block zero, begins with a poll. A
			// child initial suspend is a scheduler boundary but not necessarily
			// a fairness boundary: pendingAwait can immediately resume a long
			// static child chain on the same G without returning to ready-queue
			// selection. Polling block zero therefore bounds that chain as well
			// as ordinary CFG paths and block-zero backedges.
			b.SetBlock(p.sourceBlock(i))
			physical.pollAndSuspendForPreempt(b)
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
	physical.complete(b)
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
	if !childAwait {
		if explicitPanic {
			return fmt.Errorf("coroutine physical ABI: function %q: explicit-status panic requires PhysicalABIV1 child-await lowering", plan.ID)
		}
		return validateCoroLeafPhysicalABI(fn, plan)
	}

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
	if plan.Demand != coro.AsyncDemand {
		return fail("requires async-only demand until root and hard-sync adapters exist, got %s", plan.Demand)
	}
	if plan.Recursive {
		return fail("recursive coroutine lowering requires child frames and preemption polls")
	}
	if plan.Exec.Contains(coro.NeedsPreempt) && !programRun {
		return fail("needs-preempt execution requires the runnable scheduler ABI")
	}
	// IRQUnsafe constrains interrupt roots; an ordinary scheduler-managed G is
	// not an IRQ context. Preserve the bit in the plan/digest while allowing the
	// CFG lowering to execute it. Thread affinity and opaque execution still
	// require scheduler protocols that this ABI does not provide.
	if unsupported := plan.Exec &^ (coro.MayUnwind | coro.NeedsPreempt | coro.IRQUnsafe); unsupported != 0 {
		return fail("execution flags %s require lowering outside the CFG physical ABI", unsupported)
	}
	if fn.Parent() != nil || len(fn.FreeVars) != 0 {
		return fail("closures require the coroutine context ABI")
	}
	if len(fn.AnonFuncs) != 0 {
		return fail("nested function literals require closure body lowering")
	}
	if fn.Recover != nil {
		return fail("recover blocks require coroutine cleanup/unwind lowering")
	}
	if fn.Signature.Recv() != nil {
		return fail("methods require descriptor and receiver ABI lowering")
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
	programEntry := programRun && isCoroProgramManagedEntry(fn)
	if fn.Synthetic != "" && !(programEntry && fn.Name() == "init" && fn.Synthetic == "package initializer") {
		return fail("synthetic function %q is outside the leaf ABI", fn.Synthetic)
	}
	if list := fn.TypeParams(); list != nil && list.Len() != 0 {
		return fail("generic declarations are not materialized coroutine bodies")
	}
	if list := fn.TypeArgs(); len(list) != 0 {
		return fail("generic instances require a frozen instantiated ABI")
	}
	if (fn.Name() == "main" || strings.HasPrefix(fn.Name(), "init")) && !programEntry {
		return fail("program roots require scheduler bootstrap lowering")
	}
	if err := validateCoroLeafPhysicalSignature(plan, fn.Signature); err != nil {
		return err
	}
	pureSSA, err := newCoroPhysicalPureSSAAudit(universe, fn, frameRetentionABI)
	if err != nil {
		return fail("cannot audit pure SSA lowering: %v", err)
	}

	returns := 0
	panics := 0
	awaits := 0
	parks := 0
	spawns := 0
	infos := blocks.Infos(fn.Blocks)
	hasCyclicBlock := false
	for _, info := range infos {
		hasCyclicBlock = hasCyclicBlock || info.InLoop
	}
	if hasCyclicBlock && !plan.Exec.Contains(coro.NeedsPreempt) {
		return fail("cyclic CFG requires needs-preempt execution classification")
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if handled, reason := pureSSA.validate(instr); handled {
				if reason != "" {
					return coroLeafInstructionError(fn, plan, instr, reason)
				}
				continue
			}
			switch instr := instr.(type) {
			case *ssa.DebugRef, *ssa.Jump:
			case *ssa.Return:
				returns++
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
			case *ssa.UnOp:
				if (instr.Op != token.SUB && instr.Op != token.XOR && instr.Op != token.NOT) || !coroLeafScalar(instr.Type()) {
					return coroLeafInstructionError(fn, plan, instr, "unsupported unary operation")
				}
			case *ssa.Call:
				if whole != nil && whole.ElidesCall(instr) {
					if universe != nil {
						rawCallee := instr.Call.StaticCallee()
						if _, frozen := universe.Resolve(rawCallee); rawCallee != nil && frozen {
							semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(instr)
							if err != nil {
								return coroLeafInstructionError(fn, plan, instr, "invalid frozen intrinsic: "+err.Error())
							}
							if intrinsic && semantics.SuspendsCurrentFrame() {
								parks++
							}
						}
					}
					// The frozen frontend proved that this declaration call emits no
					// callable edge. A structured park is counted above; ordinary
					// noinit/inline intrinsics need no await/plain entry.
					continue
				}
				callee, calleePlan, err := resolveCoroStaticAwait(whole, plan, instr)
				if err == nil {
					if err := validateCoroLeafPhysicalSignature(calleePlan, callee.Signature); err != nil {
						return coroLeafInstructionError(fn, plan, instr, "child await signature: "+err.Error())
					}
					awaits++
					continue
				}
				if explicitPanic {
					if _, targetPlan, plainErr := resolveCoroStaticPlainCall(whole, instr); plainErr == nil {
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
				target, targetPlan, err := whole.ResolveClosedStaticSpawn(instr)
				if err != nil {
					return coroLeafInstructionError(fn, plan, instr, "unsupported closed static spawn: "+err.Error())
				}
				if err := validateCoroLeafPhysicalSignature(targetPlan, target.Signature); err != nil {
					return coroLeafInstructionError(fn, plan, instr, "spawn target signature: "+err.Error())
				}
				if coroPhysicalSignatureContainsFunctionValue(target.Signature) {
					return coroLeafInstructionError(fn, plan, instr, "spawn target function-valued parameters require a later canonical transport capability")
				}
				spawns++
			default:
				return coroLeafInstructionError(fn, plan, instr, "instruction is outside the CFG physical ABI allowlist")
			}
		}
	}
	if returns == 0 {
		return fail("requires at least one return instruction")
	}
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
	if spawns != 0 && (!plan.DeclaredEffect.Contains(coro.YieldOnly) || !plan.LocalEffect.Contains(coro.YieldOnly) || !plan.Effect.Contains(coro.YieldOnly)) {
		return fail("closed static spawn body lacks its exact yield-only owner seed: declared=%s local=%s final=%s", plan.DeclaredEffect, plan.LocalEffect, plan.Effect)
	}
	if plan.DeclaredEffect.Contains(coro.MayPark) && parks == 0 {
		return fail("declared may-park effect has no exact structured park intrinsic")
	}
	if unsupported := plan.Effect &^ (coro.YieldOnly | coro.AwaitStructured | coro.MayPark); unsupported != 0 {
		return fail("child-await body has unsupported final effect %s", unsupported)
	}
	if unsupported := plan.DeclaredEffect &^ (coro.YieldOnly | coro.MayPark); unsupported != 0 {
		return fail("child-await body has unsupported declared effect %s", unsupported)
	}
	if unsupported := plan.LocalEffect &^ (coro.YieldOnly | coro.MayPark); unsupported != 0 {
		return fail("child-await body has unsupported local effect %s", unsupported)
	}
	return nil
}

func validateCoroExplicitStatusPanic(audit *coroPhysicalPureSSAAudit, instruction *ssa.Panic) string {
	if instruction == nil || instruction.X == nil {
		return "explicit-status panic requires a non-nil operand"
	}
	boxed, ok := instruction.X.(*ssa.MakeInterface)
	if !ok || boxed.X == nil {
		return "explicit-status panic requires one concrete MakeInterface operand"
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
	if _, ok := types.Unalias(source).Underlying().(*types.Pointer); !ok {
		return "explicit-status panic currently requires one concrete pointer payload"
	}
	if audit == nil {
		return "explicit-status panic requires a prepared pure-SSA audit"
	}
	if reason := audit.validateMakeInterface(boxed); reason != "" {
		return "explicit-status panic MakeInterface is not pure: " + reason
	}
	if constant, ok := boxed.X.(*ssa.Const); ok && constant.Value == nil {
		// A typed nil pointer still produces a non-nil interface type word and
		// carries no frame-owned storage in its data word.
		return ""
	}
	root, reason := audit.stableAddress(boxed.X, make(map[ssa.Value]bool))
	if reason != "" || root != coroPhysicalAddressGlobal {
		if reason == "" {
			reason = "payload is not rooted in package-global storage"
		}
		return "explicit-status panic data word may outlive its coroutine frame: " + reason
	}
	return ""
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

// resolveCoroStaticPlainCall proves the synchronous island allowed inside a
// runnable physical coroutine. The exact CallPlan must select either one
// defined primary plain body or one frozen known external plain entry, and it
// must be bounded and non-suspending. A missing/open/dynamic edge may not fall
// back to the legacy source symbol.
func resolveCoroStaticPlainCall(plan *coro.SSAPlan, call ssa.CallInstruction) (*ssa.Function, coro.FunctionPlan, error) {
	if plan == nil || call == nil || call.Common() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	common := call.Common()
	if common.IsInvoke() || common.StaticCallee() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a static non-invoke call")
	}
	callPlan, ok := plan.CallPlan(call)
	if !ok {
		return nil, coro.FunctionPlan{}, fmt.Errorf("call has no compilation CallPlan")
	}
	if callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.DirectPlain || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"requires one closed non-nil direct plain target, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	target, ok := plan.Function(callPlan.Targets[0])
	if !ok || target == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("direct plain target %q is absent from the compilation plan", callPlan.Targets[0])
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return nil, coro.FunctionPlan{}, fmt.Errorf("direct plain target %q has no canonical function plan", callPlan.Targets[0])
	}
	validBody := targetPlan.External == coro.Defined && targetPlan.Emission == coro.EmitPlain && targetPlan.Primary == coro.PrimaryPlain
	validExternal := targetPlan.External == coro.ExternalKnown && targetPlan.Emission == coro.EmitExternal && targetPlan.Primary == coro.PrimaryExternal
	unsupportedExec := targetPlan.Exec &^ (coro.MayUnwind | coro.IRQUnsafe)
	if (!validBody && !validExternal) || targetPlan.FuncRep != coro.DirectPlain || targetPlan.Effect != coro.NoSuspend ||
		targetPlan.Demand == coro.NoDemand || unsupportedExec != 0 {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"target %q is not one demanded defined-or-known-external bounded no-suspend direct plain entry (external=%s emission=%s primary=%s representation=%s effect=%s exec=%s demand=%s)",
			targetPlan.ID, targetPlan.External, targetPlan.Emission, targetPlan.Primary, targetPlan.FuncRep, targetPlan.Effect, targetPlan.Exec, targetPlan.Demand,
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
	if plan.Demand != coro.AsyncDemand {
		return fail("requires async-only demand until root and hard-sync adapters exist, got %s", plan.Demand)
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
	if fn.Parent() != nil || len(fn.FreeVars) != 0 {
		return fail("closures require the coroutine context ABI")
	}
	if len(fn.AnonFuncs) != 0 {
		return fail("nested function literals require closure body lowering")
	}
	if fn.Signature.Recv() != nil {
		return fail("methods require descriptor and receiver ABI lowering")
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
	if fn.Synthetic != "" {
		return fail("synthetic function %q is outside the leaf ABI", fn.Synthetic)
	}
	if list := fn.TypeParams(); list != nil && list.Len() != 0 {
		return fail("generic declarations are not materialized coroutine bodies")
	}
	if list := fn.TypeArgs(); len(list) != 0 {
		return fail("generic instances require a frozen instantiated ABI")
	}
	if fn.Name() == "main" || strings.HasPrefix(fn.Name(), "init") {
		return fail("program roots require scheduler bootstrap lowering")
	}
	if len(fn.Blocks) != 1 {
		return fail("requires exactly one basic block, got %d", len(fn.Blocks))
	}
	if err := validateCoroLeafPhysicalSignature(plan, fn.Signature); err != nil {
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
	return sig, nil
}

func validateCoroLeafPhysicalSignature(plan coro.FunctionPlan, sig *types.Signature) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine physical ABI: function %q: %s", plan.ID, fmt.Sprintf(format, args...))
	}
	if sig == nil {
		return fail("requires a physical source signature")
	}
	if sig.Recv() != nil {
		return fail("effective method receiver requires descriptor lowering")
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

func validateCoroPhysicalConsumers(plan *coro.SSAPlan, childAwait bool) error {
	return validateCoroPhysicalConsumersCapabilities(plan, childAwait, false)
}

func validateCoroPhysicalConsumersCapabilities(plan *coro.SSAPlan, childAwait, staticSpawn bool) error {
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
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if spawn, ok := instr.(*ssa.Go); ok {
					if !staticSpawn {
						return coroLeafInstructionError(fn, function.Plan, instr, "goroutine spawn requires scheduler root lowering")
					}
					if _, _, err := plan.ResolveClosedStaticSpawn(spawn); err != nil {
						return coroLeafInstructionError(fn, function.Plan, instr, "unsupported closed static spawn: "+err.Error())
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
					}
					callPlan, found := plan.CallPlan(call)
					if !found {
						return coroLeafInstructionError(fn, function.Plan, instr, "call has no compilation CallPlan")
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
						if _, isCoroutine := coroutineIDs[target]; isCoroutine {
							hasCoroutineTarget = true
							break
						}
					}
					if hasCoroutineTarget {
						direct, ordinary := call.(*ssa.Call)
						if childAwait && ordinary && function.Plan.Emission == coro.EmitCoroutine {
							if _, _, err := resolveCoroStaticAwait(plan, function.Plan, direct); err == nil {
								// The static callee operand is represented by this exact
								// CallPlan and is not an escaped function value.
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
						return coroLeafInstructionError(fn, function.Plan, instr, "coroutine function value requires physical representation conversion")
					}
				}
			}
		}
	}
	return nil
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
	return fmt.Errorf("coroutine physical ABI: function %q: %T at %s: %s", plan.ID, instr, where, reason)
}
