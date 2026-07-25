//go:build wasm || tinygo.wasm

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import (
	_ "unsafe"

	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	llrt "github.com/goplus/llgo/runtime/internal/runtime"
)

const (
	pollNoError        = 0
	pollErrClosing     = 1
	pollErrTimeout     = 2
	pollErrNotPollable = 3
)

const coroHostPollDescCapacityV1 = 64

type coroHostPollDescV1 struct {
	fd      int32
	active  uint32
	closing bool
	read    int64
	write   int64
}

var coroHostPollDescsV1 [coroHostPollDescCapacityV1]coroHostPollDescV1

func coroHostPollDescForV1(ctx uintptr) (*coroHostPollDescV1, bool) {
	if ctx == 0 || ctx > uintptr(len(coroHostPollDescsV1)) {
		return nil, false
	}
	desc := &coroHostPollDescsV1[ctx-1]
	return desc, catomic.Load(&desc.active) != 0
}

func coroHostPollDeadlineV1(desc *coroHostPollDescV1, mode int) (int64, bool) {
	if desc == nil || catomic.Load(&desc.active) == 0 {
		return 0, false
	}
	switch mode {
	case 'r':
		return desc.read, true
	case 'w':
		return desc.write, true
	default:
		return 0, false
	}
}

func coroHostPollDeadlineExpiredV1(deadline int64) bool {
	return deadline > 0 && deadline <= runtimeNano()
}

//go:linkname poll_runtime_pollServerInit internal/poll.runtime_pollServerInit
func poll_runtime_pollServerInit() {}

// Complete host operations already suspend outside the executor, so a poll
// descriptor stores only close/deadline policy. No readiness thread, fd set,
// libc poller, or managed pointer is retained here.
//
//go:linkname poll_runtime_pollOpen internal/poll.runtime_pollOpen
func poll_runtime_pollOpen(fd uintptr) (uintptr, int) {
	if fd > uintptr(^uint32(0)>>1) {
		return 0, pollErrNotPollable
	}
	for index := range coroHostPollDescsV1 {
		desc := &coroHostPollDescsV1[index]
		ctx := uintptr(index + 1)
		// The scan is preemptible. Reserve the slot with one compiler-inlined
		// atomic transition before inspecting its cross-table retirement gate,
		// otherwise two runnable Gs can both observe the same inactive slot and
		// publish an identical runtimeCtx.
		if _, reserved := catomic.CompareAndExchange(&desc.active, 0, 1); !reserved {
			continue
		}
		if !llrt.CoroHostOperationControlIdleV1(ctx) {
			catomic.Store(&desc.active, 0)
			continue
		}
		desc.fd = int32(fd)
		desc.closing = false
		desc.read = 0
		desc.write = 0
		return ctx, pollNoError
	}
	return 0, pollErrNotPollable
}

//go:linkname poll_runtime_pollClose internal/poll.runtime_pollClose
func poll_runtime_pollClose(ctx uintptr) {
	desc, ok := coroHostPollDescForV1(ctx)
	if !ok || !desc.closing || !llrt.CoroHostOperationControlIdleV1(ctx) {
		// internal/poll holds its fd read/write reference from before the
		// deadline snapshot through HostOp resume and control unbind. Its final
		// destroy therefore reaches this hook only after every lane is idle.
		// Returning here would hide a violated lifetime contract as a permanent
		// fixed-table leak and could later turn into descriptor ABA.
		throw("runtime: close host coroutine polldesc without completed unblock")
		return
	}
	// Clear the payload before release-publishing inactivity. A concurrent
	// preemptible opener may reserve the slot immediately after the store.
	desc.fd = 0
	desc.closing = false
	desc.read = 0
	desc.write = 0
	catomic.Store(&desc.active, 0)
}

//go:linkname poll_runtime_pollWait internal/poll.runtime_pollWait
func poll_runtime_pollWait(ctx uintptr, mode int) int {
	desc, ok := coroHostPollDescForV1(ctx)
	if !ok {
		return pollErrNotPollable
	}
	if desc.closing {
		return pollErrClosing
	}
	deadline, valid := coroHostPollDeadlineV1(desc, mode)
	if !valid {
		return pollErrNotPollable
	}
	if coroHostPollDeadlineExpiredV1(deadline) {
		return pollErrTimeout
	}
	// Host reads/writes are complete operations and must not expose EAGAIN
	// without also providing a deadline/ready completion. Returning explicit
	// NotPollable here fails closed if an embedding violates that contract.
	return pollErrNotPollable
}

//go:linkname poll_runtime_pollWaitCanceled internal/poll.runtime_pollWaitCanceled
func poll_runtime_pollWaitCanceled(uintptr, int) {}

//go:linkname poll_runtime_pollReset internal/poll.runtime_pollReset
func poll_runtime_pollReset(ctx uintptr, mode int) int {
	desc, ok := coroHostPollDescForV1(ctx)
	if !ok {
		return pollErrNotPollable
	}
	if desc.closing {
		return pollErrClosing
	}
	deadline, valid := coroHostPollDeadlineV1(desc, mode)
	if !valid {
		return pollErrNotPollable
	}
	if coroHostPollDeadlineExpiredV1(deadline) {
		return pollErrTimeout
	}
	return pollNoError
}

func coroHostPollAbsoluteDeadlineV1(delay int64) int64 {
	if delay == 0 {
		return 0
	}
	now := runtimeNano()
	if delay < 0 {
		if now > 0 {
			return now
		}
		return 1
	}
	deadline := now + delay
	if deadline <= 0 {
		return int64(^uint64(0) >> 1)
	}
	return deadline
}

//go:linkname poll_runtime_pollSetDeadline internal/poll.runtime_pollSetDeadline
func poll_runtime_pollSetDeadline(ctx uintptr, delay int64, mode int) {
	desc, ok := coroHostPollDescForV1(ctx)
	if !ok || desc.closing {
		return
	}
	deadline := coroHostPollAbsoluteDeadlineV1(delay)
	switch mode {
	case 'r':
		desc.read = deadline
		_ = llrt.CoroHostOperationControlCancelV1(ctx, llrt.CoroHostOperationControlReadV1)
	case 'w':
		desc.write = deadline
		_ = llrt.CoroHostOperationControlCancelV1(ctx, llrt.CoroHostOperationControlWriteV1)
	case 'r' + 'w':
		desc.read, desc.write = deadline, deadline
		_ = llrt.CoroHostOperationControlCancelV1(
			ctx,
			llrt.CoroHostOperationControlReadV1|llrt.CoroHostOperationControlWriteV1,
		)
	}
}

//go:linkname poll_runtime_pollDeadline internal/poll.runtime_pollDeadline
func poll_runtime_pollDeadline(ctx uintptr, mode int) int64 {
	desc, ok := coroHostPollDescForV1(ctx)
	if !ok || desc.closing {
		return 0
	}
	deadline, _ := coroHostPollDeadlineV1(desc, mode)
	return deadline
}

func coroHostPollControlLaneV1(mode int) (uint32, bool) {
	switch mode {
	case 'r':
		return llrt.CoroHostOperationControlReadV1, true
	case 'w':
		return llrt.CoroHostOperationControlWriteV1, true
	default:
		return 0, false
	}
}

//go:linkname poll_runtime_pollDeadlineEpoch internal/poll.runtime_pollDeadlineEpoch
func poll_runtime_pollDeadlineEpoch(ctx uintptr, mode int) (int64, uintptr) {
	desc, ok := coroHostPollDescForV1(ctx)
	lane, laneOK := coroHostPollControlLaneV1(mode)
	if !ok || !laneOK {
		return 0, 0
	}
	// Snapshot the control epoch before the deadline. SetDeadline publishes its
	// scalar deadline first and advances the epoch second, so every interleaving
	// yields either the latest deadline or an epoch mismatch in the park hook.
	epoch, epochOK := llrt.CoroHostOperationControlEpochV1(ctx, lane)
	if !epochOK {
		return 0, 0
	}
	if desc.closing {
		// An operation that already passed prepare{Read,Write} before Close
		// must not bind to the post-Close epoch without being canceled.
		forcedMismatch := epoch + 1
		if forcedMismatch == 0 {
			forcedMismatch = 1
		}
		return 0, uintptr(forcedMismatch)
	}
	deadline, deadlineOK := coroHostPollDeadlineV1(desc, mode)
	if !deadlineOK {
		return 0, 0
	}
	return deadline, uintptr(epoch)
}

//go:linkname poll_runtime_pollUnblock internal/poll.runtime_pollUnblock
func poll_runtime_pollUnblock(ctx uintptr) {
	if desc, ok := coroHostPollDescForV1(ctx); ok {
		desc.closing = true
		_ = llrt.CoroHostOperationControlCancelV1(
			ctx,
			llrt.CoroHostOperationControlReadV1|llrt.CoroHostOperationControlWriteV1,
		)
	}
}

//go:linkname poll_runtime_isPollServerDescriptor internal/poll.runtime_isPollServerDescriptor
func poll_runtime_isPollServerDescriptor(uintptr) bool { return false }
