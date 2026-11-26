//go:build !nogc && !baremetal && !wasm && !tinygo.wasm

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

package coroalloc

import "unsafe"

// gcFreeBatcher serializes concurrent overflow GC_free calls onto a single
// non-blocking drainer so multi-P teardown bursts (e.g. releasing 10k parked
// goroutines across 4 Ps) do not contend on BDWGC's single global pthread
// mutex (GC_allocate_ml). Uncontended frees execute immediately without
// queueing; contended callers push onto an intrusive lock-free stack and
// return immediately while the active drainer batches every queued GC_free
// sequentially with zero kernel mutex sleeps and zero retained RSS.
type gcFreeBatcher struct {
	drainLock   uint32
	_pad0       [124]byte
	pendingHead unsafe.Pointer
	_pad1       [120]byte
}

var defaultGCFreeBatcher gcFreeBatcher

func (b *gcFreeBatcher) tryAcquireDrain() bool {
	return freeBatchLoad(&b.drainLock) == 0 && freeBatchCAS(&b.drainLock, 0, 1)
}

func (b *gcFreeBatcher) releaseDrain() bool {
	freeBatchStore(&b.drainLock, 0)
	// Close the push-vs-unlock window: if a producer pushed after our last
	// empty swap and before we cleared drainLock, re-acquire and continue.
	return freeBatchLoadPtr(&b.pendingHead) != nil && freeBatchCAS(&b.drainLock, 0, 1)
}

func (b *gcFreeBatcher) pushPending(ptr unsafe.Pointer) {
	for {
		old := freeBatchLoadPtr(&b.pendingHead)
		*(*unsafe.Pointer)(ptr) = old
		if freeBatchCASPtr(&b.pendingHead, old, ptr) {
			return
		}
	}
}

func (b *gcFreeBatcher) takeBatch() unsafe.Pointer {
	if freeBatchLoadPtr(&b.pendingHead) == nil {
		return nil
	}
	return freeBatchSwapPtr(&b.pendingHead, nil)
}
