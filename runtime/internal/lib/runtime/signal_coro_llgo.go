//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

import (
	_ "unsafe"

	latomic "github.com/goplus/llgo/runtime/internal/lib/sync/atomic"
)

const coroSignalNoneV1 = ^uint32(0)

const (
	coroSignalInitUninitializedV1 uint32 = iota
	coroSignalInitReadyV1
	coroSignalInitBusyV1
)

var (
	coroSignalInitStateV1   uint32
	coroSignalPollContextV1 uintptr
)

// The C side owns all state touched by the POSIX signal handler. Every entry
// below is bounded and non-blocking; in particular, none can enter Go while a
// signal handler is running.

//llgo:coro noblock
//go:linkname coroSignalInitNativeV1 C.__llgo_runtime_signal_init_v1
func coroSignalInitNativeV1() int32

//llgo:coro noblock
//go:linkname coroSignalEnableNativeV1 C.__llgo_runtime_signal_enable_v1
func coroSignalEnableNativeV1(sig uint32)

//llgo:coro noblock
//go:linkname coroSignalDisableNativeV1 C.__llgo_runtime_signal_disable_v1
func coroSignalDisableNativeV1(sig uint32)

//llgo:coro noblock
//go:linkname coroSignalIgnoreNativeV1 C.__llgo_runtime_signal_ignore_v1
func coroSignalIgnoreNativeV1(sig uint32)

//llgo:coro noblock
//go:linkname coroSignalIgnoredNativeV1 C.__llgo_runtime_signal_ignored_v1
func coroSignalIgnoredNativeV1(sig uint32) uint32

//llgo:coro noblock
//go:linkname coroSignalReceiveNativeV1 C.__llgo_runtime_signal_receive_v1
func coroSignalReceiveNativeV1() uint32

//llgo:coro noblock
//go:linkname coroSignalGenerationNativeV1 C.__llgo_runtime_signal_generation_v1
func coroSignalGenerationNativeV1() uint32

//llgo:coro noblock
//go:linkname coroSignalIdleNativeV1 C.__llgo_runtime_signal_idle_v1
func coroSignalIdleNativeV1(target uint32) uint32

func ensureCoroSignalInitV1() {
	for {
		switch latomic.LoadUint32(&coroSignalInitStateV1) {
		case coroSignalInitReadyV1:
			return
		case coroSignalInitUninitializedV1:
			if latomic.CompareAndSwapUint32(
				&coroSignalInitStateV1,
				coroSignalInitUninitializedV1,
				coroSignalInitBusyV1,
			) {
				fd := coroSignalInitNativeV1()
				if fd < 0 {
					throw("runtime: cannot initialize coroutine signal pipe")
					return
				}
				ctx, errno := poll_runtime_pollOpen(uintptr(fd))
				if ctx == 0 || errno != 0 {
					throw("runtime: cannot register coroutine signal pipe")
					return
				}
				coroSignalPollContextV1 = ctx
				latomic.StoreUint32(&coroSignalInitStateV1, coroSignalInitReadyV1)
				return
			}
		}
		coroSchedulerYield()
	}
}

// signal_enable enables Go notification for sig. The C adapter saves and
// restores the pre-existing sigaction, including llgo's fault action when an
// application explicitly asks os/signal to intercept a fault signal.
func signal_enable(sig uint32) {
	ensureCoroSignalInitV1()
	coroSignalEnableNativeV1(sig)
}

// signal_disable restores the disposition which preceded signal_enable or
// signal_ignore. A handler already in flight is accounted for by the
// generation/acknowledgement protocol used by signalWaitUntilIdle.
func signal_disable(sig uint32) {
	ensureCoroSignalInitV1()
	coroSignalDisableNativeV1(sig)
}

func signal_ignore(sig uint32) {
	ensureCoroSignalInitV1()
	coroSignalIgnoreNativeV1(sig)
}

func signal_ignored(sig uint32) bool {
	ensureCoroSignalInitV1()
	return coroSignalIgnoredNativeV1(sig) != 0
}

// signal_recv keeps the synchronous runtime/os-signal ABI. Empty-pipe waits
// use the same coroutine poll owner as internal/poll, so the executor remains
// available to run other goroutines while this receiver is blocked.
func signal_recv() uint32 {
	ensureCoroSignalInitV1()
	for {
		if sig := coroSignalReceiveNativeV1(); sig != coroSignalNoneV1 {
			return sig
		}
		if poll_runtime_pollWait(coroSignalPollContextV1, 'r') != pollNoError {
			throw("runtime: coroutine signal pipe poll failed")
			return 0
		}
	}
}

// signalWaitUntilIdle waits for all publications visible at entry to be
// acknowledged and, crucially, for signal_recv to re-enter its empty receive
// phase. os/signal calls process between two receive calls; requiring that
// later empty phase means acknowledgement cannot race ahead of channel
// delivery. The modular generation comparison is implemented in C.
func signalWaitUntilIdle() {
	ensureCoroSignalInitV1()
	target := coroSignalGenerationNativeV1()
	for coroSignalIdleNativeV1(target) == 0 {
		coroSchedulerYield()
	}
}
