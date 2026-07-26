//go:build llgo && llgo_coro && !coro_runtime_adapter_test && (wasm || tinygo.wasm || baremetal || llgo_coro_host) && !(llgo_coro_native_pipe && (darwin || linux) && !baremetal)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package runtime

import (
	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	"github.com/goplus/llgo/runtime/internal/coro"
)

func coroHostOperationControlAdvanceEpochV1(cell *coroHostOperationControlLaneV1) bool {
	if cell == nil {
		return false
	}
	for {
		current := catomic.Load(&cell.epoch)
		next := current + 1
		if next == 0 {
			next = 1
		}
		if _, swapped := catomic.CompareAndExchange(&cell.epoch, current, next); swapped {
			return true
		}
	}
}

// CoroHostOperationControlEpochV1 snapshots the reconfiguration generation
// that a complete host operation must carry into its park hook. The poll
// descriptor reads this generation before its deadline. SetDeadline/Close
// publish their descriptor state first and advance this epoch second.
func CoroHostOperationControlEpochV1(key uintptr, lane uint32) (uint32, bool) {
	cell, ok := coroHostOperationControlCellV1(key, lane)
	if !ok {
		return 0, false
	}
	return catomic.Load(&cell.epoch), true
}

// CoroHostOperationControlCancelV1 publishes a new configuration generation
// and invalidates any currently parked complete host operation in the selected
// lanes. If no operation is bound yet, its later park hook detects the epoch
// mismatch after binding and requests the same exact physical cancellation.
// The worker catalog resolves private WaitSetRecord state and retains the
// existing physical-cancel barrier.
func CoroHostOperationControlCancelV1(key uintptr, lanes uint32) bool {
	if key == 0 || key > uintptr(len(coroHostOperationControlSlotsV1)) ||
		lanes == 0 || lanes & ^uint32(CoroHostOperationControlReadV1|CoroHostOperationControlWriteV1) != 0 {
		return false
	}
	for _, lane := range [...]uint32{
		CoroHostOperationControlReadV1,
		CoroHostOperationControlWriteV1,
	} {
		if lanes&lane == 0 {
			continue
		}
		cell, _ := coroHostOperationControlCellV1(key, lane)
		if !coroHostOperationControlAdvanceEpochV1(cell) {
			return false
		}
		if cell.operation != (coro.OperationID{}) {
			_ = coroProgramWorkerSourceV1State.RequestCancelID(
				&coroProgramPV1State,
				cell.operation,
			)
		}
	}
	return true
}

// CoroHostOperationControlIdleV1 is the descriptor-retirement gate.
func CoroHostOperationControlIdleV1(key uintptr) bool {
	if key == 0 || key > uintptr(len(coroHostOperationControlSlotsV1)) {
		return false
	}
	slot := &coroHostOperationControlSlotsV1[key-1]
	return slot.read.operation == (coro.OperationID{}) &&
		slot.write.operation == (coro.OperationID{})
}
