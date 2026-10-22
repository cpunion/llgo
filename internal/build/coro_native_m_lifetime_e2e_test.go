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

package build

import (
	"strings"
	"testing"
	"time"
)

const coroNativeMLifetimeDeclarations = `package main
import _ "unsafe"
var Failed uint32
//go:linkname osThreadLock llgo.coroOSThreadLock
func osThreadLock()
//go:linkname osThreadUnlock llgo.coroOSThreadUnlock
func osThreadUnlock()
//llgo:coro noblock
//go:linkname resetState C.__llgo_coro_native_fleet_e2e_block_reset_v1
func resetState()
//llgo:coro noblock
//go:linkname isWaiting C.__llgo_coro_native_fleet_e2e_blocked_v1
func isWaiting() uintptr
//llgo:coro noblock
//go:linkname unblock C.__llgo_coro_native_fleet_e2e_release_v1
func unblock()
//llgo:coro worker
//go:linkname block C.__llgo_coro_native_fleet_e2e_block_v1
func block()
//llgo:coro noblock
//go:linkname armExit C.__llgo_coro_native_fleet_e2e_arm_thread_exit_v1
func armExit() uintptr
//llgo:coro noblock
//go:linkname exitCount C.__llgo_coro_native_fleet_e2e_thread_exit_count_v1
func exitCount() uintptr
//llgo:coro noblock
//go:linkname witness C.__llgo_coro_native_fleet_e2e_rotation_witness_v1
func witness(uint32)
func Check() int32 { return int32(Failed) }
`

func TestCoroNativeFleetLockedExitAfterParentReturnE2E(t *testing.T) {
	const source = coroNativeMLifetimeDeclarations + `
//llgo:coro noblock
//go:linkname waitParentReturn command-line-arguments.CoroRotationWaitParentReturnV1
func waitParentReturn() bool
func child() {
 for isWaiting() == 0 {}
 osThreadLock()
 if armExit() != 1 { witness(241) }
 unblock()
 if !waitParentReturn() { witness(242) }
 // The original M has requested this exact temporary owner back. Returning
 // with an unbalanced lock must destroy its pthread rather than cache it.
}
func Setup() { Failed = 0 }
func main() {
 resetState()
 go child()
 osThreadLock()
 block()
 osThreadUnlock()
 for exitCount() < 1 {}
 witness(Failed)
}
`
	output := runCoroNativeFleetE2E(t, source, "locked-exit-after-parent-return", true, 1)
	if !strings.Contains(output, "ROTATION CROSS BINDING WITNESS PASS") {
		t.Fatalf("missing in-main pthread retirement witness: %s", output)
	}
}

func TestCoroNativeFleetLockedSuccessorLifetimeCapacityE2E(t *testing.T) {
	const source = coroNativeMLifetimeDeclarations + `
const Count = 12000
var Ack chan uint32
func leaf() {
 osThreadLock()
 if armExit() != 1 { witness(251) }
 Ack <- 1
}
func child() {
 for isWaiting() == 0 {}
 for i := 0; i < Count; i++ { go leaf(); <-Ack }
 unblock()
}
func Setup() { Failed = 0; Ack = make(chan uint32) }
func main() {
 resetState()
 go child()
 osThreadLock()
 block()
 osThreadUnlock()
 for exitCount() < Count {}
 witness(Failed)
}
`
	output := runCoroNativeFleetE2EWithTimeout(t, source, "locked-successor-lifetime-capacity", true, 1, 90*time.Second)
	if !strings.Contains(output, "ROTATION CROSS BINDING WITNESS PASS") {
		t.Fatalf("missing in-main 12000 pthread retirement witness: %s", output)
	}
}
