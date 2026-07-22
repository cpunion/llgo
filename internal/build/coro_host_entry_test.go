//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package build

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

func TestRequiredCoroHostPullRuntimeRootsV1(t *testing.T) {
	ssaPkg, files := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func __llgo_coro_program_begin_v1() {}
type coroProgramRunResultV2 struct { Flags, Used, ExecutorSlot, ExecutorGeneration, Epoch, DeadlineLo, DeadlineHi, Reserved uint32 }
type hostActionV1 struct { Kind, ExecutorSlot, ExecutorGeneration, Epoch, DeadlineLo, DeadlineHi, Reserved0, Reserved1 uint32 }
func __llgo_coro_program_run_slice_v2(unsafe.Pointer, unsafe.Pointer, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_program_continue_slice_v2(uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_host_next_action_v1(*hostActionV1) uint32 { return 0 }
func __llgo_coro_host_profile_v1() uint32 { return 0 }
func __llgo_coro_host_next_deadline_v1(*hostActionV1) bool { return false }
func __llgo_coro_host_publish_time_v1(uint32, uint32) bool { return false }
func __llgo_coro_host_ack_cancel_v1(uint32, uint32, uint32, uint32) bool { return false }
func __llgo_coro_host_continue_slice_v1(uint32, uint32, uint32, uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_host_post_wait_v1(uint32, uint32, uint32, uint32) uint32 { return 0 }
func __llgo_coro_wait_prepare_v1(unsafe.Pointer, *uint32, *uint32, *uint32, *uint32, *uint32) bool { return false }
func __llgo_coro_wait_rollback_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_wait_retire_completed_v1(unsafe.Pointer, uint32, uint32, uint32) bool { return false }
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_await_prepare_v2() {}
func __llgo_coro_await_prepare_v3() {}
func __llgo_coro_await_consume_v1() {}
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_park_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_complete_prepare_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32) {}
func __llgo_coro_critical_enter_v1(unsafe.Pointer) {}
func __llgo_coro_critical_exit_v1(unsafe.Pointer) bool { return false }
func __llgo_coro_frame_free_v1() {}
`, nil)
	prog := llssa.NewProgram(&llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
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
		prog: prog,
		buildConf: &Config{
			BuildMode:                     BuildModeExe,
			Goos:                          "wasip1",
			Goarch:                        "wasm",
			EnableCoroEntryResolution:     true,
			EnableCoroPhysicalABI:         true,
			EnableCoroChildAwait:          true,
			EnableCoroProgramBootstrapABI: true,
			EnableCoroProgramBootstrapRun: true,
		},
		coroEmission:    emission,
		coroSSAEmission: ssaEmission,
	}
	roots, requiredPlain, directPlain, closedDynamic, err := requiredCoroProgramRuntimePlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(directPlain) != 0 || len(closedDynamic) != 0 {
		t.Fatalf("host-pull raw island acquired dynamic/C callback edges: direct=%d dynamic=%d", len(directPlain), len(closedDynamic))
	}
	wantRoots := []string{
		"init",
		coroFrameAllocatorBootstrapSymbolV1,
		coroProgramBeginSymbolV1,
		coroProgramRunSliceSymbolV2,
		coroProgramContinueSliceSymbolV2,
		coroWaitPrepareSymbolV1,
		coroWaitRollbackSymbolV1,
		coroWaitRetireCompletedSymbolV1,
		coroHostNextActionSymbolV1,
		coroHostProfileSymbolV1,
		coroHostNextDeadlineSymbolV1,
		coroHostPublishTimeSymbolV1,
		coroHostAckCancelSymbolV1,
		coroHostContinueSliceSymbolV1,
		coroHostPostWaitSymbolV1,
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
		"__llgo_coro_await_prepare_v3",
		"__llgo_coro_await_consume_v1",
		"__llgo_coro_complete_prepare_v2",
		"__llgo_coro_critical_enter_v1",
		"__llgo_coro_critical_exit_v1",
	}
	if len(roots) != len(wantRoots) {
		t.Fatalf("host-pull runtime roots = %d, want %d", len(roots), len(wantRoots))
	}
	for index, root := range roots {
		wantDemand := coro.SyncDemand
		if index == 0 {
			wantDemand = coro.AsyncDemand
		}
		if root.Function == nil || root.Function.Name() != wantRoots[index] || root.Demand != wantDemand {
			t.Fatalf("host-pull root %d = %+v, want %s/%s", index, root, wantRoots[index], wantDemand)
		}
		if _, ok := requiredPlain[root.Function]; index != 0 && !ok {
			t.Fatalf("host-pull root %q is absent from the trusted plain island", wantRoots[index])
		}
	}
	for _, legacy := range []string{coroProgramRunSymbolV1, coroProgramContinueSymbolV1, coroNativePostWaitSymbolV1} {
		if fn := ssaPkg.Func(legacy); fn != nil {
			if _, ok := requiredPlain[fn]; ok {
				t.Fatalf("host-pull plan retained incompatible runtime root %q", legacy)
			}
		}
	}

	profile := ssaPkg.Func(coroHostProfileSymbolV1)
	original := profile.Signature
	profile.Signature = types.NewSignatureType(nil, nil, nil, types.NewTuple(),
		types.NewTuple(types.NewParam(token.NoPos, nil, "profile", types.Typ[types.Uint64])), false)
	_, _, _, _, invalidErr := requiredCoroProgramRuntimePlan(ctx)
	profile.Signature = original
	if invalidErr == nil || !strings.Contains(invalidErr.Error(), "host profile ABI") {
		t.Fatalf("invalid host profile ABI error = %v", invalidErr)
	}
}
