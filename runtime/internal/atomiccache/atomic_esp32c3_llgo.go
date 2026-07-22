//go:build llgo && esp32c3

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

package atomiccache

import "unsafe"

// ESP32-C3 is RV32IMC and intentionally has no A extension or libatomic.
// These operations are used only by managed runtime metadata: neither the ISR
// admission path nor a raw collector callback traverses or publishes a table.
// The target's managed executor serializes each leaf below, so a coroutine may
// be preempted while scanning a snapshot but not during its compare/store.
//
// This is deliberately narrower than runtime/internal/coro's atomic surface.
// Scheduler admission can be posted by an ISR and must continue to fail closed
// until ESP32-C3 provides an IRQ-critical atomic adapter.
func loadPointer(address *unsafe.Pointer) unsafe.Pointer {
	return *address
}

func compareAndSwapPointer(address *unsafe.Pointer, old, new unsafe.Pointer) bool {
	if *address != old {
		return false
	}
	*address = new
	return true
}

func loadUint32(address *uint32) uint32 {
	return *address
}
