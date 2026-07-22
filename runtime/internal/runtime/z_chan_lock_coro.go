//go:build (llgo && llgo_coro) || coro_channel_owner_test || coro_sema_owner_test || coro_notify_owner_test

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

package runtime

const (
	channelOwnerGateIdle uint32 = iota
	channelOwnerGateHeld
)

// channelMutex is not a general-purpose blocking mutex. Channel, select,
// timer-channel, and close mutations may execute concurrently on independent
// fleet Ps, but every critical section is a required-plain no-suspend island.
// An owner therefore releases the gate without depending on another managed
// continuation running on the same physical thread. A compact atomic spin is
// sufficient for the shared hchan metadata and keeps the primitive usable by
// native, wasm, embedded, and bare-metal profiles without pthread/libuv state.
//
// The gate must never be held across llvm.coro.suspend. Compiler-owned channel
// try/park/resume helpers are required-plain runtime islands, and the physical
// suspend is emitted by cl only after their call has returned.
type channelMutex struct {
	state uint32
}

// Init matches the only call shape used by z_chan.go without importing a
// native mutex attribute type into the coroutine runtime. Channel construction is
// owner-local and unpublished, so the zeroing store does not need an atomic.
func (m *channelMutex) Init(_ *struct{}) int32 {
	if m == nil {
		coroRuntimeAbort("initialize nil coroutine channel owner gate")
		return -1
	}
	m.state = channelOwnerGateIdle
	return 0
}

func (m *channelMutex) Lock() {
	if m == nil {
		coroRuntimeAbort("lock nil coroutine channel owner gate")
		return
	}
	for !channelMutexCompareAndSwap(&m.state, channelOwnerGateIdle, channelOwnerGateHeld) {
		// The atomic retry is deliberately the only wait operation. The holder
		// cannot suspend or call scheduler code, so progress never depends on
		// waking a coroutine parked behind this gate.
	}
}

func (m *channelMutex) Unlock() {
	if m == nil || !channelMutexCompareAndSwap(&m.state, channelOwnerGateHeld, channelOwnerGateIdle) {
		coroRuntimeAbort("unbalanced coroutine channel owner gate release")
	}
}
