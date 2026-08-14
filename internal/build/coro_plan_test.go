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
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/crosscompile"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func frontendElidesNoInitCall(call ssa.CallInstruction) bool {
	return cl.FrontendElidesNoInitCall(call)
}

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
func patchPublicInit() {}
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

	baseCallSitePlan := func(call ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error) {
		plan := cl.CoroCallSitePlan{}
		if frontendElidesNoInitCall(call) {
			plan.Elision = cl.CoroCallElidedNoInit
		}
		return plan, true, nil
	}
	input := CoroPlanInput{Program: ssaPkg.Prog, callSitePlan: baseCallSitePlan}
	plan, err := input.Analyze(coro.Roots{
		{Function: ssaPkg.Func("init"), Demand: coro.SyncDemand},
		{Function: ssaPkg.Func("calls"), Demand: coro.AsyncDemand},
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
	_, err = input.Analyze(coro.Roots{{Function: ssaPkg.Func("calls"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			return call == directOrdinary, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "builder cannot elide ordinary call") {
		t.Fatalf("ordinary builder elision error = %v, want fail-closed rejection", err)
	}

	unevaluatedInput := input
	unevaluatedInput.callSitePlan = func(call ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error) {
		return cl.CoroCallSitePlan{Elision: cl.CoroCallElidedFrontendUnevaluated}, true, nil
	}
	unevaluatedPlan, err := unevaluatedInput.Analyze(
		coro.Roots{{Function: ssaPkg.Func("calls"), Demand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range ordinaryCalls {
		wantElided := false
		switch exact := call.(type) {
		case *ssa.Call:
			wantElided = exact != nil && exact.Common().StaticCallee() != nil && !exact.Common().IsInvoke()
		case *ssa.Defer:
			wantElided = exact != nil && exact.DeferStack == nil && exact.Common().StaticCallee() != nil && !exact.Common().IsInvoke()
		}
		if got := unevaluatedPlan.ElidesCall(call); got != wantElided {
			t.Fatalf("frontend-unevaluated %T call elided=%t, want %t: %s", call, got, wantElided, call)
		}
		_, planned := unevaluatedPlan.CallPlan(call)
		if planned == wantElided {
			t.Fatalf("frontend-unevaluated %T CallPlan present=%t, want %t: %s", call, planned, !wantElided, call)
		}
	}

	var ordinaryInitCall ssa.CallInstruction
	for _, call := range initCalls {
		callee := call.Common().StaticCallee()
		if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg != nil && callee.Pkg.Pkg.Path() == "example.com/ordinary" {
			ordinaryInitCall = call
			break
		}
	}
	if ordinaryInitCall == nil {
		t.Fatal("synthetic init has no exact ordinary-package initializer call")
	}
	const patchLogicalName = "$llgo.patch.public-init-v1:test"
	patchPublicInit := ssaPkg.Func("patchPublicInit")
	patchInput := input
	patchInput.callSitePlan = func(call ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error) {
		plan, frozen, err := baseCallSitePlan(call)
		if call == ordinaryInitCall {
			plan.Elision = cl.CoroCallElidedPatchRedirect
		}
		return plan, frozen, err
	}
	patchInput.loweredCalls = func(owner *ssa.Function) ([]coro.SSALoweredCall, error) {
		if owner != ssaPkg.Func("init") {
			return nil, nil
		}
		return []coro.SSALoweredCall{{LogicalName: patchLogicalName, Target: patchPublicInit}}, nil
	}
	patchPlan, err := patchInput.Analyze(
		coro.Roots{{Function: ssaPkg.Func("init"), Demand: coro.SyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !patchPlan.ElidesCall(ordinaryInitCall) {
		t.Fatal("exact patched original-init occurrence was not frontend-elided")
	}
	if _, exists := patchPlan.CallPlan(ordinaryInitCall); exists {
		t.Fatal("frontend-elided patched original-init occurrence retained a source CallPlan")
	}
	record, exists := patchPlan.ResolveLoweredCallRecord(ssaPkg.Func("init"), patchLogicalName)
	if !exists || record.Target != patchPublicInit || record.RawPlain || record.UnwindOnly || record.ExplicitStatusElided {
		t.Fatalf("planned patch init lowered occurrence = %+v, %v; want ordinary public-init target", record, exists)
	}
}

func TestCoroPlanInputFreezesElidedCallCertificateAndRejectsForgery(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/elidedcertificate", `package elidedcertificate
func intrinsic()
func root() { intrinsic() }
`, nil)
	root := ssaPkg.Func("root")
	calls := coroPlanTestCalls(root)
	if len(calls) != 1 {
		t.Fatalf("root calls = %d, want one", len(calls))
	}
	exactCall := calls[0]
	const frontendCertificate = "frontend-exact-worker-call-certificate"
	input := CoroPlanInput{
		Program: ssaPkg.Prog,
		callSitePlan: func(call ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error) {
			if call == exactCall {
				return cl.CoroCallSitePlan{
					IntrinsicSemantics: cl.CoroIntrinsicCallInlineSuspend,
					Intrinsic:          true,
					Elision:            cl.CoroCallElidedIntrinsic,
					ElisionCertificate: frontendCertificate,
				}, true, nil
			}
			return cl.CoroCallSitePlan{}, false, nil
		},
	}
	plan, err := input.Analyze(coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := plan.ElidedCallCertificate(exactCall); !ok || got != frontendCertificate {
		t.Fatalf("planned elided-call certificate = %q, %t", got, ok)
	}
	_, err = input.Analyze(coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyElidedCallCertificate: func(_ *ssa.Function, call ssa.CallInstruction) (string, error) {
			if call == exactCall {
				return "forged-worker-call-certificate", nil
			}
			return "", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot forge an elided-call capability") {
		t.Fatalf("forged elided-call certificate error = %v", err)
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
				Program:            ssaPkg.Prog,
				EmissionUniverse:   ssaEmission,
				resolveFunction:    emission.Resolve,
				functionBackground: emission.FunctionBackground,
				callSitePlan:       emission.CoroCallSitePlan,
			}
			functionIDs := emission.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
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
			if semantics, intrinsic, err := coroIntrinsicCallSiteSemanticsForTest(emission, call); err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineNoSuspend {
				t.Fatalf("alias intrinsic site semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
			}
			if !plan.ElidesCall(call) {
				t.Fatal("valid intrinsic site was not retained as exact elided call")
			}
			if _, ok := plan.CallPlan(call); ok {
				t.Fatal("valid intrinsic site unexpectedly has a managed CallPlan")
			}
			metadata := coro.PlanDigestMetadata{
				CoroABI: coro.PhysicalABIV1, SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
				PanicABI: coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV1,
				LoweringFactsSchema: coro.LoweringFactsSchema, LoweringFactsDigest: strings.Repeat("0", sha256.Size*2),
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
	semantics, intrinsic, err := coroIntrinsicCallSiteSemanticsForTest(emission, parkCall)
	if err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineSuspend || !semantics.SuspendsCurrentFrame() {
		t.Fatalf("park semantics = %v, %v, %v; want inline-suspend, true, nil", semantics, intrinsic, err)
	}
	input := CoroPlanInput{
		Program:            ssaPkg.Prog,
		EmissionUniverse:   ssaEmission,
		resolveFunction:    emission.Resolve,
		functionBackground: emission.FunctionBackground,
		callSitePlan:       emission.CoroCallSitePlan,
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
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
		CoroABI: coro.PhysicalABIV1, SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI: coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV1,
		LoweringFactsSchema: coro.LoweringFactsSchema, LoweringFactsDigest: strings.Repeat("0", sha256.Size*2),
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
type LocalContext struct{}
func EnterLocalContext(*LocalContext) *LocalContext { return nil }
func LeaveLocalContext(*LocalContext, *LocalContext) {}
func __llgo_coro_program_begin_v1() { bootstrapHelper() }
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
type coroProgramRunResultV2 struct { Flags, Used, ExecutorSlot, ExecutorGeneration, Epoch, DeadlineLo, DeadlineHi, Reserved uint32 }
func __llgo_coro_program_run_slice_v2(unsafe.Pointer, unsafe.Pointer, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_program_continue_slice_v2(uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_program_report_panic_v1(unsafe.Pointer) {}
func __llgo_coro_worker_park_v1() {}
func __llgo_coro_worker_resume_v1() {}
func __llgo_coro_os_thread_locked_v1(unsafe.Pointer) bool { return false }
func __llgo_coro_os_thread_foreign_call_v1(unsafe.Pointer, uintptr, uintptr, uint32, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, *uintptr, *uintptr, *uintptr) uint32 { return 0 }
func __llgo_coro_native_worker_complete_v1(uint32, uint32, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) uint32 { return 0 }
func __llgo_coro_native_fleet_owner_v2(uint32) uint32 { return 0 }
func __llgo_coro_foreign_reentry_acquire_v1(*unsafe.Pointer) unsafe.Pointer { return nil }
func __llgo_coro_foreign_reentry_run_v1(unsafe.Pointer, *unsafe.Pointer, *unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_foreign_reentry_failure_v1(uint32, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_same_m_foreign_call_v1(unsafe.Pointer, uintptr, uintptr) {}
func __llgo_coro_timer_park_v2(g, handle, header, storage unsafe.Pointer, delay int64) {}
func __llgo_coro_timer_park_controlled_v2(g, handle, header, storage, controller unsafe.Pointer, control, ownerRoute *uint32, expected uint32, deadline int64) {}
func __llgo_coro_timer_resume_v2(g, storage unsafe.Pointer) uint32 { return 1 }
func __llgo_coro_timer_request_controlled_v2(route uint32) uint32 { return 0 }
func __llgo_coro_poll_park_v2(g, handle, header, storage unsafe.Pointer, context uintptr, fd int32, interest uint32, deadline int64) {}
func __llgo_coro_poll_resume_v2(g, storage unsafe.Pointer) uint32 { return 1 }
func __llgo_coro_poll_update_deadline_or_abort_v1(context uintptr, interest uint32, deadline int64) {}
func __llgo_coro_poll_post_closing_or_abort_v1(context uintptr, interest uint32) {}
func __llgo_coro_keyed_park_v2(g, handle, header, state unsafe.Pointer) {}
func __llgo_coro_keyed_resume_v2(g, state unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_sema_prepare_or_abort_v2(state, addr unsafe.Pointer) {}
func __llgo_coro_sema_release_or_abort_v2(addr unsafe.Pointer) {}
func __llgo_coro_notify_prepare_or_abort_v2(state, notifyAddr unsafe.Pointer, target uint32) {}
func __llgo_coro_notify_one_or_abort_v2(notifyAddr unsafe.Pointer, waitSnapshot uint32) {}
func __llgo_coro_notify_all_or_abort_v2(notifyAddr unsafe.Pointer, waitSnapshot uint32) {}
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_frame_publish_v3() {}
func __llgo_coro_frame_destroy_commit_v2() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_await_prepare_v3(g, parent, child unsafe.Pointer, mode uint32, typeWord, dataWord unsafe.Pointer) {}
func __llgo_coro_await_inline_v1(g, parent, child unsafe.Pointer) bool { return false }
func __llgo_coro_await_consume_v1(g, parent, typeOut, dataOut unsafe.Pointer) uint32 { return 0 }
var preemptRequest uint32
func __llgo_coro_preempt_poll_v1() bool { return atomicExchange(&preemptRequest, 0) == 1 }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_complete_prepare_v2(g, handle, header unsafe.Pointer, status uint32) {}
func __llgo_coro_critical_enter_v1(g unsafe.Pointer) {}
func __llgo_coro_critical_exit_v1(g unsafe.Pointer) bool { return false }
func __llgo_coro_os_thread_lock_v1(g unsafe.Pointer) {}
func __llgo_coro_os_thread_unlock_v1(g unsafe.Pointer) {}
func __llgo_coro_frame_free_v1() {}
func __llgo_coro_chan_send_try_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, uint32, uint32) uint32 { return 0 }
func __llgo_coro_chan_recv_try_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, uint32, uint32) uint32 { return 0 }
func __llgo_coro_chan_resume_v2(unsafe.Pointer, unsafe.Pointer) uint32 { return 0 }
type Chan struct{}
type ChanOp struct{}
func CoroChanTrySend(unsafe.Pointer, *Chan, unsafe.Pointer, int) bool { return false }
func CoroChanTryRecv(unsafe.Pointer, *Chan, unsafe.Pointer, int) (bool, bool) { return false, false }
func CoroChanTryCloseTask(unsafe.Pointer, *Chan) uint32 { return 0 }
func CoroChanSelectTry(...ChanOp) (int, bool, bool, bool) { return 0, false, false, false }
func CoroChanSelectPark(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) {}
func CoroChanSelectResume(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) (int, bool, uint32) { return 0, false, 0 }
func __llgo_coro_panic_prepare_v1() {}
func __llgo_coro_panic_trace_replace_v1(unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_recover_take_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_payload_v1(uint32, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_prepare_v1() {}
func __llgo_coro_fault_payload_v2(uint32, uint64, uintptr, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_prepare_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32, uint64, uintptr) {}
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
		prog:            prog,
		buildConf:       &Config{},
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
	wantRoots := []string{
		"init",
		coroFrameAllocatorBootstrapSymbolV1,
		coroProgramBeginSymbolV1,
		coroProgramRunSymbolV1,
		coroProgramContinueSymbolV1,
		"__llgo_coro_frame_alloc_v1",
		"__llgo_coro_frame_publish_v1",
		"__llgo_coro_frame_publish_v3",
		"__llgo_coro_frame_destroy_commit_v2",
		"__llgo_coro_await_prepare_v1",
		"__llgo_coro_preempt_poll_v1",
		"__llgo_coro_yield_prepare_v1",
		coroRunDecisionTakeSymbolV1,
		coroRunDecisionTakeZeroSymbolV1,
		"__llgo_coro_complete_prepare_v1",
		"__llgo_coro_frame_free_v1",
		"__llgo_coro_await_prepare_v3",
		"__llgo_coro_await_inline_v1",
		"__llgo_coro_await_consume_v1",
		"__llgo_coro_complete_prepare_v2",
		"__llgo_coro_critical_enter_v1",
		"__llgo_coro_critical_exit_v1",
		coroOSThreadLockSymbolV1,
		coroOSThreadUnlockSymbolV1,
		"CoroChanTrySend",
		"CoroChanTryRecv",
		"CoroChanTryCloseTask",
		"CoroChanSelectTry",
		"CoroChanSelectPark",
		"CoroChanSelectResume",
		coroChanSendTryParkSymbolV2,
		coroChanRecvTryParkSymbolV2,
		coroChanResumeSymbolV2,
		"__llgo_coro_fault_prepare_v1",
		"__llgo_coro_fault_prepare_v2",
		"__llgo_coro_panic_prepare_v1",
		coroPanicTraceReplaceSymbolV1,
		"__llgo_coro_recover_take_v1",
		"__llgo_coro_fault_payload_v1",
		"__llgo_coro_fault_payload_v2",
		"__llgo_coro_spawn_begin_v1",
		"__llgo_coro_spawn_commit_v1",
		coroProgramMainReturnSymbolV1,
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
	logicalProg := llssa.NewProgram(nil)
	defer logicalProg.Dispose()
	logicalProg.SetLocalityInfo("example.com/state.value", llssa.LocalityInfo{Locality: llssa.GoroutineLocal})
	logicalProg.SetLocalStorage("example.com/state.value", llssa.LocalStorageNativeTLS)
	plainProg := ctx.prog
	ctx.prog = logicalProg
	logicalRoots, logicalPlain, _, _, err := requiredCoroProgramRuntimePlan(ctx)
	ctx.prog = plainProg
	if err != nil {
		t.Fatal(err)
	}
	wantLogicalRoots := append(
		[]string{"init", "EnterLocalContext", "LeaveLocalContext"},
		wantRoots[1:]...,
	)
	if len(logicalRoots) != len(wantLogicalRoots) {
		t.Fatalf("logical-locality runtime roots = %d, want %d", len(logicalRoots), len(wantLogicalRoots))
	}
	for index, root := range logicalRoots {
		wantDemand := coro.SyncDemand
		if index == 0 {
			wantDemand = coro.AsyncDemand
		}
		if root.Function == nil || root.Function.Name() != wantLogicalRoots[index] ||
			root.Demand != wantDemand {
			t.Fatalf("logical-locality root %d = %+v, want %s/%s",
				index, root, wantLogicalRoots[index], wantDemand)
		}
		if index != 0 {
			if _, plain := logicalPlain[root.Function]; !plain {
				t.Fatalf("logical-locality root %q is absent from the direct-plain runtime island", wantLogicalRoots[index])
			}
		}
	}
	enterLocalContext := ssaPkg.Func("EnterLocalContext")
	originalEnterLocalContextSignature := enterLocalContext.Signature
	enterLocalContext.Signature = types.NewSignatureType(
		nil, nil, nil,
		originalEnterLocalContextSignature.Params(),
		types.NewTuple(),
		false,
	)
	ctx.prog = logicalProg
	_, _, _, _, invalidLocalContextErr := requiredCoroProgramRuntimePlan(ctx)
	ctx.prog = plainProg
	enterLocalContext.Signature = originalEnterLocalContextSignature
	if invalidLocalContextErr == nil ||
		!strings.Contains(invalidLocalContextErr.Error(), "local-context entry ABI") {
		t.Fatalf("invalid local-context entry ABI error = %v", invalidLocalContextErr)
	}
	for _, name := range []string{
		coroTimerParkSymbolV2,
		coroTimerParkControlledSymbolV2,
		coroTimerResumeSymbolV2,
		coroTimerRequestControlledSymbolV2,
		coroPollParkSymbolV2,
		coroPollResumeSymbolV2,
		coroPollUpdateDeadlineOrAbortSymbolV1,
		coroPollPostClosingOrAbortSymbolV1,
		coroKeyedParkSymbolV2,
		coroKeyedResumeSymbolV2,
		coroSemaphorePrepareOrAbortSymbolV2,
		coroSemaphoreReleaseOrAbortSymbolV2,
		coroNotifyPrepareOrAbortSymbolV2,
		coroNotifyOneOrAbortSymbolV2,
		coroNotifyAllOrAbortSymbolV2,
	} {
		if _, ok := requiredPlain[ssaPkg.Func(name)]; ok {
			t.Fatalf("inactive native timer hook %q entered the required plain island", name)
		}
	}
	timerCtx := &context{
		buildConf: &Config{
			Goos:   "linux",
			Goarch: "amd64"},
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
		coroProgramReportPanicSymbolV1,
		coroWorkerParkSymbolV1,
		coroWorkerResumeSymbolV1,
		coroOSThreadLockedSymbolV1,
		coroOSThreadForeignCallSymbolV1,
		coroNativeWorkerCompleteSymbolV1,
		coroNativeFleetOwnerSymbolV2,
		coroForeignReentryAcquireSymbolV1,
		coroForeignReentryRunSymbolV1,
		coroForeignReentryFailureSymbolV1,
		coroSameMForeignCallSymbolV1,
		coroTimerParkSymbolV2,
		coroTimerParkControlledSymbolV2,
		coroTimerResumeSymbolV2,
		coroTimerRequestControlledSymbolV2,
		coroKeyedParkSymbolV2,
		coroKeyedResumeSymbolV2,
		coroSemaphorePrepareOrAbortSymbolV2,
		coroSemaphoreReleaseOrAbortSymbolV2,
		coroNotifyPrepareOrAbortSymbolV2,
		coroNotifyOneOrAbortSymbolV2,
		coroNotifyAllOrAbortSymbolV2,
		coroPollParkSymbolV2,
		coroPollResumeSymbolV2,
		coroPollUpdateDeadlineOrAbortSymbolV1,
		coroPollPostClosingOrAbortSymbolV1,
	}
	wantTimerRoots = append(wantTimerRoots, wantRoots[5:]...)
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
		coroTimerParkSymbolV2,
		coroTimerParkControlledSymbolV2,
		coroTimerResumeSymbolV2,
		coroTimerRequestControlledSymbolV2,
		coroPollParkSymbolV2,
		coroPollResumeSymbolV2,
		coroPollUpdateDeadlineOrAbortSymbolV1,
		coroPollPostClosingOrAbortSymbolV1,
		coroKeyedParkSymbolV2,
		coroKeyedResumeSymbolV2,
		coroSemaphorePrepareOrAbortSymbolV2,
		coroSemaphoreReleaseOrAbortSymbolV2,
		coroNotifyPrepareOrAbortSymbolV2,
		coroNotifyOneOrAbortSymbolV2,
		coroNotifyAllOrAbortSymbolV2,
	} {
		if _, ok := timerPlain[ssaPkg.Func(name)]; !ok {
			t.Fatalf("native timer hook %q is absent from the required plain island", name)
		}
	}
	if len(timerDirect) != 0 || len(timerClosed) != 0 {
		t.Fatalf("native timer roots produced callback proofs: direct=%d dynamic=%d", len(timerDirect), len(timerClosed))
	}
	timerParkFn := ssaPkg.Func(coroTimerParkSymbolV2)
	originalTimerParkSignature := timerParkFn.Signature
	timerParkFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "storage", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "delay", types.Typ[types.Uint64]),
		),
		types.NewTuple(), false)
	_, _, _, _, invalidTimerParkErr := requiredCoroProgramRuntimePlan(timerCtx)
	timerParkFn.Signature = originalTimerParkSignature
	if invalidTimerParkErr == nil || !strings.Contains(invalidTimerParkErr.Error(), "timer park V2 ABI") {
		t.Fatalf("invalid Timer V2 park ABI error = %v", invalidTimerParkErr)
	}
	timerResumeFn := ssaPkg.Func(coroTimerResumeSymbolV2)
	originalTimerResumeSignature := timerResumeFn.Signature
	timerResumeFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "storage", types.Typ[types.UnsafePointer]),
		),
		types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint64])), false)
	_, _, _, _, invalidTimerResumeErr := requiredCoroProgramRuntimePlan(timerCtx)
	timerResumeFn.Signature = originalTimerResumeSignature
	if invalidTimerResumeErr == nil || !strings.Contains(invalidTimerResumeErr.Error(), "timer resume V2 ABI") {
		t.Fatalf("invalid Timer V2 resume ABI error = %v", invalidTimerResumeErr)
	}
	controlledParkFn := ssaPkg.Func(coroTimerParkControlledSymbolV2)
	originalControlledParkSignature := controlledParkFn.Signature
	controlledParkFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "storage", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "controller", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "control", types.NewPointer(types.Typ[types.Uint64])),
			types.NewParam(token.NoPos, nil, "expected", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "deadline", types.Typ[types.Int64]),
		), types.NewTuple(), false)
	_, _, _, _, invalidControlledParkErr := requiredCoroProgramRuntimePlan(timerCtx)
	controlledParkFn.Signature = originalControlledParkSignature
	if invalidControlledParkErr == nil || !strings.Contains(invalidControlledParkErr.Error(), "controlled coroutine timer park V2 ABI") {
		t.Fatalf("invalid controlled timer V2 park ABI error = %v", invalidControlledParkErr)
	}
	controlledRequestFn := ssaPkg.Func(coroTimerRequestControlledSymbolV2)
	originalControlledRequestSignature := controlledRequestFn.Signature
	controlledRequestFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "route", types.Typ[types.Uint64]),
		), types.NewTuple(types.NewParam(token.NoPos, nil, "result", types.Typ[types.Bool])), false)
	_, _, _, _, invalidControlledRequestErr := requiredCoroProgramRuntimePlan(timerCtx)
	controlledRequestFn.Signature = originalControlledRequestSignature
	if invalidControlledRequestErr == nil || !strings.Contains(invalidControlledRequestErr.Error(), "controlled coroutine timer request V2 ABI") {
		t.Fatalf("invalid controlled timer V2 request ABI error = %v", invalidControlledRequestErr)
	}
	pollParkFn := ssaPkg.Func(coroPollParkSymbolV2)
	originalPollParkSignature := pollParkFn.Signature
	pollParkFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "storage", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "context", types.Typ[types.Uintptr]),
			types.NewParam(token.NoPos, nil, "fd", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "interest", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "deadline", types.Typ[types.Int64]),
		), types.NewTuple(), false)
	_, _, _, _, invalidPollParkErr := requiredCoroProgramRuntimePlan(timerCtx)
	pollParkFn.Signature = originalPollParkSignature
	if invalidPollParkErr == nil || !strings.Contains(invalidPollParkErr.Error(), "poll park V2 ABI") {
		t.Fatalf("invalid Poll V2 park ABI error = %v", invalidPollParkErr)
	}
	pollResumeFn := ssaPkg.Func(coroPollResumeSymbolV2)
	originalPollResumeSignature := pollResumeFn.Signature
	pollResumeFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "storage", types.Typ[types.UnsafePointer]),
		), types.NewTuple(types.NewParam(token.NoPos, nil, "result", types.Typ[types.Uint64])), false)
	_, _, _, _, invalidPollResumeErr := requiredCoroProgramRuntimePlan(timerCtx)
	pollResumeFn.Signature = originalPollResumeSignature
	if invalidPollResumeErr == nil || !strings.Contains(invalidPollResumeErr.Error(), "poll resume V2 ABI") {
		t.Fatalf("invalid Poll V2 resume ABI error = %v", invalidPollResumeErr)
	}
	pollUpdateFn := ssaPkg.Func(coroPollUpdateDeadlineOrAbortSymbolV1)
	originalPollUpdateSignature := pollUpdateFn.Signature
	pollUpdateFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "context", types.Typ[types.Uintptr]),
			types.NewParam(token.NoPos, nil, "interest", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "deadline", types.Typ[types.Uint64]),
		), types.NewTuple(), false)
	_, _, _, _, invalidPollUpdateErr := requiredCoroProgramRuntimePlan(timerCtx)
	pollUpdateFn.Signature = originalPollUpdateSignature
	if invalidPollUpdateErr == nil || !strings.Contains(invalidPollUpdateErr.Error(), "poll update-deadline-or-abort ABI") {
		t.Fatalf("invalid poll update deadline ABI error = %v", invalidPollUpdateErr)
	}
	pollPostFn := ssaPkg.Func(coroPollPostClosingOrAbortSymbolV1)
	originalPollPostSignature := pollPostFn.Signature
	pollPostFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "fd", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "interest", types.Typ[types.Uint32]),
		), types.NewTuple(), false)
	_, _, _, _, invalidPollPostErr := requiredCoroProgramRuntimePlan(timerCtx)
	pollPostFn.Signature = originalPollPostSignature
	if invalidPollPostErr == nil || !strings.Contains(invalidPollPostErr.Error(), "poll post-closing-or-abort ABI") {
		t.Fatalf("invalid poll post closing ABI error = %v", invalidPollPostErr)
	}
	keyedParkFn := ssaPkg.Func(coroKeyedParkSymbolV2)
	originalKeyedParkSignature := keyedParkFn.Signature
	keyedParkFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "state", types.Typ[types.Uintptr]),
		), types.NewTuple(), false)
	_, _, _, _, invalidKeyedParkErr := requiredCoroProgramRuntimePlan(timerCtx)
	keyedParkFn.Signature = originalKeyedParkSignature
	if invalidKeyedParkErr == nil || !strings.Contains(invalidKeyedParkErr.Error(), "keyed park V2 ABI") {
		t.Fatalf("invalid keyed park V2 ABI error = %v", invalidKeyedParkErr)
	}
	keyedResumeFn := ssaPkg.Func(coroKeyedResumeSymbolV2)
	originalKeyedResumeSignature := keyedResumeFn.Signature
	keyedResumeFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "state", types.Typ[types.UnsafePointer]),
		), types.NewTuple(types.NewParam(token.NoPos, nil, "result", types.Typ[types.Uint64])), false)
	_, _, _, _, invalidKeyedResumeErr := requiredCoroProgramRuntimePlan(timerCtx)
	keyedResumeFn.Signature = originalKeyedResumeSignature
	if invalidKeyedResumeErr == nil || !strings.Contains(invalidKeyedResumeErr.Error(), "keyed resume V2 ABI") {
		t.Fatalf("invalid keyed resume V2 ABI error = %v", invalidKeyedResumeErr)
	}
	semaphorePrepareFn := ssaPkg.Func(coroSemaphorePrepareOrAbortSymbolV2)
	originalSemaphorePrepareSignature := semaphorePrepareFn.Signature
	semaphorePrepareFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "state", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "addr", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "extra", types.Typ[types.Uint32]),
		), types.NewTuple(), false)
	_, _, _, _, invalidSemaphorePrepareErr := requiredCoroProgramRuntimePlan(timerCtx)
	semaphorePrepareFn.Signature = originalSemaphorePrepareSignature
	if invalidSemaphorePrepareErr == nil || !strings.Contains(invalidSemaphorePrepareErr.Error(), "semaphore prepare V2 ABI") {
		t.Fatalf("invalid semaphore prepare ABI error = %v", invalidSemaphorePrepareErr)
	}
	semaphoreReleaseFn := ssaPkg.Func(coroSemaphoreReleaseOrAbortSymbolV2)
	originalSemaphoreReleaseSignature := semaphoreReleaseFn.Signature
	semaphoreReleaseFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "key", types.Typ[types.Uint64])), types.NewTuple(), false)
	_, _, _, _, invalidSemaphoreReleaseErr := requiredCoroProgramRuntimePlan(timerCtx)
	semaphoreReleaseFn.Signature = originalSemaphoreReleaseSignature
	if invalidSemaphoreReleaseErr == nil || !strings.Contains(invalidSemaphoreReleaseErr.Error(), "semaphore release V2 ABI") {
		t.Fatalf("invalid semaphore release ABI error = %v", invalidSemaphoreReleaseErr)
	}
	notifyPrepareFn := ssaPkg.Func(coroNotifyPrepareOrAbortSymbolV2)
	originalNotifyPrepareSignature := notifyPrepareFn.Signature
	notifyPrepareFn.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "state", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "notifyAddr", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "target", types.Typ[types.Uint64]),
		), types.NewTuple(), false)
	_, _, _, _, invalidNotifyPrepareErr := requiredCoroProgramRuntimePlan(timerCtx)
	notifyPrepareFn.Signature = originalNotifyPrepareSignature
	if invalidNotifyPrepareErr == nil || !strings.Contains(invalidNotifyPrepareErr.Error(), "notify prepare V2 ABI") {
		t.Fatalf("invalid notify prepare ABI error = %v", invalidNotifyPrepareErr)
	}
	for _, name := range []string{coroNotifyOneOrAbortSymbolV2, coroNotifyAllOrAbortSymbolV2} {
		notifyFn := ssaPkg.Func(name)
		original := notifyFn.Signature
		notifyFn.Signature = types.NewSignatureType(nil, nil, nil,
			types.NewTuple(
				types.NewParam(token.NoPos, nil, "notifyAddr", types.Typ[types.UnsafePointer]),
				types.NewParam(token.NoPos, nil, "waitSnapshot", types.Typ[types.Uint64]),
			), types.NewTuple(), false)
		_, _, _, _, invalidNotifyErr := requiredCoroProgramRuntimePlan(timerCtx)
		notifyFn.Signature = original
		if invalidNotifyErr == nil || !strings.Contains(invalidNotifyErr.Error(), "notify publication V2 ABI") {
			t.Fatalf("invalid %s ABI error = %v", name, invalidNotifyErr)
		}
	}
	panicHook := ssaPkg.Func("__llgo_coro_panic_prepare_v1")
	panicTraceReplaceHook := ssaPkg.Func(coroPanicTraceReplaceSymbolV1)
	recoverHook := ssaPkg.Func("__llgo_coro_recover_take_v1")
	payloadHook := ssaPkg.Func("__llgo_coro_fault_payload_v1")
	faultHook := ssaPkg.Func("__llgo_coro_fault_prepare_v1")
	payloadArgsHook := ssaPkg.Func("__llgo_coro_fault_payload_v2")
	faultArgsHook := ssaPkg.Func("__llgo_coro_fault_prepare_v2")
	if panicHook == nil || panicTraceReplaceHook == nil || recoverHook == nil ||
		payloadHook == nil || faultHook == nil ||
		payloadArgsHook == nil || faultArgsHook == nil {
		t.Fatal("explicit-status panic hooks are absent from the runtime fixture")
	}
	for _, hook := range []*ssa.Function{
		panicHook, panicTraceReplaceHook, recoverHook,
		payloadHook, faultHook, payloadArgsHook, faultArgsHook,
	} {
		if _, ok := requiredPlain[hook]; !ok {
			t.Fatalf("stackless architecture hook %q is absent from the required plain island", hook.Name())
		}
	}
	originalPanicTraceReplaceSignature := panicTraceReplaceHook.Signature
	panicTraceReplaceHook.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
		), types.NewTuple(), false)
	_, _, _, _, invalidPanicTraceReplaceErr := requiredCoroProgramRuntimePlan(ctx)
	panicTraceReplaceHook.Signature = originalPanicTraceReplaceSignature
	if invalidPanicTraceReplaceErr == nil ||
		!strings.Contains(invalidPanicTraceReplaceErr.Error(), "panic trace replacement ABI") {
		t.Fatalf("invalid panic trace replacement ABI error = %v", invalidPanicTraceReplaceErr)
	}
	originalPayloadSignature := payloadHook.Signature
	payloadHook.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "kind", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "typeOut", types.Typ[types.UnsafePointer]),
		), types.NewTuple(), false)
	_, _, _, _, invalidPayloadErr := requiredCoroProgramRuntimePlan(ctx)
	payloadHook.Signature = originalPayloadSignature
	if invalidPayloadErr == nil || !strings.Contains(invalidPayloadErr.Error(), "fault payload ABI") {
		t.Fatalf("invalid fault payload ABI error = %v", invalidPayloadErr)
	}
	originalPayloadArgsSignature := payloadArgsHook.Signature
	payloadArgsHook.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "kind", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "arg0", types.Typ[types.Uintptr]),
			types.NewParam(token.NoPos, nil, "typeOut", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "dataOut", types.Typ[types.UnsafePointer]),
		), types.NewTuple(), false)
	_, _, _, _, invalidPayloadArgsErr := requiredCoroProgramRuntimePlan(ctx)
	payloadArgsHook.Signature = originalPayloadArgsSignature
	if invalidPayloadArgsErr == nil || !strings.Contains(invalidPayloadArgsErr.Error(), "parameterized coroutine fault payload ABI") {
		t.Fatalf("invalid parameterized fault payload ABI error = %v", invalidPayloadArgsErr)
	}
	originalFaultArgsSignature := faultArgsHook.Signature
	faultArgsHook.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "header", types.Typ[types.UnsafePointer]),
			types.NewParam(token.NoPos, nil, "kind", types.Typ[types.Uint32]),
			types.NewParam(token.NoPos, nil, "arg0", types.Typ[types.Uintptr]),
		), types.NewTuple(), false)
	_, _, _, _, invalidFaultArgsErr := requiredCoroProgramRuntimePlan(ctx)
	faultArgsHook.Signature = originalFaultArgsSignature
	if invalidFaultArgsErr == nil || !strings.Contains(invalidFaultArgsErr.Error(), "parameterized coroutine fault prepare ABI") {
		t.Fatalf("invalid parameterized fault prepare ABI error = %v", invalidFaultArgsErr)
	}
	channelNames := []string{
		"CoroChanTrySend",
		"CoroChanTryRecv",
		"CoroChanTryCloseTask",
		"CoroChanSelectTry",
		"CoroChanSelectPark",
		"CoroChanSelectResume",
		coroChanSendTryParkSymbolV2,
		coroChanRecvTryParkSymbolV2,
		coroChanResumeSymbolV2,
		"__llgo_coro_fault_prepare_v1",
		"__llgo_coro_fault_prepare_v2",
	}
	for _, name := range channelNames {
		fn := ssaPkg.Func(name)
		if fn == nil {
			t.Fatalf("channel runtime hook %q is absent", name)
		}
		if _, ok := requiredPlain[fn]; !ok {
			t.Fatalf("channel runtime hook %q is not a required plain root", name)
		}
	}
	channelResume := ssaPkg.Func(coroChanResumeSymbolV2)
	originalChannelResumeSignature := channelResume.Signature
	channelResume.Signature = types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "g", types.Typ[types.UnsafePointer])),
		types.NewTuple(types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32])), false)
	_, _, _, _, invalidChannelResumeErr := requiredCoroProgramRuntimePlan(ctx)
	channelResume.Signature = originalChannelResumeSignature
	if invalidChannelResumeErr == nil || !strings.Contains(invalidChannelResumeErr.Error(), "channel resume ABI") {
		t.Fatalf("invalid channel resume ABI error = %v", invalidChannelResumeErr)
	}
	for _, name := range []string{"__llgo_coro_spawn_begin_v1", "__llgo_coro_spawn_commit_v1", coroProgramMainReturnSymbolV1} {
		fn := ssaPkg.Func(name)
		if fn == nil {
			t.Fatalf("closed-static-spawn runtime hook %q is absent", name)
		}
		if _, ok := requiredPlain[fn]; !ok {
			t.Fatalf("closed-static-spawn runtime hook %q is not a required plain root", name)
		}
		found := false
		for _, root := range roots {
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
	for _, fn := range []*ssa.Function{ssaPkg.Func("bootstrapHelper"), closureLoop} {
		if _, ok := requiredPlain[fn]; !ok {
			t.Fatalf("required plain closure omitted %s", fn.Name())
		}
	}
	if _, ok := requiredPlain[externalABI]; ok {
		t.Fatal("default may-block external ABI was rewritten as a required no-suspend plain leaf")
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
		Program:               ssaPkg.Prog,
		EmissionUniverse:      ssaEmission,
		resolveFunction:       emission.Resolve,
		functionBackground:    emission.FunctionBackground,
		callableIdentity:      emission.CoroCallableIdentityCertificate,
		callableContract:      emission.CoroCallableContractCertificate,
		callSitePlan:          emission.CoroCallSitePlan,
		requiredRoots:         roots,
		requiredPlain:         requiredPlain,
		requiredHostPlain:     maps.Clone(requiredPlain),
		requiredDirectPlain:   directPlain,
		requiredClosedDynamic: closedDynamic,
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
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
	panicHookPlan, ok := plan.FunctionPlan(panicHook)
	if !ok || panicHookPlan.Emission != coro.EmitRawPlain || panicHookPlan.Demand != coro.SyncDemand ||
		panicHookPlan.ManagedDemand != coro.NoDemand || !panicHookPlan.RawPlainDemand || !panicHookPlan.RawPlainOnly ||
		panicHookPlan.FuncRep != coro.DirectPlain || panicHookPlan.Effect.MaySuspend() ||
		panicHookPlan.Exec.Contains(coro.NeedsPreempt) {
		t.Fatalf("explicit-status panic prepare hook plan = %+v, want required raw-only direct-plain", panicHookPlan)
	}
	closurePlan, ok := plan.FunctionPlan(closureLoop)
	if !ok || closurePlan.Exec.Contains(coro.NeedsPreempt) || closurePlan.Emission != coro.EmitRawPlain || !closurePlan.RawPlainOnly {
		t.Fatalf("required closure loop plan = %+v, want one trusted raw-only body", closurePlan)
	}
	pollPlan, ok := plan.FunctionPlan(ssaPkg.Func("__llgo_coro_preempt_poll_v1"))
	if !ok || pollPlan.Exec.Contains(coro.NeedsPreempt) || pollPlan.Emission != coro.EmitRawPlain || !pollPlan.RawPlainOnly {
		t.Fatalf("preempt poll plan = %+v, want one trusted raw-only atomic poll", pollPlan)
	}
	runDecisionPlan, ok := plan.FunctionPlan(runDecisionFn)
	if !ok || runDecisionPlan.Exec.Contains(coro.NeedsPreempt) ||
		runDecisionPlan.Emission != coro.EmitRawPlain || !runDecisionPlan.RawPlainOnly || runDecisionPlan.Demand != coro.SyncDemand ||
		runDecisionPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("run-decision hook plan = %+v, want one required raw-only direct-plain body", runDecisionPlan)
	}
	runDecisionZeroPlan, ok := plan.FunctionPlan(runDecisionZeroFn)
	if !ok || runDecisionZeroPlan.Exec.Contains(coro.NeedsPreempt) ||
		runDecisionZeroPlan.Emission != coro.EmitRawPlain || !runDecisionZeroPlan.RawPlainOnly || runDecisionZeroPlan.Demand != coro.SyncDemand ||
		runDecisionZeroPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("zero-ticket run-decision hook plan = %+v, want one required raw-only direct-plain body", runDecisionZeroPlan)
	}
	unrelatedPlan, ok := plan.FunctionPlan(unrelatedLoop)
	if !ok || !unrelatedPlan.Exec.Contains(coro.NeedsPreempt) || !unrelatedPlan.Effect.Contains(coro.YieldOnly) || unrelatedPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("unrelated loop plan = %+v, want coroutine preemption", unrelatedPlan)
	}
	externalPlan, ok := plan.FunctionPlan(externalABI)
	if !ok || externalPlan.External != coro.ExternalUnknownForeign ||
		externalPlan.Emission != coro.EmitExternal || externalPlan.Demand != coro.SyncDemand ||
		externalPlan.ManagedDemand != coro.NoDemand || !externalPlan.RawPlainDemand ||
		externalPlan.Exec != coro.BlockForeign|coro.IRQUnsafe ||
		externalPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("raw bodyless ABI plan = %+v, want certified synchronous call on the foreign caller stack", externalPlan)
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
		CoroABI: coro.PhysicalABIV1, SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI: coro.PanicExplicitStatusABIV0, FuncRepABI: coro.FuncRepABIV1,
		LoweringFactsSchema: coro.LoweringFactsSchema, LoweringFactsDigest: strings.Repeat("0", sha256.Size*2),
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
	if !ok || irqClosure.Emission != coro.EmitRawPlain || !irqClosure.RawPlainOnly || !irqClosure.Exec.Contains(coro.IRQUnsafe) ||
		irqClosure.Exec.Contains(coro.ThreadAffine|coro.BlockForeign|coro.OpaqueExec) {
		t.Fatalf("required plain IRQ-unsafe closure plan = %+v, want exact raw-only plain implementation", irqClosure)
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

func TestStacklessArchitectureHasOneABIIdentity(t *testing.T) {
	conf := &Config{}
	if got := activeCoroABIVersion(conf); got != coro.PhysicalABIV1 {
		t.Fatalf("coroutine ABI = %q", got)
	}
	if got := activeCoroPanicABIVersion(conf); got != coro.PanicExplicitStatusABIV0 {
		t.Fatalf("panic ABI = %q", got)
	}
	if got := activeCoroFuncRepABIVersion(conf); got != coro.FuncRepABIV1 {
		t.Fatalf("function representation ABI = %q", got)
	}
}

func TestRequiredCoroProgramRuntimePlanRejectsInvalidIntrinsicSite(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func __llgo_coro_program_begin_v1() { bootstrapHelper() }
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_frame_publish_v3() {}
func __llgo_coro_frame_destroy_commit_v2() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_await_prepare_v3(g, parent, child unsafe.Pointer, mode uint32, typeWord, dataWord unsafe.Pointer) {}
func __llgo_coro_await_inline_v1(g, parent, child unsafe.Pointer) bool { return false }
func __llgo_coro_await_consume_v1(g, parent, typeOut, dataOut unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_complete_prepare_v2(g, handle, header unsafe.Pointer, status uint32) {}
func __llgo_coro_critical_enter_v1(g unsafe.Pointer) {}
func __llgo_coro_critical_exit_v1(g unsafe.Pointer) bool { return false }
func __llgo_coro_os_thread_lock_v1(g unsafe.Pointer) {}
func __llgo_coro_os_thread_unlock_v1(g unsafe.Pointer) {}
func __llgo_coro_frame_free_v1() {}
func __llgo_coro_chan_send_try_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, uint32, uint32) uint32 { return 0 }
func __llgo_coro_chan_recv_try_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, uint32, uint32) uint32 { return 0 }
func __llgo_coro_chan_resume_v2(unsafe.Pointer, unsafe.Pointer) uint32 { return 0 }
type Chan struct{}
type ChanOp struct{}
func CoroChanTrySend(unsafe.Pointer, *Chan, unsafe.Pointer, int) bool { return false }
func CoroChanTryRecv(unsafe.Pointer, *Chan, unsafe.Pointer, int) (bool, bool) { return false, false }
func CoroChanTryCloseTask(unsafe.Pointer, *Chan) uint32 { return 0 }
func CoroChanSelectTry(...ChanOp) (int, bool, bool, bool) { return 0, false, false, false }
func CoroChanSelectPark(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) {}
func CoroChanSelectResume(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) (int, bool, uint32) { return 0, false, 0 }
func __llgo_coro_fault_prepare_v1() {}
func __llgo_coro_fault_prepare_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32, uint64, uintptr) {}
func __llgo_coro_panic_prepare_v1() {}
func __llgo_coro_panic_trace_replace_v1(unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_recover_take_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_payload_v1(uint32, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_payload_v2(uint32, uint64, uintptr, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_spawn_begin_v1() {}
func __llgo_coro_spawn_commit_v1() {}
func __llgo_coro_program_main_return_v1() {}
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
		buildConf:       &Config{},
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
func managedCaller() { syncCallback() }
func dynamicCallback() { dynamic() }
func dynamicTargetA() {}
func dynamicTargetB() {}
func keepDynamicOpen() { dynamic = dynamicTargetA; dynamic = dynamicTargetB }
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
	if !ok || callbackPlan.ManagedDemand != coro.NoDemand || !callbackPlan.RawPlainDemand || !callbackPlan.RawPlainOnly ||
		!callbackPlan.RawPlainEntry || !plan.HasRawPlainVariant(syncCallback) ||
		callbackPlan.FuncRep != coro.DirectPlain || callbackPlan.Primary != coro.PrimaryPlain || callbackPlan.Emission != coro.EmitRawPlain {
		t.Fatalf("sync C callback plan = %+v, want one raw-only plain callback body", callbackPlan)
	}
	valuePlan, ok := plan.ValuePlan(use.call.Common().Args[use.argument])
	if !ok || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != coro.DirectPlain || valuePlan.Funcs[0].MayBeNil || len(valuePlan.Funcs[0].Targets) != 1 {
		t.Fatalf("sync C callback value plan = %+v, present=%t", valuePlan, ok)
	}

	mixedConfig := coro.SSAConfig{MaxPlainInstructions: -1, FunctionIDs: fixture.functionIDs}
	mixedPlan, err := fixture.input.Analyze(coro.Roots{{
		Function: fixture.pkg.Func("managedCaller"), Demand: coro.AsyncDemand,
	}}, mixedConfig)
	if err != nil {
		t.Fatal(err)
	}
	mixedCallback, ok := mixedPlan.FunctionPlan(syncCallback)
	if !ok || mixedCallback.ManagedDemand == coro.NoDemand || !mixedCallback.RawPlainDemand || mixedCallback.RawPlainOnly ||
		!mixedCallback.RawPlainEntry || !mixedPlan.HasRawPlainVariant(syncCallback) ||
		mixedCallback.FuncRep != coro.DirectPlain || mixedCallback.Primary != coro.PrimaryPlain ||
		mixedCallback.Emission != coro.EmitPlain {
		t.Fatalf("managed/raw C callback plan = %+v, want one shared no-suspend plain body", mixedCallback)
	}
	dynamicPlan, ok := plan.FunctionPlan(dynamicCallback)
	if !ok || dynamicPlan.Effect.IsOpaque() ||
		!dynamicPlan.Effect.Contains(coro.AwaitStructured|coro.OutcomeStructured) ||
		dynamicPlan.Exec.IsOpaque() || dynamicPlan.FuncRep != coro.DirectCoro || dynamicPlan.RawPlainDemand {
		t.Fatalf("dynamic C callback plan = %+v, want a closed-dynamic managed blocker without a raw entry", dynamicPlan)
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
	_, err = fixture.analyze(coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyRawDirectPlainCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			return call == dynamicUse && argument == 0, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "builder cannot authorize raw direct-plain ABI") {
		t.Fatalf("unauthorized builder raw direct-plain error = %v", err)
	}
}

func TestRequiredCoroDirectPlainArgumentsLeaveManagedReentryForInference(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixtureSource(t, `
var ready chan struct{}

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=managed-callback memory=borrow-until-return
//go:linkname foreign C.foreign_managed_reentry_build_probe
func foreign(func(uintptr) uintptr, uintptr) uintptr

func callback(value uintptr) uintptr {
	<-ready
	return value + 1
}

func install() { _ = foreign(callback, 1) }
`, false)
	direct, plain, err := requiredCoroDirectPlainCallArguments(
		fixture.ctx, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 0 {
		t.Fatalf("managed reentry produced raw direct-plain uses: %+v", direct)
	}
	if _, raw := plain[fixture.pkg.Func("callback")]; raw {
		t.Fatal("managed callback entered the raw/plain closure")
	}

	functionIDs := fixture.ctx.coroEmission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI =
		coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	input := CoroPlanInput{
		Program:            fixture.pkg.Prog,
		EmissionUniverse:   fixture.ctx.coroSSAEmission,
		resolveFunction:    fixture.ctx.coroEmission.Resolve,
		functionBackground: fixture.ctx.coroEmission.FunctionBackground,
		callableIdentity:   fixture.ctx.coroEmission.CoroCallableIdentityCertificate,
		callableContract:   fixture.ctx.coroEmission.CoroCallableContractCertificate,
		rawCFunctionType: func(typ types.Type) (bool, error) {
			if typ == nil {
				return false, nil
			}
			_, signature := types.Unalias(typ).Underlying().(*types.Signature)
			return signature && fixture.ctx.prog.TypeBackground(typ) == llssa.InC, nil
		},
	}
	plan, err := input.Analyze(coro.Roots{{
		Function: fixture.pkg.Func("install"),
		Demand:   coro.AsyncDemand,
	}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		FunctionIDs:          functionIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	callback := functionPlanForBuildTest(t, plan, fixture.pkg.Func("callback"))
	if callback.Emission != coro.EmitCoroutine ||
		callback.Primary != coro.PrimaryCoroutine ||
		callback.ManagedDemand == coro.NoDemand ||
		callback.RawPlainDemand || callback.RawPlainEntry ||
		!callback.Effect.Contains(coro.MayPark) {
		t.Fatalf(
			"managed callback plan = %+v; want inference-only coroutine entry",
			callback,
		)
	}
}

func TestRequiredCoroDirectPlainCallbackAdmitsRawCLeafAndElidedIntrinsic(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
//llgo:type C
type CCallback func()

//llgo:type C
type InstallerCallback func(CCallback)

func installC(InstallerCallback) {}

//llgo:link inlineCString llgo.cstr
func inlineCString(string) *byte

func callback(previous CCallback) {
	_ = inlineCString("finalizer")
	if previous != nil {
		previous()
	}
}

func install() { installC(InstallerCallback(callback)) }
`)
	if len(fixture.directPlain) != 1 || fixture.directPlain[0].target != fixture.pkg.Func("callback") {
		t.Fatalf("direct-plain callbacks = %+v, want callback", fixture.directPlain)
	}
	plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	callback := fixture.pkg.Func("callback")
	callbackPlan := functionPlanForBuildTest(t, plan, callback)
	if callbackPlan.ManagedDemand != coro.NoDemand || !callbackPlan.RawPlainDemand || !callbackPlan.RawPlainOnly ||
		!callbackPlan.RawPlainEntry || callbackPlan.Emission != coro.EmitRawPlain || callbackPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("callback plan = %+v, want one raw-only entry", callbackPlan)
	}
	var rawCall ssa.CallInstruction
	for _, call := range coroPlanTestCalls(callback) {
		if call.Common().StaticCallee() == nil {
			rawCall = call
			break
		}
	}
	if rawCall == nil {
		t.Fatal("callback has no raw C code-pointer call")
	}
	callPlan, ok := plan.CallPlan(rawCall)
	if !ok || callPlan.Transport != coro.RawCCodePointer || callPlan.Rep != coro.DirectPlain ||
		callPlan.Kind != coro.CallForeign || !callPlan.Open || callPlan.Unresolved != coro.UnknownForeign {
		t.Fatalf("raw callback CallPlan = %+v, present=%t", callPlan, ok)
	}
}

func TestRequiredCoroDirectPlainCallbackAdmitsCertifiedStaticCLeaf(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
//llgo:type C
type CCallback func()

func installC(CCallback) {}

//llgo:coro sync
//go:linkname bounded C.bounded
func bounded()

func callback() { bounded() }
func install() { installC(CCallback(callback)) }
`)
	if len(fixture.directPlain) != 1 || fixture.directPlain[0].target != fixture.pkg.Func("callback") {
		t.Fatalf("direct-plain callbacks = %+v, want callback", fixture.directPlain)
	}
	if _, present := fixture.requiredPlain[fixture.pkg.Func("bounded")]; !present {
		t.Fatal("certified synchronous C leaf did not enter the exact raw/plain closure")
	}
	plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	callbackPlan := functionPlanForBuildTest(t, plan, fixture.pkg.Func("callback"))
	if callbackPlan.ManagedDemand != coro.NoDemand || !callbackPlan.RawPlainDemand ||
		!callbackPlan.RawPlainOnly || !callbackPlan.RawPlainEntry ||
		callbackPlan.Emission != coro.EmitRawPlain {
		t.Fatalf("callback plan = %+v, want one certified raw-only entry", callbackPlan)
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
	if _, ok := fixture.requiredPlain[cLeaf]; ok {
		t.Fatal("default may-block C leaf was rewritten as a required no-suspend plain leaf")
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
	t.Run("raw C value may have another raw boundary", func(t *testing.T) {
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
		plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
		if err != nil {
			t.Fatal(err)
		}
		callbackPlan := functionPlanForBuildTest(t, plan, fixture.pkg.Func("callback"))
		if callbackPlan.ManagedDemand != coro.NoDemand || !callbackPlan.RawPlainDemand || !callbackPlan.RawPlainOnly ||
			callbackPlan.Emission != coro.EmitRawPlain {
			t.Fatalf("multi-boundary raw callback plan = %+v", callbackPlan)
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
		if _, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1}); err == nil || !strings.Contains(err.Error(), "real local suspend effect may-park") {
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
	if required.External != coro.ExternalUnknownForeign || required.Effect != coro.NoSuspend ||
		!required.Exec.Contains(coro.BlockForeign|coro.IRQUnsafe) ||
		required.FuncRep != coro.DirectPlain || required.Emission != coro.EmitExternal {
		t.Fatalf("required scheduler-stack C declaration = %+v, want conservative external-unknown-foreign direct plain", required)
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

	_, err = fixture.input.Analyze(coro.Roots{{Function: callC, Demand: coro.AsyncDemand}}, coro.SSAConfig{
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
	if err == nil || !strings.Contains(err.Error(), `frontend C declaration "foreignC" conflicts with its frozen callable contract`) {
		t.Fatalf("handcrafted C summary error = %v, want frozen callable-contract rejection", err)
	}
}

func TestCoroPlanInputAutomaticallyColorsPreflightedLibraryDeclaration(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
func imported()
func caller() { imported() }
func install() {}
`)
	imported := fixture.pkg.Func("imported")
	caller := fixture.pkg.Func("caller")
	importedID, err := coro.StableFunctionID(imported, fixture.functionIDs)
	if err != nil {
		t.Fatal(err)
	}
	fixture.input.importedLibraryEffects = map[*ssa.Function]coro.LibraryEffectFunction{
		imported: {
			ID:            importedID,
			ABIHash:       strings.Repeat("a", 64),
			Effect:        coro.MayPark,
			Exec:          coro.MayUnwind,
			FuncRep:       coro.DirectCoro,
			Primary:       coro.PrimaryCoroutine,
			ManagedEntry:  coro.ManagedEntryCoroutine,
			PrimarySymbol: "example.com/library.imported$coro",
		},
	}
	plan, err := fixture.input.Analyze(
		coro.Roots{{Function: caller, Demand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1, FunctionIDs: fixture.functionIDs},
	)
	if err != nil {
		t.Fatal(err)
	}
	importedPlan := functionPlanForBuildTest(t, plan, imported)
	if importedPlan.External != coro.ExternalKnown || importedPlan.Effect != coro.MayPark ||
		importedPlan.FuncRep != coro.DirectCoro || importedPlan.Emission != coro.EmitExternal {
		t.Fatalf("imported library declaration = %+v", importedPlan)
	}
	callerPlan := functionPlanForBuildTest(t, plan, caller)
	if !callerPlan.Effect.Contains(coro.MayPark|coro.AwaitStructured) ||
		!callerPlan.Exec.Contains(coro.MayUnwind) ||
		callerPlan.Primary != coro.PrimaryCoroutine {
		t.Fatalf("library effect did not automatically color caller: %+v", callerPlan)
	}

	_, err = fixture.input.Analyze(
		coro.Roots{{Function: caller, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			MaxPlainInstructions: -1,
			FunctionIDs:          fixture.functionIDs,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == imported {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "conflicts with producer-owned metadata") {
		t.Fatalf("builder override error = %v", err)
	}
}

func TestCoroPlanInputRejectsUnprovenBodylessRequiredDeclarations(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `func bad()
func install() { bad() }
`)
	if _, ok := fixture.requiredPlain[fixture.pkg.Func("bad")]; !ok {
		t.Fatal("bodyless Go declaration did not enter the static required closure")
	}
	if _, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1}); err == nil || !strings.Contains(err.Error(), "has no frozen frontend C ABI proof") {
		t.Fatalf("bodyless Go required declaration error = %v", err)
	}
}

func TestRequiredCoroProgramRuntimePlanRejectsPythonOutsideProgramRoot(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "bodyless", body: "func bad()\n"},
		{name: "bodyful", body: "func bad() {}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixtureSource(t,
				requiredCoroPhysicalRuntimeFixture+"//llgo:link bad py.bad\n"+test.body+"func install() { bad() }\n",
				false,
			)
			if _, _, _, _, err := requiredCoroProgramRuntimePlan(fixture.ctx); err == nil ||
				!strings.Contains(err.Error(), "has no compiler-owned program-root owner realm") {
				t.Fatalf("Python %s required runtime error = %v", test.name, err)
			}
		})
	}
}

func TestCoroPlanInputElidesCompilerIntrinsicFromRequiredPlain(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "bodyless", body: "func bad()\n"},
		{name: "bodyful", body: "func bad() {}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixture(t, "//llgo:link bad llgo.unreachable\n"+test.body+`func install() { bad() }
`)
			bad := fixture.pkg.Func("bad")
			if _, ok := fixture.requiredPlain[bad]; ok {
				t.Fatal("compiler-inline intrinsic entered the static required-plain closure")
			}
			if semantics, intrinsic, err := fixture.ctx.coroEmission.CoroIntrinsicSemantics(bad); err != nil || !intrinsic || semantics != cl.CoroIntrinsicCallInlineNoSuspend {
				t.Fatalf("intrinsic semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
			}
			plan, err := fixture.analyze(coro.SSAConfig{MaxPlainInstructions: -1})
			if err != nil {
				t.Fatal(err)
			}
			call := onlyBuildTestCall(t, fixture.pkg.Func("install"))
			if !plan.ElidesCall(call) {
				t.Fatal("compiler-inline intrinsic call was not frozen as an elided call site")
			}
			if _, ok := plan.CallPlan(call); ok {
				t.Fatal("compiler-inline intrinsic unexpectedly has a managed CallPlan")
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
	roots         coro.Roots
	requiredPlain map[*ssa.Function]struct{}
	directPlain   []requiredCoroDirectPlainCallArgument
	closedDynamic map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate
	functionIDs   coro.FunctionIDConfig
}

func (f requiredCoroRuntimeFixture) analyze(config coro.SSAConfig) (*coro.SSAPlan, error) {
	config.FunctionIDs = f.functionIDs
	return f.input.Analyze(nil, config)
}

const requiredCoroPhysicalRuntimeFixture = `
func __llgo_coro_await_prepare_v3(g, parent, child unsafe.Pointer, mode uint32, typeWord, dataWord unsafe.Pointer) {}
func __llgo_coro_await_inline_v1(g, parent, child unsafe.Pointer) bool { return false }
func __llgo_coro_await_consume_v1(g, parent, typeOut, dataOut unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v2(g, handle, header unsafe.Pointer, status uint32) {}
func __llgo_coro_critical_enter_v1(g unsafe.Pointer) {}
func __llgo_coro_critical_exit_v1(g unsafe.Pointer) bool { return false }
func __llgo_coro_os_thread_lock_v1(g unsafe.Pointer) {}
func __llgo_coro_os_thread_unlock_v1(g unsafe.Pointer) {}
`

func buildRequiredCoroRuntimeFixture(t *testing.T, body string) requiredCoroRuntimeFixture {
	t.Helper()
	return buildRequiredCoroRuntimeFixtureSource(t, requiredCoroPhysicalRuntimeFixture+body, true)
}

func buildRequiredCoroRuntimeFixtureSource(
	t *testing.T,
	body string,
	validateRuntimePlan bool,
) requiredCoroRuntimeFixture {
	t.Helper()
	source := `package runtime
import "unsafe"
func __llgo_coro_program_begin_v1() { install() }
func __llgo_coro_program_run_v1() {}
func __llgo_coro_program_continue_v1(uint32) {}
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_frame_publish_v3() {}
func __llgo_coro_frame_destroy_commit_v2() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_frame_free_v1() {}
func __llgo_coro_chan_send_try_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, uint32, uint32) uint32 { return 0 }
func __llgo_coro_chan_recv_try_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr, uint32, uint32) uint32 { return 0 }
func __llgo_coro_chan_resume_v2(unsafe.Pointer, unsafe.Pointer) uint32 { return 0 }
type Chan struct{}
type ChanOp struct{}
func CoroChanTrySend(unsafe.Pointer, *Chan, unsafe.Pointer, int) bool { return false }
func CoroChanTryRecv(unsafe.Pointer, *Chan, unsafe.Pointer, int) (bool, bool) { return false, false }
func CoroChanTryCloseTask(unsafe.Pointer, *Chan) uint32 { return 0 }
func CoroChanSelectTry(...ChanOp) (int, bool, bool, bool) { return 0, false, false, false }
func CoroChanSelectPark(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) {}
func CoroChanSelectResume(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) (int, bool, uint32) { return 0, false, 0 }
func __llgo_coro_fault_prepare_v1() {}
func __llgo_coro_fault_prepare_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32, uint64, uintptr) {}
func __llgo_coro_panic_prepare_v1() {}
func __llgo_coro_panic_trace_replace_v1(unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_recover_take_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_payload_v1(uint32, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_payload_v2(uint32, uint64, uintptr, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_spawn_begin_v1() {}
func __llgo_coro_spawn_commit_v1() {}
func __llgo_coro_program_main_return_v1() {}
` + body
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, source, nil)
	prog := llssa.NewProgram(nil)
	t.Cleanup(prog.Dispose)
	cl.ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
	emission, err := cl.PrepareEmissionUniverse(prog, nil, []cl.EmissionPackage{{
		SSA:            ssaPkg,
		Files:          files,
		Identity:       llssa.PkgRuntime,
		RawDataSymbols: cl.CoroRawDataSymbolProfile{Complete: true},
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
		buildConf:                   &Config{},
		coroEmission:                emission,
		coroSSAEmission:             ssaEmission,
		coroTLSDestructorFixturePkg: llssa.PkgRuntime,
	}
	if !validateRuntimePlan {
		// Negative ABI tests need the parsed, immutable emission universe but
		// intentionally cannot build the required-root closure yet: the error
		// under test is produced by that exact operation.
		return requiredCoroRuntimeFixture{pkg: ssaPkg, ctx: ctx}
	}
	roots, requiredPlain, directPlain, closedDynamic, err := requiredCoroProgramRuntimePlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	return requiredCoroRuntimeFixture{
		pkg: ssaPkg,
		ctx: ctx,
		input: CoroPlanInput{
			Program:            ssaPkg.Prog,
			EmissionUniverse:   ssaEmission,
			resolveFunction:    emission.Resolve,
			functionBackground: emission.FunctionBackground,
			callableIdentity:   emission.CoroCallableIdentityCertificate,
			callableContract:   emission.CoroCallableContractCertificate,
			callSitePlan:       emission.CoroCallSitePlan,
			rawCFunctionType: func(typ types.Type) (bool, error) {
				if typ == nil {
					return false, nil
				}
				if _, signature := types.Unalias(typ).Underlying().(*types.Signature); !signature {
					return false, nil
				}
				return prog.TypeBackground(typ) == llssa.InC, nil
			},
			requiredRoots:               roots,
			requiredPlain:               requiredPlain,
			requiredHostPlain:           requiredPlain,
			requiredDirectPlain:         directPlain,
			requiredClosedDynamic:       closedDynamic,
			requiredGlobalFunctionSlots: ctx.coroGlobalFunctionSlots,
		},
		roots:         roots,
		requiredPlain: requiredPlain,
		directPlain:   directPlain,
		closedDynamic: closedDynamic,
		functionIDs:   functionIDs,
	}
}

func TestRequiredCoroProgramRuntimePlanCriticalRoots(t *testing.T) {
	const physicalHooks = `
func __llgo_coro_await_prepare_v3() {}
func __llgo_coro_await_inline_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) bool { return false }
func __llgo_coro_await_consume_v1() {}
func __llgo_coro_complete_prepare_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32) {}
func __llgo_coro_os_thread_lock_v1(unsafe.Pointer) {}
func __llgo_coro_os_thread_unlock_v1(unsafe.Pointer) {}
`
	const exact = physicalHooks + `
func __llgo_coro_critical_enter_v1(unsafe.Pointer) {}
func __llgo_coro_critical_exit_v1(unsafe.Pointer) bool { return false }
func install() {}
`
	t.Run("exact runnable PhysicalABIV1 roots", func(t *testing.T) {
		fixture := buildRequiredCoroRuntimeFixtureSource(t, exact, true)
		roots, requiredPlain, _, _, err := requiredCoroProgramRuntimePlan(fixture.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"__llgo_coro_critical_enter_v1", "__llgo_coro_critical_exit_v1"} {
			fn := fixture.pkg.Func(name)
			found := false
			for _, root := range roots {
				if root.Function == fn && root.Demand.Join(root.ManagedDemand) == coro.SyncDemand && !root.RawPlainDemand {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("missing synchronous critical runtime root %q", name)
			}
			if _, required := requiredPlain[fn]; !required {
				t.Fatalf("critical runtime root %q did not enter required plain closure", name)
			}
		}
	})

	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing",
			body: physicalHooks + `func install() {}`,
			want: `critical_enter_v1" has no emitted Go body`,
		},
		{
			name: "wrong enter parameter",
			body: physicalHooks + `
func __llgo_coro_critical_enter_v1(uintptr) {}
func __llgo_coro_critical_exit_v1(unsafe.Pointer) bool { return false }
func install() {}
`,
			want: "must have exact func(unsafe.Pointer) signature",
		},
		{
			name: "wrong exit result",
			body: physicalHooks + `
func __llgo_coro_critical_enter_v1(unsafe.Pointer) {}
func __llgo_coro_critical_exit_v1(unsafe.Pointer) uint32 { return 0 }
func install() {}
`,
			want: "must have exact func(unsafe.Pointer) bool signature",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixtureSource(t, test.body, false)
			_, _, _, _, err := requiredCoroProgramRuntimePlan(fixture.ctx)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("critical runtime root error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestBuildCoroPlanRejectsPartialStacklessArchive(t *testing.T) {
	err := buildCoroPlan(&context{buildConf: &Config{
		BuildMode: BuildModeCArchive,
	}})
	if err == nil || !strings.Contains(err.Error(), "c-archive requires flattened package members") {
		t.Fatalf("partial stackless archive error = %v", err)
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
		universe, err := cl.PrepareEmissionUniverseWithOptions(prog, nil, []cl.EmissionPackage{{SSA: ssaPkg, Files: files}}, cl.EmissionUniverseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		functionIDs := universe.FunctionIDConfig()
		functionIDs.CoroABI = coro.PhysicalABIV1
		functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
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
		factsReport, err := universe.BuildCoroLoweringFactsReport(plan)
		if err != nil {
			t.Fatal(err)
		}
		compilation := &cl.Compilation{
			CoroPlan: plan,

			CoroPlanDigest:          strings.Repeat("0", 64),
			CoroLoweringFacts:       factsReport.Facts,
			CoroLoweringFactsDigest: factsReport.Digest,
			CoroABI:                 coro.PhysicalABIV1,
			SchedulerABI:            coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:                coro.PanicExplicitStatusABIV0,
			FuncRepABI:              coro.FuncRepABIV1,
			EmissionUniverse:        universe}
		lpkg, _, err := cl.NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, cl.PackageOptions{
			Compilation: compilation,
			CacheHit:    cacheHit,
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

	_, err = input.Analyze(roots, coro.SSAConfig{
		ClassifyDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return []*ssa.Function{method, method2}, nil
			}
			return nil, nil
		},
		ClassifySyncDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return []*ssa.Function{method}, nil
			}
			return nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "conflict with the frozen frontend raw-ABI references") {
		t.Fatalf("builder-invented synchronous demand-reference error = %v", err)
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

func TestValidateCoroUnwindOnlyLoweredCallsExplicitStatusAcceptsExactNoUnwindPlainTarget(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/unwindexplicitplain", `package unwindexplicitplain
var channel chan int
func owner() { <-channel }
func safe(value int) bool { return value == 0 }
func affine(value int) bool { return value == 0 }
`, nil)
	owner := ssaPkg.Func("owner")
	safe := ssaPkg.Func("safe")
	affine := ssaPkg.Func("affine")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{owner, safe, affine})
	if err != nil {
		t.Fatal(err)
	}
	build := func(target *ssa.Function) *coro.SSAPlan {
		t.Helper()
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
			EmissionUniverse: universe,
			OutcomeMode:      coro.OutcomeExplicitStatus,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == affine {
					return coro.SSAFunctionPolicy{Exec: coro.ThreadAffine}, nil
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

	safePlan := build(safe)
	if got := functionPlanForBuildTest(t, safePlan, owner); got.Emission != coro.EmitCoroutine {
		t.Fatalf("owner plan = %+v; want physical coroutine", got)
	}
	if got := functionPlanForBuildTest(t, safePlan, safe); got.Emission != coro.EmitPlain ||
		got.Effect != coro.NoSuspend || got.Exec.Contains(coro.MayUnwind) {
		t.Fatalf("safe helper plan = %+v; want exact no-unwind plain body", got)
	}
	if err := validateCoroUnwindOnlyLoweredCalls(safePlan, coro.PanicExplicitStatusABIV0); err != nil {
		t.Fatalf("exact no-unwind plain helper rejected under ExplicitStatus: %v", err)
	}

	affinePlan := build(affine)
	if got := functionPlanForBuildTest(t, affinePlan, owner); got.Emission != coro.EmitCoroutine {
		t.Fatalf("thread-affine case owner plan = %+v; want physical coroutine", got)
	}
	if got := functionPlanForBuildTest(t, affinePlan, affine); got.Emission != coro.EmitPlain ||
		!got.Exec.Contains(coro.ThreadAffine) || got.Exec.Contains(coro.MayUnwind) {
		t.Fatalf("thread-affine helper plan = %+v; want no-unwind plain body with an incompatible execution constraint", got)
	}
	if err := validateCoroUnwindOnlyLoweredCalls(affinePlan, coro.PanicExplicitStatusABIV0); err == nil ||
		!strings.Contains(err.Error(), "no exact ExplicitStatus coroutine child") {
		t.Fatalf("thread-affine plain helper error = %v; want ExplicitStatus rejection", err)
	}
}

func TestValidateCoroUnwindOnlyLoweredCallsAcceptsDynamicErrorCoroutineChild(t *testing.T) {
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
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: owner, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: universe,
		OutcomeMode:      coro.OutcomeExplicitStatus,
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
	if err := validateCoroUnwindOnlyLoweredCalls(plan, coro.PanicExplicitStatusABIV0); err != nil {
		t.Fatalf("dynamic error coroutine child rejected: %v", err)
	}
	if got, ok := plan.FunctionPlan(failure); !ok || got.FuncRep != coro.DirectCoro || !got.Exec.Contains(coro.OpaqueExec) {
		t.Fatalf("dynamic Error target was unexpectedly forced plain: %+v, present=%v", got, ok)
	}
}

func TestValidateCoroUnwindOnlyLoweredCallsAcceptsStaticCallToDispatchRepresentedPlainBody(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/unwindstaticdispatch", `package unwindstaticdispatch
var sink func()
func owner() {}
func helper() { target() }
func target() {}
func publish() { sink = target }
`, nil)
	owner := ssaPkg.Func("owner")
	helper := ssaPkg.Func("helper")
	target := ssaPkg.Func("target")
	publish := ssaPkg.Func("publish")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{owner, helper, target, publish})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: owner, Demand: coro.SyncDemand},
		{Function: publish, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse: universe,
		ClassifyLoweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
			if fn == owner {
				return []coro.SSALoweredCall{{LogicalName: "runtime.Panic", Target: helper, UnwindOnly: true}}, nil
			}
			return nil, nil
		},
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := plan.FunctionPlan(target); !ok || got.FuncRep != coro.Dispatch ||
		got.Emission != coro.EmitPlain || got.Primary != coro.PrimaryPlain || got.Effect != coro.NoSuspend {
		t.Fatalf("stored static target plan = %+v, present=%v; want Dispatch representation with one plain body", got, ok)
	}
	if err := validateCoroUnwindOnlyLoweredCalls(plan, coro.PanicExplicitStatusABIV0); err != nil {
		t.Fatalf("exact static edge to Dispatch-represented plain body rejected: %v", err)
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
		{"compile-time architecture", nil, coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, coro.PanicExplicitStatusABIV0, coro.FuncRepABIV1},
		{"target config", &Config{}, coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, coro.PanicExplicitStatusABIV0, coro.FuncRepABIV1},
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

	t.Run("configuration-only context has no program to analyze", func(t *testing.T) {
		ctx := &context{buildConf: &Config{BuildMode: BuildModeExe}}
		err := buildCoroPlan(ctx)
		if err != nil {
			t.Fatalf("configuration-only buildCoroPlan error = %v", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("configuration-only build installed coroutine compilation state")
		}
	})

	t.Run("child await rejects nested c-archive", func(t *testing.T) {
		ctx := &context{buildConf: &Config{
			BuildMode: BuildModeCArchive}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "c-archive requires flattened package members") {
			t.Fatalf("buildCoroPlan error = %v, want flattened-archive rejection", err)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("invalid c-archive configuration installed coroutine compilation state")
		}
	})

	t.Run("custom builder must return input analysis", func(t *testing.T) {
		builderCalls := 0
		ctx := &context{buildConf: &Config{
			BuildMode: BuildModeExe,

			CoroPlanBuilder: func(CoroPlanInput) (*coro.SSAPlan, error) {
				builderCalls++
				return &coro.SSAPlan{}, nil
			}}}
		err := buildCoroPlan(ctx)
		if err == nil || !strings.Contains(err.Error(), "plan created by CoroPlanInput.Analyze") {
			t.Fatalf("buildCoroPlan error = %v, want Analyze provenance rejection", err)
		}
		if builderCalls != 1 {
			t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
		}
		if ctx.coroPlan != nil || ctx.clCompilation != nil {
			t.Fatal("unproven builder installed coroutine compilation state")
		}
	})

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

	t.Run("Do uses the default planner", func(t *testing.T) {
		conf := NewDefaultConf(ModeGen)
		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if err != nil {
			t.Fatalf("Do with default coroutine planner: %v", err)
		}
		if len(pkgs) == 0 {
			t.Fatal("Do with default coroutine planner returned no packages")
		}
	})

	t.Run("Do rejects active builder that bypasses input Analyze", func(t *testing.T) {
		conf := NewDefaultConf(ModeGen)
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
	newContext := func(digest string) *context {
		ctx := &context{
			buildConf: &Config{
				Goos:   "linux",
				Goarch: "amd64"},
			crossCompile: crosscompile.Export{
				LLVMTarget: "x86_64-unknown-linux-gnu",
				CPU:        "x86-64",
				Features:   "+sse2",
				TargetABI:  "gnu",
			},
		}
		activateCoroPackageCacheTestWithDigest(t, ctx, digest)
		return ctx
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
	targetMismatch := newContext(digestA)
	targetMismatch.clCompilation.CoroTargetCapabilities = 0
	if targetMismatch.canUsePackageCache() {
		t.Fatal("target capability mismatch unexpectedly permits package cache")
	}
	frameRetentionMismatch := newContext(digestA)
	frameRetentionMismatch.clCompilation.CoroFrameRetentionABI = ""
	if frameRetentionMismatch.canUsePackageCache() {
		t.Fatal("frame-retention ABI identity mismatch unexpectedly permits package cache")
	}
	frameRetentionMismatch.clCompilation.CoroFrameRetentionABI = coro.FrameRetentionParkABIV2
	if !frameRetentionMismatch.canUsePackageCache() {
		t.Fatal("matching frame-retention ABI identity unexpectedly disables package cache")
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
		{name: "host", conf: Config{}, want: true},
		{name: "target", conf: Config{Target: "embedded"}, want: true},
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
		{name: "host initializes runtime", conf: Config{}, wantInit: true, wantLink: true},
		{name: "named target initializes runtime", conf: Config{Target: "embedded"}, wantInit: true, wantLink: true},
		{name: "legacy runtime reference", conf: Config{Target: "embedded"}, needRuntime: true, wantInit: true, wantLink: true},
		{name: "python links with runtime init", conf: Config{Target: "embedded"}, needPyInit: true, wantInit: true, wantLink: true},
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
	if err == nil ||
		!strings.Contains(err.Error(), "declared may-park effect has no exact structured park intrinsic") {
		t.Fatalf("Do error = %v, want exact coroutine physical-ABI rejection before codegen", err)
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
