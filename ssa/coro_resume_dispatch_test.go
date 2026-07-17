//go:build !llgo

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

package ssa

import (
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

type coroResumeDispatchTestFixture struct {
	prog Program
	pkg  Package
	fn   Function
	coro *CoroBuilder

	sharedCleanup llvm.BasicBlock
	gates         []llvm.BasicBlock
	normals       []BasicBlock
	defaultCalls  int
	overrideCalls int

	conditionalEntry   llvm.BasicBlock
	conditionalSuspend llvm.BasicBlock
	conditionalNormal  BasicBlock
	conditionalTail    llvm.BasicBlock
	logicalPhi         Expr
}

func TestCoroBuilderResumeDispatchCFG(t *testing.T) {
	Initialize(InitAll)
	for _, test := range []struct {
		name   string
		target *Target
	}{
		{name: "native"},
		{name: "wasm", target: &Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoroResumeDispatchTestFixture(t, test.target)
			mod := fixture.pkg.Module()
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify resume-dispatch coroutine: %v\n%s", err, mod.String())
			}

			if fixture.defaultCalls != 3 || fixture.overrideCalls != 1 {
				t.Fatalf("resume dispatch calls = default:%d override:%d, want 3/1",
					fixture.defaultCalls, fixture.overrideCalls)
			}
			if len(fixture.gates) != 4 || len(fixture.normals) != 4 {
				t.Fatalf("resume gate/normal blocks = %d/%d, want 4/4",
					len(fixture.gates), len(fixture.normals))
			}
			if fixture.coro.InitialResumeBlock() != fixture.normals[0] {
				t.Fatal("initial resume did not restore the compiler-owned normal block")
			}
			for index, gate := range fixture.gates {
				normal := fixture.normals[index]
				if gate.C == normal.first.C {
					t.Fatalf("resume %d gate aliases its normal continuation", index)
				}
				terminator := gate.LastInstruction()
				if terminator.IsNil() || terminator.InstructionOpcode() != llvm.Br ||
					terminator.SuccessorsCount() != 2 {
					t.Fatalf("resume %d gate lacks its terminating dispatch: %v", index, terminator)
				}
				if !coroTerminatorTargets(terminator, normal.first) ||
					!coroTerminatorTargets(terminator, fixture.sharedCleanup) {
					t.Fatalf("resume %d dispatch does not target normal and shared cleanup", index)
				}
				if !coroSuspendSwitchTargets(fixture.fn, gate) {
					t.Fatalf("resume %d gate is not a case-0 coro.suspend target", index)
				}
			}

			conditionalBranch := fixture.conditionalEntry.LastInstruction()
			if conditionalBranch.IsNil() || conditionalBranch.InstructionOpcode() != llvm.Br ||
				conditionalBranch.SuccessorsCount() != 2 {
				t.Fatalf("conditional suspend entry lacks a two-way branch: %v", conditionalBranch)
			}
			if got := conditionalBranch.Successor(0); got.C != fixture.conditionalSuspend.C {
				t.Fatal("conditional true edge does not enter the suspend publication block")
			}
			if got := conditionalBranch.Successor(1); got.C != fixture.conditionalTail.C {
				t.Fatal("conditional false edge does not enter the joined continuation directly")
			}
			if conditionalBranch.Successor(1).C == fixture.gates[2].C {
				t.Fatal("conditional false edge incorrectly passes through the resume gate")
			}
			if fixture.conditionalNormal.last.LastInstruction().Successor(0).C != fixture.conditionalTail.C {
				t.Fatal("conditional true resume normal block does not join the continuation")
			}

			if got := fixture.logicalPhi.impl.IncomingBlock(0); got.C != fixture.normals[3].last.C {
				t.Fatal("logical phi predecessor is not the per-site dispatch normal tail")
			}
			ir := mod.String()
			if strings.Count(ir, "call void @default_resume_dispatch") != 3 ||
				strings.Count(ir, "call void @exact_resume_dispatch") != 1 ||
				strings.Count(ir, "call void @conditional_suspend_publish") != 1 {
				t.Fatalf("resume dispatch marker calls do not cover initial/unconditional/conditional/per-site paths:\n%s", ir)
			}
			if strings.Count(ir, "@llvm.coro.suspend(token none, i1 true)") != 1 {
				t.Fatalf("final suspend shape changed or gained a dispatch gate:\n%s", ir)
			}
		})
	}
}

func TestCoroBuilderConditionalResumeDispatchOverridesDefault(t *testing.T) {
	Initialize(InitAll)
	for _, test := range []struct {
		name   string
		target *Target
	}{
		{name: "native"},
		{name: "wasm", target: &Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := NewProgram(test.target)
			defer prog.Dispose()
			pkg := prog.NewPackage("coroconditionaldispatch", "coro/resume/conditional/dispatch")
			defer pkg.Module().Dispose()
			fn := pkg.NewFunc("coro_conditional_resume_dispatch", functionSignature(
				[]types.Type{types.Typ[types.Bool]},
				[]types.Type{types.Typ[types.UnsafePointer]},
			), InGo)
			b := fn.MakeBody(1)
			defer b.Dispose()
			defaultMarker := pkg.NewFunc("conditional_default_gate", functionSignature(nil, nil), InC)
			exactMarker := pkg.NewFunc("conditional_exact_gate", functionSignature(nil, nil), InC)
			publishMarker := pkg.NewFunc("conditional_exact_publish", functionSignature(nil, nil), InC)
			cleanup := fn.MakeBlock()
			finish := fn.MakeBlock()
			defaultCalls := 0
			exactCalls := 0
			coro := b.BeginCoro(CoroOptions{
				Frame: CoroFrameOps{
					Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
					Free:  func(Builder, Expr, Expr, Expr) {},
				},
				AfterResumeDispatch: func(b Builder, normal BasicBlock) {
					defaultCalls++
					b.Call(defaultMarker.Expr)
					b.Jump(normal)
				},
			})

			logical := fn.MakeBlock()
			b.Jump(logical)
			b.SetBlock(logical)
			entry := logical.last
			var suspend llvm.BasicBlock
			var gate llvm.BasicBlock
			var normal BasicBlock
			if got := coro.SuspendCurrentBlockIfWithResumeDispatch(
				fn.Param(0),
				func(b Builder) {
					suspend = b.impl.GetInsertBlock()
					b.Call(publishMarker.Expr)
				},
				func(b Builder, destination BasicBlock) {
					exactCalls++
					gate = b.impl.GetInsertBlock()
					normal = destination
					b.Call(exactMarker.Expr)
					b.If(fn.Param(0), destination, cleanup)
				},
			); got != logical {
				t.Fatal("conditional dispatch suspend did not preserve its logical block")
			}
			continuation := logical.last
			b.Jump(finish)
			b.SetBlock(cleanup)
			b.Jump(finish)
			b.SetBlock(finish)
			coro.Finish()
			b.EndBuild()

			if defaultCalls != 1 || exactCalls != 1 {
				t.Fatalf("resume dispatch calls = default:%d exact:%d, want 1/1", defaultCalls, exactCalls)
			}
			branch := entry.LastInstruction()
			if branch.IsNil() || branch.InstructionOpcode() != llvm.Br || branch.SuccessorsCount() != 2 ||
				branch.Successor(0).C != suspend.C || branch.Successor(1).C != continuation.C {
				t.Fatalf("conditional dispatch entry has the wrong true/false edges: %v", branch)
			}
			if branch.Successor(1).C == gate.C {
				t.Fatal("conditional dispatch false edge passed through the exact resume gate")
			}
			if normal == nil || normal.last.LastInstruction().Successor(0).C != continuation.C {
				t.Fatal("exact resume normal path did not join the shared continuation")
			}
			if !coroSuspendSwitchTargets(fn, gate) {
				t.Fatal("exact conditional gate is not a case-0 coro.suspend target")
			}
			ir := pkg.Module().String()
			if strings.Count(ir, "call void @conditional_default_gate") != 1 ||
				strings.Count(ir, "call void @conditional_exact_gate") != 1 ||
				strings.Count(ir, "call void @conditional_exact_publish") != 1 {
				t.Fatalf("conditional per-site dispatch did not remain exclusive:\n%s", ir)
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify conditional per-site dispatch: %v\n%s", err, ir)
			}
		})
	}
}

func TestCoroBuilderResumeDispatchOverridesAndRejectsMisuse(t *testing.T) {
	t.Run("option callbacks are mutually exclusive", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		mustPanicContains(t, "callbacks are mutually exclusive", func() {
			b.BeginCoro(CoroOptions{
				Frame: CoroFrameOps{
					Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
					Free:  func(Builder, Expr, Expr, Expr) {},
				},
				AfterResume: func(Builder) {},
				AfterResumeDispatch: func(b Builder, normal BasicBlock) {
					b.Jump(normal)
				},
			})
		})
	})

	t.Run("dispatch must terminate gate", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		mustPanicContains(t, "resume-dispatch callback must terminate insertion block", func() {
			b.BeginCoro(CoroOptions{
				Frame: CoroFrameOps{
					Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
					Free:  func(Builder, Expr, Expr, Expr) {},
				},
				AfterResumeDispatch: func(Builder, BasicBlock) {},
			})
		})
	})

	t.Run("dispatch cannot emit into destination", func(t *testing.T) {
		prog, b := newCoroCallbackTestBuilder(t)
		mustPanicContains(t, "resume-dispatch callback changed insertion block", func() {
			b.BeginCoro(CoroOptions{
				Frame: CoroFrameOps{
					Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
					Free:  func(Builder, Expr, Expr, Expr) {},
				},
				AfterResumeDispatch: func(b Builder, normal BasicBlock) {
					b.SetBlock(normal)
					b.Unreachable()
				},
			})
		})
	})

	t.Run("per-site callback is exclusive with default", func(t *testing.T) {
		Initialize(InitAll)
		prog := NewProgram(nil)
		defer prog.Dispose()
		pkg := prog.NewPackage("corooverridekind", "coro/resume/override/kind")
		defer pkg.Module().Dispose()
		fn := pkg.NewFunc("coro_resume_override_kind", coroHandleSignature(), InGo)
		b := fn.MakeBody(1)
		defer b.Dispose()
		defaultCalls := 0
		dispatchCalls := 0
		coro := b.BeginCoro(CoroOptions{
			Frame: CoroFrameOps{
				Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
				Free:  func(Builder, Expr, Expr, Expr) {},
			},
			AfterResume: func(Builder) { defaultCalls++ },
		})
		mustPanicContains(t, "requires a callback", func() {
			coro.SuspendCurrentBlockWithResumeDispatch(nil)
		})
		mustPanicContains(t, "requires a callback", func() {
			coro.SuspendCurrentBlockIfWithResumeDispatch(prog.BoolVal(true), nil, nil)
		})
		coro.SuspendCurrentBlockWithResumeDispatch(func(b Builder, normal BasicBlock) {
			dispatchCalls++
			b.Jump(normal)
		})
		if defaultCalls != 1 || dispatchCalls != 1 {
			t.Fatalf("per-site callback selection = default:%d dispatch:%d, want 1/1",
				defaultCalls, dispatchCalls)
		}
		coro.Finish()
		b.EndBuild()
		if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
			t.Fatalf("verify mixed default/per-site resume callbacks: %v\n%s", err, pkg.String())
		}
	})
}

func TestCoroBuilderResumeDispatchCoroSplitReachability(t *testing.T) {
	Initialize(InitAll)
	for _, test := range []struct {
		name   string
		target *Target
	}{
		{name: "native"},
		{name: "wasm", target: &Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoroResumeDispatchTestFixture(t, test.target)
			runCoroPasses(t, &coroTestFixture{
				prog: fixture.prog,
				pkg:  fixture.pkg,
				fn:   fixture.fn,
				coro: fixture.coro,
			}, "coro-early,cgscc(coro-split),coro-cleanup")

			mod := fixture.pkg.Module()
			for _, name := range []string{"coro_resume_dispatch", "coro_resume_dispatch.destroy"} {
				fn := mod.NamedFunction(name)
				if fn.IsNil() {
					t.Fatalf("CoroSplit did not create %s:\n%s", name, mod.String())
				}
				for _, marker := range []string{"default_resume_dispatch", "exact_resume_dispatch"} {
					if coroFunctionHasReachableDirectCall(fn, marker) {
						t.Fatalf("%s has a reachable %s gate outside .resume:\n%s", name, marker, fn.String())
					}
				}
			}
			resume := mod.NamedFunction("coro_resume_dispatch.resume")
			if resume.IsNil() {
				t.Fatalf("CoroSplit did not create resume entry:\n%s", mod.String())
			}
			for _, marker := range []string{"default_resume_dispatch", "exact_resume_dispatch"} {
				if !coroFunctionHasReachableDirectCall(resume, marker) {
					t.Fatalf("resume entry has no reachable %s gate:\n%s", marker, resume.String())
				}
			}
		})
	}
}

func newCoroResumeDispatchTestFixture(t *testing.T, target *Target) *coroResumeDispatchTestFixture {
	t.Helper()
	prog := NewProgram(target)
	pkg := prog.NewPackage("cororesumedispatch", "coro/resume/dispatch")
	t.Cleanup(func() {
		pkg.Module().Dispose()
		prog.Dispose()
	})

	fn := pkg.NewFunc("coro_resume_dispatch", functionSignature(
		[]types.Type{types.Typ[types.Bool]},
		[]types.Type{types.Typ[types.UnsafePointer]},
	), InGo)
	b := fn.MakeBody(1)
	t.Cleanup(b.Dispose)
	defaultMarker := pkg.NewFunc("default_resume_dispatch", functionSignature(nil, nil), InC)
	overrideMarker := pkg.NewFunc("exact_resume_dispatch", functionSignature(nil, nil), InC)
	publishMarker := pkg.NewFunc("conditional_suspend_publish", functionSignature(nil, nil), InC)
	sink := pkg.NewFunc("resume_dispatch_phi_sink", functionSignature(
		[]types.Type{types.Typ[types.Uint8]}, nil,
	), InC)
	sharedCleanup := fn.MakeBlock()
	finish := fn.MakeBlock()
	fixture := &coroResumeDispatchTestFixture{
		prog:          prog,
		pkg:           pkg,
		fn:            fn,
		sharedCleanup: sharedCleanup.first,
	}

	dispatch := func(marker Function, calls *int) CoroResumeDispatch {
		return func(b Builder, normal BasicBlock) {
			*calls++
			fixture.gates = append(fixture.gates, b.impl.GetInsertBlock())
			fixture.normals = append(fixture.normals, normal)
			b.Call(marker.Expr)
			b.If(fn.Param(0), normal, sharedCleanup)
		}
	}
	coro := b.BeginCoro(CoroOptions{
		Frame: CoroFrameOps{
			Alloc: func(Builder, Expr, Expr) Expr { return prog.Nil(prog.VoidPtr()) },
			Free:  func(Builder, Expr, Expr, Expr) {},
		},
		AfterResumeDispatch: dispatch(defaultMarker, &fixture.defaultCalls),
	})
	fixture.coro = coro

	if got := coro.Suspend(); got != fixture.normals[1] {
		t.Fatal("unconditional Suspend did not expose its compiler-owned normal block")
	}
	logical := fn.MakeBlock()
	join := fn.MakeBlock()
	b.Jump(logical)
	b.SetBlock(logical)
	fixture.conditionalEntry = logical.last
	coro.SuspendCurrentBlockIf(fn.Param(0), func(b Builder) {
		fixture.conditionalSuspend = b.impl.GetInsertBlock()
		b.Call(publishMarker.Expr)
	})
	fixture.conditionalNormal = fixture.normals[2]
	fixture.conditionalTail = logical.last

	coro.SuspendCurrentBlockWithResumeDispatch(dispatch(overrideMarker, &fixture.overrideCalls))
	b.Jump(join)
	b.SetBlock(join)
	phi := b.Phi(prog.Byte())
	phi.AddIncoming(b, []BasicBlock{logical}, func(int, BasicBlock) Expr {
		return prog.IntVal(7, prog.Byte())
	})
	fixture.logicalPhi = phi.Expr
	b.Call(sink.Expr, phi.Expr)
	b.Jump(finish)

	b.SetBlock(sharedCleanup)
	b.Jump(finish)
	b.SetBlock(finish)
	coro.Finish()
	b.EndBuild()
	return fixture
}

func coroTerminatorTargets(terminator llvm.Value, target llvm.BasicBlock) bool {
	for index := 0; index < terminator.SuccessorsCount(); index++ {
		if terminator.Successor(index).C == target.C {
			return true
		}
	}
	return false
}

func coroSuspendSwitchTargets(fn Function, target llvm.BasicBlock) bool {
	suspendID := llvm.LookupIntrinsicID("llvm.coro.suspend")
	for _, block := range fn.impl.BasicBlocks() {
		terminator := block.LastInstruction()
		if terminator.IsNil() || terminator.InstructionOpcode() != llvm.Switch {
			continue
		}
		condition := terminator.Operand(0)
		if condition.IsACallInst().IsNil() || condition.CalledValue().IntrinsicID() != suspendID {
			continue
		}
		if coroTerminatorTargets(terminator, target) {
			return true
		}
	}
	return false
}

// coroFunctionHasReachableDirectCall follows executable CFG edges rather than
// matching text. CoroSplit may retain dead case-0 gate clones in the optnone
// ramp and destroy functions after replacing coro.suspend with a constant.
func coroFunctionHasReachableDirectCall(function llvm.Value, callee string) bool {
	entry := function.EntryBasicBlock()
	if entry.IsNil() {
		return false
	}
	type cfgEdge struct {
		block       llvm.BasicBlock
		predecessor llvm.BasicBlock
	}
	type cfgState struct {
		cfgEdge
		constants map[llvm.Value]uint64
	}
	seen := make(map[cfgEdge][]map[llvm.Value]uint64)
	pending := []cfgState{{cfgEdge: cfgEdge{block: entry}, constants: make(map[llvm.Value]uint64)}}
	for len(pending) != 0 {
		state := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		alreadySeen := false
		for _, constants := range seen[state.cfgEdge] {
			if sameCoroResumeCFGConstants(constants, state.constants) {
				alreadySeen = true
				break
			}
		}
		if alreadySeen {
			continue
		}
		seen[state.cfgEdge] = append(seen[state.cfgEdge], state.constants)
		constants := copyCoroResumeCFGConstants(state.constants)
		for instruction := state.block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if !instruction.IsAPHINode().IsNil() {
				value, ok := coroResumeCFGPHIIncomingConstant(instruction, state.predecessor, constants)
				if ok {
					constants[instruction] = value
				} else {
					delete(constants, instruction)
				}
			}
			if (!instruction.IsACallInst().IsNil() || !instruction.IsAInvokeInst().IsNil()) &&
				instruction.CalledValue().Name() == callee {
				return true
			}
		}
		terminator := state.block.LastInstruction()
		for _, successor := range executableCoroResumeTerminatorSuccessors(terminator, constants) {
			pending = append(pending, cfgState{
				cfgEdge:   cfgEdge{block: successor, predecessor: state.block},
				constants: constants,
			})
		}
	}
	return false
}

func executableCoroResumeTerminatorSuccessors(
	terminator llvm.Value, constants map[llvm.Value]uint64,
) []llvm.BasicBlock {
	count := terminator.SuccessorsCount()
	if count == 0 {
		return nil
	}
	if terminator.InstructionOpcode() == llvm.Br && count == 2 {
		if condition, ok := coroResumeCFGConstant(terminator.Operand(0), constants); ok {
			if condition != 0 {
				return []llvm.BasicBlock{terminator.Successor(0)}
			}
			return []llvm.BasicBlock{terminator.Successor(1)}
		}
	}
	if terminator.InstructionOpcode() == llvm.Switch {
		if condition, ok := coroResumeCFGConstant(terminator.Operand(0), constants); ok {
			selected := 0
			for successor := 1; successor < count; successor++ {
				if terminator.GetSwitchCaseValue(successor).ZExtValue() == condition {
					selected = successor
					break
				}
			}
			return []llvm.BasicBlock{terminator.Successor(selected)}
		}
	}
	successors := make([]llvm.BasicBlock, count)
	for successor := range successors {
		successors[successor] = terminator.Successor(successor)
	}
	return successors
}

func coroResumeCFGPHIIncomingConstant(
	phi llvm.Value, predecessor llvm.BasicBlock, constants map[llvm.Value]uint64,
) (uint64, bool) {
	if predecessor.IsNil() {
		return 0, false
	}
	for incoming := 0; incoming < phi.IncomingCount(); incoming++ {
		if phi.IncomingBlock(incoming) == predecessor {
			return coroResumeCFGConstant(phi.IncomingValue(incoming), constants)
		}
	}
	return 0, false
}

func coroResumeCFGConstant(value llvm.Value, constants map[llvm.Value]uint64) (uint64, bool) {
	if !value.IsAConstantInt().IsNil() {
		return value.ZExtValue(), true
	}
	constant, ok := constants[value]
	return constant, ok
}

func copyCoroResumeCFGConstants(constants map[llvm.Value]uint64) map[llvm.Value]uint64 {
	result := make(map[llvm.Value]uint64, len(constants))
	for value, constant := range constants {
		result[value] = constant
	}
	return result
}

func sameCoroResumeCFGConstants(left, right map[llvm.Value]uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for value, constant := range left {
		if other, ok := right[value]; !ok || other != constant {
			return false
		}
	}
	return true
}
