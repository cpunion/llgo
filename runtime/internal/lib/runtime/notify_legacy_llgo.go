//go:build (darwin || linux) && (!llgo || !llgo_coro || coro_runtime_adapter_test || (!(llgo_coro_native_pipe && llgo_coro_native_timer) && !tinygo.wasm && !baremetal && !llgo_coro_host))

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

type notifyState struct {
	mu   psync.Mutex
	cond psync.Cond
}

var notifyOnce psync.Once
var notifyMu psync.Mutex
var notifyMap map[uintptr]*notifyState

func initNotifyMap() {
	notifyMu.Init(nil)
	notifyMap = make(map[uintptr]*notifyState)
}

func getNotifyState(l *notifyList) *notifyState {
	notifyOnce.Do(initNotifyMap)
	key := uintptr(unsafe.Pointer(l))
	notifyMu.Lock()
	st := notifyMap[key]
	if st == nil {
		st = &notifyState{}
		st.mu.Init(nil)
		st.cond.Init(nil)
		notifyMap[key] = st
	}
	notifyMu.Unlock()
	return st
}

//go:linkname sync_runtime_notifyListWait sync.runtime_notifyListWait
func sync_runtime_notifyListWait(l *notifyList, t uint32) {
	st := getNotifyState(l)
	st.mu.Lock()
	for !notifyListTicketLess(t, latomic.LoadUint32(&l.notify)) {
		st.cond.Wait(&st.mu)
	}
	st.mu.Unlock()
}

//go:linkname sync_runtime_notifyListNotifyAll sync.runtime_notifyListNotifyAll
func sync_runtime_notifyListNotifyAll(l *notifyList) {
	st := getNotifyState(l)
	st.mu.Lock()
	latomic.StoreUint32(&l.notify, latomic.LoadUint32(&l.wait))
	st.cond.Broadcast()
	st.mu.Unlock()
}

//go:linkname sync_runtime_notifyListNotifyOne sync.runtime_notifyListNotifyOne
func sync_runtime_notifyListNotifyOne(l *notifyList) {
	st := getNotifyState(l)
	st.mu.Lock()
	if latomic.LoadUint32(&l.notify) != latomic.LoadUint32(&l.wait) {
		latomic.AddUint32(&l.notify, 1)
		st.cond.Signal()
	}
	st.mu.Unlock()
}
