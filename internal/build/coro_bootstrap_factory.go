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

package build

import (
	"encoding/hex"
	"fmt"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	llvm "github.com/xgo-dev/llvm"
)

const (
	coroProgramFrameAllocHookV1       = "__llgo_coro_frame_alloc_v1"
	coroProgramFramePublishHookV3     = "__llgo_coro_frame_publish_v3"
	coroProgramAwaitPrepareHookV2     = "__llgo_coro_await_prepare_v2"
	coroProgramAwaitConsumeHookV1     = "__llgo_coro_await_consume_v1"
	coroProgramPanicPrepareHookV1     = "__llgo_coro_panic_prepare_v1"
	coroProgramCompletePrepareHookV2  = "__llgo_coro_complete_prepare_v2"
	coroProgramFrameFreeHookV1        = "__llgo_coro_frame_free_v1"
	coroProgramPhysicalABIVersionV1   = 1
	coroProgramSuspendNoneV1          = 0
	coroProgramSuspendCallV1          = 1
	coroProgramSuspendFrameCompleteV1 = 2
	coroProgramSuspendPanicV1         = 5
	coroProgramLifecycleInitialV1     = 1
	coroProgramLifecycleActiveV1      = 2
	coroProgramLifecycleSuspendedV1   = 3
	coroProgramLifecycleFinalV1       = 4
	coroProgramFrameTraceHiddenV1     = 1 << 0
	coroProgramCompletionReturnV1     = 1
	coroProgramCompletionPanicV1      = 2
	coroProgramCompletionAbortV1      = 3
	coroProgramCompletionShutdownV1   = 4
	coroProgramCompletionGoexitV1     = 6
	coroProgramFrameMetadataWordsV2   = 14
)

const (
	coroProgramHeaderGV1 = iota
	coroProgramHeaderParentV1
	coroProgramHeaderDescriptorV1
	coroProgramHeaderAllocationBaseV1
	coroProgramHeaderResultSlotV1
	coroProgramHeaderSuspendReasonV1
	coroProgramHeaderLifecycleV1
	coroProgramHeaderStateIDV1
	coroProgramHeaderLineV1
	coroProgramHeaderFlagsV1
)

type coroProgramBootstrapFactoryTargetV2 struct {
	Plain  llssa.Function
	Anchor llssa.Expr
}

func declareCoroProgramRunDecisionTakeV1(pkg llssa.Package) llssa.Function {
	pointer := types.Typ[types.UnsafePointer]
	uint32Type := types.Typ[types.Uint32]
	uint32Pointer := types.NewPointer(uint32Type)
	return pkg.NewFunc(coroRunDecisionTakeSymbolV1, newSignature(
		[]types.Type{
			pointer,
			uint32Type,
			uint32Type,
			uint32Pointer,
			uint32Pointer,
			uint32Pointer,
			uint32Pointer,
			uint32Pointer,
		},
		nil,
	), llssa.InC)
}

func emitCoroProgramTakeNormalRunDecisionV1(
	b llssa.Builder,
	take llssa.Function,
	g llssa.Expr,
) {
	zero := b.Prog.IntVal(0, b.Prog.Uint32())
	nilWord := b.Prog.Nil(b.Prog.Pointer(b.Prog.Uint32()))
	b.Call(
		take.Expr,
		g,
		zero,
		zero,
		nilWord,
		nilWord,
		nilWord,
		nilWord,
		nilWord,
	)
}

// emitCoroProgramBootstrapFactoryV2 defines the compiler-owned heterogeneous
// startup coroutine. DirectPlain steps are statically called. CoroRoot steps
// load the exact validated descriptor factory from their bound package
// anchor/index, create an initial-suspended child, and use the status-carrying
// parent/await scheduler handoff. The bootstrap is itself the final managed
// caller of init/main roots, so it must propagate Goexit instead of mistaking
// that terminal language outcome for an ordinary return. The runtime never
// chooses or invokes a user function pointer; the compiler emits the complete
// statically ordered startup program.
func emitCoroProgramBootstrapFactoryV2(
	pkg llssa.Package,
	bootstrap *coroProgramBootstrapV1,
	targets []coroProgramBootstrapFactoryTargetV2,
	finalHash [16]byte,
	notifyMainReturn bool,
) llssa.Function {
	validateCoroProgramBootstrapFactoryV2(pkg, bootstrap, targets)

	prog := pkg.Prog
	pointer := types.Typ[types.UnsafePointer]
	factory := pkg.NewFunc(coroProgramBootstrapFactorySymbolV2, newSignature(
		[]types.Type{pointer, pointer, pointer},
		[]types.Type{pointer},
	), llssa.InC)
	if factory.HasBody() {
		panic(fmt.Sprintf("coroutine program bootstrap factory symbol %q already has a body", coroProgramBootstrapFactorySymbolV2))
	}
	factoryValue := pkg.Module().NamedFunction(coroProgramBootstrapFactorySymbolV2)
	factoryValue.SetVisibility(llvm.HiddenVisibility)

	emptyPayload := prog.Struct()
	descriptor := pkg.NewCoroFrameDescriptor(
		coroProgramBootstrapFrameDescriptorPrefixV2+hex.EncodeToString(finalHash[:]),
		llssa.CoroFrameDescriptorOptions{
			Version:  coroProgramPhysicalABIVersionV1,
			ABIHash:  finalHash,
			Flags:    coroProgramFrameTraceHiddenV1,
			Result:   emptyPayload,
			Function: "runtime.__llgo_coro_program_bootstrap",
		},
	)

	b := factory.MakeBody(1)
	g := factory.Param(0)
	out := factory.Param(1)
	null := prog.Nil(prog.VoidPtr())
	completionTypeWord := b.AllocaT(prog.VoidPtr())
	completionDataWord := b.AllocaT(prog.VoidPtr())
	terminalStatus := b.AllocaT(prog.Uint32())
	b.Store(terminalStatus, prog.IntVal(coroProgramCompletionReturnV1, prog.Uint32()))
	descriptorPointer := b.Convert(prog.VoidPtr(), descriptor)
	headerType := coroProgramBootstrapHeaderTypeV1(prog)
	header := b.AllocaT(headerType)
	frameMetadataType := prog.Type(
		types.NewArray(types.Typ[types.Uintptr], coroProgramFrameMetadataWordsV2),
		llssa.InGo,
	)
	frameMetadata := b.AllocaT(frameMetadataType)

	alloc := pkg.NewFunc(coroProgramFrameAllocHookV1, newSignature(
		[]types.Type{pointer, types.Typ[types.Uintptr], types.Typ[types.Uintptr], pointer},
		[]types.Type{pointer},
	), llssa.InC)
	publish := pkg.NewFunc(coroProgramFramePublishHookV3, newSignature(
		[]types.Type{pointer, pointer, pointer, pointer, pointer, pointer, pointer}, nil,
	), llssa.InC)
	await := pkg.NewFunc(coroProgramAwaitPrepareHookV2, newSignature(
		[]types.Type{pointer, pointer, pointer}, nil,
	), llssa.InC)
	consume := pkg.NewFunc(coroProgramAwaitConsumeHookV1, newSignature(
		[]types.Type{pointer, pointer, pointer, pointer},
		[]types.Type{types.Typ[types.Uint32]},
	), llssa.InC)
	panicPrepare := pkg.NewFunc(coroProgramPanicPrepareHookV1, newSignature(
		[]types.Type{pointer, pointer, pointer, pointer, pointer}, nil,
	), llssa.InC)
	complete := pkg.NewFunc(coroProgramCompletePrepareHookV2, newSignature(
		[]types.Type{pointer, pointer, pointer, types.Typ[types.Uint32]}, nil,
	), llssa.InC)
	free := pkg.NewFunc(coroProgramFrameFreeHookV1, newSignature(
		[]types.Type{pointer, pointer, types.Typ[types.Uintptr], types.Typ[types.Uintptr], pointer}, nil,
	), llssa.InC)
	runDecisionTake := declareCoroProgramRunDecisionTakeV1(pkg)
	var mainReturn llssa.Function
	if notifyMainReturn {
		mainReturn = pkg.NewFunc(coroProgramMainReturnSymbolV1, newSignature(
			[]types.Type{pointer}, nil,
		), llssa.InC)
	}

	frame := llssa.CoroFrameOps{
		Alloc: func(b llssa.Builder, size, align llssa.Expr) llssa.Expr {
			return b.Call(alloc.Expr, g, size, align, descriptorPointer)
		},
		Free: func(b llssa.Builder, storage, size, align llssa.Expr) {
			b.KeepAlive(frameMetadata)
			b.Call(free.Expr, g, storage, size, align, descriptorPointer)
		},
	}
	coroBuilder := b.BeginCoro(llssa.CoroOptions{
		Promise: header,
		Frame:   frame,
		AfterResume: func(b llssa.Builder) {
			emitCoroProgramTakeNormalRunDecisionV1(b, runDecisionTake, g)
		},
		BeforeInitialSuspend: func(b llssa.Builder, handle, storage llssa.Expr) {
			b.Call(
				publish.Expr,
				g,
				handle,
				b.Convert(prog.VoidPtr(), header),
				storage,
				b.Convert(prog.VoidPtr(), frameMetadata),
				descriptorPointer,
				out,
			)
		},
	})

	b.SetBlock(coroBuilder.InitialResumeBlock())
	b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendNoneV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleActiveV1, prog.Uint16()))

	rootFactorySig := newSignature(
		[]types.Type{pointer, pointer, pointer},
		[]types.Type{pointer},
	)
	// A signature used as a value is llssa's callable vkFuncPtr shape. FuncDecl
	// is the declaration/function type used by statically named functions and
	// cannot represent the loaded opaque pointer here.
	rootFactoryType := prog.Type(rootFactorySig, llssa.InC)
	rootDescriptorType := prog.Struct(
		prog.Uint32(), prog.Uint32(), prog.Uint64(), prog.Uint64(),
		prog.VoidPtr(),
		prog.Uintptr(), prog.Uintptr(), prog.Uintptr(), prog.Uintptr(),
	)
	panicked := factory.MakeBlock()
	terminal := factory.MakeBlock()
	finish := factory.MakeBlock()
	for index, step := range bootstrap.Steps {
		target := targets[index]
		switch step.Kind {
		case coroProgramStepDirectPlainV1:
			b.Call(target.Plain.Expr)
		case coroProgramStepCoroRootV1:
			entries := b.Load(b.FieldAddr(target.Anchor, 5))
			entryPointer := b.Convert(prog.Pointer(prog.VoidPtr()), entries)
			descriptorRaw := b.Load(b.Advance(entryPointer, prog.IntVal(step.Aux, prog.Uintptr())))
			rootDescriptor := b.Convert(prog.Pointer(rootDescriptorType), descriptorRaw)
			rootFactoryRaw := b.Load(b.FieldAddr(rootDescriptor, 4))
			// LLVM uses opaque pointers, but llssa still needs the callable
			// declaration kind/signature on the expression. This is a pure type
			// retag, not a pointer-to-function Go conversion (which would leave
			// Builder.Call with a non-callable vkPtr expression).
			rootFactory := b.ChangeType(rootFactoryType, rootFactoryRaw)
			child := b.Call(rootFactory, g, null, null)
			childHeader := b.CoroPromise(child, headerType)
			b.Store(b.FieldAddr(childHeader, coroProgramHeaderParentV1), coroBuilder.Handle())
			stateID := uint64(index + 1)
			b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendCallV1, prog.Uint16()))
			b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleSuspendedV1, prog.Uint16()))
			b.Store(b.FieldAddr(header, coroProgramHeaderStateIDV1), prog.IntVal(stateID, prog.Uint32()))
			b.Store(b.FieldAddr(header, coroProgramHeaderLineV1), prog.IntVal(0, prog.Uint32()))
			b.Call(await.Expr, g, coroBuilder.Handle(), child)
			coroBuilder.SuspendCurrentBlock()
			b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendNoneV1, prog.Uint16()))
			b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleActiveV1, prog.Uint16()))
			status := b.Call(
				consume.Expr,
				g,
				coroBuilder.Handle(),
				b.Convert(prog.VoidPtr(), completionTypeWord),
				b.Convert(prog.VoidPtr(), completionDataWord),
			)
			// Return continues the statically ordered startup program. Every other
			// legal managed-child outcome terminates the bootstrap itself. Store
			// the payload-free status before dispatch so Abort, Shutdown, and
			// Goexit share the exact root completion path; Panic has a distinct
			// payload-carrying transaction below.
			b.Store(terminalStatus, status)
			returned := factory.MakeBlock()
			invalid := factory.MakeBlock()
			dispatch := b.Switch(status, invalid)
			dispatch.Case(prog.IntVal(coroProgramCompletionReturnV1, prog.Uint32()), returned)
			dispatch.Case(prog.IntVal(coroProgramCompletionPanicV1, prog.Uint32()), panicked)
			dispatch.Case(prog.IntVal(coroProgramCompletionAbortV1, prog.Uint32()), terminal)
			dispatch.Case(prog.IntVal(coroProgramCompletionShutdownV1, prog.Uint32()), terminal)
			dispatch.Case(prog.IntVal(coroProgramCompletionGoexitV1, prog.Uint32()), terminal)
			dispatch.End(b)
			b.SetBlockEx(invalid, llssa.AtEnd, false)
			b.Unreachable()
			b.SetBlockContinuation(returned)
		}
		// This is deliberately the normal continuation of the exact V2 main
		// step, not an entry-module call after program_run. A non-return terminal
		// path never returns through this point, so it cannot be
		// mistaken for command-main return and cannot cancel background Gs.
		if mainReturn != nil && step.Role == coroProgramStepRoleMainV2 {
			b.Call(mainReturn.Expr, g)
		}
	}
	b.Jump(terminal)

	b.SetBlockEx(panicked, llssa.AtEnd, false)
	b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendPanicV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleFinalV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderStateIDV1), prog.IntVal(uint64(len(bootstrap.Steps)+1), prog.Uint32()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLineV1), prog.IntVal(0, prog.Uint32()))
	b.Call(
		panicPrepare.Expr,
		g,
		coroBuilder.Handle(),
		b.Convert(prog.VoidPtr(), header),
		b.Load(completionTypeWord),
		b.Load(completionDataWord),
	)
	b.Jump(finish)

	b.SetBlockEx(terminal, llssa.AtEnd, false)

	b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendFrameCompleteV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleFinalV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderStateIDV1), prog.IntVal(uint64(len(bootstrap.Steps)+1), prog.Uint32()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLineV1), prog.IntVal(0, prog.Uint32()))
	b.Call(
		complete.Expr,
		g,
		coroBuilder.Handle(),
		b.Convert(prog.VoidPtr(), header),
		b.Load(terminalStatus),
	)
	b.Jump(finish)

	b.SetBlockContinuation(finish)
	coroBuilder.Finish()
	b.Dispose()
	return factory
}

func validateCoroProgramBootstrapFactoryV2(
	pkg llssa.Package, bootstrap *coroProgramBootstrapV1, targets []coroProgramBootstrapFactoryTargetV2,
) {
	if pkg == nil || pkg.Prog == nil {
		panic("coroutine program bootstrap v2 factory requires an LLVM package")
	}
	if bootstrap == nil || bootstrap.Version != coroProgramBootstrapVersionV2 || len(bootstrap.Steps) < 5 || len(targets) != len(bootstrap.Steps) {
		panic("coroutine program bootstrap v2 factory requires a complete validated startup program")
	}
	for index, step := range bootstrap.Steps {
		target := targets[index]
		wantRole := coroProgramStepRolePackageInitV2
		switch index {
		case 0:
			wantRole = coroProgramStepRoleRuntimeInitV2
		case 1:
			wantRole = coroProgramStepRoleABIInitV2
		case 2:
			wantRole = coroProgramStepRolePublicRuntimeInitV2
		case len(bootstrap.Steps) - 1:
			wantRole = coroProgramStepRoleMainV2
		}
		if step.Role != wantRole || step.FunctionID == "" || step.Target == "" {
			panic(fmt.Sprintf("coroutine program bootstrap v2 factory step %d has noncanonical identity or role", index))
		}
		switch step.Kind {
		case coroProgramStepDirectPlainV1:
			if step.Owner != "" || step.CatalogTarget != "" || step.Aux != 0 || target.Plain == nil || !target.Anchor.IsNil() ||
				target.Plain.Pkg != pkg || target.Plain.Name() != step.Target {
				panic(fmt.Sprintf("coroutine program bootstrap v2 direct step %d target does not match %q", index, step.Target))
			}
			sig, ok := target.Plain.RawType().(*types.Signature)
			if !ok || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 0 || sig.Results().Len() != 0 {
				panic(fmt.Sprintf("coroutine program bootstrap v2 direct step %d target %q does not have void() C ABI", index, step.Target))
			}
		case coroProgramStepCoroRootV1:
			if step.Owner == "" || step.CatalogTarget == "" || target.Plain != nil || target.Anchor.IsNil() || target.Anchor.Name() != step.CatalogTarget {
				panic(fmt.Sprintf("coroutine program bootstrap v2 coroutine step %d anchor does not match %q", index, step.CatalogTarget))
			}
		default:
			panic(fmt.Sprintf("coroutine program bootstrap v2 factory step %d has invalid kind %d", index, step.Kind))
		}
	}
}

// coroProgramBootstrapHeaderTypeV1 must remain field-for-field identical to
// runtime/internal/coro.HeaderV1 and cl's physical coroutine header.
func coroProgramBootstrapHeaderTypeV1(prog llssa.Program) llssa.Type {
	return prog.Struct(
		prog.VoidPtr(), // G
		prog.VoidPtr(), // Parent
		prog.VoidPtr(), // Descriptor
		prog.VoidPtr(), // AllocationBase
		prog.VoidPtr(), // ResultSlot
		prog.Uint16(),  // SuspendReason
		prog.Uint16(),  // Lifecycle
		prog.Uint32(),  // StateID
		prog.Uint32(),  // Line
		prog.Uint32(),  // Flags
	)
}
