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

package coroworker

// MaxArgs is the fixed V1 scalar argument capacity. It covers the uintptr-only
// llgo.syscall families used by POSIX file and socket paths, including Go's
// RawSyscall9 dispatch shape. Wider or typed foreign signatures fail closed
// before submission.
const MaxArgs = 9

const (
	QueueTakeInvalid uint32 = iota
	QueueTakeJob
	QueueTakeStop
)

// QueueReservation is an opaque nonzero encoding of the exact monotonically
// increasing C11 ring position owned by one producer. Zero reports failed
// admission. A successful token must be consumed exactly once by
// QueueSubmitReserved or QueueCancelReservation.
type QueueReservation uintptr

// Job is the exact Go view of llgo_coro_worker_job_v1. It is copied by value
// into the native C11 ring and deliberately contains no G, coroutine handle,
// ParkState, WaitSetRecord, or typed Go pointer. SourceSlot includes the exact
// executor route, so one process-shared pool can serve multiple P domains.
// Args are opaque syscall words; the parked compiler frame owns the
// corresponding retention lifetime.
type Job struct {
	SourceSlot uint32
	Generation uint32
	Function   uintptr
	// TraceTarget is the compiler-known source C entry represented by Function.
	// Function may be a generated word-call thunk, so keeping this separate lets
	// the fault result name the original boundary without native-stack reverse
	// lookup. It is scalar metadata only and is never invoked by the worker.
	TraceTarget uintptr
	Argc        uint32
	Args        [MaxArgs]uintptr
}

const (
	FaultNone uintptr = iota
	FaultMemory
	FaultDivide
)

// Result is the pointer-free result copied into a WorkerOperationSource
// payload before publication. A fault result replaces the normal triple with
// two code identities; it never carries a signal-stack or coroutine pointer.
type Result struct {
	R1          uintptr
	R2          uintptr
	Errno       uintptr
	Fault       uintptr
	FaultPC     uintptr
	FaultTarget uintptr
}
