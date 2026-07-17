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
	coroProgramFramePublishHookV1     = "__llgo_coro_frame_publish_v1"
	coroProgramCompletePrepareHookV1  = "__llgo_coro_complete_prepare_v1"
	coroProgramFrameFreeHookV1        = "__llgo_coro_frame_free_v1"
	coroProgramPhysicalABIVersionV1   = 1
	coroProgramSuspendNoneV1          = 0
	coroProgramSuspendCallV1          = 1
	coroProgramSuspendFrameCompleteV1 = 2
	coroProgramLifecycleInitialV1     = 1
	coroProgramLifecycleActiveV1      = 2
	coroProgramLifecycleSuspendedV1   = 3
	coroProgramLifecycleFinalV1       = 4
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

// emitCoroProgramBootstrapFactoryV1 defines the compiler-owned program-root
// coroutine. The caller supplies the exact two target declarations used by the
// already validated bootstrap table; the factory deliberately does not look up
// symbols or rediscover startup semantics from the LLVM module.
//
// The third physical parameter is the v1 startup payload. This first runnable
// boundary has an empty startup payload, so the parameter is required to be nil
// by the caller and is intentionally never read or otherwise materialized in
// the generated body. The result payload is also empty; out is nevertheless
// published in HeaderV1.ResultSlot so the frame contract remains identical to
// later result-bearing roots.
func emitCoroProgramBootstrapFactoryV1(
	pkg llssa.Package,
	bootstrap *coroProgramBootstrapV1,
	targets [2]llssa.Function,
	finalHash [16]byte,
) llssa.Function {
	validateCoroProgramBootstrapFactoryV1(pkg, bootstrap, targets)

	prog := pkg.Prog
	pointer := types.Typ[types.UnsafePointer]
	factory := pkg.NewFunc(coroProgramBootstrapFactorySymbolV1, newSignature(
		[]types.Type{pointer, pointer, pointer},
		[]types.Type{pointer},
	), llssa.InC)
	if factory.HasBody() {
		panic(fmt.Sprintf("coroutine program bootstrap factory symbol %q already has a body", coroProgramBootstrapFactorySymbolV1))
	}
	factoryValue := pkg.Module().NamedFunction(coroProgramBootstrapFactorySymbolV1)
	factoryValue.SetVisibility(llvm.HiddenVisibility)

	emptyPayload := prog.Struct()
	descriptor := pkg.NewCoroFrameDescriptor(
		coroProgramBootstrapFrameDescriptorPrefixV1+hex.EncodeToString(finalHash[:]),
		llssa.CoroFrameDescriptorOptions{
			Version: coroProgramPhysicalABIVersionV1,
			ABIHash: finalHash,
			Result:  emptyPayload,
		},
	)

	b := factory.MakeBody(1)
	g := factory.Param(0)
	out := factory.Param(1)
	// factory.Param(2) is the empty startup payload and must remain unused.
	null := prog.Nil(prog.VoidPtr())
	descriptorPointer := b.Convert(prog.VoidPtr(), descriptor)
	headerType := coroProgramBootstrapHeaderTypeV1(prog)
	header := b.AllocaT(headerType)

	alloc := pkg.NewFunc(coroProgramFrameAllocHookV1, newSignature(
		[]types.Type{pointer, types.Typ[types.Uintptr], types.Typ[types.Uintptr], pointer},
		[]types.Type{pointer},
	), llssa.InC)
	publish := pkg.NewFunc(coroProgramFramePublishHookV1, newSignature(
		[]types.Type{pointer, pointer, pointer, pointer}, nil,
	), llssa.InC)
	complete := pkg.NewFunc(coroProgramCompletePrepareHookV1, newSignature(
		[]types.Type{pointer, pointer, pointer}, nil,
	), llssa.InC)
	free := pkg.NewFunc(coroProgramFrameFreeHookV1, newSignature(
		[]types.Type{pointer, pointer, types.Typ[types.Uintptr], types.Typ[types.Uintptr], pointer}, nil,
	), llssa.InC)
	runDecisionTake := declareCoroProgramRunDecisionTakeV1(pkg)

	frame := llssa.CoroFrameOps{
		Alloc: func(b llssa.Builder, size, align llssa.Expr) llssa.Expr {
			return b.Call(alloc.Expr, g, size, align, descriptorPointer)
		},
		Free: func(b llssa.Builder, storage, size, align llssa.Expr) {
			b.Call(free.Expr, g, storage, size, align, descriptorPointer)
		},
	}
	coro := b.BeginCoro(llssa.CoroOptions{
		Promise: header,
		Frame:   frame,
		AfterResume: func(b llssa.Builder) {
			emitCoroProgramTakeNormalRunDecisionV1(b, runDecisionTake, g)
		},
		BeforeInitialSuspend: func(b llssa.Builder, handle, storage llssa.Expr) {
			values := []llssa.Expr{
				g,
				null,
				descriptorPointer,
				null,
				out,
				prog.IntVal(coroProgramSuspendNoneV1, prog.Uint16()),
				prog.IntVal(coroProgramLifecycleInitialV1, prog.Uint16()),
				prog.IntVal(0, prog.Uint32()),
				prog.IntVal(0, prog.Uint32()),
			}
			for index, value := range values {
				b.Store(b.FieldAddr(header, index), value)
			}
			b.Call(publish.Expr, g, handle, b.Convert(prog.VoidPtr(), header), storage)
		},
	})

	b.SetBlock(coro.InitialResumeBlock())
	b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendNoneV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleActiveV1, prog.Uint16()))
	b.Call(targets[0].Expr)
	b.Call(targets[1].Expr)

	b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendFrameCompleteV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleFinalV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderStateIDV1), prog.IntVal(1, prog.Uint32()))
	b.Call(complete.Expr, g, coro.Handle(), b.Convert(prog.VoidPtr(), header))
	coro.Finish()
	b.Dispose()
	return factory
}

// emitCoroProgramBootstrapFactoryV2 defines the compiler-owned heterogeneous
// startup coroutine. DirectPlain steps are statically called. CoroRoot steps
// load the exact validated descriptor factory from their bound package
// anchor/index, create an initial-suspended child, and reuse the ordinary v1
// parent/await scheduler handoff. The runtime never chooses or invokes a user
// function pointer; the compiler emits this fixed five-stage program.
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
			Version: coroProgramPhysicalABIVersionV1,
			ABIHash: finalHash,
			Result:  emptyPayload,
		},
	)

	b := factory.MakeBody(1)
	g := factory.Param(0)
	out := factory.Param(1)
	null := prog.Nil(prog.VoidPtr())
	descriptorPointer := b.Convert(prog.VoidPtr(), descriptor)
	headerType := coroProgramBootstrapHeaderTypeV1(prog)
	header := b.AllocaT(headerType)

	alloc := pkg.NewFunc(coroProgramFrameAllocHookV1, newSignature(
		[]types.Type{pointer, types.Typ[types.Uintptr], types.Typ[types.Uintptr], pointer},
		[]types.Type{pointer},
	), llssa.InC)
	publish := pkg.NewFunc(coroProgramFramePublishHookV1, newSignature(
		[]types.Type{pointer, pointer, pointer, pointer}, nil,
	), llssa.InC)
	await := pkg.NewFunc("__llgo_coro_await_prepare_v1", newSignature(
		[]types.Type{pointer, pointer, pointer}, nil,
	), llssa.InC)
	complete := pkg.NewFunc(coroProgramCompletePrepareHookV1, newSignature(
		[]types.Type{pointer, pointer, pointer}, nil,
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
			values := []llssa.Expr{
				g,
				null,
				descriptorPointer,
				null,
				out,
				prog.IntVal(coroProgramSuspendNoneV1, prog.Uint16()),
				prog.IntVal(coroProgramLifecycleInitialV1, prog.Uint16()),
				prog.IntVal(0, prog.Uint32()),
				prog.IntVal(0, prog.Uint32()),
			}
			for index, value := range values {
				b.Store(b.FieldAddr(header, index), value)
			}
			b.Call(publish.Expr, g, handle, b.Convert(prog.VoidPtr(), header), storage)
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
			b.Call(await.Expr, g, coroBuilder.Handle(), child)
			coroBuilder.SuspendCurrentBlock()
			b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendNoneV1, prog.Uint16()))
			b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleActiveV1, prog.Uint16()))
		}
		// This is deliberately the normal continuation of the exact V2 main
		// step, not an entry-module call after program_run. A panic or Goexit
		// terminal path never returns through this point, so it cannot be
		// mistaken for command-main return and cannot cancel background Gs.
		if mainReturn != nil && step.Role == coroProgramStepRoleMainV2 {
			b.Call(mainReturn.Expr, g)
		}
	}

	b.Store(b.FieldAddr(header, coroProgramHeaderSuspendReasonV1), prog.IntVal(coroProgramSuspendFrameCompleteV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderLifecycleV1), prog.IntVal(coroProgramLifecycleFinalV1, prog.Uint16()))
	b.Store(b.FieldAddr(header, coroProgramHeaderStateIDV1), prog.IntVal(uint64(len(bootstrap.Steps)+1), prog.Uint32()))
	b.Call(complete.Expr, g, coroBuilder.Handle(), b.Convert(prog.VoidPtr(), header))
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
	if bootstrap == nil || bootstrap.abiVersion() != coroProgramBootstrapVersionV2 || len(bootstrap.Steps) != 5 || len(targets) != 5 {
		panic("coroutine program bootstrap v2 factory requires exactly five validated steps")
	}
	roles := [...]uint32{
		coroProgramStepRoleRuntimeInitV2,
		coroProgramStepRoleABIInitV2,
		coroProgramStepRolePublicRuntimeInitV2,
		coroProgramStepRolePackageInitV2,
		coroProgramStepRoleMainV2,
	}
	for index, step := range bootstrap.Steps {
		target := targets[index]
		if step.Role != roles[index] || step.FunctionID == "" || step.Target == "" {
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
		prog.Uint32(),  // Flags
	)
}

func validateCoroProgramBootstrapFactoryV1(
	pkg llssa.Package, bootstrap *coroProgramBootstrapV1, targets [2]llssa.Function,
) {
	if pkg == nil || pkg.Prog == nil {
		panic("coroutine program bootstrap factory requires an LLVM package")
	}
	if bootstrap == nil || len(bootstrap.Steps) != len(targets) {
		panic("coroutine program bootstrap factory requires exactly two validated steps")
	}
	roles := [...]uint32{coroProgramStepRoleInitV1, coroProgramStepRoleMainV1}
	for index, step := range bootstrap.Steps {
		target := targets[index]
		if step.Kind != coroProgramStepDirectPlainV1 || step.Role != roles[index] || step.Aux != 0 {
			panic(fmt.Sprintf("coroutine program bootstrap factory step %d is not canonical DirectPlain Init/Main", index))
		}
		if step.FunctionID == "" || step.Target == "" || target == nil || target.Pkg != pkg || target.Name() != step.Target {
			panic(fmt.Sprintf("coroutine program bootstrap factory step %d target does not match %q", index, step.Target))
		}
		sig, ok := target.RawType().(*types.Signature)
		if !ok || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 0 || sig.Results().Len() != 0 {
			panic(fmt.Sprintf("coroutine program bootstrap factory step %d target %q does not have the exact void() C ABI", index, step.Target))
		}
	}
}
