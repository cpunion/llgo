//go:build (darwin || linux) && !baremetal && !wasm && !tinygo.wasm

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

package coro

import "unsafe"

// Exact direct-indexed bins matching coroalloc.nativeCacheAllocationSize for
// every cacheable bucket <= 8192B:
//   index 0:      256B
//   indices 1..24: 288B..1024B (step 32B)
//   indices 25..48: 1152B..4096B (step 128B)
//   index 49:     8192B
// Eliminating hash collisions avoids spilling hot frames into the C TLS cache.
const (
	pFrameCacheSlots      = 50
	pFrameCacheMaxBucket  = 8192
	pFrameCacheSmallMax   = 1024
	pFrameCacheMedMax     = 4096
	pFrameCacheSmallDepth = 16
	pFrameCacheMedDepth   = 4
	pFrameCacheLargeDepth = 2
)

type pCachedFrameBin struct {
	head  unsafe.Pointer
	count uint32
}

type pFrameCache struct {
	slots [pFrameCacheSlots]pCachedFrameBin
}

func pFrameCacheIndex(bucket uintptr) int {
	if bucket <= pFrameCacheSmallMax {
		if bucket < 256 || bucket&31 != 0 {
			return -1
		}
		return int((bucket - 256) >> 5)
	}
	if bucket <= pFrameCacheMedMax {
		if bucket&127 != 0 {
			return -1
		}
		return 24 + int((bucket-pFrameCacheSmallMax)>>7)
	}
	if bucket == pFrameCacheMaxBucket {
		return 49
	}
	return -1
}

func pFrameCacheCap(bucket uintptr) uint32 {
	if bucket <= pFrameCacheSmallMax {
		return pFrameCacheSmallDepth
	}
	if bucket <= pFrameCacheMedMax {
		return pFrameCacheMedDepth
	}
	return pFrameCacheLargeDepth
}

// TakeCachedFrame pops one pre-zeroed coroutine frame matching bucket from g's
// exclusively owned running P without crossing into C or TLS lookup.
// Newly spawned tasks (g.runP == nil) and AuditEnabled builds always miss.
func TakeCachedFrame(g *G, bucket uintptr) unsafe.Pointer {
	if AuditEnabled || g == nil {
		return nil
	}
	p := g.runP
	if p == nil {
		return nil
	}
	idx := pFrameCacheIndex(bucket)
	if idx < 0 {
		return nil
	}
	slot := &p.frameCache.slots[idx]
	ptr := slot.head
	if ptr != nil {
		next := *(*unsafe.Pointer)(ptr)
		*(*unsafe.Pointer)(ptr) = nil
		slot.head = next
		slot.count--
		return ptr
	}
	return nil
}

// PutCachedFrame clears [0, usedSize) and pushes raw onto the bounded LIFO
// list for bucket in g's exclusively owned running P. Returning false leaves
// raw untouched so the caller hands it to coroalloc without double-zeroing.
func PutCachedFrame(g *G, raw unsafe.Pointer, bucket, usedSize uintptr) bool {
	if AuditEnabled || g == nil || raw == nil || usedSize == 0 || usedSize > bucket {
		return false
	}
	p := g.runP
	if p == nil {
		return false
	}
	idx := pFrameCacheIndex(bucket)
	if idx < 0 {
		return false
	}
	slot := &p.frameCache.slots[idx]
	if slot.count >= pFrameCacheCap(bucket) {
		return false
	}
	Zero(raw, usedSize)
	*(*unsafe.Pointer)(raw) = slot.head
	slot.head = raw
	slot.count++
	return true
}
