//go:build !llgo
// +build !llgo

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
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestCoroPlanInputElidesOnlyFrontendNoInitCalls(t *testing.T) {
	newImport := func(path, kind string) *types.Package {
		pkg := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
		if kind != "" {
			pkg.Scope().Insert(types.NewConst(
				token.NoPos, pkg, "LLGoPackage", types.Typ[types.String], constant.MakeString(kind),
			))
		}
		pkg.MarkComplete()
		return pkg
	}
	imports := coroPlanTestImporter{
		"example.com/noinit":   newImport("example.com/noinit", "noinit"),
		"example.com/decl":     newImport("example.com/decl", "decl"),
		"example.com/ordinary": newImport("example.com/ordinary", ""),
	}
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/elided", `package elided
import (
	_ "example.com/noinit"
	_ "example.com/decl"
	_ "example.com/ordinary"
)
func target() {}
func calls(fn func()) {
	target()
	go target()
	defer target()
	fn()
}
`, imports)

	initCalls := coroPlanTestCalls(ssaPkg.Func("init"))
	wantElided := map[string]bool{
		"example.com/noinit":   true,
		"example.com/decl":     true,
		"example.com/ordinary": false,
	}
	seenImports := make(map[string]bool)
	for _, call := range initCalls {
		callee := call.Common().StaticCallee()
		if callee == nil || callee.Pkg == nil || callee.Pkg.Pkg == nil {
			continue
		}
		path := callee.Pkg.Pkg.Path()
		want, relevant := wantElided[path]
		if !relevant {
			continue
		}
		seenImports[path] = true
		if got := frontendElidesNoInitCall(call); got != want {
			t.Fatalf("frontendElidesNoInitCall(%s.init) = %t, want %t", path, got, want)
		}
	}
	if len(seenImports) != len(wantElided) {
		t.Fatalf("synthetic init import calls = %v, want all of %v", seenImports, wantElided)
	}

	ordinaryCalls := coroPlanTestCalls(ssaPkg.Func("calls"))
	if len(ordinaryCalls) != 4 {
		t.Fatalf("calls body has %d call instructions, want direct/go/defer/dynamic", len(ordinaryCalls))
	}
	for _, call := range ordinaryCalls {
		if frontendElidesNoInitCall(call) {
			t.Fatalf("ordinary %T call was classified as frontend-elided: %s", call, call)
		}
	}

	input := CoroPlanInput{Program: ssaPkg.Prog}
	plan, err := input.Analyze(coro.Roots{
		{Function: ssaPkg.Func("init"), Demand: coro.SyncDemand},
		{Function: ssaPkg.Func("calls"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range initCalls {
		callee := call.Common().StaticCallee()
		if callee == nil || callee.Pkg == nil || callee.Pkg.Pkg == nil {
			continue
		}
		want, relevant := wantElided[callee.Pkg.Pkg.Path()]
		if !relevant {
			continue
		}
		_, planned := plan.CallPlan(call)
		if planned == want {
			t.Fatalf("CallPlan(%s.init) present=%t, want present=%t", callee.Pkg.Pkg.Path(), planned, !want)
		}
	}
	for _, call := range ordinaryCalls {
		if _, planned := plan.CallPlan(call); !planned {
			t.Fatalf("ordinary %T call has no CallPlan: %s", call, call)
		}
	}
	var directOrdinary ssa.CallInstruction
	for _, call := range ordinaryCalls {
		if _, direct := call.(*ssa.Call); direct && call.Common().StaticCallee() == ssaPkg.Func("target") {
			directOrdinary = call
			break
		}
	}
	if directOrdinary == nil {
		t.Fatal("calls body has no ordinary direct target call")
	}
	_, err = input.Analyze(coro.Roots{{Function: ssaPkg.Func("calls"), Demand: coro.SyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			return call == directOrdinary, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "builder cannot elide ordinary call") {
		t.Fatalf("ordinary builder elision error = %v, want fail-closed rejection", err)
	}
}

func TestCoroPlanInputValidatesFrozenIntrinsicCallSites(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "constant string through canonical alias",
			source: `package intrinsiccalls
//llgo:link CStr llgo.cstr
func CStr(string) *byte
//llgo:link CStrAlias llgo.cstr
func CStrAlias(string) *byte
func root() { _ = CStrAlias("frozen") }
`,
		},
		{
			name: "variable string",
			source: `package intrinsiccalls
//llgo:link CStr llgo.cstr
func CStr(string) *byte
func root(value string) { _ = CStr(value) }
`,
			wantErr: "requires exactly one compile-time string constant argument",
		},
		{
			name: "non-string constant",
			source: `package intrinsiccalls
//llgo:link CStr llgo.cstr
func CStr(int) *byte
func root() { _ = CStr(1) }
`,
			wantErr: "requires exactly one compile-time string constant argument",
		},
		{
			name: "advance pointer by integer",
			source: `package intrinsiccalls
//llgo:link Advance llgo.advance
func Advance(*int, int) *int
func root(value *int) { _ = Advance(value, 1) }
`,
		},
		{
			name: "advance wrong arity",
			source: `package intrinsiccalls
//llgo:link Advance llgo.advance
func Advance(*int, int, int) *int
func root(value *int) { _ = Advance(value, 1, 2) }
`,
			wantErr: "requires exactly two arguments",
		},
		{
			name: "advance non-pointer",
			source: `package intrinsiccalls
//llgo:link Advance llgo.advance
func Advance(int, int) int
func root(value int) { _ = Advance(value, 1) }
`,
			wantErr: "requires a pointer first argument",
		},
		{
			name: "advance non-integer offset",
			source: `package intrinsiccalls
//llgo:link Advance llgo.advance
func Advance(*int, string) *int
func root(value *int) { _ = Advance(value, "1") }
`,
			wantErr: "requires an integer offset argument",
		},
		{
			name: "advance mismatched result",
			source: `package intrinsiccalls
//llgo:link Advance llgo.advance
func Advance(*int, int) *byte
func root(value *int) { _ = Advance(value, 1) }
`,
			wantErr: "requires one result matching its pointer argument",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/intrinsiccalls", test.source, nil)
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
				SSA: ssaPkg, Files: files, Identity: "example.com/intrinsiccalls",
			}})
			if err != nil {
				t.Fatal(err)
			}
			ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
			if err != nil {
				t.Fatal(err)
			}
			input := CoroPlanInput{
				Program:                ssaPkg.Prog,
				EmissionUniverse:       ssaEmission,
				resolveFunction:        emission.Resolve,
				functionBackground:     emission.FunctionBackground,
				intrinsicCallSemantics: emission.CoroIntrinsicCallSiteSemantics,
			}
			functionIDs := emission.FunctionIDConfig()
			functionIDs.CoroABI = coro.EntryResolutionABIV0
			functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
			functionIDs.ArchiveReady = true
			analyze := func() (*coro.SSAPlan, error) {
				return input.Analyze(coro.Roots{{Function: ssaPkg.Func("root"), Demand: coro.SyncDemand}}, coro.SSAConfig{
					MaxPlainInstructions: -1,
					FunctionIDs:          functionIDs,
				})
			}
			plan, err := analyze()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("invalid intrinsic call error = %v; want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := coroPlanTestCalls(ssaPkg.Func("root"))
			if len(calls) != 1 {
				t.Fatalf("root intrinsic calls = %d, want one", len(calls))
			}
			call := calls[0]
			if semantics, intrinsic, err := emission.CoroIntrinsicCallSiteSemantics(call); err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineNoSuspend {
				t.Fatalf("alias intrinsic site semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
			}
			if !plan.ElidesCall(call) {
				t.Fatal("valid intrinsic site was not retained as exact elided call")
			}
			if _, ok := plan.CallPlan(call); ok {
				t.Fatal("valid intrinsic site unexpectedly has a managed CallPlan")
			}
			metadata := coro.PlanDigestMetadata{
				CoroABI: coro.EntryResolutionABIV0, SchedulerABI: coro.SchedulerNoneABIV0,
				PanicABI: coro.PanicLegacyABIV0, FuncRepABI: coro.FuncRepABIV0,
				TargetTriple: "x86_64-unknown-linux-gnu", PointerBits: 64,
				Endianness: "little", DataLayout: "e-p:64:64",
			}
			digest, err := plan.CoroPlanDigest(metadata)
			if err != nil {
				t.Fatal(err)
			}
			again, err := analyze()
			if err != nil {
				t.Fatal(err)
			}
			secondDigest, err := again.CoroPlanDigest(metadata)
			if err != nil || secondDigest != digest || !again.ElidesCall(call) {
				t.Fatalf("exact intrinsic site digest = %q, %v (elided=%t); want stable %q", secondDigest, err, again.ElidesCall(call), digest)
			}
		})
	}
}

func TestCoroParkIntrinsicSeedsCallerEffectAndStableDigest(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/coropark", `package coropark
type WaitToken struct { word uint32 }
type WaitTicket uint32
//llgo:link Park llgo.coroPark
func Park(*WaitToken, WaitTicket)
func root(token *WaitToken, ticket WaitTicket) uint32 {
	before := uint32(ticket) + 1
	Park(token, ticket)
	return before + uint32(ticket)
}
`, nil)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: "example.com/coropark",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("root")
	calls := coroPlanTestCalls(root)
	if len(calls) != 1 {
		t.Fatalf("root calls = %d, want one exact park site", len(calls))
	}
	parkCall := calls[0]
	semantics, intrinsic, err := emission.CoroIntrinsicCallSiteSemantics(parkCall)
	if err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineSuspend || !semantics.SuspendsCurrentFrame() {
		t.Fatalf("park semantics = %v, %v, %v; want inline-suspend, true, nil", semantics, intrinsic, err)
	}
	input := CoroPlanInput{
		Program:                ssaPkg.Prog,
		EmissionUniverse:       ssaEmission,
		resolveFunction:        emission.Resolve,
		functionBackground:     emission.FunctionBackground,
		intrinsicCallSemantics: emission.CoroIntrinsicCallSiteSemantics,
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	analyze := func() (*coro.SSAPlan, error) {
		return input.Analyze(coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			MaxPlainInstructions: -1,
			FunctionIDs:          functionIDs,
		})
	}
	plan, err := analyze()
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.Primary != coro.PrimaryCoroutine ||
		rootPlan.FuncRep != coro.DirectCoro || !rootPlan.DeclaredEffect.Contains(coro.MayPark) ||
		!rootPlan.LocalEffect.Contains(coro.MayPark) || !rootPlan.Effect.Contains(coro.MayPark) {
		t.Fatalf("park root plan = %+v, present=%t; want one tainted coroutine primary", rootPlan, ok)
	}
	if !plan.ElidesCall(parkCall) {
		t.Fatal("park declaration call is not retained as an exact elided site")
	}
	if _, ok := plan.CallPlan(parkCall); ok {
		t.Fatal("park declaration unexpectedly retained a managed CallPlan")
	}
	metadata := coro.PlanDigestMetadata{
		CoroABI: coro.PhysicalABIV1, SchedulerABI: coro.SchedulerChildAwaitABIV0,
		PanicABI: coro.PanicLegacyABIV0, FuncRepABI: coro.FuncRepABIV0,
		TargetTriple: "x86_64-unknown-linux-gnu", PointerBits: 64,
		Endianness: "little", DataLayout: "e-p:64:64",
	}
	digest, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	again, err := analyze()
	if err != nil {
		t.Fatal(err)
	}
	againDigest, err := again.CoroPlanDigest(metadata)
	if err != nil || againDigest != digest || !again.ElidesCall(parkCall) {
		t.Fatalf("park plan digest = %q, %v (elided=%t); want stable %q", againDigest, err, again.ElidesCall(parkCall), digest)
	}
}

func TestRequiredCoroProgramRuntimePlanPlainClosureAndConflicts(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func __llgo_coro_program_begin_v1() { bootstrapHelper() }
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
type coroProgramRunResultV2 struct { Flags, Used, ExecutorSlot, ExecutorGeneration, Epoch, DeadlineLo, DeadlineHi, Reserved uint32 }
func __llgo_coro_program_run_slice_v2(unsafe.Pointer, unsafe.Pointer, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_program_continue_slice_v2(uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_wait_prepare_v1(unsafe.Pointer, *uint32, *uint32, *uint32, *uint32, *uint32) bool { return false }
func __llgo_coro_wait_rollback_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_wait_retire_completed_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_native_post_wait_v1(uint32, uint32, uint32, uint32) uint32 { return 0 }
func __llgo_coro_timer_prepare_after_v1(unsafe.Pointer, int64, *uint32, *uint32, *uint32) bool { return false }
func __llgo_coro_timer_retire_completed_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_timer_prepare_after_or_abort_v1(token unsafe.Pointer, delay int64, ticket, slot, generation *uint32) {
	__llgo_coro_timer_prepare_after_v1(token, delay, ticket, slot, generation)
}
func __llgo_coro_timer_retire_completed_or_abort_v1(token unsafe.Pointer, ticket, slot, generation uint32) {
	__llgo_coro_timer_retire_completed_v1(token, ticket, slot, generation)
}
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_await_prepare_v1() {}
var preemptRequest uint32
func __llgo_coro_preempt_poll_v1() bool { return atomicExchange(&preemptRequest, 0) == 1 }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_park_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_frame_free_v1() {}
func __llgo_coro_panic_prepare_v1() {}
func __llgo_coro_spawn_begin_v1() {}
func __llgo_coro_spawn_commit_v1() {}
func __llgo_coro_program_main_return_v1() {}
func bootstrapHelper() { closureLoop(); externalABI(); inlineIntrinsic("bootstrap") }
func closureLoop() { for i := 0; i < 2; i++ {} }
func unrelatedLoop() { for {} }
//llgo:link externalABI C.externalABI
func externalABI()
//llgo:link inlineIntrinsic llgo.cstr
func inlineIntrinsic(string) *byte
//llgo:link atomicExchange llgo.atomicXchg
func atomicExchange(*uint32, uint32) uint32
`, nil)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: llssa.PkgRuntime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &context{
		buildConf:       &Config{EnableCoroChildAwait: true, EnableCoroProgramBootstrapRun: true},
		coroEmission:    emission,
		coroSSAEmission: ssaEmission,
	}
	roots, requiredPlain, directPlain, closedDynamic, err := requiredCoroProgramRuntimePlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rootsAgain, plainAgain, directAgain, closedAgain, err := requiredCoroProgramRuntimePlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rootsAgain, roots) || !reflect.DeepEqual(plainAgain, requiredPlain) ||
		!reflect.DeepEqual(directAgain, directPlain) || !reflect.DeepEqual(closedAgain, closedDynamic) {
		t.Fatal("required runtime roots/plain closure is not deterministic")
	}
	if len(directPlain) != 0 {
		t.Fatalf("required direct-plain C callbacks = %d, want none", len(directPlain))
	}
	continueFn := ssaPkg.Func(coroProgramContinueSymbolV1)
	originalContinueSignature := continueFn.Signature
	continueFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "epoch", types.Typ[types.Uint64])),
		types.NewTuple(), false)
	_, _, _, _, invalidContinueErr := requiredCoroProgramRuntimePlan(ctx)
	continueFn.Signature = originalContinueSignature
	if invalidContinueErr == nil || !strings.Contains(invalidContinueErr.Error(), "must have exact func(uint32) signature") {
		t.Fatalf("invalid continuation ABI error = %v", invalidContinueErr)
	}
	prepareFn := ssaPkg.Func(coroWaitPrepareSymbolV1)
	originalPrepareSignature := prepareFn.Signature
	prepareFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "token", types.Typ[types.UnsafePointer])),
		types.NewTuple(types.NewParam(token.NoPos, nil, "ok", types.Typ[types.Bool])), false)
	_, _, _, _, invalidPrepareErr := requiredCoroProgramRuntimePlan(ctx)
	prepareFn.Signature = originalPrepareSignature
	if invalidPrepareErr == nil || !strings.Contains(invalidPrepareErr.Error(), "wait prepare ABI") {
		t.Fatalf("invalid wait prepare ABI error = %v", invalidPrepareErr)
	}
	runDecisionFn := ssaPkg.Func(coroRunDecisionTakeSymbolV1)
	if runDecisionFn == nil {
		t.Fatal("run-decision hook is absent from the runtime fixture")
	}
	originalRunDecisionSignature := runDecisionFn.Signature
	runDecisionFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer])),
		types.NewTuple(), false)
	_, _, _, _, invalidRunDecisionErr := requiredCoroProgramRuntimePlan(ctx)
	runDecisionFn.Signature = originalRunDecisionSignature
	if invalidRunDecisionErr == nil || !strings.Contains(invalidRunDecisionErr.Error(), "run-decision ABI") {
		t.Fatalf("invalid run-decision ABI error = %v", invalidRunDecisionErr)
	}
	runDecisionZeroFn := ssaPkg.Func(coroRunDecisionTakeZeroSymbolV1)
	if runDecisionZeroFn == nil {
		t.Fatal("zero-ticket run-decision hook is absent from the runtime fixture")
	}
	originalRunDecisionZeroSignature := runDecisionZeroFn.Signature
	runDecisionZeroFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer])),
		types.NewTuple(), false)
	_, _, _, _, invalidRunDecisionZeroErr := requiredCoroProgramRuntimePlan(ctx)
	runDecisionZeroFn.Signature = originalRunDecisionZeroSignature
	if invalidRunDecisionZeroErr == nil || !strings.Contains(invalidRunDecisionZeroErr.Error(), "zero-ticket run-decision ABI") {
		t.Fatalf("invalid zero-ticket run-decision ABI error = %v", invalidRunDecisionZeroErr)
	}
	retireFn := ssaPkg.Func(coroWaitRetireCompletedSymbolV1)
	originalRetireSignature := retireFn.Signature
	retireFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "token", types.Typ[types.UnsafePointer])),
		types.NewTuple(types.NewParam(token.NoPos, nil, "ok", types.Typ[types.Bool])), false)
	_, _, _, _, invalidRetireErr := requiredCoroProgramRuntimePlan(ctx)
	retireFn.Signature = originalRetireSignature
	if invalidRetireErr == nil || !strings.Contains(invalidRetireErr.Error(), "wait owner ABI") {
		t.Fatalf("invalid wait retire ABI error = %v", invalidRetireErr)
	}
	wantRoots := []string{
		"init",
		coroFrameAllocatorBootstrapSymbolV1,
		coroProgramBeginSymbolV1,
		coroProgramRunSymbolV1,
		coroProgramContinueSymbolV1,
		coroWaitPrepareSymbolV1,
		coroWaitRollbackSymbolV1,
		coroWaitRetireCompletedSymbolV1,
		"__llgo_coro_frame_alloc_v1",
		"__llgo_coro_frame_publish_v1",
		"__llgo_coro_await_prepare_v1",
		"__llgo_coro_preempt_poll_v1",
		"__llgo_coro_yield_prepare_v1",
		"__llgo_coro_park_prepare_v1",
		coroRunDecisionTakeSymbolV1,
		coroRunDecisionTakeZeroSymbolV1,
		"__llgo_coro_complete_prepare_v1",
		"__llgo_coro_frame_free_v1",
	}
	if len(roots) != len(wantRoots) {
		t.Fatalf("required runtime roots = %d, want %d", len(roots), len(wantRoots))
	}
	for index, root := range roots {
		wantDemand := coro.SyncDemand
		if index == 0 {
			wantDemand = coro.AsyncDemand
		}
		if root.Function == nil || root.Function.Name() != wantRoots[index] || root.Demand != wantDemand {
			t.Fatalf("required root %d = %+v, want %s/%s", index, root, wantRoots[index], wantDemand)
		}
	}
	for _, name := range []string{coroNativePostWaitSymbolV1, coroTimerPrepareAfterOrAbortSymbolV1, coroTimerRetireCompletedOrAbortSymbolV1} {
		if _, ok := requiredPlain[ssaPkg.Func(name)]; ok {
			t.Fatalf("inactive native timer hook %q entered the required plain island", name)
		}
	}
	timerCtx := &context{
		buildConf: &Config{
			Goos:                          "linux",
			Goarch:                        "amd64",
			EnableCoroChildAwait:          true,
			EnableCoroProgramBootstrapRun: true,
		},
		coroEmission:    ctx.coroEmission,
		coroSSAEmission: ctx.coroSSAEmission,
	}
	timerRoots, timerPlain, timerDirect, timerClosed, err := requiredCoroProgramRuntimePlan(timerCtx)
	if err != nil {
		t.Fatal(err)
	}
	wantTimerRoots := []string{
		"init",
		coroFrameAllocatorBootstrapSymbolV1,
		coroProgramBeginSymbolV1,
		coroProgramRunSliceSymbolV2,
		coroProgramContinueSliceSymbolV2,
		coroWaitPrepareSymbolV1,
		coroWaitRollbackSymbolV1,
		coroWaitRetireCompletedSymbolV1,
		coroNativePostWaitSymbolV1,
		coroTimerPrepareAfterOrAbortSymbolV1,
		coroTimerRetireCompletedOrAbortSymbolV1,
		"__llgo_coro_frame_alloc_v1",
		"__llgo_coro_frame_publish_v1",
		"__llgo_coro_await_prepare_v1",
		"__llgo_coro_preempt_poll_v1",
		"__llgo_coro_yield_prepare_v1",
		"__llgo_coro_park_prepare_v1",
		coroRunDecisionTakeSymbolV1,
		coroRunDecisionTakeZeroSymbolV1,
		"__llgo_coro_complete_prepare_v1",
		"__llgo_coro_frame_free_v1",
	}
	if len(timerRoots) != len(wantTimerRoots) {
		t.Fatalf("native timer runtime roots = %d, want %d", len(timerRoots), len(wantTimerRoots))
	}
	for index, root := range timerRoots {
		wantDemand := coro.SyncDemand
		if index == 0 {
			wantDemand = coro.AsyncDemand
		}
		if root.Function == nil || root.Function.Name() != wantTimerRoots[index] || root.Demand != wantDemand {
			t.Fatalf("native timer root %d = %+v, want %s/%s", index, root, wantTimerRoots[index], wantDemand)
		}
	}
	runSliceFn := ssaPkg.Func(coroProgramRunSliceSymbolV2)
	originalRunSliceSignature := runSliceFn.Signature
	runSliceFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer])),
		types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32])), false)
	_, _, _, _, invalidRunSliceErr := requiredCoroProgramRuntimePlan(timerCtx)
	runSliceFn.Signature = originalRunSliceSignature
	if invalidRunSliceErr == nil || !strings.Contains(invalidRunSliceErr.Error(), "run-slice ABI") {
		t.Fatalf("invalid native run-slice ABI error = %v", invalidRunSliceErr)
	}
	continueSliceFn := ssaPkg.Func(coroProgramContinueSliceSymbolV2)
	originalContinueSliceSignature := continueSliceFn.Signature
	continueSliceFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "executorSlot", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "executorGeneration", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "epoch", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "budget", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "out", types.NewPointer(types.Typ[types.Uint64])),
		),
		types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32])), false)
	_, _, _, _, invalidContinueSliceErr := requiredCoroProgramRuntimePlan(timerCtx)
	continueSliceFn.Signature = originalContinueSliceSignature
	if invalidContinueSliceErr == nil || !strings.Contains(invalidContinueSliceErr.Error(), "continue-slice ABI") {
		t.Fatalf("invalid native continue-slice ABI error = %v", invalidContinueSliceErr)
	}
	for _, name := range []string{
		coroNativePostWaitSymbolV1,
		coroTimerPrepareAfterOrAbortSymbolV1,
		coroTimerRetireCompletedOrAbortSymbolV1,
		coroTimerPrepareAfterSymbolV1,
		coroTimerRetireCompletedSymbolV1,
	} {
		if _, ok := timerPlain[ssaPkg.Func(name)]; !ok {
			t.Fatalf("native timer hook %q is absent from the required plain island", name)
		}
	}
	if len(timerDirect) != 0 || len(timerClosed) != 0 {
		t.Fatalf("native timer roots produced callback proofs: direct=%d dynamic=%d", len(timerDirect), len(timerClosed))
	}
	timerPrepareFn := ssaPkg.Func(coroTimerPrepareAfterOrAbortSymbolV1)
	originalTimerPrepareSignature := timerPrepareFn.Signature
	timerPrepareFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "token", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "delay", types.Typ[types.Uint64]),
			types.NewParam(token.NoPos, nil, "ticket", types.NewPointer(types.Typ[types.Uint32])),
			types.NewParam(token.NoPos, nil, "slot", types.NewPointer(types.Typ[types.Uint32])),
			types.NewParam(token.NoPos, nil, "generation", types.NewPointer(types.Typ[types.Uint32])),
		),
		types.NewTuple(), false)
	_, _, _, _, invalidTimerPrepareErr := requiredCoroProgramRuntimePlan(timerCtx)
	timerPrepareFn.Signature = originalTimerPrepareSignature
	if invalidTimerPrepareErr == nil || !strings.Contains(invalidTimerPrepareErr.Error(), "timer prepare-or-abort ABI") {
		t.Fatalf("invalid timer prepare ABI error = %v", invalidTimerPrepareErr)
	}
	timerRetireFn := ssaPkg.Func(coroTimerRetireCompletedOrAbortSymbolV1)
	originalTimerRetireSignature := timerRetireFn.Signature
	timerRetireFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "token", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "ticket", types.Typ[types.Uint64]),
			types.NewParam(token.NoPos, nil, "slot", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "generation", types.Typ[types.Uint32]),
		),
		types.NewTuple(), false)
	_, _, _, _, invalidTimerRetireErr := requiredCoroProgramRuntimePlan(timerCtx)
	timerRetireFn.Signature = originalTimerRetireSignature
	if invalidTimerRetireErr == nil || !strings.Contains(invalidTimerRetireErr.Error(), "timer retire-or-abort ABI") {
		t.Fatalf("invalid timer retire ABI error = %v", invalidTimerRetireErr)
	}
	panicHook := ssaPkg.Func("__llgo_coro_panic_prepare_v1")
	if panicHook == nil {
		t.Fatal("explicit-status panic prepare hook is absent from the runtime fixture")
	}
	if _, ok := requiredPlain[panicHook]; ok {
		t.Fatal("inactive explicit-status panic prepare hook entered the required plain island")
	}
	panicCtx := &context{
		buildConf: &Config{
			EnableCoroChildAwait:             true,
			EnableCoroProgramBootstrapRun:    true,
			EnableCoroExplicitStatusPanicABI: true,
		},
		coroEmission:    ctx.coroEmission,
		coroSSAEmission: ctx.coroSSAEmission,
	}
	panicRoots, panicPlain, panicDirect, panicClosed, err := requiredCoroProgramRuntimePlan(panicCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(panicRoots) != len(wantRoots)+1 ||
		panicRoots[len(panicRoots)-1].Function != panicHook ||
		panicRoots[len(panicRoots)-1].Demand != coro.SyncDemand {
		t.Fatalf("explicit-status runtime roots = %+v, want legacy roots plus exact panic prepare/sync", panicRoots)
	}
	if _, ok := panicPlain[panicHook]; !ok {
		t.Fatal("active explicit-status panic prepare hook is absent from the required plain island")
	}
	if len(panicDirect) != 0 || len(panicClosed) != 0 {
		t.Fatalf("explicit-status panic hook produced callback proofs: direct=%d dynamic=%d", len(panicDirect), len(panicClosed))
	}
	spawnCtx := &context{
		buildConf: &Config{
			EnableCoroChildAwait:          true,
			EnableCoroProgramBootstrapRun: true,
			EnableCoroClosedStaticSpawn:   true,
		},
		coroEmission:    ctx.coroEmission,
		coroSSAEmission: ctx.coroSSAEmission,
	}
	spawnRoots, spawnPlain, _, _, err := requiredCoroProgramRuntimePlan(spawnCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(spawnRoots) != len(wantRoots)+3 {
		t.Fatalf("closed-static-spawn runtime roots = %d, want %d", len(spawnRoots), len(wantRoots)+3)
	}
	for _, name := range []string{"__llgo_coro_spawn_begin_v1", "__llgo_coro_spawn_commit_v1", coroProgramMainReturnSymbolV1} {
		fn := ssaPkg.Func(name)
		if fn == nil {
			t.Fatalf("closed-static-spawn runtime hook %q is absent", name)
		}
		if _, ok := spawnPlain[fn]; !ok {
			t.Fatalf("closed-static-spawn runtime hook %q is not a required plain root", name)
		}
		found := false
		for _, root := range spawnRoots {
			if root.Function == fn && root.Demand == coro.SyncDemand {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("closed-static-spawn runtime hook %q has no sync root", name)
		}
	}
	if _, ok := requiredPlain[ssaPkg.Func("init")]; ok {
		t.Fatal("managed runtime.init leaked into the native required-plain island")
	}
	closureLoop := ssaPkg.Func("closureLoop")
	unrelatedLoop := ssaPkg.Func("unrelatedLoop")
	externalABI := ssaPkg.Func("externalABI")
	inlineIntrinsic := ssaPkg.Func("inlineIntrinsic")
	atomicExchange := ssaPkg.Func("atomicExchange")
	for _, fn := range []*ssa.Function{ssaPkg.Func("bootstrapHelper"), closureLoop, externalABI} {
		if _, ok := requiredPlain[fn]; !ok {
			t.Fatalf("required plain closure omitted %s", fn.Name())
		}
	}
	if _, ok := requiredPlain[unrelatedLoop]; ok {
		t.Fatal("required plain closure captured an unrelated function")
	}
	for _, intrinsic := range []*ssa.Function{inlineIntrinsic, atomicExchange} {
		if _, ok := requiredPlain[intrinsic]; ok {
			t.Fatalf("compiler-inline no-suspend intrinsic %q entered the runtime plain-function island", intrinsic.Name())
		}
	}
	if semantics, intrinsic, err := emission.CoroIntrinsicSemantics(inlineIntrinsic); err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("inline intrinsic semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	if semantics, intrinsic, err := emission.CoroIntrinsicSemantics(atomicExchange); err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("atomic exchange semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}

	input := CoroPlanInput{
		Program:                ssaPkg.Prog,
		EmissionUniverse:       ssaEmission,
		resolveFunction:        emission.Resolve,
		functionBackground:     emission.FunctionBackground,
		intrinsicCallSemantics: emission.CoroIntrinsicCallSiteSemantics,
		requiredRoots:          roots,
		requiredPlain:          requiredPlain,
		requiredDirectPlain:    directPlain,
		requiredClosedDynamic:  closedDynamic,
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	analyze := func(classify func(*ssa.Function) (coro.SSAFunctionPolicy, error)) (*coro.SSAPlan, error) {
		return input.Analyze(coro.Roots{{Function: unrelatedLoop, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			MaxPlainInstructions: -1,
			ClassifyFunction:     classify,
			FunctionIDs:          functionIDs,
		})
	}
	plan, err := analyze(nil)
	if err != nil {
		t.Fatal(err)
	}
	panicInput := input
	panicInput.requiredRoots = panicRoots
	panicInput.requiredPlain = panicPlain
	panicInput.requiredDirectPlain = panicDirect
	panicInput.requiredClosedDynamic = panicClosed
	panicPlan, err := panicInput.Analyze(coro.Roots{{Function: unrelatedLoop, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		FunctionIDs:          functionIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	panicHookPlan, ok := panicPlan.FunctionPlan(panicHook)
	if !ok || panicHookPlan.Emission != coro.EmitPlain || panicHookPlan.Demand != coro.SyncDemand ||
		panicHookPlan.FuncRep != coro.DirectPlain || panicHookPlan.Effect.MaySuspend() ||
		panicHookPlan.Exec.Contains(coro.NeedsPreempt) {
		t.Fatalf("explicit-status panic prepare hook plan = %+v, want required sync direct-plain", panicHookPlan)
	}
	closurePlan, ok := plan.FunctionPlan(closureLoop)
	if !ok || closurePlan.Exec.Contains(coro.NeedsPreempt) || closurePlan.Effect.MaySuspend() || closurePlan.Emission != coro.EmitPlain {
		t.Fatalf("required closure loop plan = %+v, want one trusted plain body", closurePlan)
	}
	pollPlan, ok := plan.FunctionPlan(ssaPkg.Func("__llgo_coro_preempt_poll_v1"))
	if !ok || pollPlan.Effect.MaySuspend() || pollPlan.Exec.Contains(coro.NeedsPreempt) || pollPlan.Emission != coro.EmitPlain {
		t.Fatalf("preempt poll plan = %+v, want one trusted plain atomic poll", pollPlan)
	}
	parkHookPlan, ok := plan.FunctionPlan(ssaPkg.Func("__llgo_coro_park_prepare_v1"))
	if !ok || parkHookPlan.Effect.MaySuspend() || parkHookPlan.Exec.Contains(coro.NeedsPreempt) ||
		parkHookPlan.Emission != coro.EmitPlain || parkHookPlan.Demand != coro.SyncDemand ||
		parkHookPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("park prepare hook plan = %+v, want one required sync direct-plain body", parkHookPlan)
	}
	runDecisionPlan, ok := plan.FunctionPlan(runDecisionFn)
	if !ok || runDecisionPlan.Effect.MaySuspend() || runDecisionPlan.Exec.Contains(coro.NeedsPreempt) ||
		runDecisionPlan.Emission != coro.EmitPlain || runDecisionPlan.Demand != coro.SyncDemand ||
		runDecisionPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("run-decision hook plan = %+v, want one required sync direct-plain body", runDecisionPlan)
	}
	runDecisionZeroPlan, ok := plan.FunctionPlan(runDecisionZeroFn)
	if !ok || runDecisionZeroPlan.Effect.MaySuspend() || runDecisionZeroPlan.Exec.Contains(coro.NeedsPreempt) ||
		runDecisionZeroPlan.Emission != coro.EmitPlain || runDecisionZeroPlan.Demand != coro.SyncDemand ||
		runDecisionZeroPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("zero-ticket run-decision hook plan = %+v, want one required sync direct-plain body", runDecisionZeroPlan)
	}
	unrelatedPlan, ok := plan.FunctionPlan(unrelatedLoop)
	if !ok || !unrelatedPlan.Exec.Contains(coro.NeedsPreempt) || !unrelatedPlan.Effect.Contains(coro.YieldOnly) || unrelatedPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("unrelated loop plan = %+v, want coroutine preemption", unrelatedPlan)
	}
	externalPlan, ok := plan.FunctionPlan(externalABI)
	if !ok || externalPlan.External != coro.ExternalKnown || externalPlan.Emission != coro.EmitExternal || externalPlan.Demand != coro.SyncDemand {
		t.Fatalf("required bodyless ABI plan = %+v, want sync external-known", externalPlan)
	}
	var intrinsicCall ssa.CallInstruction
	for _, call := range coroPlanTestCalls(ssaPkg.Func("bootstrapHelper")) {
		if call.Common().StaticCallee() == inlineIntrinsic {
			intrinsicCall = call
			break
		}
	}
	if intrinsicCall == nil || !plan.ElidesCall(intrinsicCall) {
		t.Fatalf("inline intrinsic call = %v; want exact frontend-lowered call site", intrinsicCall)
	}
	if _, ok := plan.CallPlan(intrinsicCall); ok {
		t.Fatal("compiler-inline no-suspend intrinsic unexpectedly has a managed CallPlan")
	}

	metadata := coro.PlanDigestMetadata{
		CoroABI: coro.PhysicalABIV1, SchedulerABI: coro.SchedulerProgramBootstrapABIV2,
		PanicABI: coro.PanicLegacyABIV0, FuncRepABI: coro.FuncRepABIV0,
		TargetTriple: "x86_64-unknown-linux-gnu", PointerBits: 64,
		Endianness: "little", DataLayout: "e-p:64:64",
	}
	digest, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	second, err := analyze(nil)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if secondDigest != digest {
		t.Fatalf("required runtime plan digest changed: %s != %s", secondDigest, digest)
	}

	irqPlan, err := analyze(func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == closureLoop {
			return coro.SSAFunctionPolicy{Exec: coro.IRQUnsafe}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err != nil {
		t.Fatalf("required plain ordinary-G IRQ-unsafe plan: %v", err)
	}
	irqClosure, ok := irqPlan.FunctionPlan(closureLoop)
	if !ok || irqClosure.Emission != coro.EmitPlain || !irqClosure.Exec.Contains(coro.IRQUnsafe) ||
		irqClosure.Exec.Contains(coro.ThreadAffine|coro.BlockForeign|coro.OpaqueExec) {
		t.Fatalf("required plain IRQ-unsafe closure plan = %+v, want exact ordinary-G plain implementation", irqClosure)
	}

	conflicts := []struct {
		name   string
		target *ssa.Function
		policy coro.SSAFunctionPolicy
		want   string
	}{
		{name: "effect", target: closureLoop, policy: coro.SSAFunctionPolicy{Effect: coro.MayPark}, want: "required no-suspend policy"},
		{name: "exec", target: closureLoop, policy: coro.SSAFunctionPolicy{Exec: coro.ThreadAffine}, want: "required plain execution policy"},
		{name: "dispatch", target: closureLoop, policy: coro.SSAFunctionPolicy{NeedsDispatch: true}, want: "required direct representation"},
		{name: "defined external", target: closureLoop, policy: coro.SSAFunctionPolicy{External: coro.ExternalKnown, OverrideExternal: true}, want: "required defined classification"},
		{name: "bodyless external", target: externalABI, policy: coro.SSAFunctionPolicy{External: coro.ExternalUnknownManaged, OverrideExternal: true}, want: "frontend C declaration"},
	}
	for _, test := range conflicts {
		t.Run(test.name, func(t *testing.T) {
			_, err := analyze(func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == test.target {
					return test.policy, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("conflict error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRequiredCoroProgramRuntimePlanKeepsEntryInitWithoutRunnableBootstrap(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func __llgo_coro_program_begin_v1() {}
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_park_prepare_v1() {}
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_frame_free_v1() {}
`, nil)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: llssa.PkgRuntime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &context{
		buildConf:       &Config{EnableCoroChildAwait: true},
		coroEmission:    emission,
		coroSSAEmission: ssaEmission,
	}
	roots, requiredPlain, directPlain, closedDynamic, err := requiredCoroProgramRuntimePlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].Function != ssaPkg.Func("init") || roots[0].Demand != coro.SyncDemand {
		t.Fatalf("entry-only runtime roots = %+v, want exact runtime package init/sync", roots)
	}
	if _, ok := requiredPlain[ssaPkg.Func("init")]; !ok {
		t.Fatal("entry-only runtime init is absent from required plain closure")
	}
	for _, name := range []string{
		coroFrameAllocatorBootstrapSymbolV1,
		coroProgramBeginSymbolV1,
		coroProgramRunSymbolV1,
		coroProgramContinueSymbolV1,
		"__llgo_coro_frame_alloc_v1",
		"__llgo_coro_frame_publish_v1",
		"__llgo_coro_await_prepare_v1",
		"__llgo_coro_preempt_poll_v1",
		"__llgo_coro_yield_prepare_v1",
		"__llgo_coro_park_prepare_v1",
		coroRunDecisionTakeSymbolV1,
		coroRunDecisionTakeZeroSymbolV1,
		"__llgo_coro_complete_prepare_v1",
		"__llgo_coro_frame_free_v1",
	} {
		if _, ok := requiredPlain[ssaPkg.Func(name)]; ok {
			t.Fatalf("descriptor-only child-await plan trusted runnable hook %q", name)
		}
	}
	if len(directPlain) != 0 || len(closedDynamic) != 0 {
		t.Fatalf("entry-only runtime plan produced callback proofs: direct=%d dynamic=%d", len(directPlain), len(closedDynamic))
	}
}

func TestRequiredCoroProgramRuntimePlanRejectsInvalidIntrinsicSite(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func __llgo_coro_program_begin_v1() { bootstrapHelper() }
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
func __llgo_coro_wait_prepare_v1(unsafe.Pointer, *uint32, *uint32, *uint32, *uint32, *uint32) bool { return false }
func __llgo_coro_wait_rollback_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_wait_retire_completed_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_park_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_frame_free_v1() {}
func intrinsicInput() string { return "not constant at the call site" }
func bootstrapHelper() { inlineIntrinsic(intrinsicInput()) }
//llgo:link inlineIntrinsic llgo.cstr
func inlineIntrinsic(string) *byte
`, nil)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: llssa.PkgRuntime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &context{
		buildConf:       &Config{EnableCoroChildAwait: true, EnableCoroProgramBootstrapRun: true},
		coroEmission:    emission,
		coroSSAEmission: ssaEmission,
	}
	_, _, _, _, err = requiredCoroProgramRuntimePlan(ctx)
	if err == nil || !strings.Contains(err.Error(), "requires exactly one compile-time string constant argument") {
		t.Fatalf("invalid runtime-closure intrinsic error = %v; want exact call-site rejection", err)
	}
}

func TestRequiredCoroProgramRuntimePlanDirectPlainCFunctionArgument(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
//llgo:type C
type CCallback func()

var dynamic func()

func installC(CCallback) {}
func syncCallback() { for i := 0; i < 2; i++ {} }
func dynamicCallback() { dynamic() }
func install() {
	installC(CCallback(syncCallback))
	installC(CCallback(dynamicCallback))
}
`)
	if len(fixture.directPlain) != 1 {
		t.Fatalf("required direct-plain callbacks = %d, want 1", len(fixture.directPlain))
	}
	use := fixture.directPlain[0]
	syncCallback := fixture.pkg.Func("syncCallback")
	dynamicCallback := fixture.pkg.Func("dynamicCallback")
	if use.target != syncCallback || use.call.Parent() != fixture.pkg.Func("install") || use.argument != 0 {
		t.Fatalf("required direct-plain callback = %+v, want install arg0 -> syncCallback", use)
	}
	if _, ok := fixture.requiredPlain[syncCallback]; !ok {
		t.Fatal("sync C callback was not added to the required plain island")
	}
	if _, ok := fixture.requiredPlain[dynamicCallback]; ok {
		t.Fatal("dynamic C callback incorrectly entered the required plain island")
	}

	plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	callbackPlan, ok := plan.FunctionPlan(syncCallback)
	if !ok || callbackPlan.Effect != coro.NoSuspend || callbackPlan.Exec.Contains(coro.NeedsPreempt) ||
		callbackPlan.FuncRep != coro.DirectPlain || callbackPlan.Primary != coro.PrimaryPlain || callbackPlan.Emission != coro.EmitPlain {
		t.Fatalf("sync C callback plan = %+v, want one non-suspending direct plain body", callbackPlan)
	}
	valuePlan, ok := plan.ValuePlan(use.call.Common().Args[use.argument])
	if !ok || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != coro.DirectPlain || valuePlan.Funcs[0].MayBeNil || len(valuePlan.Funcs[0].Targets) != 1 {
		t.Fatalf("sync C callback value plan = %+v, present=%t", valuePlan, ok)
	}
	dynamicPlan, ok := plan.FunctionPlan(dynamicCallback)
	if !ok || !dynamicPlan.Effect.IsOpaque() || dynamicPlan.FuncRep != coro.Dispatch {
		t.Fatalf("dynamic C callback plan = %+v, want real Dispatch blocker", dynamicPlan)
	}

	var dynamicUse ssa.CallInstruction
	for _, block := range fixture.pkg.Func("install").Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call == use.call {
				continue
			}
			if target, ok := exactCoroStaticFunctionValue(fixture.ctx, call.Common().Args[0]); ok && target == dynamicCallback {
				dynamicUse = call
			}
		}
	}
	if dynamicUse == nil {
		t.Fatal("dynamic C callback use not found")
	}
	_, err = fixture.analyze(coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyDirectPlainCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			return call == dynamicUse && argument == 0, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "builder cannot authorize direct-plain ABI") {
		t.Fatalf("unauthorized builder direct-plain error = %v", err)
	}
}

func TestRequiredCoroProgramRuntimePlanDoesNotTraverseFrozenCStubBodies(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
//llgo:type C
type CCallback func()

func installC(CCallback) {}
func hidden() {}

//llgo:link cLeaf C.c_leaf
func cLeaf() { hidden() }

//llgo:link cCallback C.c_callback
func cCallback() { hidden() }

func install() {
	cLeaf()
	installC(CCallback(cCallback))
}
`)
	cLeaf := fixture.pkg.Func("cLeaf")
	hidden := fixture.pkg.Func("hidden")
	cCallback := fixture.pkg.Func("cCallback")
	if _, ok := fixture.requiredPlain[cLeaf]; !ok {
		t.Fatal("exact frozen C static callee was not retained as a required plain leaf")
	}
	if _, ok := fixture.requiredPlain[hidden]; ok {
		t.Fatal("callee reachable only through a frozen C fallback body entered requiredPlain")
	}
	if _, ok := fixture.requiredPlain[cCallback]; ok {
		t.Fatal("bodyful frozen C callback was proved from its non-emitted fallback body")
	}
	if len(fixture.directPlain) != 0 {
		t.Fatalf("frozen C stub produced %d direct-plain callback uses", len(fixture.directPlain))
	}
}

func TestRequiredCoroProgramRuntimePlanDirectPlainCFunctionArgumentFailsClosed(t *testing.T) {
	t.Run("other boundary", func(t *testing.T) {
		fixture := buildRequiredCoroRuntimeFixture(t, `
//llgo:type C
type CCallback func()

var escaped CCallback

func installC(CCallback) {}
func callback() {}
func install() {
	value := CCallback(callback)
	installC(value)
	escaped = value
}
`)
		if len(fixture.directPlain) != 1 {
			t.Fatalf("required direct-plain callbacks = %d, want 1", len(fixture.directPlain))
		}
		if _, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1}); err == nil || !strings.Contains(err.Error(), "another canonical boundary") {
			t.Fatalf("other-boundary error = %v", err)
		}
	})

	t.Run("suspending body", func(t *testing.T) {
		fixture := buildRequiredCoroRuntimeFixture(t, `
//llgo:type C
type CCallback func()

var channel chan int

func installC(CCallback) {}
func callback() { <-channel }
func install() { installC(CCallback(callback)) }
`)
		if len(fixture.directPlain) != 1 {
			t.Fatalf("required direct-plain callbacks = %d, want 1", len(fixture.directPlain))
		}
		if _, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1}); err == nil || !strings.Contains(err.Error(), "not a defined closed singleton with one non-suspending plain body") {
			t.Fatalf("suspending callback error = %v", err)
		}
	})
}

func TestCoroPlanInputClassifiesFrozenBodylessCDeclarations(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
//llgo:link requiredC C.required_c
func requiredC()

//llgo:link foreignC C.foreign_c
func foreignC()

func goDeclaration()

func install() { requiredC() }
func callC() { foreignC() }
func callGo() { goDeclaration() }
`)
	callC := fixture.pkg.Func("callC")
	callGo := fixture.pkg.Func("callGo")
	config := coro.SSAConfig{MaxPlainInstructions: -1, FunctionIDs: fixture.functionIDs}
	plan, err := fixture.input.Analyze(coro.Roots{
		{Function: callC, Demand: coro.AsyncDemand},
		{Function: callGo, Demand: coro.AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}

	required := functionPlanForBuildTest(t, plan, fixture.pkg.Func("requiredC"))
	if required.External != coro.ExternalKnown || required.Effect != coro.NoSuspend || required.Exec.Contains(coro.BlockForeign) ||
		required.FuncRep != coro.DirectPlain || required.Emission != coro.EmitExternal {
		t.Fatalf("required scheduler-stack C declaration = %+v, want trusted external-known direct plain", required)
	}
	foreign := functionPlanForBuildTest(t, plan, fixture.pkg.Func("foreignC"))
	if foreign.External != coro.ExternalUnknownForeign || foreign.Effect != coro.NoSuspend || !foreign.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) ||
		foreign.FuncRep != coro.DirectPlain || foreign.Emission != coro.EmitExternal {
		t.Fatalf("ordinary frozen C declaration = %+v, want external-unknown-foreign direct plain", foreign)
	}
	goDeclaration := functionPlanForBuildTest(t, plan, fixture.pkg.Func("goDeclaration"))
	if goDeclaration.External != coro.ExternalUnknownManaged || !goDeclaration.Effect.IsOpaque() || !goDeclaration.Exec.IsOpaque() ||
		goDeclaration.FuncRep != coro.Dispatch || goDeclaration.Emission != coro.EmitExternal {
		t.Fatalf("ordinary bodyless Go declaration = %+v, want unknown-managed Dispatch", goDeclaration)
	}

	callCPlan := functionPlanForBuildTest(t, plan, callC)
	if !callCPlan.Effect.Contains(coro.WaitForeign) || callCPlan.Effect.IsOpaque() {
		t.Fatalf("C caller plan = %+v, want precise WaitForeign", callCPlan)
	}
	cCall := onlyBuildTestCall(t, callC)
	if got, ok := plan.CallPlan(cCall); !ok || got.Kind != coro.CallForeign || got.Rep != coro.DirectPlain || got.Open {
		t.Fatalf("C static CallPlan = %+v, present=%t", got, ok)
	}
	goCall := onlyBuildTestCall(t, callGo)
	if got, ok := plan.CallPlan(goCall); !ok || got.Kind != coro.CallDirect || got.Rep != coro.Dispatch || got.Open {
		t.Fatalf("Go declaration CallPlan = %+v, present=%t", got, ok)
	}

	known, err := fixture.input.Analyze(coro.Roots{{Function: callC, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		FunctionIDs:          fixture.functionIDs,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == fixture.pkg.Func("foreignC") {
				return coro.SSAFunctionPolicy{
					Effect:           coro.WaitHost,
					Exec:             coro.ThreadAffine,
					External:         coro.ExternalKnown,
					OverrideExternal: true,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	knownForeign := functionPlanForBuildTest(t, known, fixture.pkg.Func("foreignC"))
	if !known.IgnoresBody(fixture.pkg.Func("foreignC")) || knownForeign.External != coro.ExternalKnown ||
		!knownForeign.Effect.Contains(coro.WaitHost) || !knownForeign.Exec.Contains(coro.ThreadAffine) ||
		knownForeign.FuncRep != coro.DirectCoro || knownForeign.Emission != coro.EmitExternal {
		t.Fatalf("explicit frozen C summary = %+v, ignored=%t; want preserved known async/host policy", knownForeign, known.IgnoresBody(fixture.pkg.Func("foreignC")))
	}
}

func TestCoroPlanInputRejectsUnprovenBodylessRequiredDeclarations(t *testing.T) {
	for _, test := range []struct {
		name      string
		directive string
	}{
		{name: "Go"},
		{name: "Python", directive: "//llgo:link bad py.bad\n"},
		{name: "intrinsic", directive: "//llgo:link bad llgo.unreachable\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixture(t, test.directive+`func bad()
func install() { bad() }
`)
			if _, ok := fixture.requiredPlain[fixture.pkg.Func("bad")]; !ok {
				t.Fatalf("bodyless %s declaration did not enter the static required closure", test.name)
			}
			if _, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1}); err == nil || !strings.Contains(err.Error(), "has no frozen frontend C ABI proof") {
				t.Fatalf("bodyless %s required declaration error = %v", test.name, err)
			}
		})
	}
}

func TestCoroPlanInputRejectsBodyfulNonGoRequiredDeclarations(t *testing.T) {
	for _, test := range []struct {
		name      string
		directive string
	}{
		{name: "Python", directive: "//llgo:link bad py.bad\n"},
		{name: "intrinsic", directive: "//llgo:link bad llgo.unreachable\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixture(t, test.directive+`func bad() {}
func install() { bad() }
`)
			if _, ok := fixture.requiredPlain[fixture.pkg.Func("bad")]; !ok {
				t.Fatalf("bodyful %s declaration did not enter the exact static required closure", test.name)
			}
			if _, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1}); err == nil || !strings.Contains(err.Error(), "has no frozen frontend C ABI proof") {
				t.Fatalf("bodyful %s required declaration error = %v", test.name, err)
			}
		})
	}
}

func functionPlanForBuildTest(t *testing.T, plan *coro.SSAPlan, fn *ssa.Function) coro.FunctionPlan {
	t.Helper()
	function, ok := plan.FunctionPlan(fn)
	if !ok {
		t.Fatalf("missing FunctionPlan for %s", fn)
	}
	return function
}

func onlyBuildTestCall(t *testing.T, fn *ssa.Function) ssa.CallInstruction {
	t.Helper()
	var result ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
				continue
			}
			if result != nil {
				t.Fatalf("%s has multiple non-builtin calls", fn)
			}
			result = call
		}
	}
	if result == nil {
		t.Fatalf("%s has no non-builtin call", fn)
	}
	return result
}

type requiredCoroRuntimeFixture struct {
	pkg           *ssa.Package
	ctx           *context
	input         CoroPlanInput
	requiredPlain map[*ssa.Function]struct{}
	directPlain   []requiredCoroDirectPlainCallArgument
	closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	functionIDs   coro.FunctionIDConfig
}

func (f requiredCoroRuntimeFixture) analyze(config coro.SSAConfig) (*coro.SSAPlan, error) {
	config.FunctionIDs = f.functionIDs
	return f.input.Analyze(nil, config)
}

func buildRequiredCoroRuntimeFixture(t *testing.T, body string) requiredCoroRuntimeFixture {
	t.Helper()
	source := `package runtime
import "unsafe"
func __llgo_coro_program_begin_v1() { install() }
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
func __llgo_coro_wait_prepare_v1(unsafe.Pointer, *uint32, *uint32, *uint32, *uint32, *uint32) bool { return false }
func __llgo_coro_wait_rollback_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_wait_retire_completed_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_park_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_frame_free_v1() {}
` + body
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, source, nil)
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	cl.ParsePkgSyntax(prog, ssaPkg.Pkg, files)
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: llssa.PkgRuntime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &context{
		prog:                        prog,
		buildConf:                   &Config{EnableCoroChildAwait: true, EnableCoroProgramBootstrapRun: true},
		coroEmission:                emission,
		coroSSAEmission:             ssaEmission,
		coroTLSDestructorFixturePkg: llssa.PkgRuntime,
	}
	roots, requiredPlain, directPlain, closedDynamic, err := requiredCoroProgramRuntimePlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	return requiredCoroRuntimeFixture{
		pkg: ssaPkg,
		ctx: ctx,
		input: CoroPlanInput{
			Program:               ssaPkg.Prog,
			EmissionUniverse:      ssaEmission,
			resolveFunction:       emission.Resolve,
			functionBackground:    emission.FunctionBackground,
			requiredRoots:         roots,
			requiredPlain:         requiredPlain,
			requiredDirectPlain:   directPlain,
			requiredClosedDynamic: closedDynamic,
		},
		requiredPlain: requiredPlain,
		directPlain:   directPlain,
		closedDynamic: closedDynamic,
		functionIDs:   functionIDs,
	}
}

func TestBuildCoroPlanInstallsArchiveDigest(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", `package p; func F(value int) int { return value + 1 }`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file}
	ssaPkg, _, err := ssautil.BuildPackage(
		&types.Config{Importer: importer.Default()},
		fset,
		types.NewPackage("example.com/p", "p"),
		files,
		ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
	)
	if err != nil {
		t.Fatal(err)
	}
	aPkg := &aPackage{
		Package: &packages.Package{
			ID:      "example.com/p",
			PkgPath: "example.com/p",
			Name:    "p",
			Types:   ssaPkg.Pkg,
			Syntax:  files,
		},
		SSA: ssaPkg,
	}
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		progSSA: ssaPkg.Prog,
		prog:    prog,
		buildConf: &Config{
			EnableCoroEntryResolution: true,
			CoroPlanBuilder: func(input CoroPlanInput) (*coro.SSAPlan, error) {
				return input.Analyze(coro.Roots{{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand}}, coro.SSAConfig{
					MaxPlainInstructions: -1,
				})
			},
		},
	}
	if err := buildCoroPlan(ctx, aPkg); err != nil {
		t.Fatal(err)
	}
	if len(ctx.coroPlanDigest) != sha256.Size*2 {
		t.Fatalf("CoroPlanDigest length = %d, want %d", len(ctx.coroPlanDigest), sha256.Size*2)
	}
	if ctx.clCompilation == nil || ctx.clCompilation.CoroPlanDigest != ctx.coroPlanDigest {
		t.Fatalf("compilation digest = %+v, want %q", ctx.clCompilation, ctx.coroPlanDigest)
	}
	if ctx.coroPlanMetadata.CoroABI != coro.EntryResolutionABIV0 ||
		ctx.coroPlanMetadata.SchedulerABI != coro.SchedulerNoneABIV0 ||
		ctx.coroPlanMetadata.TargetTriple != prog.TargetSpec().Triple {
		t.Fatalf("installed digest metadata = %+v", ctx.coroPlanMetadata)
	}
	if !ctx.canUsePackageCache() {
		t.Fatal("complete active coroutine plan did not enable package cache")
	}
	manifest := newManifestBuilder()
	ctx.collectCommonInputs(manifest)
	if manifest.common.CoroPlanDigest != ctx.coroPlanDigest || manifest.common.CoroDataLayout != prog.DataLayout() {
		t.Fatalf("manifest coroutine inputs = %+v", manifest.common)
	}

	explicitProg := llssa.NewProgram(nil)
	defer explicitProg.Dispose()
	explicitCtx := &context{
		progSSA: ssaPkg.Prog,
		prog:    explicitProg,
		buildConf: &Config{
			EnableCoroEntryResolution:        true,
			EnableCoroExplicitStatusPanicABI: true,
			CoroPlanBuilder: func(input CoroPlanInput) (*coro.SSAPlan, error) {
				return input.Analyze(coro.Roots{{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand}}, coro.SSAConfig{
					MaxPlainInstructions: -1,
				})
			},
		},
	}
	if err := buildCoroPlan(explicitCtx, aPkg); err == nil ||
		!strings.Contains(err.Error(), coro.PanicExplicitStatusABIV0) ||
		!strings.Contains(err.Error(), "lowering and runtime semantics are not implemented") {
		t.Fatalf("explicit-status panic ABI build error = %v", err)
	}
	if explicitCtx.coroPlan != nil || explicitCtx.clCompilation != nil || explicitCtx.coroPlanDigest != "" || explicitCtx.coroPlanMetadata.PanicABI != "" {
		t.Fatalf("identity-only explicit-status panic build retained active state: plan=%v compilation=%v digest=%q metadata=%+v",
			explicitCtx.coroPlan, explicitCtx.clCompilation, explicitCtx.coroPlanDigest, explicitCtx.coroPlanMetadata)
	}

	badProg := llssa.NewProgram(nil)
	defer badProg.Dispose()
	badCtx := &context{
		progSSA: ssaPkg.Prog,
		prog:    badProg,
		buildConf: &Config{
			EnableCoroEntryResolution: true,
			CoroPlanBuilder: func(input CoroPlanInput) (*coro.SSAPlan, error) {
				return input.Analyze(coro.Roots{{Function: ssaPkg.Func("F"), Demand: coro.SyncDemand}}, coro.SSAConfig{
					FunctionIDs:          coro.FunctionIDConfig{CoroABI: "conflicting-coro-abi"},
					MaxPlainInstructions: -1,
				})
			},
		},
	}
	if err := buildCoroPlan(badCtx, aPkg); err == nil || !strings.Contains(err.Error(), "does not match FunctionID ABI") {
		t.Fatalf("conflicting builder ABI error = %v", err)
	}
	if badCtx.coroPlan != nil || badCtx.clCompilation != nil || badCtx.coroPlanDigest != "" {
		t.Fatal("conflicting builder ABI installed partial coroutine state")
	}
}

func TestCoroPhysicalABICacheRegistrationPreservesCollectedFuncInfo(t *testing.T) {
	const source = `package p
func Leaf(value uint32) uint32 { return value + 1 }
`
	compile := func(cacheHit bool) []funcInfoRecord {
		t.Helper()
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "p.go", source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files := []*ast.File{file}
		ssaPkg, _, err := ssautil.BuildPackage(
			&types.Config{Importer: importer.Default()},
			fset,
			types.NewPackage("example.com/p", "p"),
			files,
			ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
		)
		if err != nil {
			t.Fatal(err)
		}
		prog := llssa.NewProgram(nil)
		defer prog.Dispose()
		prog.EnableFuncInfoMetadata(true)
		universe, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{SSA: ssaPkg, Files: files}})
		if err != nil {
			t.Fatal(err)
		}
		ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		functionIDs := universe.FunctionIDConfig()
		functionIDs.CoroABI = coro.PhysicalABIV0
		functionIDs.SchedulerABI = coro.SchedulerNoneABIV0
		functionIDs.ArchiveReady = true
		leaf := ssaPkg.Func("Leaf")
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: leaf, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == leaf {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		lpkg, _, err := cl.NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, cl.PackageOptions{
			Compilation: &cl.Compilation{
				CoroPlan:                  plan,
				EnableCoroEntryResolution: true,
				EnableCoroPhysicalABI:     true,
				CoroPlanDigest:            strings.Repeat("0", 64),
				CoroABI:                   coro.PhysicalABIV0,
				SchedulerABI:              coro.SchedulerNoneABIV0,
				PanicABI:                  coro.PanicLegacyABIV0,
				FuncRepABI:                coro.FuncRepABIV0,
				EmissionUniverse:          universe,
			},
			CacheHit: cacheHit,
		})
		if err != nil {
			t.Fatal(err)
		}
		return collectFuncInfo([]Package{{LPkg: lpkg}})
	}

	sourceRecords := compile(false)
	cachedRecords := compile(true)
	if !reflect.DeepEqual(cachedRecords, sourceRecords) {
		t.Fatalf("cache registration funcinfo differs from source compilation:\nsource: %+v\ncached: %+v", sourceRecords, cachedRecords)
	}
	wantSymbol := "example.com/p.Leaf$coro"
	wantDisplay := "example.com/p.Leaf"
	found := false
	for _, record := range cachedRecords {
		if record.symbol == "example.com/p.Leaf" {
			t.Fatalf("cache registration exposed legacy plain symbol: %+v", record)
		}
		if record.symbol == wantSymbol {
			found = true
			if record.name != wantDisplay {
				t.Fatalf("coroutine funcinfo display name = %q, want %q", record.name, wantDisplay)
			}
		}
	}
	if !found {
		t.Fatalf("cache registration funcinfo is missing %q: %+v", wantSymbol, cachedRecords)
	}
}

func TestCoroPlanBuilderRunsBeforeCodegenWithoutChangingIR(t *testing.T) {
	t.Setenv(llgoBuildCache, "on")
	cacheRoot := t.TempDir()
	oldCacheRootFunc := cacheRootFunc
	cacheRootFunc = func() string { return cacheRoot }
	t.Cleanup(func() { cacheRootFunc = oldCacheRootFunc })

	var (
		builderCalls       int
		builderDone        bool
		planned            *coro.SSAPlan
		mainFn             *ssa.Function
		cacheRegistrations int
		sourceCompilations int
	)
	observed := make(map[*ssa.Package]int)
	builder := func(input CoroPlanInput) (*coro.SSAPlan, error) {
		builderCalls++
		var err error
		mainFn, err = findSingleSSAMain(input.Program)
		if err != nil {
			return nil, err
		}
		planned, err = input.Analyze(coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{})
		if err == nil {
			builderDone = true
		}
		return planned, err
	}

	baselineIR, baselineModules := buildModeGenIR(t, "../../cl/_testgo/chan", nil, nil, nil)
	plannedIR, plannedModules := buildModeGenIR(t, "../../cl/_testgo/chan", builder, func(pkg *ssa.Package, plan *coro.SSAPlan) {
		if plan != planned {
			t.Errorf("package %s observed plan %p, want compilation plan %p", pkg, plan, planned)
		}
		observed[pkg]++
	}, func(Package) {
		if !builderDone {
			t.Error("ModuleHook ran before CoroPlanBuilder completed")
		}
	}, func(pkg Package) {
		if pkg.CacheHit {
			cacheRegistrations++
			if observed[pkg.SSA] != 0 {
				t.Errorf("cached package %s reported coroutine source compilation", pkg.PkgPath)
			}
			return
		}
		sourceCompilations++
		if observed[pkg.SSA] != 1 {
			t.Errorf("source package %s observed coroutine plan %d times, want 1", pkg.PkgPath, observed[pkg.SSA])
		}
	})
	if builderCalls != 1 {
		t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
	}
	if planned == nil || mainFn == nil {
		t.Fatal("CoroPlanBuilder did not publish a plan for main")
	}
	if sourceCompilations == 0 || len(observed) != sourceCompilations {
		t.Fatalf("source compilation observations = %d for %d packages, want one per package", len(observed), sourceCompilations)
	}
	if cacheRegistrations == 0 {
		t.Fatal("planned build had no cache registration to verify")
	}
	id, ok := planned.FunctionID(mainFn)
	if !ok {
		t.Fatal("main function is absent from coroutine plan")
	}
	mainPlan, ok := planned.BasePlan().Lookup(id)
	if !ok || !mainPlan.Effect.Contains(coro.MayPark) || mainPlan.Demand != coro.AsyncDemand {
		t.Fatalf("main coroutine plan = %+v, %v", mainPlan, ok)
	}

	if plannedIR != baselineIR {
		t.Fatal("report-only CoroPlanBuilder changed emitted LLVM IR")
	}
	if len(plannedModules) == 0 || !reflect.DeepEqual(plannedModules, baselineModules) {
		t.Fatalf("report-only CoroPlanBuilder changed generated package modules:\nbaseline: %x\nplanned: %x", baselineModules, plannedModules)
	}
}

func TestCoroPlanInputCanonicalizesPatchedRoot(t *testing.T) {
	original := buildSSAOrderTestPackage(t, `package p
func f() {}
func g() {}
`)
	canonical := original.Pkg.Func("g")
	universe, err := coro.NewSSAEmissionUniverse(original.Prog, []*ssa.Function{canonical})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          original.Prog,
		EmissionUniverse: universe,
		resolveFunction: func(fn *ssa.Function) (*ssa.Function, bool) {
			if fn == original {
				return canonical, true
			}
			return fn, universe.Contains(fn)
		},
	}
	roots := coro.Roots{{Function: original, Demand: coro.SyncDemand}}
	builderResolverCalls := 0
	plan, err := input.Analyze(roots, coro.SSAConfig{
		ResolveFunction: func(*ssa.Function) (*ssa.Function, bool, error) {
			builderResolverCalls++
			return nil, false, fmt.Errorf("builder resolver must not override frozen frontend aliases")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if builderResolverCalls != 0 {
		t.Fatalf("builder ResolveFunction calls = %d, want 0", builderResolverCalls)
	}
	if roots[0].Function != original {
		t.Fatal("Analyze mutated the builder-owned root slice")
	}
	if resolved, ok := input.ResolveFunction(original); !ok || resolved != canonical {
		t.Fatalf("ResolveFunction(original) = %v, %v; want exact canonical function", resolved, ok)
	}
	if _, ok := plan.FunctionPlan(original); ok {
		t.Fatal("original patched declaration entered the exact-pointer plan")
	}
	got, ok := plan.FunctionPlan(canonical)
	if !ok || got.Demand != coro.SyncDemand {
		t.Fatalf("canonical plan = %+v, %v; want SyncDemand", got, ok)
	}
}

func TestCoroPlanInputOwnsFrozenDemandReferences(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/demandrefs", `package demandrefs
func owner() {}
func method() {}
func method2() {}
func extra() {}
func alias() {}
`, nil)
	owner := ssaPkg.Func("owner")
	method := ssaPkg.Func("method")
	method2 := ssaPkg.Func("method2")
	extra := ssaPkg.Func("extra")
	alias := ssaPkg.Func("alias")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{owner, method, method2, extra})
	if err != nil {
		t.Fatal(err)
	}
	frozen := []*ssa.Function{method, method2}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		resolveFunction: func(fn *ssa.Function) (*ssa.Function, bool) {
			if fn == alias {
				return method, true
			}
			return fn, universe.Contains(fn)
		},
		demandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return frozen, nil
			}
			return nil, nil
		},
	}
	roots := coro.Roots{{Function: owner, Demand: coro.SyncDemand}}
	plan, err := input.Analyze(roots, coro.SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []*ssa.Function{method, method2} {
		methodPlan, ok := plan.FunctionPlan(target)
		if !ok || methodPlan.Demand != coro.SyncDemand || methodPlan.Emission != coro.EmitPlain {
			t.Fatalf("frozen method %s plan = %+v, present=%v", target.Name(), methodPlan, ok)
		}
	}
	// A completed exact-pointer plan does not retain the frontend callback's
	// backing slice.
	frozen[0] = extra
	methodPlan, ok := plan.FunctionPlan(method)
	if !ok || methodPlan.Demand != coro.SyncDemand {
		t.Fatalf("callback slice mutation changed completed method plan = %+v, present=%v", methodPlan, ok)
	}
	frozen[0] = method

	tests := []struct {
		name      string
		requested []*ssa.Function
	}{
		{name: "missing", requested: []*ssa.Function{method}},
		{name: "extra", requested: []*ssa.Function{method, method2, extra}},
		{name: "alias", requested: []*ssa.Function{method, alias}},
		{name: "duplicate", requested: []*ssa.Function{method, method}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := input.Analyze(roots, coro.SSAConfig{
				ClassifyDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
					if fn == owner {
						return test.requested, nil
					}
					return nil, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "conflict with the frozen frontend method-table references") {
				t.Fatalf("builder %s demand-reference error = %v", test.name, err)
			}
		})
	}

	accepted, err := input.Analyze(roots, coro.SSAConfig{
		ClassifyDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return []*ssa.Function{method2, method}, nil
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("builder exact frozen reference was rejected: %v", err)
	}
	if got, ok := accepted.FunctionPlan(method); !ok || got.Demand != coro.SyncDemand {
		t.Fatalf("accepted exact reference plan = %+v, present=%v", got, ok)
	}
}

func TestCoroPlanInputOwnsFrozenLoweredCalls(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/loweredcalls", `package loweredcalls
var channel chan int
func owner() {}
func helper() { <-channel }
func helper2() {}
func extra() {}
func alias() {}
`, nil)
	owner := ssaPkg.Func("owner")
	helper := ssaPkg.Func("helper")
	helper2 := ssaPkg.Func("helper2")
	extra := ssaPkg.Func("extra")
	alias := ssaPkg.Func("alias")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{owner, helper, helper2, extra})
	if err != nil {
		t.Fatal(err)
	}
	frozen := []coro.SSALoweredCall{
		{LogicalName: "runtime.helper", Target: helper},
		{LogicalName: "runtime.helper2", Target: helper2},
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		resolveFunction: func(fn *ssa.Function) (*ssa.Function, bool) {
			if fn == alias {
				return helper, true
			}
			return fn, universe.Contains(fn)
		},
		loweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
			if fn == owner {
				return frozen, nil
			}
			return nil, nil
		},
	}
	roots := coro.Roots{{Function: owner, Demand: coro.SyncDemand}}
	plan, err := input.Analyze(roots, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	ownerPlan, ok := plan.FunctionPlan(owner)
	if !ok || !ownerPlan.Effect.Contains(coro.MayPark) || ownerPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("owner plan = %+v, present=%v; frozen lowered call did not propagate effect", ownerPlan, ok)
	}
	if got, ok := plan.FunctionPlan(helper); !ok || got.Demand != coro.AsyncDemand || got.Emission != coro.EmitCoroutine {
		t.Fatalf("suspending helper plan = %+v, present=%v", got, ok)
	}
	// The completed plan owns both the record slice and its exact mapping.
	frozen[0].Target = extra
	if target, ok := plan.ResolveLoweredCall(owner, "runtime.helper"); !ok || target != helper {
		t.Fatalf("callback slice mutation changed completed lowered call: %v, %v", target, ok)
	}
	frozen[0].Target = helper

	tests := []struct {
		name      string
		requested []coro.SSALoweredCall
	}{
		{name: "missing", requested: []coro.SSALoweredCall{{LogicalName: "runtime.helper", Target: helper}}},
		{name: "extra", requested: []coro.SSALoweredCall{{LogicalName: "runtime.helper", Target: helper}, {LogicalName: "runtime.helper2", Target: helper2}, {LogicalName: "runtime.extra", Target: extra}}},
		{name: "renamed", requested: []coro.SSALoweredCall{{LogicalName: "runtime.renamed", Target: helper}, {LogicalName: "runtime.helper2", Target: helper2}}},
		{name: "retargeted", requested: []coro.SSALoweredCall{{LogicalName: "runtime.helper", Target: helper2}, {LogicalName: "runtime.helper2", Target: helper}}},
		{name: "unwind class", requested: []coro.SSALoweredCall{{LogicalName: "runtime.helper", Target: helper, UnwindOnly: true}, {LogicalName: "runtime.helper2", Target: helper2}}},
		{name: "alias", requested: []coro.SSALoweredCall{{LogicalName: "runtime.helper", Target: alias}, {LogicalName: "runtime.helper2", Target: helper2}}},
		{name: "duplicate", requested: []coro.SSALoweredCall{{LogicalName: "runtime.helper", Target: helper}, {LogicalName: "runtime.helper", Target: helper}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := input.Analyze(roots, coro.SSAConfig{
				ClassifyLoweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
					if fn == owner {
						return test.requested, nil
					}
					return nil, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "conflict with the frozen frontend helper calls") {
				t.Fatalf("builder %s lowered-call error = %v", test.name, err)
			}
		})
	}

	accepted, err := input.Analyze(roots, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
			if fn == owner {
				return []coro.SSALoweredCall{
					{LogicalName: "runtime.helper2", Target: helper2},
					{LogicalName: "runtime.helper", Target: helper},
				}, nil
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("builder exact frozen lowered calls were rejected: %v", err)
	}
	if got := accepted.LoweredCalls(owner); len(got) != 2 || got[0].LogicalName != "runtime.helper" || got[1].LogicalName != "runtime.helper2" {
		t.Fatalf("accepted lowered calls = %+v", got)
	}
}

func TestValidateCoroUnwindOnlyLoweredCallsRequiresLegacyPlainTarget(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/unwindlowered", `package unwindlowered
var channel chan int
func owner() {}
func plain() {}
func suspending() { <-channel }
func external()
`, nil)
	owner := ssaPkg.Func("owner")
	plain := ssaPkg.Func("plain")
	suspending := ssaPkg.Func("suspending")
	external := ssaPkg.Func("external")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{owner, plain, suspending, external})
	if err != nil {
		t.Fatal(err)
	}
	build := func(target *ssa.Function) *coro.SSAPlan {
		t.Helper()
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
			EmissionUniverse: universe,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == suspending {
					// These flags describe a control-flow role; they are not a
					// certificate that a physically suspending body is plain.
					return coro.SSAFunctionPolicy{Exec: coro.NoReturn | coro.PanicOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyLoweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
				if fn == owner {
					return []coro.SSALoweredCall{{LogicalName: "runtime.Helper", Target: target, UnwindOnly: true}}, nil
				}
				return nil, nil
			},
			MaxPlainInstructions: -1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	plainPlan := build(plain)
	if err := validateCoroUnwindOnlyLoweredCalls(plainPlan, coro.PanicLegacyABIV0); err != nil {
		t.Fatalf("bounded plain unwind helper rejected: %v", err)
	}
	if err := validateCoroUnwindOnlyLoweredCalls(plainPlan, coro.PanicExplicitStatusABIV0); err == nil ||
		!strings.Contains(err.Error(), "has no certified unwind-helper call contract") {
		t.Fatalf("identity-only explicit-status unwind helper error = %v", err)
	}
	if err := validateCoroUnwindOnlyLoweredCalls(plainPlan, coro.PanicLegacyABIV0); err != nil {
		t.Fatalf("explicit-status rejection changed the legacy bounded-plain certificate: %v", err)
	}
	forged := coroLegacyPanicPlainCertificate{owner: owner, logicalName: "runtime.Helper", target: suspending}
	if err := forged.validate(plainPlan); err == nil || !strings.Contains(err.Error(), "not bound to an exact frozen unwind-only target") {
		t.Fatalf("name-only retargeted certificate error = %v", err)
	}
	suspendingPlan := build(suspending)
	if got, ok := suspendingPlan.FunctionPlan(owner); !ok || got.Effect != coro.NoSuspend || got.Emission != coro.EmitPlain {
		t.Fatalf("unwind-only edge polluted owner before preflight: %+v, present=%v", got, ok)
	}
	err = validateCoroUnwindOnlyLoweredCalls(suspendingPlan, coro.PanicLegacyABIV0)
	if err == nil || !strings.Contains(err.Error(), "exact "+coro.PanicLegacyABIV0+" plain certificate") ||
		!strings.Contains(err.Error(), "effect=may-park") || !strings.Contains(err.Error(), "panic-only") {
		t.Fatalf("suspending unwind helper error = %v", err)
	}
	if err := validateCoroUnwindOnlyLoweredCalls(build(external), coro.PanicLegacyABIV0); err == nil ||
		!strings.Contains(err.Error(), "is not a defined Go body") {
		t.Fatalf("external unwind helper error = %v", err)
	}
}

func TestValidateCoroUnwindOnlyLoweredCallsRejectsDynamicErrorMethod(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/unwinderror", `package unwinderror
func owner() {}
func failure(err error) { _ = err.Error() }
`, nil)
	owner := ssaPkg.Func("owner")
	failure := ssaPkg.Func("failure")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{owner, failure})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse: universe,
		ClassifyLoweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
			if fn == owner {
				return []coro.SSALoweredCall{{LogicalName: "runtime.Panic", Target: failure, UnwindOnly: true}}, nil
			}
			return nil, nil
		},
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = validateCoroUnwindOnlyLoweredCalls(plan, coro.PanicLegacyABIV0)
	if err == nil || !strings.Contains(err.Error(), "dynamic invoke Error") ||
		!strings.Contains(err.Error(), "not a bounded DirectPlain edge") {
		t.Fatalf("dynamic error method unwind helper error = %v", err)
	}
	if got, ok := plan.FunctionPlan(failure); !ok || got.FuncRep != coro.DirectCoro || !got.Exec.Contains(coro.OpaqueExec) {
		t.Fatalf("dynamic Error target was unexpectedly forced plain: %+v, present=%v", got, ok)
	}
}

func TestActiveCoroABIVersions(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		coroABI   string
		scheduler string
		panicABI  string
		funcRep   string
	}{
		{"nil defaults", nil, coro.EntryResolutionABIV0, coro.SchedulerNoneABIV0, coro.PanicLegacyABIV0, coro.FuncRepABIV0},
		{"entry resolution", &Config{}, coro.EntryResolutionABIV0, coro.SchedulerNoneABIV0, coro.PanicLegacyABIV0, coro.FuncRepABIV0},
		{"physical leaf", &Config{EnableCoroPhysicalABI: true}, coro.PhysicalABIV0, coro.SchedulerNoneABIV0, coro.PanicLegacyABIV0, coro.FuncRepABIV0},
		{"explicit status panic", &Config{EnableCoroExplicitStatusPanicABI: true}, coro.EntryResolutionABIV0, coro.SchedulerNoneABIV0, coro.PanicExplicitStatusABIV0, coro.FuncRepABIV0},
		{"plain dispatch", &Config{EnableCoroPlainDispatch: true}, coro.EntryResolutionABIV0, coro.SchedulerNoneABIV0, coro.PanicLegacyABIV0, coro.FuncRepABIV1},
		{"child await", &Config{EnableCoroPhysicalABI: true, EnableCoroChildAwait: true}, coro.PhysicalABIV1, coro.SchedulerChildAwaitABIV0, coro.PanicLegacyABIV0, coro.FuncRepABIV0},
		{"closed static spawn", &Config{EnableCoroPhysicalABI: true, EnableCoroChildAwait: true, EnableCoroClosedStaticSpawn: true, EnableCoroProgramBootstrapRun: true}, coro.PhysicalABIV1, coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0, coro.PanicLegacyABIV0, coro.FuncRepABIV0},
		{"program bootstrap runtime with plain dispatch", &Config{EnableCoroPhysicalABI: true, EnableCoroChildAwait: true, EnableCoroPlainDispatch: true, EnableCoroProgramBootstrapRun: true}, coro.PhysicalABIV1, coro.SchedulerProgramBootstrapABIV2, coro.PanicLegacyABIV0, coro.FuncRepABIV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := activeCoroABIVersion(test.config); got != test.coroABI {
				t.Fatalf("coroutine ABI = %q, want %q", got, test.coroABI)
			}
			if got := activeCoroSchedulerABIVersion(test.config); got != test.scheduler {
				t.Fatalf("scheduler ABI = %q, want %q", got, test.scheduler)
			}
			if got := activeCoroPanicABIVersion(test.config); got != test.panicABI {
				t.Fatalf("panic ABI = %q, want %q", got, test.panicABI)
			}
			if got := activeCoroFuncRepABIVersion(test.config); got != test.funcRep {
				t.Fatalf("function representation ABI = %q, want %q", got, test.funcRep)
			}
		})
	}
}

func TestBuildCoroPlanErrors(t *testing.T) {
	t.Run("builder error", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		ctx := &context{
			buildConf: &Config{CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
				return nil, sentinel
			}},
		}
		err := buildCoroPlan(ctx)
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "build coroutine plan") {
			t.Fatalf("buildCoroPlan error = %v", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("failed builder installed coroutine compilation state")
		}
	})

	t.Run("nil plan", func(t *testing.T) {
		ctx := &context{
			buildConf: &Config{CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
				return nil, nil
			}},
		}
		if err := buildCoroPlan(ctx); err == nil || !strings.Contains(err.Error(), "nil plan") {
			t.Fatalf("buildCoroPlan error = %v, want nil-plan rejection", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("nil-plan builder installed coroutine compilation state")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		ctx := &context{buildConf: &Config{}}
		if err := buildCoroPlan(ctx); err != nil || ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatalf("disabled buildCoroPlan = %v, plan %v, compilation %v", err, ctx.coroPlan, ctx.clCompilation)
		}
	})

	t.Run("nil context or config", func(t *testing.T) {
		if err := buildCoroPlan(nil); err != nil {
			t.Fatalf("nil-context buildCoroPlan = %v", err)
		}
		ctx := &context{}
		if err := buildCoroPlan(ctx); err != nil || ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatalf("nil-config buildCoroPlan = %v, plan %v, compilation %v", err, ctx.coroPlan, ctx.clCompilation)
		}
	})

	t.Run("entry resolution requires builder", func(t *testing.T) {
		ctx := &context{buildConf: &Config{EnableCoroEntryResolution: true}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "CoroPlanBuilder is required") {
			t.Fatalf("buildCoroPlan error = %v, want missing-builder rejection", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("missing builder installed coroutine compilation state")
		}
	})

	t.Run("physical ABI requires entry resolution", func(t *testing.T) {
		ctx := &context{buildConf: &Config{EnableCoroPhysicalABI: true}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "entry resolution is required") {
			t.Fatalf("buildCoroPlan error = %v, want entry-resolution requirement", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("invalid physical ABI configuration installed coroutine compilation state")
		}
	})

	t.Run("explicit-status panic ABI requires entry resolution", func(t *testing.T) {
		ctx := &context{buildConf: &Config{EnableCoroExplicitStatusPanicABI: true}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "entry resolution is required") {
			t.Fatalf("buildCoroPlan error = %v, want explicit-status entry-resolution requirement", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("invalid explicit-status panic ABI configuration installed coroutine compilation state")
		}
	})

	t.Run("child await requires physical ABI", func(t *testing.T) {
		ctx := &context{buildConf: &Config{
			EnableCoroEntryResolution: true,
			EnableCoroChildAwait:      true,
		}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "physical ABI is required") {
			t.Fatalf("buildCoroPlan error = %v, want physical-ABI requirement", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("invalid child-await configuration installed coroutine compilation state")
		}
	})

	t.Run("child await rejects nested c-archive", func(t *testing.T) {
		ctx := &context{buildConf: &Config{
			BuildMode:                 BuildModeCArchive,
			EnableCoroEntryResolution: true,
			EnableCoroPhysicalABI:     true,
			EnableCoroChildAwait:      true,
		}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "c-archive requires flattened package members") {
			t.Fatalf("buildCoroPlan error = %v, want c-archive extraction rejection", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("invalid c-archive configuration installed coroutine compilation state")
		}
	})

	for _, test := range []struct {
		name string
		conf Config
		want string
	}{
		{
			name: "plain dispatch requires entry resolution",
			conf: Config{EnableCoroPlainDispatch: true},
			want: "plain dispatch: coroutine entry resolution is required",
		},
		{
			name: "program bootstrap runtime requires descriptor ABI",
			conf: Config{BuildMode: BuildModeExe, EnableCoroEntryResolution: true, EnableCoroPhysicalABI: true, EnableCoroChildAwait: true, EnableCoroProgramBootstrapRun: true},
			want: "program bootstrap ABI is required",
		},
		{
			name: "program bootstrap requires entry resolution",
			conf: Config{BuildMode: BuildModeExe, EnableCoroProgramBootstrapABI: true},
			want: "entry resolution is required",
		},
		{
			name: "program bootstrap requires physical ABI",
			conf: Config{BuildMode: BuildModeExe, EnableCoroEntryResolution: true, EnableCoroProgramBootstrapABI: true},
			want: "physical ABI is required",
		},
		{
			name: "program bootstrap requires child await",
			conf: Config{BuildMode: BuildModeExe, EnableCoroEntryResolution: true, EnableCoroPhysicalABI: true, EnableCoroProgramBootstrapABI: true},
			want: "child await is required",
		},
		{
			name: "program bootstrap requires executable",
			conf: Config{BuildMode: BuildModeCShared, EnableCoroEntryResolution: true, EnableCoroPhysicalABI: true, EnableCoroChildAwait: true, EnableCoroProgramBootstrapABI: true},
			want: "executable build mode is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			builderCalls := 0
			test.conf.CoroPlanBuilder = func(CoroPlanInput) (*coro.SSAPlan, error) {
				builderCalls++
				return nil, errors.New("builder must not run")
			}
			ctx := &context{buildConf: &test.conf}
			err := buildCoroPlan(ctx)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("buildCoroPlan error = %v, want %q", err, test.want)
			}
			if builderCalls != 0 {
				t.Fatalf("CoroPlanBuilder calls = %d, want 0", builderCalls)
			}
			if ctx.coroPlan != nil || ctx.clCompilation != nil {
				t.Fatal("invalid program-bootstrap configuration installed coroutine compilation state")
			}
		})
	}

	t.Run("entry resolution requires prepared emission universe", func(t *testing.T) {
		builderCalls := 0
		ctx := &context{buildConf: &Config{
			EnableCoroEntryResolution: true,
			CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
				builderCalls++
				return &coro.SSAPlan{}, nil
			},
		}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "prepared emission universe is required") {
			t.Fatalf("buildCoroPlan error = %v, want missing-universe rejection", err)
		}
		if builderCalls != 0 {
			t.Fatalf("CoroPlanBuilder calls = %d, want 0", builderCalls)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("missing universe installed coroutine compilation state")
		}
	})

	for _, tt := range []struct {
		name string
	}{
		{name: "report only"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := &coro.SSAPlan{}
			builderCalls := 0
			observerCalls := 0
			ctx := &context{buildConf: &Config{
				CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
					builderCalls++
					return plan, nil
				},
				CoroPlanObserver: func(_ *ssa.Package, got *coro.SSAPlan) {
					observerCalls++
					if got != plan {
						t.Errorf("observed plan = %p, want %p", got, plan)
					}
				},
			}}

			if err := buildCoroPlan(ctx); err != nil {
				t.Fatalf("buildCoroPlan: %v", err)
			}
			if builderCalls != 1 {
				t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
			}
			if ctx.coroPlan != plan || ctx.clCompilation == nil || ctx.clCompilation.CoroPlan != plan {
				t.Fatalf("installed plan = %p, compilation = %+v, want %p", ctx.coroPlan, ctx.clCompilation, plan)
			}
			if ctx.clCompilation.EnableCoroEntryResolution {
				t.Fatal("report-only compilation unexpectedly enabled entry resolution")
			}
			ctx.clCompilation.CoroPlanObserver(nil, plan)
			if observerCalls != 1 {
				t.Fatalf("CoroPlanObserver calls = %d, want 1", observerCalls)
			}
		})
	}

	t.Run("Do stops before codegen", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		conf := NewDefaultConf(ModeGen)
		conf.CoroPlanBuilder = func(CoroPlanInput) (*coro.SSAPlan, error) {
			return nil, sentinel
		}
		moduleCalls := 0
		conf.ModuleHook = func(Package) {
			moduleCalls++
		}

		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "build coroutine plan") {
			t.Fatalf("Do error = %v", err)
		}
		if len(pkgs) != 0 {
			t.Fatalf("Do packages = %+v, want none", pkgs)
		}
		if moduleCalls != 0 {
			t.Fatalf("ModuleHook calls = %d, want 0", moduleCalls)
		}
	})

	t.Run("Do rejects entry resolution without builder before codegen", func(t *testing.T) {
		conf := NewDefaultConf(ModeGen)
		conf.EnableCoroEntryResolution = true
		moduleCalls := 0
		conf.ModuleHook = func(Package) {
			moduleCalls++
		}

		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if err == nil || !strings.Contains(err.Error(), "CoroPlanBuilder is required") {
			t.Fatalf("Do error = %v, want missing-builder rejection", err)
		}
		if len(pkgs) != 0 {
			t.Fatalf("Do packages = %+v, want none", pkgs)
		}
		if moduleCalls != 0 {
			t.Fatalf("ModuleHook calls = %d, want 0", moduleCalls)
		}
	})

	t.Run("Do rejects active builder that bypasses input Analyze", func(t *testing.T) {
		conf := NewDefaultConf(ModeGen)
		conf.EnableCoroEntryResolution = true
		conf.CoroPlanBuilder = func(CoroPlanInput) (*coro.SSAPlan, error) {
			return &coro.SSAPlan{}, nil
		}
		observerCalls := 0
		moduleCalls := 0
		conf.CoroPlanObserver = func(*ssa.Package, *coro.SSAPlan) {
			observerCalls++
		}
		conf.ModuleHook = func(Package) {
			moduleCalls++
		}

		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if err == nil || !strings.Contains(err.Error(), "plan created by CoroPlanInput.Analyze") {
			t.Fatalf("Do error = %v, want Analyze bypass rejection", err)
		}
		if len(pkgs) != 0 {
			t.Fatalf("Do packages = %+v, want none", pkgs)
		}
		if observerCalls != 0 || moduleCalls != 0 {
			t.Fatalf("observer/module calls = %d/%d, want 0/0", observerCalls, moduleCalls)
		}
	})
}

func TestCoroEntryResolutionUsesPlanMatchedPackageCache(t *testing.T) {
	t.Setenv(llgoBuildCache, "on")
	cacheRoot := t.TempDir()
	oldCacheRootFunc := cacheRootFunc
	cacheRootFunc = func() string { return cacheRoot }
	t.Cleanup(func() { cacheRootFunc = oldCacheRootFunc })

	archive, err := os.CreateTemp(t.TempDir(), "seed-*.a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.WriteString("plain archive"); err != nil {
		archive.Close()
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	const pkgPath = "example.com/coro-cache"
	metadata := coro.PlanDigestMetadata{
		CoroABI:        coro.EntryResolutionABIV0,
		SchedulerABI:   coro.SchedulerNoneABIV0,
		PanicABI:       coro.PanicLegacyABIV0,
		FuncRepABI:     coro.FuncRepABIV0,
		TargetTriple:   "x86_64-unknown-linux-gnu",
		TargetCPU:      "x86-64",
		TargetFeatures: "+sse2",
		TargetABI:      "gnu",
		PointerBits:    64,
		Endianness:     "little",
		DataLayout:     "e-p:64:64",
	}
	newContext := func(digest string) *context {
		plan := &coro.SSAPlan{}
		emission := &cl.EmissionUniverse{}
		compilation := &cl.Compilation{
			CoroPlan:                  plan,
			EnableCoroEntryResolution: true,
			CoroPlanDigest:            digest,
			CoroABI:                   metadata.CoroABI,
			SchedulerABI:              metadata.SchedulerABI,
			PanicABI:                  metadata.PanicABI,
			FuncRepABI:                metadata.FuncRepABI,
			EmissionUniverse:          emission,
		}
		return &context{
			buildConf: &Config{
				Goos:                      "linux",
				Goarch:                    "amd64",
				EnableCoroEntryResolution: true,
			},
			coroPlan:         plan,
			coroEmission:     emission,
			coroPlanDigest:   digest,
			coroPlanMetadata: metadata,
			clCompilation:    compilation,
		}
	}
	manifest := func(ctx *context, path string) (string, string) {
		m := newManifestBuilder()
		ctx.collectCommonInputs(m)
		m.pkg.PkgPath = path
		return m.Build(), m.Fingerprint()
	}
	newPackage := func(ctx *context) *aPackage {
		manifestText, fingerprint := manifest(ctx, pkgPath)
		return &aPackage{
			Package: &packages.Package{
				PkgPath: pkgPath,
				Name:    "corocache",
			},
			Fingerprint: fingerprint,
			Manifest:    manifestText,
		}
	}

	digestA := strings.Repeat("a", 64)
	seedCtx := newContext(digestA)
	seedPkg := newPackage(seedCtx)
	seedPkg.ArchiveFile = archive.Name()
	seedPkg.NeedRt = true
	seedPkg.NeedPyInit = true
	seedPkg.CoroRootAnchorV1 = "__llgo_coro_root_package_v1.0123456789abcdef0123456789abcdef"
	if err := seedCtx.saveToCache(seedPkg); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	seedPaths := seedCtx.ensureCacheManager().PackagePaths(seedCtx.targetTriple(), pkgPath, seedPkg.Fingerprint)
	if _, err := os.Stat(seedPaths.Archive); err != nil {
		t.Fatalf("seed archive: %v", err)
	}

	matchingPkg := newPackage(seedCtx)
	if !seedCtx.tryLoadFromCache(matchingPkg) || !matchingPkg.CacheHit {
		t.Fatal("matching coroutine plan did not reuse the package archive")
	}
	dispatchCtx := newContext(digestA)
	dispatchCtx.buildConf.EnableCoroPlainDispatch = true
	dispatchCtx.clCompilation.EnableCoroPlainDispatch = true
	dispatchCtx.clCompilation.FuncRepABI = coro.FuncRepABIV1
	dispatchCtx.coroPlanMetadata.FuncRepABI = coro.FuncRepABIV1
	if !dispatchCtx.canUsePackageCache() {
		t.Fatal("matching plain-dispatch ABI unexpectedly disabled package cache")
	}
	dispatchCtx.clCompilation.EnableCoroPlainDispatch = false
	if dispatchCtx.canUsePackageCache() {
		t.Fatal("plain-dispatch capability mismatch unexpectedly permits package cache")
	}
	explicitStatusCtx := newContext(digestA)
	explicitStatusCtx.buildConf.EnableCoroExplicitStatusPanicABI = true
	explicitStatusCtx.clCompilation.EnableCoroExplicitStatusPanicABI = true
	explicitStatusCtx.clCompilation.PanicABI = coro.PanicExplicitStatusABIV0
	explicitStatusCtx.coroPlanMetadata.PanicABI = coro.PanicExplicitStatusABIV0
	if !explicitStatusCtx.canUsePackageCache() {
		t.Fatal("matching explicit-status panic ABI identity unexpectedly disabled package cache")
	}
	explicitStatusCtx.clCompilation.EnableCoroExplicitStatusPanicABI = false
	if explicitStatusCtx.canUsePackageCache() {
		t.Fatal("explicit-status panic capability mismatch unexpectedly permits package cache")
	}
	frameRetentionMismatch := newContext(digestA)
	frameRetentionMismatch.buildConf.EnableCoroPhysicalABI = true
	frameRetentionMismatch.buildConf.EnableCoroChildAwait = true
	frameRetentionMismatch.buildConf.EnableCoroProgramBootstrapRun = true
	frameRetentionMismatch.clCompilation.EnableCoroPhysicalABI = true
	frameRetentionMismatch.clCompilation.EnableCoroChildAwait = true
	frameRetentionMismatch.clCompilation.EnableCoroProgramBootstrapRun = true
	frameRetentionMismatch.coroPlanMetadata.CoroABI = coro.PhysicalABIV1
	frameRetentionMismatch.coroPlanMetadata.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	frameRetentionMismatch.coroPlanMetadata.FrameRetentionABI = coro.FrameRetentionTimerABIV1
	frameRetentionMismatch.clCompilation.CoroABI = coro.PhysicalABIV1
	frameRetentionMismatch.clCompilation.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	if frameRetentionMismatch.canUsePackageCache() {
		t.Fatal("frame-retention ABI identity mismatch unexpectedly permits package cache")
	}
	frameRetentionMismatch.clCompilation.CoroFrameRetentionABI = coro.FrameRetentionTimerABIV1
	if !frameRetentionMismatch.canUsePackageCache() {
		t.Fatal("matching frame-retention ABI identity unexpectedly disables package cache")
	}
	bootstrapMismatch := newContext(digestA)
	bootstrapMismatch.clCompilation.EnableCoroProgramBootstrapRun = true
	if bootstrapMismatch.canUsePackageCache() {
		t.Fatal("program-bootstrap-run capability mismatch unexpectedly permits package cache")
	}
	if !matchingPkg.NeedRt || !matchingPkg.NeedPyInit {
		t.Fatalf("cache metadata runtime flags = %v/%v, want true/true", matchingPkg.NeedRt, matchingPkg.NeedPyInit)
	}
	if matchingPkg.CoroRootAnchorV1 != seedPkg.CoroRootAnchorV1 {
		t.Fatalf("cache metadata coroutine root anchor = %q, want %q", matchingPkg.CoroRootAnchorV1, seedPkg.CoroRootAnchorV1)
	}

	digestB := strings.Repeat("b", 64)
	mismatchCtx := newContext(digestB)
	mismatchPkg := newPackage(mismatchCtx)
	mismatchPaths := mismatchCtx.ensureCacheManager().PackagePaths(mismatchCtx.targetTriple(), pkgPath, mismatchPkg.Fingerprint)
	if err := mismatchCtx.cacheManager.EnsureDir(mismatchPaths); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Archive, mismatchPaths.Archive); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Manifest, mismatchPaths.Manifest); err != nil {
		t.Fatal(err)
	}
	if mismatchCtx.tryLoadFromCache(mismatchPkg) {
		t.Fatal("mismatched coroutine manifest was accepted from a forced cache path")
	}
	if mismatchPkg.CacheHit || mismatchPkg.ArchiveFile != "" {
		t.Fatalf("mismatched cache read mutated package: hit=%v archive=%q", mismatchPkg.CacheHit, mismatchPkg.ArchiveFile)
	}
	forgedPkg := newPackage(seedCtx)
	forgedPkg.Fingerprint = strings.Repeat("c", 64)
	forgedPaths := seedCtx.ensureCacheManager().PackagePaths(seedCtx.targetTriple(), pkgPath, forgedPkg.Fingerprint)
	if err := seedCtx.cacheManager.EnsureDir(forgedPaths); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Archive, forgedPaths.Archive); err != nil {
		t.Fatal(err)
	}
	if err := copyFileAtomic(seedPaths.Manifest, forgedPaths.Manifest); err != nil {
		t.Fatal(err)
	}
	if seedCtx.tryLoadFromCache(forgedPkg) {
		t.Fatal("manifest stored under a forged fingerprint path was accepted")
	}

	incomplete := newContext("")
	if incomplete.canUsePackageCache() {
		t.Fatal("active context without CoroPlanDigest unexpectedly permits package cache")
	}
	incompletePkg := newPackage(incomplete)
	if incomplete.tryLoadFromCache(incompletePkg) {
		t.Fatal("active context without CoroPlanDigest read a cache archive")
	}
	if incomplete.cacheManager != nil {
		t.Fatal("incomplete coroutine context initialized a cache manager")
	}

	mismatchedUniverse := newContext(digestA)
	mismatchedUniverse.clCompilation.EmissionUniverse = &cl.EmissionUniverse{}
	if mismatchedUniverse.canUsePackageCache() {
		t.Fatal("active context with mismatched emission universe unexpectedly permits package cache")
	}
}

func TestCoroEntryResolutionBuildsPreparedRuntimePackages(t *testing.T) {
	for _, test := range []struct {
		name        string
		conf        Config
		needRuntime bool
		needPyInit  bool
		want        bool
	}{
		{name: "host report only", conf: Config{}, want: true},
		{name: "target report only stays lazy", conf: Config{Target: "embedded"}},
		{name: "target active emits frozen universe", conf: Config{Target: "embedded", EnableCoroEntryResolution: true}, want: true},
		{name: "target runtime lowering", conf: Config{Target: "embedded"}, needRuntime: true, want: true},
		{name: "target python lowering", conf: Config{Target: "embedded"}, needPyInit: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldBuildRuntimePackages(&test.conf, test.needRuntime, test.needPyInit); got != test.want {
				t.Fatalf("shouldBuildRuntimePackages = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCoroRuntimeLinkRequirements(t *testing.T) {
	for _, test := range []struct {
		name        string
		conf        Config
		needRuntime bool
		needPyInit  bool
		wantInit    bool
		wantLink    bool
	}{
		{name: "host keeps runtime link", conf: Config{}, wantLink: true},
		{name: "named target stays lazy", conf: Config{Target: "embedded"}},
		{name: "entry resolution alone stays lazy", conf: Config{Target: "embedded", EnableCoroEntryResolution: true}},
		{name: "child await initializes and links runtime", conf: Config{Target: "embedded", EnableCoroChildAwait: true}, wantInit: true, wantLink: true},
		{name: "legacy runtime reference", conf: Config{Target: "embedded"}, needRuntime: true, wantInit: true, wantLink: true},
		{name: "python links without runtime init", conf: Config{Target: "embedded"}, needPyInit: true, wantLink: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotInit, gotLink := runtimeLinkRequirements(&test.conf, test.needRuntime, test.needPyInit)
			if gotInit != test.wantInit || gotLink != test.wantLink {
				t.Fatalf("runtime link requirements = init:%v link:%v, want init:%v link:%v", gotInit, gotLink, test.wantInit, test.wantLink)
			}
		})
	}
}

func TestCoroEmissionCoverageStopsBeforeAnyPackageCodegen(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.EnableCoroEntryResolution = true

	var (
		builderCalls  int
		observerCalls int
		moduleCalls   int
	)
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		builderCalls++
		if input.EmissionUniverse == nil {
			return nil, fmt.Errorf("missing prepared emission universe")
		}
		mainFn, err := findSingleSSAMain(input.Program)
		if err != nil {
			return nil, err
		}
		return input.Analyze(coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			Include: func(fn *ssa.Function) (bool, error) {
				return fn.Pkg == nil || fn.Pkg.Pkg == nil ||
					fn.Pkg.Pkg.Path() != "github.com/goplus/llgo/internal/build/_testgo/coro_emission/zmiss" ||
					fn.Name() != "Missing", nil
			},
		})
	}
	conf.CoroPlanObserver = func(*ssa.Package, *coro.SSAPlan) {
		observerCalls++
	}
	conf.ModuleHook = func(Package) {
		moduleCalls++
	}

	pkgs, err := Do([]string{"./_testgo/coro_emission"}, conf)
	if err == nil || !strings.Contains(err.Error(), "zmiss") || !strings.Contains(err.Error(), "Missing") {
		t.Fatalf("Do error = %v, want missing zmiss.Missing coverage", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Do packages = %+v, want none", pkgs)
	}
	if builderCalls != 1 {
		t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
	}
	if observerCalls != 0 {
		t.Fatalf("CoroPlanObserver calls = %d, want 0", observerCalls)
	}
	if moduleCalls != 0 {
		t.Fatalf("ModuleHook calls = %d, want 0", moduleCalls)
	}
}

func TestCoroUnsupportedEntryResolutionReturnsErrorBeforeCodegen(t *testing.T) {
	conf := NewDefaultConf(ModeGen)
	conf.EnableCoroEntryResolution = true
	var (
		observerCalls int
		moduleCalls   int
		builderBuilt  bool
	)
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		mainFn, err := findSingleSSAMain(input.Program)
		if err != nil {
			return nil, err
		}
		plan, err := input.Analyze(coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == mainFn {
					return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
		if err == nil {
			builderBuilt = true
		}
		return plan, err
	}
	conf.CoroPlanObserver = func(*ssa.Package, *coro.SSAPlan) {
		observerCalls++
	}
	conf.ModuleHook = func(Package) {
		moduleCalls++
	}

	pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if !builderBuilt {
		t.Fatalf("CoroPlanBuilder did not successfully return a plan: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "compile package") ||
		(!strings.Contains(err.Error(), "requires coroutine physical ABI lowering") &&
			!strings.Contains(err.Error(), "requires an unimplemented dispatch descriptor")) {
		t.Fatalf("Do error = %v, want cl coroutine preflight error returned from buildPkg", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Do packages = %+v, want none", pkgs)
	}
	if observerCalls != 0 || moduleCalls != 0 {
		t.Fatalf("observer/module calls = %d/%d, want 0/0", observerCalls, moduleCalls)
	}
}

func TestCoroEmissionUniverseAcceptsModeTestVariants(t *testing.T) {
	conf := NewDefaultConf(ModeTest)
	sentinel := errors.New("mode-test emission universe prepared")
	var (
		builderCalls int
		moduleCalls  int
	)
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		builderCalls++
		if input.EmissionUniverse == nil {
			return nil, fmt.Errorf("missing prepared emission universe")
		}
		if _, err := input.Analyze(nil, coro.SSAConfig{}); err != nil {
			return nil, fmt.Errorf("analyze ModeTest emission universe: %w", err)
		}
		return nil, sentinel
	}
	conf.ModuleHook = func(Package) { moduleCalls++ }

	pkgs, err := Do([]string{"../../cl/_testgo/runtest"}, conf)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Do error = %v, want builder sentinel after ModeTest universe preparation", err)
	}
	if len(pkgs) != 0 {
		t.Fatalf("Do packages = %+v, want none", pkgs)
	}
	if builderCalls != 1 || moduleCalls != 0 {
		t.Fatalf("builder/module calls = %d/%d, want 1/0", builderCalls, moduleCalls)
	}
	// ABI-identical functions copied into a test variant intentionally resolve
	// to one physical symbol. Distinct same-path bodies remain exact and are
	// covered by cl.TestEmissionUniverseKeepsSamePathTestVariantsExact.
}

func buildModeGenIR(t *testing.T, pattern string, builder CoroPlanBuilder, observer CoroPlanObserver, moduleHooks ...ModuleHook) (string, map[string][sha256.Size]byte) {
	t.Helper()
	conf := NewDefaultConf(ModeGen)
	conf.CoroPlanBuilder = builder
	conf.CoroPlanObserver = observer
	modules := make(map[string][sha256.Size]byte)
	conf.ModuleHook = func(pkg Package) {
		key := pkg.ID
		if _, exists := modules[key]; exists {
			t.Errorf("ModuleHook ran more than once for %s", key)
		}
		modules[key] = sha256.Sum256([]byte(pkg.LPkg.String()))
		for _, hook := range moduleHooks {
			if hook != nil {
				hook(pkg)
			}
		}
	}
	pkgs, err := Do([]string{pattern}, conf)
	if err != nil {
		t.Fatalf("Do(%q): %v", pattern, err)
	}
	if len(pkgs) != 1 || pkgs[0].LPkg == nil {
		t.Fatalf("Do(%q) packages = %+v, want one generated package", pattern, pkgs)
	}
	ir := pkgs[0].LPkg.String()
	pkgs[0].LPkg.Prog.Dispose()
	return ir, modules
}

func findSingleSSAMain(prog *ssa.Program) (*ssa.Function, error) {
	if prog == nil {
		return nil, fmt.Errorf("nil SSA program")
	}
	var found *ssa.Function
	for _, pkg := range prog.AllPackages() {
		if pkg == nil || pkg.Pkg == nil || pkg.Pkg.Name() != "main" {
			continue
		}
		fn := pkg.Func("main")
		if fn == nil {
			continue
		}
		if found != nil && found != fn {
			return nil, fmt.Errorf("multiple SSA main functions: %s and %s", found, fn)
		}
		found = fn
	}
	if found == nil {
		return nil, fmt.Errorf("SSA main function not found")
	}
	return found, nil
}

type coroPlanTestImporter map[string]*types.Package

func (p coroPlanTestImporter) Import(path string) (*types.Package, error) {
	if pkg := p[path]; pkg != nil {
		return pkg, nil
	}
	return nil, fmt.Errorf("test import %q is unavailable", path)
}

func buildCoroPlanTestPackage(
	t *testing.T, pkgPath, source string, sourceImporter types.Importer,
) (*ssa.Package, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "coro_plan_test.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if sourceImporter == nil {
		sourceImporter = importer.Default()
	}
	files := []*ast.File{file}
	ssaPkg, _, err := ssautil.BuildPackage(
		&types.Config{Importer: sourceImporter},
		fset,
		types.NewPackage(pkgPath, file.Name.Name),
		files,
		ssa.SanityCheckFunctions|ssa.InstantiateGenerics,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ssaPkg, files
}

func coroPlanTestCalls(fn *ssa.Function) []ssa.CallInstruction {
	if fn == nil {
		return nil
	}
	var calls []ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok {
				calls = append(calls, call)
			}
		}
	}
	return calls
}
