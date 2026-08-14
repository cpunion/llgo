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

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroWorkerParkHookV1                  = "__llgo_coro_worker_park_v1"
	coroWorkerResumeHookV1                = "__llgo_coro_worker_resume_v1"
	coroHostOperationParkHookV1           = "__llgo_coro_host_operation_park_v1"
	coroHostOperationResumeHookV1         = "__llgo_coro_host_operation_resume_v1"
	coroHostOperationDeadlineParkHookV1   = "__llgo_coro_host_operation_deadline_park_v1"
	coroHostOperationDeadlineResumeHookV1 = "__llgo_coro_host_operation_deadline_resume_v1"
	coroOSThreadLockedHookV1              = "__llgo_coro_os_thread_locked_v1"
	coroOSThreadForeignCallHookV1         = "__llgo_coro_os_thread_foreign_call_v1"
	coroNativeSyscallCallHookV1           = "__llgo_coro_native_syscall_call_v1"
)

const (
	coroHostOperationDeadlineFlagV1          = uint64(1 << 31)
	coroHostOperationDeadlineMetadataWordsV1 = 6
)

const (
	coroWorkerResumeSuccessV1 uint64 = iota + 1
	coroWorkerResumeTaskAbortV1
	coroWorkerResumeShutdownV1
	coroWorkerResumeFaultMemoryV1
	coroWorkerResumeFaultDivideV1
)

const (
	coroHostOperationResumeSuccessV1 uint64 = iota + 1
	coroHostOperationResumeTaskAbortV1
	coroHostOperationResumeShutdownV1
)

func coroWorkerParkSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "function", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "traceTarget", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "argc", types.Typ[types.Uint32]),
	}
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		params = append(params, types.NewParam(token.NoPos, nil, fmt.Sprintf("a%d", index), types.Typ[types.Uintptr]))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), nil, false)
}

func coroWorkerResumeSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	wordPointer := types.NewPointer(types.Typ[types.Uintptr])
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "r1", wordPointer),
		types.NewParam(token.NoPos, nil, "r2", wordPointer),
		types.NewParam(token.NoPos, nil, "errno", wordPointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroHostOperationParkSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "opcode", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "argc", types.Typ[types.Uint32]),
	}
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		params = append(params, types.NewParam(token.NoPos, nil, fmt.Sprintf("a%d", index), types.Typ[types.Uintptr]))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), nil, false)
}

func coroHostOperationResumeSignature() *types.Signature {
	return coroWorkerResumeSignature()
}

func coroHostOperationDeadlineParkSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "state", pointer),
		types.NewParam(token.NoPos, nil, "opcode", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "argc", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "deadlineLo", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "deadlineHi", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "timeoutErrno", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "controlKey", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "controlLane", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "controlEpoch", types.Typ[types.Uintptr]),
	}
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		params = append(params, types.NewParam(token.NoPos, nil, fmt.Sprintf("a%d", index), types.Typ[types.Uintptr]))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), nil, false)
}

func coroOSThreadLockedSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(types.NewParam(token.NoPos, nil, "g", pointer))
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "locked", types.Typ[types.Bool]))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func coroOSThreadForeignCallSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	wordPointer := types.NewPointer(types.Typ[types.Uintptr])
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "function", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "traceTarget", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "argc", types.Typ[types.Uint32]),
	}
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		params = append(params, types.NewParam(
			token.NoPos, nil, fmt.Sprintf("a%d", index), types.Typ[types.Uintptr],
		))
	}
	params = append(params,
		types.NewParam(token.NoPos, nil, "r1", wordPointer),
		types.NewParam(token.NoPos, nil, "r2", wordPointer),
		types.NewParam(token.NoPos, nil, "errno", wordPointer),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]))
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), results, false)
}

func (p *context) requireCoroWorkerBody(b llssa.Builder) *coroBodyContext {
	body := p.coroBody()
	if body == nil || b.Func != p.fn {
		panic("coroutine worker lowering requires an active planned physical coroutine body")
	}
	if body.abi.version < coroPhysicalABIVersionV1 || body.completion == nil ||
		body.finalSuspend == nil || body.unsupportedRunDecision == nil {
		panic("coroutine worker lowering requires the complete PhysicalABIV1 scheduler ABI")
	}
	return body
}

type coroWorkerWordResultV1 struct {
	r1    llssa.Expr
	r2    llssa.Expr
	errno llssa.Expr
}

type coroHostWordOperationV1 struct {
	metadata []llssa.Expr
}

// emitCoroSameMWordCall is the shared synchronous status transaction for the
// two runtime boundaries which execute a word call on the current native M.
// The caller owns result storage so a locked-foreign direct branch can share it
// with its worker branch, while a native syscall outcome can use native-stack
// storage without acquiring a coroutine frame.
func (p *context) emitCoroSameMWordCall(
	b llssa.Builder,
	hookName string,
	function llssa.Expr,
	traceTarget llssa.Expr,
	args []llssa.Expr,
	r1, r2, errno llssa.Expr,
) {
	if p == nil {
		panic("same-M word call requires one active structured body and exact storage")
	}
	task := p.managedPhysicalTask()
	if !p.hasStructuredOutcomePhysicalBody() || b == nil || b.Func != p.fn ||
		task.IsNil() || function.IsNil() || traceTarget.IsNil() ||
		r1.IsNil() || r2.IsNil() || errno.IsNil() || len(args) > coroWorkerMaxArgsV1 {
		panic("same-M word call requires one active structured body and exact storage")
	}
	switch hookName {
	case coroNativeSyscallCallHookV1:
	case coroOSThreadForeignCallHookV1:
		if !p.hasCoroPhysicalBody() {
			panic("locked foreign word call requires a full coroutine body")
		}
	default:
		panic("same-M word call received an unsupported runtime boundary")
	}
	word := p.prog.Uintptr()
	if !types.Identical(function.RawType(), word.RawType()) ||
		!types.Identical(traceTarget.RawType(), word.RawType()) {
		panic("same-M function or trace target is not uintptr-shaped")
	}
	for index, argument := range args {
		if argument.IsNil() || !types.Identical(argument.RawType(), word.RawType()) {
			panic(fmt.Sprintf("same-M word call argument %d is not uintptr-shaped", index))
		}
	}

	zero := p.prog.Zero(word)
	directArgs := make([]llssa.Expr, 0, 4+coroWorkerMaxArgsV1+3)
	directArgs = append(directArgs,
		task,
		function,
		traceTarget,
		p.prog.IntVal(uint64(len(args)), p.prog.Uint32()),
	)
	for index := 0; index < coroWorkerMaxArgsV1; index++ {
		if index < len(args) {
			directArgs = append(directArgs, args[index])
		} else {
			directArgs = append(directArgs, zero)
		}
	}
	directArgs = append(directArgs, r1, r2, errno)
	direct := p.pkg.NewFunc(hookName, coroOSThreadForeignCallSignature(), llssa.InC)
	status := b.Call(direct.Expr, directArgs...)
	normal := b.Func.MakeBlock()
	memoryFault := b.Func.MakeBlock()
	divideFault := b.Func.MakeBlock()
	invalid := b.Func.MakeBlock()
	dispatch := b.Switch(status, invalid)
	dispatch.Case(p.prog.IntVal(coroWorkerResumeSuccessV1, p.prog.Uint32()), normal)
	dispatch.Case(p.prog.IntVal(coroWorkerResumeFaultMemoryV1, p.prog.Uint32()), memoryFault)
	dispatch.Case(p.prog.IntVal(coroWorkerResumeFaultDivideV1, p.prog.Uint32()), divideFault)
	dispatch.End(b)
	b.SetBlockEx(memoryFault, llssa.AtEnd, false)
	p.compileCoroTerminalFault(b, coroFaultNilV1)
	b.SetBlockEx(divideFault, llssa.AtEnd, false)
	p.compileCoroTerminalFault(b, coroFaultIntegerDivideByZeroV1)
	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()
	b.SetBlockContinuation(normal)
}

// compileCoroNativeSyscallWordCall is the synchronous native-M half of a
// certified llgo.syscall operation. The runtime hook releases and reacquires
// the managed execution domain around the blocking call, but it never parks
// this LLVM coroutine. Consequently the same transaction is valid in either a
// full coroutine body or its exact static-outcome twin.
func (p *context) compileCoroNativeSyscallWordCall(
	b llssa.Builder,
	function llssa.Expr,
	traceTarget llssa.Expr,
	args []llssa.Expr,
	keepalives []llssa.Expr,
) coroWorkerWordResultV1 {
	word := p.prog.Uintptr()
	r1 := b.Alloc(word, false)
	r2 := b.Alloc(word, false)
	errno := b.Alloc(word, false)
	p.emitCoroSameMWordCall(
		b, coroNativeSyscallCallHookV1, function, traceTarget, args, r1, r2, errno,
	)
	if len(keepalives) != 0 {
		b.KeepAlive(keepalives...)
	}
	return coroWorkerWordResultV1{r1: b.Load(r1), r2: b.Load(r2), errno: b.Load(errno)}
}

// compileCoroHostOperation lowers one source-style synchronous host request
// into the shared scalar external-operation source. Unlike a native worker it
// has no current-thread/direct branch: only a later host turn owns the
// operation. Pointer-shaped operands remain typed in compiler-owned frame
// slots until exact completion, while the host catalog receives copied words.
func (p *context) compileCoroHostOperation(
	b llssa.Builder,
	args []ssa.Value,
	results *types.Tuple,
	shape coroHostOperationCallShape,
) llssa.Expr {
	if !shape.valid() ||
		len(args) != 1+int(shape.metadataWords)+int(shape.argumentCount) {
		panic("coroutine host operation disagrees with its frozen ProgramIR shape")
	}
	word := p.prog.Uintptr()
	metadata := make([]llssa.Expr, int(shape.metadataWords))
	for index := range metadata {
		metadata[index] = p.compileValue(b, args[index+1])
		if !types.Identical(metadata[index].RawType(), word.RawType()) {
			panic(fmt.Sprintf("coroutine host operation metadata %d is not word-shaped", index))
		}
	}
	physicalWords := make([]llssa.Expr, 0, int(shape.argumentCount))
	keepaliveSlots := make([]llssa.Expr, 0, int(shape.argumentCount))
	for index, argument := range args[1+int(shape.metadataWords):] {
		value := p.compileValue(b, argument)
		if shape.pointerMask&(uint16(1)<<index) != 0 {
			slot := p.coroFrameAlloc(value.Type)
			b.Store(slot, value)
			keepaliveSlots = append(keepaliveSlots, slot)
			value = b.Convert(word, value)
		}
		if !types.Identical(value.RawType(), word.RawType()) {
			panic(fmt.Sprintf("coroutine host operation argument %d is not word-shaped", index))
		}
		physicalWords = append(physicalWords, value)
	}
	result := p.compileCoroWorkerWordCall(
		b,
		p.prog.IntVal(uint64(shape.opcode), p.prog.Uint32()),
		llssa.Expr{},
		physicalWords,
		keepaliveSlots,
		&coroHostWordOperationV1{metadata: metadata},
	)
	return b.Aggregate(
		p.type_(results, llssa.InGo),
		result.r1,
		result.r2,
		result.errno,
	)
}

// compileCoroWorkerWordCall is the one physically suspending ForeignWait
// transaction used by host operations and exact ordinary C-call thunks.
// function always names a uniform uintptr (...uintptr) thunk whose arity is
// len(args); typed foreign declarations are never called through this ABI
// directly. Certified native syscalls use compileCoroNativeSyscallWordCall and
// never enter this park/resume transaction.
func (p *context) compileCoroWorkerWordCall(
	b llssa.Builder,
	function llssa.Expr,
	traceTarget llssa.Expr,
	args []llssa.Expr,
	keepaliveSlots []llssa.Expr,
	host *coroHostWordOperationV1,
) coroWorkerWordResultV1 {
	body := p.requireCoroWorkerBody(b)
	if function.IsNil() || host == nil && traceTarget.IsNil() || host != nil && !traceTarget.IsNil() ||
		len(args) > coroWorkerMaxArgsV1 ||
		host != nil && len(host.metadata) != 0 &&
			len(host.metadata) != coroHostOperationDeadlineMetadataWordsV1 {
		panic("coroutine worker word call received an invalid function or argument count")
	}
	word := p.prog.Uintptr()
	keyType := word
	if host != nil {
		keyType = p.prog.Uint32()
	}
	if !types.Identical(function.RawType(), keyType.RawType()) {
		panic("coroutine external word operation has the wrong key type")
	}
	if host == nil && !types.Identical(traceTarget.RawType(), word.RawType()) {
		panic("coroutine worker trace target is not uintptr-shaped")
	}
	for index, argument := range args {
		if argument.IsNil() || !types.Identical(argument.RawType(), word.RawType()) {
			panic(fmt.Sprintf("coroutine worker word call argument %d is not uintptr-shaped", index))
		}
	}

	stateType := "CoroWorkerParkV1"
	parkHook := coroWorkerParkHookV1
	parkSignature := coroWorkerParkSignature()
	resumeHook := coroWorkerResumeHookV1
	resumeSignature := coroWorkerResumeSignature()
	normal := coroWorkerResumeSuccessV1
	abort := coroWorkerResumeTaskAbortV1
	shutdown := coroWorkerResumeShutdownV1
	faults := []coroParkFaultRoute{
		{status: coroWorkerResumeFaultMemoryV1, kind: coroFaultNilV1},
		{status: coroWorkerResumeFaultDivideV1, kind: coroFaultIntegerDivideByZeroV1},
	}
	metadata := []llssa.Expr(nil)
	if host != nil {
		stateType = "CoroHostOperationParkV1"
		parkHook = coroHostOperationParkHookV1
		parkSignature = coroHostOperationParkSignature()
		resumeHook = coroHostOperationResumeHookV1
		resumeSignature = coroHostOperationResumeSignature()
		normal = coroHostOperationResumeSuccessV1
		abort = coroHostOperationResumeTaskAbortV1
		shutdown = coroHostOperationResumeShutdownV1
		faults = nil
		metadata = host.metadata
		if len(metadata) != 0 {
			stateType = "CoroHostOperationDeadlineParkV1"
			parkHook = coroHostOperationDeadlineParkHookV1
			parkSignature = coroHostOperationDeadlineParkSignature()
			resumeHook = coroHostOperationDeadlineResumeHookV1
		}
	}
	r1 := b.Alloc(p.prog.Uintptr(), false)
	r2 := b.Alloc(p.prog.Uintptr(), false)
	errno := b.Alloc(p.prog.Uintptr(), false)
	zero := p.prog.Zero(p.prog.Uintptr())
	argcValue := p.prog.IntVal(uint64(len(args)), p.prog.Uint32())

	emitPark := func(worker llssa.Builder) {
		state := worker.Alloc(p.prog.RuntimeType(stateType), false)
		physicalArgs := make([]llssa.Expr, 0, 7+len(metadata)+coroWorkerMaxArgsV1)
		physicalArgs = append(physicalArgs,
			body.task,
			body.coro.Handle(),
			worker.Convert(worker.Prog.VoidPtr(), body.header),
			worker.Convert(worker.Prog.VoidPtr(), state),
			function,
		)
		if host == nil {
			physicalArgs = append(physicalArgs, traceTarget)
		}
		physicalArgs = append(physicalArgs, argcValue)
		physicalArgs = append(physicalArgs, metadata...)
		for index := 0; index < coroWorkerMaxArgsV1; index++ {
			if index < len(args) {
				physicalArgs = append(physicalArgs, args[index])
			} else {
				physicalArgs = append(physicalArgs, zero)
			}
		}
		body.emitCoroParkOperation(p, worker, coroParkOperation{
			prepare: func(active llssa.Builder, _, _ uint32) llssa.Expr {
				return active.Prog.BoolVal(true)
			},
			park: func(suspend llssa.Builder) {
				park := p.pkg.NewFunc(parkHook, parkSignature, llssa.InC)
				suspend.Call(park.Expr, physicalArgs...)
			},
			resume: func(resume llssa.Builder) llssa.Expr {
				hook := p.pkg.NewFunc(resumeHook, resumeSignature, llssa.InC)
				return resume.Call(
					hook.Expr,
					body.task,
					resume.Convert(resume.Prog.VoidPtr(), state),
					r1,
					r2,
					errno,
				)
			},
			normal:   []uint64{normal},
			faults:   faults,
			abort:    abort,
			shutdown: shutdown,
		})
	}
	if host != nil {
		emitPark(b)
		p.emitCoroKeepaliveSlots(b, keepaliveSlots)
		return coroWorkerWordResultV1{r1: b.Load(r1), r2: b.Load(r2), errno: b.Load(errno)}
	}

	join := b.Func.MakeBlock()
	lockedHook := p.pkg.NewFunc(coroOSThreadLockedHookV1, coroOSThreadLockedSignature(), llssa.InC)
	locked := b.Call(lockedHook.Expr, body.task)
	directBlock := b.Func.MakeBlock()
	workerBlock := b.Func.MakeBlock()
	b.If(locked, directBlock, workerBlock)
	b.SetBlockEx(directBlock, llssa.AtEnd, false)
	p.emitCoroSameMWordCall(
		b, coroOSThreadForeignCallHookV1, function, traceTarget, args, r1, r2, errno,
	)
	b.Jump(join)
	b.SetBlockEx(workerBlock, llssa.AtEnd, false)
	emitPark(b)
	b.Jump(join)
	b.SetBlockContinuation(join)
	// Keep every independently proved typed owner live until direct return or
	// worker completion has selected this normal path; llvm.fake.use emits no
	// machine code but forces CoroSplit to retain the values in the frame.
	p.emitCoroKeepaliveSlots(b, keepaliveSlots)
	return coroWorkerWordResultV1{r1: b.Load(r1), r2: b.Load(r2), errno: b.Load(errno)}
}

// compileCoroCallKeepaliveSlots spills the exact typed owners which the
// frame-retention proof binds to one suspending call into ramp-entry slots.
// Compiler-owned resume/cancellation dispatch can enter a continuation through
// an edge on which the source SSA value does not dominate. Reloading the slot
// in that continuation preserves both valid LLVM SSA and the typed owner until
// the physical completion/retirement boundary.
func (p *context) coroCallKeepaliveSources(call ssa.CallInstruction) []ssa.Value {
	plan := p.coroEmissionPlan()
	if plan == nil || plan.frameRetention == nil || call == nil {
		return nil
	}
	return plan.frameRetention.exactCallKeepaliveSources(call)
}

func (p *context) coroCallKeepaliveStorageType(source ssa.Value) llssa.Type {
	if p == nil || source == nil {
		panic("coroutine keepalive storage requires one exact source value")
	}
	if coroFrameRetentionIntegerLike(source.Type()) {
		return p.prog.VoidPtr()
	}
	return p.type_(source.Type(), llssa.InGo)
}

func (p *context) compileCoroCallKeepaliveValues(
	b llssa.Builder,
	call ssa.CallInstruction,
) []llssa.Expr {
	sources := p.coroCallKeepaliveSources(call)
	values := make([]llssa.Expr, len(sources))
	for index, source := range sources {
		value := p.compileValue(b, source)
		if coroFrameRetentionIntegerLike(source.Type()) {
			// uintptr transports retain exact pointer provenance only under the
			// selected non-moving conservative/no-GC profile. Re-type the copied
			// word as a pointer in the compiler-owned keepalive slot so the frame
			// carries an address-shaped root rather than an optimizer-only integer.
			value = b.Convert(p.prog.VoidPtr(), value)
		}
		values[index] = value
	}
	return values
}

func (p *context) compileCoroCallKeepaliveSlots(b llssa.Builder, call ssa.CallInstruction) []llssa.Expr {
	sources := p.coroCallKeepaliveSources(call)
	values := p.compileCoroCallKeepaliveValues(b, call)
	if len(values) != len(sources) {
		panic("coroutine keepalive values lost their exact sources")
	}
	slots := make([]llssa.Expr, len(values))
	for index, value := range values {
		// The source-language storage type is intentionally independent from
		// the transient expression type. In particular a static *ssa.Function
		// compiles to an LLVM function declaration, which cannot itself be an
		// alloca element; storing it in its Go function carrier performs the
		// same declaration-to-{code, environment} conversion as an ordinary
		// assignment while retaining the exact source across suspension.
		slots[index] = p.coroFrameAlloc(p.coroCallKeepaliveStorageType(sources[index]))
		b.Store(slots[index], value)
	}
	return slots
}

func (p *context) emitCoroKeepaliveSlots(b llssa.Builder, slots []llssa.Expr) {
	values := make([]llssa.Expr, len(slots))
	for index, slot := range slots {
		if slot.IsNil() {
			panic("coroutine keepalive contains a nil frame slot")
		}
		values[index] = b.Load(slot)
	}
	b.KeepAlive(values...)
}

func (p *context) coroWorkerOrdinaryCall(common *ssa.CallCommon) *ssa.Call {
	if p == nil || p.goFn == nil || common == nil {
		return nil
	}
	for _, block := range p.goFn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && &call.Call == common {
				return call
			}
		}
	}
	return nil
}

// compileCoroSyscallOperation lowers one source-style synchronous llgo.syscall
// family operation through its frozen target recipe. A certified native target
// blocks synchronously on the current M after releasing the execution domain;
// a worker-only target uses the full park/resume transaction. All conventions
// share the same result and errno projection.
func (p *context) compileCoroSyscallOperation(
	b llssa.Builder,
	call *ssa.CallCommon,
	args []ssa.Value,
	results *types.Tuple,
	convention syscallFailureConvention,
	semantics CoroIntrinsicCallSemantics,
) llssa.Expr {
	direct := p.coroWorkerOrdinaryCall(call)
	if direct == nil {
		panic("coroutine syscall lowering lost its exact source call")
	}
	compiled := make([]llssa.Expr, len(args))
	for index, argument := range args {
		compiled[index] = p.compileValue(b, argument)
	}
	var result coroWorkerWordResultV1
	switch semantics {
	case CoroIntrinsicCallInlineNativeBlock:
		keepalives := p.compileCoroCallKeepaliveValues(b, direct)
		result = p.compileCoroNativeSyscallWordCall(
			b, compiled[0], compiled[0], compiled[1:], keepalives,
		)
	case CoroIntrinsicCallInlineSuspend:
		keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, direct)
		result = p.compileCoroWorkerWordCall(
			b, compiled[0], compiled[0], compiled[1:], keepaliveSlots, nil,
		)
	default:
		panic(fmt.Sprintf("coroutine syscall lowering received incompatible semantics %d", semantics))
	}
	errnoValue := p.filterSyscallErrno(b, result.r1, result.errno, convention)
	return b.Aggregate(p.type_(results, llssa.InGo), result.r1, result.r2, errnoValue)
}
