//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

// Compiler-owned guards cannot require a syntactic source defer just to get
// an SSA Recover block. Exercise the real shared completion dispatcher, not a
// separate delivery state machine or a fabricated source recovery target.
func TestCoroCleanupCompletionWithoutSourceRecover(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, "package foo\nfunc Root() {}\n")
	owner := ssaPkg.Func("Root")
	if owner.Recover != nil {
		t.Fatal("fixture unexpectedly acquired a source Recover block")
	}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("cleanup", "cleanup")
	defer pkg.Module().Dispose()
	parameters := types.NewTuple(
		types.NewParam(token.NoPos, nil, "panicking", types.Typ[types.Bool]),
		types.NewParam(token.NoPos, nil, "continuation", types.Typ[types.Uint32]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.Uint32]))
	fn := pkg.NewFunc("cleanup", types.NewSignatureType(nil, nil, nil, parameters, results, false), llssa.InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	ctx := &context{prog: prog, pkg: pkg, fn: fn, goFn: owner}
	state := &coroStaticCleanupState{
		panicActive: b.AllocaT(prog.Bool()), continuation: b.AllocaT(prog.Uint32()),
		panic: fn.MakeBlock(), complete: fn.MakeBlock(),
	}
	b.Store(state.panicActive, fn.Param(0))
	b.Store(state.continuation, fn.Param(1))
	state.emitCompletionDispatch(ctx, b)
	b.SetBlock(state.panic)
	b.Return(prog.IntVal(11, prog.Uint32()))
	b.SetBlock(state.complete)
	b.Return(prog.IntVal(22, prog.Uint32()))
	b.EndBuild()
	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify implicit cleanup completion: %v\n%s", err, module.String())
	}
	switches, traps := 0, 0
	for _, block := range module.NamedFunction("cleanup").BasicBlocks() {
		end := block.LastInstruction()
		switch end.InstructionOpcode() {
		case llvm.Switch:
			switches++
			if end.SuccessorsCount() != 2 || end.GetSwitchCaseValue(1).ZExtValue() != uint64(coroStaticCleanupContinueComplete) {
				t.Fatalf("implicit cleanup switch must have only complete=%d and the invalid default:\n%s", coroStaticCleanupContinueComplete, end.String())
			}
		case llvm.Unreachable:
			traps++
		}
	}
	if switches != 1 || traps != 1 {
		t.Fatalf("completion switches=%d traps=%d, want one exact complete case and one invalid-continuation trap", switches, traps)
	}
}

// A bounded hidden helper can select the synchronous outcome ABI instead of a
// coroutine frame. That optimization must preserve the same popped-cleanup
// base as the suspended path, not create a new source-panic recovery base.
func TestCoroInternalOutcomeCleanupPreservesDrainContinuation(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			const source = `package foo
func Root() {
	defer println("cleanup")
	println("ordinary")
	print("ordinary")
}
`
			fixture := prepareCoroPrintTestPlanSource(t, target.arch, source, true, false)
			defer fixture.prog.Dispose()
			universe, err := coro.NewSSAEmissionUniverse(fixture.root.Prog, fixture.universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := fixture.universe.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
			helper := fixture.runtimePkg.ssa.Func("PrintBatchV1")
			plan, err := coro.AnalyzeSSA(fixture.root.Prog, coro.Roots{{Function: fixture.root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse: universe, FunctionIDs: ids, MaxPlainInstructions: 64,
				OutcomeMode:          coro.OutcomeExplicitStatus,
				ClassifyLocalBody:    fixture.universe.CoroLocalBodyFacts,
				ClassifyLoweredCalls: fixture.universe.CoroLoweredCalls,
				ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if function == helper {
						return coro.SSAFunctionPolicy{Effect: coro.OutcomeStructured, Exec: coro.MayUnwind}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if fact, found := plan.FunctionPlan(helper); !found || !fact.HasStaticOutcome() {
				t.Fatalf("helper did not select its bounded outcome entry: %+v present=%t", fact, found)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: fixture.universe}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			pkg, _, err := NewPackageExWithEmbedOptions(fixture.prog, nil, nil, nil, fixture.fooPkg.ssa,
				[]*ast.File{fixture.fooPkg.file}, goembed.VarMap{}, PackageOptions{Compilation: compilation})
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			check := func(fn llvm.Value) {
				t.Helper()
				var continuation, recoverValue llvm.Value
				for _, block := range fn.BasicBlocks() {
					dispatch := block.LastInstruction()
					if dispatch.InstructionOpcode() != llvm.Switch || dispatch.SuccessorsCount() != 4 {
						continue
					}
					mask := uint64(0)
					for i := 1; i < dispatch.SuccessorsCount(); i++ {
						value := dispatch.GetSwitchCaseValue(i)
						if n := value.ZExtValue(); n < 64 {
							mask |= 1 << n
						}
					}
					if mask != 1<<coroStaticCleanupContinueComplete|1<<coroStaticCleanupContinueRecover|1<<coroStaticCleanupContinueFirstRun {
						continue
					}
					if !continuation.IsNil() || dispatch.Operand(0).InstructionOpcode() != llvm.Load {
						t.Fatal("cleanup continuation lost its unique frame storage")
					}
					continuation = dispatch.Operand(0).Operand(0)
					for i := 1; i < dispatch.SuccessorsCount(); i++ {
						if value := dispatch.GetSwitchCaseValue(i); value.ZExtValue() == uint64(coroStaticCleanupContinueRecover) {
							recoverValue = value
						}
					}
				}
				if continuation.IsNil() || recoverValue.IsNil() {
					t.Fatalf("cleanup continuation dispatch is missing:\n%s", fn.String())
				}
				calls, sourcePanics, sourceFaults := 0, 0, 0
				for _, block := range fn.BasicBlocks() {
					for call := block.FirstInstruction(); !call.IsNil(); call = llvm.NextInstruction(call) {
						if call.InstructionOpcode() != llvm.Call || call.CalledValue().Name() != llssa.PkgRuntime+".PrintBatchV1"+coroOutcomePlainPrimarySuffix {
							continue
						}
						calls++
						dispatch := block.LastInstruction()
						if dispatch.InstructionOpcode() != llvm.Switch {
							t.Fatalf("outcome helper lost its immediate completion dispatch: %s", block.AsValue().String())
						}
						for i := 1; i < dispatch.SuccessorsCount(); i++ {
							if coroTestBlockStoresValueTo(dispatch.Successor(i), recoverValue, continuation) {
								switch dispatch.GetSwitchCaseValue(i).ZExtValue() {
								case coroAwaitCompletionPanic:
									sourcePanics++
								case coroAwaitCompletionFaultNil:
									sourceFaults++
								}
							}
						}
					}
				}
				if calls != 3 || sourcePanics != 2 || sourceFaults != 2 {
					t.Fatalf("outcome calls=%d source-panic continuations=%d nil-fault continuations=%d, want two ordinary calls and one base-preserving cleanup:\n%s", calls, sourcePanics, sourceFaults, fn.String())
				}
			}
			check(requireCoroPhysicalFunction(t, module, "foo.Root"))
			runCoroABITestPipeline(t, fixture.prog, module)
			check(module.NamedFunction("foo.Root$coro.resume"))
		})
	}
}

// A deferred builtin is already a real compiler-owned cleanup invocation:
// its generated PrintBatch helper is shielded, but is not a Go deferred
// function and must not receive direct-recover authority. Resource drops use
// this same invocation/child transaction rather than a new await protocol.
func TestCoroInternalCleanupAwaitDoesNotGrantParentRecover(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			source := strings.Replace(coroPrintFixture, "\tword := uintptr(pointer)", "\tdefer println(\"cleanup\")\n\tword := uintptr(pointer)", 1)
			fixture := prepareCoroPrintTestPlanSource(t, target.arch, source, true, false)
			defer fixture.prog.Dispose()
			compilation := &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			pkg, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.fooPkg.ssa, []*ast.File{fixture.fooPkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify compiler-owned cleanup: %v\n%s", err, module.String())
			}
			check := func(fn llvm.Value) {
				t.Helper()
				awaits := 0
				for _, block := range fn.BasicBlocks() {
					for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
						if instruction.InstructionOpcode() != llvm.Call || instruction.CalledValue().Name() != coroAwaitPrepareInlineHookV4 {
							continue
						}
						awaits++
						mode := instruction.Operand(3)
						if mode.IsAConstantInt().IsNil() || mode.ZExtValue() != coroAwaitRecoverNone ||
							!instruction.Operand(4).IsNull() || !instruction.Operand(5).IsNull() {
							t.Fatalf("compiler helper inherited parent panic authority:\n%s", instruction.String())
						}
					}
				}
				if awaits != 3 {
					t.Fatalf("awaits=%d, want two ordinary prints and one shielded deferred print:\n%s", awaits, fn.String())
				}
				if countCoroIRDirectCalls(fn, coroRecoverAliasBeginHookV1) != 0 {
					t.Fatalf("internal cleanup published a direct-recover alias:\n%s", fn.String())
				}
				if begins, ends := countCoroIRDirectCalls(fn, coroCleanupShieldBeginHookV1), countCoroIRDirectCalls(fn, coroCleanupShieldEndHookV1); begins != 1 || ends != 2 {
					t.Fatalf("cleanup scopes begin=%d end=%d, want one deferred helper and reconciliation on normal/canceled lanes:\n%s", begins, ends, fn.String())
				}
			}
			check(requireCoroPhysicalFunction(t, module, "foo.Root"))
			runCoroABITestPipeline(t, fixture.prog, module)
			check(module.NamedFunction("foo.Root$coro.resume"))
		})
	}
}

// Go main returning need not wait for another goroutine's source defers. The
// finite compiler-owned drop scope must not become a general Go defer mask.
func TestCoroSourceDeferDoesNotMaskTaskCancellation(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			source := strings.Replace(coroPrintFixture, "\tword := uintptr(pointer)", "\tdefer cleanup()\n\tword := uintptr(pointer)", 1) + "\nfunc cleanup() { println(\"cleanup\") }\n"
			fixture := prepareCoroPrintTestPlanSource(t, target.arch, source, true, false)
			defer fixture.prog.Dispose()
			compilation := &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			pkg, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.fooPkg.ssa, []*ast.File{fixture.fooPkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			check := func(fn llvm.Value) {
				t.Helper()
				if countCoroIRDirectCalls(fn, coroCleanupShieldBeginHookV1) != 0 ||
					countCoroIRDirectCalls(fn, coroCleanupShieldEndHookV1) != 0 {
					t.Fatalf("source Go defer acquired an internal cleanup mask:\n%s", fn.String())
				}
			}
			check(requireCoroPhysicalFunction(t, module, "foo.Root"))
			runCoroABITestPipeline(t, fixture.prog, module)
			check(module.NamedFunction("foo.Root$coro.resume"))
		})
	}
}
