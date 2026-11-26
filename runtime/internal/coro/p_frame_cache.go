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

const (
	pFrameCacheSlots         = 16
	pFrameCacheMaxBucket     = 8192
	pFrameCacheSmallMax      = 1024
	pFrameCacheSmallDepth    = 8
	pFrameCacheLargeDepth    = 2
)

type pCachedFrameBin struct {
	head   unsafe.Pointer
	bucket uintptr
	count  uint32
}

type pFrameCache struct {
	slots [pFrameCacheSlots]pCachedFrameBin
}

func pFrameCacheIndex(bucket uintptr) uintptr {
	return ((bucket >> 5) ^ (bucket >> 8) ^ (bucket >> 12)) & (pFrameCacheSlots - 1)
}

func pFrameCacheCap(bucket uintptr) uint32 {
	if bucket <= pFrameCacheSmallMax {
		return pFrameCacheSmallDepth
	}
	return pFrameCacheLargeDepth
}

// TakeCachedFrame pops one pre-zeroed coroutine frame matching bucket from g's
// exclusively owned running P without crossing into C or TLS lookup.
// Newly spawned tasks (g.runP == nil) and AuditEnabled builds always miss.
func TakeCachedFrame(g *G, bucket uintptr) unsafe.Pointer {
	if AuditEnabled || g == nil || bucket == 0 || bucket > pFrameCacheMaxBucket {
		return nil
	}
	p := g.runP
	if p == nil {
		return nil
	}
	slot := &p.frameCache.slots[pFrameCacheIndex(bucket)]
	if slot.bucket == bucket && slot.head != nil {
		ptr := slot.head
		next := *(*unsafe.Pointer)(ptr)
		*(*unsafe.Pointer)(ptr) = nil
		slot.head = next
		slot.count--
		if next == nil {
			slot.bucket = 0
		}
		return ptr
	}
	return nil
}

// PutCachedFrame clears [0, usedSize) and pushes raw onto the bounded LIFO
// list for bucket in g's exclusively owned running P. Returning false leaves
// raw untouched so the caller hands it to coroalloc without double-zeroing.
func PutCachedFrame(g *G, raw unsafe.Pointer, bucket, usedSize uintptr) bool {
	if AuditEnabled || g == nil || raw == nil ||
		bucket == 0 || bucket > pFrameCacheMaxBucket ||
		usedSize == 0 || usedSize > bucket {
		return false
	}
	p := g.runP
	if p == nil {
		return false
	}
	slot := &p.frameCache.slots[pFrameCacheIndex(bucket)]
	if slot.head != nil && (slot.bucket != bucket || slot.count >= pFrameCacheCap(bucket)) {
		return false
	}
	Zero(raw, usedSize)
	*(*unsafe.Pointer)(raw) = slot.head
	slot.head = raw
	slot.bucket = bucket
	slot.count++
	return true
}
