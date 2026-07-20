//go:build (darwin || linux) && (!llgo || !llgo_coro || !llgo_coro_native_pipe || !llgo_coro_native_timer || coro_runtime_adapter_test)

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
	"unsafe"

	psync "github.com/goplus/llgo/runtime/internal/clite/pthread/sync"
	latomic "github.com/goplus/llgo/runtime/internal/lib/sync/atomic"
)

type semaState struct {
	mu      psync.Mutex
	cond    psync.Cond
	waiters uint32
}

var semaOnce psync.Once
var semaMu psync.Mutex
var semaMap map[uintptr]*semaState

func initSemaMap() {
	semaMu.Init(nil)
	semaMap = make(map[uintptr]*semaState)
}

func getSemaState(addr *uint32) *semaState {
	semaOnce.Do(initSemaMap)
	key := uintptr(unsafe.Pointer(addr))
	semaMu.Lock()
	st := semaMap[key]
	if st == nil {
		st = &semaState{}
		st.mu.Init(nil)
		st.cond.Init(nil)
		semaMap[key] = st
	}
	semaMu.Unlock()
	return st
}

func semaAcquire(addr *uint32) {
	for {
		value := latomic.LoadUint32(addr)
		if value != 0 && latomic.CompareAndSwapUint32(addr, value, value-1) {
			return
		}
		state := getSemaState(addr)
		state.mu.Lock()
		for {
			value = latomic.LoadUint32(addr)
			if value != 0 && latomic.CompareAndSwapUint32(addr, value, value-1) {
				state.mu.Unlock()
				return
			}
			state.waiters++
			state.cond.Wait(&state.mu)
			state.waiters--
		}
	}
}

func semaRelease(addr *uint32) {
	latomic.AddUint32(addr, 1)
	state := getSemaState(addr)
	state.mu.Lock()
	if state.waiters != 0 {
		state.cond.Signal()
	}
	state.mu.Unlock()
}
