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

import (
	"testing"
	"unsafe"
)

func TestJobMatchesCWorkerQueuePODLayout(t *testing.T) {
	pointer := unsafe.Sizeof(uintptr(0))
	wantArgs := uintptr(8) + 2*pointer + uintptr(4)
	if remainder := wantArgs % uintptr(unsafe.Alignof(uintptr(0))); remainder != 0 {
		wantArgs += uintptr(unsafe.Alignof(uintptr(0))) - remainder
	}
	wantSize := wantArgs + uintptr(MaxArgs)*pointer
	if remainder := wantSize % uintptr(unsafe.Alignof(uintptr(0))); remainder != 0 {
		wantSize += uintptr(unsafe.Alignof(uintptr(0))) - remainder
	}
	if unsafe.Offsetof(Job{}.SourceSlot) != 0 ||
		unsafe.Offsetof(Job{}.Generation) != 4 ||
		unsafe.Offsetof(Job{}.Function) != 8 ||
		unsafe.Offsetof(Job{}.TraceTarget) != uintptr(8)+pointer ||
		unsafe.Offsetof(Job{}.Argc) != uintptr(8)+2*pointer ||
		unsafe.Offsetof(Job{}.Args) != wantArgs ||
		unsafe.Sizeof(Job{}) != wantSize {
		t.Fatalf("Job C ABI layout = size %d source %d generation %d function %d trace %d argc %d args %d",
			unsafe.Sizeof(Job{}), unsafe.Offsetof(Job{}.SourceSlot),
			unsafe.Offsetof(Job{}.Generation), unsafe.Offsetof(Job{}.Function),
			unsafe.Offsetof(Job{}.TraceTarget),
			unsafe.Offsetof(Job{}.Argc), unsafe.Offsetof(Job{}.Args))
	}
}

func TestQueueConstantsMatchCHeaderContract(t *testing.T) {
	if QueueCapacity != 1024 || QueueTakeInvalid != 0 || QueueTakeJob != 1 || QueueTakeStop != 2 {
		t.Fatalf("worker queue constants = capacity %d statuses %d/%d/%d",
			QueueCapacity, QueueTakeInvalid, QueueTakeJob, QueueTakeStop)
	}
}
