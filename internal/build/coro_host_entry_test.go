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
type hostOperationActionV1 struct {
	Kind, SourceSlot, SourceGeneration, Opcode, ArgCount, Reserved uint32
	Args [18]uint32
}
type CoroHostOperationParkV1 struct{}
func __llgo_coro_program_run_slice_v2(unsafe.Pointer, unsafe.Pointer, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_program_continue_slice_v2(uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_host_next_action_v1(*hostActionV1) uint32 { return 0 }
func __llgo_coro_host_profile_v1() uint32 { return 0 }
func __llgo_coro_host_next_deadline_v1(*hostActionV1) bool { return false }
func __llgo_coro_host_publish_time_v1(uint32, uint32) bool { return false }
func __llgo_coro_host_publish_wall_time_v1(uint32, uint32, uint32) bool { return false }
func __llgo_coro_host_ack_cancel_v1(uint32, uint32, uint32, uint32) bool { return false }
func __llgo_coro_host_continue_slice_v1(uint32, uint32, uint32, uint32, uint32, uint32, uint32, *coroProgramRunResultV2) uint32 { return 0 }
func __llgo_coro_host_next_operation_v1(*hostOperationActionV1) uint32 { return 0 }
func __llgo_coro_host_complete_operation_v1(uint32, uint32, uint32, uint32, uint32, uint32, uint32, uint32, uint32, uint32) uint32 { return 0 }
func __llgo_coro_host_operation_park_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32, uint32, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) {}
func __llgo_coro_host_operation_resume_v1(unsafe.Pointer, unsafe.Pointer, *uintptr, *uintptr, *uintptr) uint32 { return 0 }
func __llgo_coro_timer_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, int64) {}
func __llgo_coro_timer_park_controlled_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, *uint32, *uint32, uint32, int64) {}
func __llgo_coro_timer_resume_v2(unsafe.Pointer, unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_timer_request_controlled_v2(uint32) uint32 { return 0 }
func __llgo_coro_keyed_park_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_keyed_resume_v2(unsafe.Pointer, unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_sema_prepare_or_abort_v2(unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_sema_release_or_abort_v2(unsafe.Pointer) {}
func __llgo_coro_notify_prepare_or_abort_v2(unsafe.Pointer, unsafe.Pointer, uint32) {}
func __llgo_coro_notify_one_or_abort_v2(unsafe.Pointer, uint32) {}
func __llgo_coro_notify_all_or_abort_v2(unsafe.Pointer, uint32) {}
func __llgo_coro_frame_allocator_bootstrap_v1() {}
func __llgo_coro_frame_alloc_v1() {}
func __llgo_coro_frame_publish_v1() {}
func __llgo_coro_await_prepare_v1() {}
func __llgo_coro_await_prepare_v3(g, parent, child unsafe.Pointer, mode uint32, typeWord, dataWord unsafe.Pointer) {}
func __llgo_coro_await_consume_v1(g, parent, typeOut, dataOut unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_preempt_poll_v1() bool { return false }
func __llgo_coro_yield_prepare_v1() {}
func __llgo_coro_run_decision_take_v1(unsafe.Pointer, uint32, uint32, *uint32, *uint32, *uint32, *uint32, *uint32) {}
func __llgo_coro_run_decision_take_zero_v1(unsafe.Pointer) uint32 { return 0 }
func __llgo_coro_complete_prepare_v1() {}
func __llgo_coro_complete_prepare_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32) {}
func __llgo_coro_critical_enter_v1(unsafe.Pointer) {}
func __llgo_coro_critical_exit_v1(unsafe.Pointer) bool { return false }
func __llgo_coro_os_thread_lock_v1(unsafe.Pointer) {}
func __llgo_coro_os_thread_unlock_v1(unsafe.Pointer) {}
func __llgo_coro_frame_free_v1() {}
func __llgo_coro_chan_send_park_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr) {}
func __llgo_coro_chan_recv_park_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uintptr) {}
func __llgo_coro_chan_resume_v1(unsafe.Pointer, unsafe.Pointer) uint32 { return 0 }
type Chan struct{}
type ChanOp struct{}
func CoroChanTrySend(*Chan, unsafe.Pointer, int) bool { return false }
func CoroChanTryRecv(*Chan, unsafe.Pointer, int) (bool, bool) { return false, false }
func CoroChanTryClose(*Chan) uint32 { return 0 }
func CoroChanSelectTry(...ChanOp) (int, bool, bool, bool) { return 0, false, false, false }
func CoroChanSelectPark(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) {}
func CoroChanSelectResume(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, ...ChanOp) (int, bool, uint32) { return 0, false, 0 }
func __llgo_coro_fault_prepare_v1() {}
func __llgo_coro_fault_prepare_v2(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, uint32, uint64, uintptr) {}
func __llgo_coro_panic_prepare_v1() {}
func __llgo_coro_recover_take_v1(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_payload_v1(uint32, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_fault_payload_v2(uint32, uint64, uintptr, unsafe.Pointer, unsafe.Pointer) {}
func __llgo_coro_spawn_begin_v1() {}
func __llgo_coro_spawn_commit_v1() {}
func __llgo_coro_program_main_return_v1() {}
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
			BuildMode: BuildModeExe,
			Goos:      "wasip1",
			Goarch:    "wasm"},
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
		coroHostNextActionSymbolV1,
		coroHostProfileSymbolV1,
		coroHostNextDeadlineSymbolV1,
		coroHostPublishTimeSymbolV1,
		coroHostPublishWallTimeSymbolV1,
		coroHostAckCancelSymbolV1,
		coroHostContinueSliceSymbolV1,
		coroHostNextOperationSymbolV1,
		coroHostCompleteOperationSymbolV1,
		coroHostOperationParkSymbolV1,
		coroHostOperationResumeSymbolV1,
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
		"__llgo_coro_frame_alloc_v1",
		"__llgo_coro_frame_publish_v1",
		"__llgo_coro_await_prepare_v1",
		"__llgo_coro_preempt_poll_v1",
		"__llgo_coro_yield_prepare_v1",
		coroRunDecisionTakeSymbolV1,
		coroRunDecisionTakeZeroSymbolV1,
		"__llgo_coro_complete_prepare_v1",
		"__llgo_coro_frame_free_v1",
		"__llgo_coro_await_prepare_v3",
		"__llgo_coro_await_consume_v1",
		"__llgo_coro_complete_prepare_v2",
		"__llgo_coro_critical_enter_v1",
		"__llgo_coro_critical_exit_v1",
		coroOSThreadLockSymbolV1,
		coroOSThreadUnlockSymbolV1,
		"CoroChanTrySend",
		"CoroChanTryRecv",
		"CoroChanTryClose",
		"CoroChanSelectTry",
		"CoroChanSelectPark",
		"CoroChanSelectResume",
		coroChanSendParkSymbolV1,
		coroChanRecvParkSymbolV1,
		coroChanResumeSymbolV1,
		"__llgo_coro_fault_prepare_v1",
		"__llgo_coro_fault_prepare_v2",
		"__llgo_coro_panic_prepare_v1",
		"__llgo_coro_recover_take_v1",
		"__llgo_coro_fault_payload_v1",
		"__llgo_coro_fault_payload_v2",
		"__llgo_coro_spawn_begin_v1",
		"__llgo_coro_spawn_commit_v1",
		coroProgramMainReturnSymbolV1,
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
	for _, legacy := range []string{coroProgramRunSymbolV1, coroProgramContinueSymbolV1} {
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
