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

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroFrameRetentionNilConstRecognizesUnsafePointer(t *testing.T) {
	value := ssa.NewConst(nil, types.Typ[types.UnsafePointer])
	if !coroFrameRetentionNilConst(value) {
		t.Fatalf("unsafe.Pointer zero constant %v was not recognized as nil", value)
	}
}

const coroFrameRetentionFixture = `package foo

import "unsafe"

type ParkState struct { words [16]uintptr }

//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
//go:linkname prepare C.__llgo_coro_fixture_prepare
func prepare(unsafe.Pointer, unsafe.Pointer)

//go:linkname park llgo.coroPark
func park(*ParkState, uint32)

func Root(addr *uint32) uint32 {
	if addr == nil {
		return 0
	}
	var state ParkState
	prepare(unsafe.Pointer(&state), unsafe.Pointer(addr))
	park(&state, 0)
	return *addr
}
`

func TestCoroGenericParkStateRetentionIsSourceIndependent(t *testing.T) {
	for _, symbol := range []string{"__llgo_coro_fixture_prepare", "__llgo_coro_another_source_prepare"} {
		t.Run(symbol, func(t *testing.T) {
			source := strings.ReplaceAll(coroFrameRetentionFixture, "__llgo_coro_fixture_prepare", symbol)
			prog, ssaPkg, files, universe, proof := prepareCoroFrameRetentionProof(
				t, source, CoroFrameRetentionParkABIV2,
			)
			defer prog.Dispose()
			allocations := coroFrameRetentionHeapAllocs(ssaPkg.Func("Root"))
			if len(allocations) != 1 || len(proof.allocations) != 1 {
				t.Fatalf("generic park proof selected %d/%d heap allocations, want 1/1", len(proof.allocations), len(allocations))
			}
			if _, retained := proof.allocations[allocations[0]]; !retained {
				t.Fatal("generic park state was not selected for coroutine-frame storage")
			}

			root := ssaPkg.Func("Root")
			var prepare *ssa.Call
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if ok && call.Common() != nil && call.Common().StaticCallee() != nil && call.Common().StaticCallee().Name() == "prepare" {
						prepare = call
					}
				}
			}
			if prepare == nil {
				t.Fatal("fixture has no prepare call")
			}
			rootedAddr := false
			for _, value := range proof.exactCallKeepaliveRoots(prepare) {
				rootedAddr = rootedAddr || value == root.Params[0]
			}
			if !rootedAddr {
				t.Fatal("borrow prepare did not retain its typed source key")
			}

			plan := analyzeCoroFrameRetentionFixture(t, ssaPkg, universe, root, 1)
			compilation := &Compilation{
				CoroPlan: plan, EmissionUniverse: universe,
				CoroFrameRetentionABI: CoroFrameRetentionParkABIV2,
			}
			enableCoroPreemptCompilation(compilation)
			pkg, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			if strings.Contains(body, "AllocZ") || !strings.Contains(body, "alloca %foo.ParkState") ||
				!strings.Contains(body, "call void @"+symbol) ||
				strings.Count(body, "call void @"+coroKeyedParkHookV2) != 1 ||
				strings.Count(body, "call i32 @"+coroKeyedResumeHookV2) != 1 {
				t.Fatalf("generic park state did not lower through the single frame-owned path:\n%s", body)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify generic park state before CoroSplit: %v\n%s", err, module.String())
			}
			runCoroABITestPipeline(t, prog, module)
			if resume := module.NamedFunction("foo.Root$coro.resume"); resume.IsNil() ||
				strings.Contains(resume.String(), "AllocZ") || !strings.Contains(resume.String(), symbol) {
				t.Fatalf("CoroSplit lost frame-owned generic park state:\n%s", module.String())
			}
		})
	}
}

func TestCoroGenericParkStateRetentionRequiresExactLifetimeProof(t *testing.T) {
	tests := []struct {
		name   string
		abi    string
		source string
	}{
		{name: "profile absent", source: coroFrameRetentionFixture},
		{
			name: "legacy progress-only annotation",
			abi:  CoroFrameRetentionParkABIV2,
			source: strings.Replace(coroFrameRetentionFixture,
				"//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return",
				"//llgo:coro noblock", 1),
		},
		{
			name: "retained memory",
			abi:  CoroFrameRetentionParkABIV2,
			source: strings.Replace(coroFrameRetentionFixture,
				"memory=borrow-until-return", "memory=retained", 1),
		},
		{
			name: "blocking prepare",
			abi:  CoroFrameRetentionParkABIV2,
			source: strings.Replace(coroFrameRetentionFixture,
				"progress=executor-safe", "progress=may-block", 1),
		},
		{
			name: "pointer-bearing opaque state",
			abi:  CoroFrameRetentionParkABIV2,
			source: strings.Replace(coroFrameRetentionFixture,
				"type ParkState struct { words [16]uintptr }", "type ParkState struct { pointer unsafe.Pointer }", 1),
		},
		{
			name: "duplicate prepare owner",
			abi:  CoroFrameRetentionParkABIV2,
			source: strings.Replace(coroFrameRetentionFixture,
				"prepare(unsafe.Pointer(&state), unsafe.Pointer(addr))\n\tpark",
				"prepare(unsafe.Pointer(&state), unsafe.Pointer(addr))\n\tprepare(unsafe.Pointer(&state), unsafe.Pointer(addr))\n\tpark", 1),
		},
		{
			name: "missing prepare owner",
			abi:  CoroFrameRetentionParkABIV2,
			source: strings.Replace(coroFrameRetentionFixture,
				"\tprepare(unsafe.Pointer(&state), unsafe.Pointer(addr))\n", "", 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, ssaPkg, _, _, proof := prepareCoroFrameRetentionProof(t, test.source, test.abi)
			defer prog.Dispose()
			if len(proof.allocations) != 0 {
				t.Fatalf("uncertified generic park selected %d frame allocations, want zero", len(proof.allocations))
			}
			if len(coroFrameRetentionHeapAllocs(ssaPkg.Func("Root"))) == 0 {
				t.Fatal("fixture unexpectedly has no escaping park state")
			}
		})
	}
}

func prepareCoroFrameRetentionProof(t *testing.T, source, abi string) (
	llssa.Program, *ssa.Package, []*ast.File, *EmissionUniverse, *coroFrameRetentionProof,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, nil, ssaPkg.Func("Root"), abi)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, ssaPkg, files, universe, audit.currentFrameRetentionProof()
}

func analyzeCoroFrameRetentionFixture(t *testing.T, ssaPkg *ssa.Package, universe *EmissionUniverse, root *ssa.Function, maxPlain int) *coro.SSAPlan {
	t.Helper()
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse, FunctionIDs: functionIDs, MaxPlainInstructions: maxPlain,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
			}
			background, classified, backgroundErr := universe.FunctionBackground(fn)
			if backgroundErr != nil || !classified || background != llssa.InC {
				return coro.SSAFunctionPolicy{}, backgroundErr
			}
			certificate, certified, certificateErr := universe.CoroCallableContractCertificate(fn)
			if certificateErr != nil {
				return coro.SSAFunctionPolicy{}, certificateErr
			}
			if certified {
				external := coro.ExternalUnknownForeign
				exec := coro.BlockForeign | coro.IRQUnsafe | coro.CallableContractExecConstraints(certificate.Contract)
				if certificate.Contract.Progress == coro.ProgressExecutorSafe {
					external = coro.ExternalKnown
					exec &^= coro.BlockForeign
				}
				return coro.SSAFunctionPolicy{
					IgnoreBody: true, External: external, OverrideExternal: true,
					Exec: exec, CallableContractCertificate: certificate,
				}, nil
			}
			return coro.SSAFunctionPolicy{
				IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
				Exec: coro.BlockForeign | coro.IRQUnsafe,
			}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func coroFrameRetentionHeapAllocs(fn *ssa.Function) []*ssa.Alloc {
	var result []*ssa.Alloc
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if alloc, ok := instruction.(*ssa.Alloc); ok && alloc.Heap {
				result = append(result, alloc)
			}
		}
	}
	return result
}
