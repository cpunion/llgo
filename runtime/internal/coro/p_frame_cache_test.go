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

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestPFrameCachePutZeroesUsedAndRoundTrips(t *testing.T) {
	p := new(P)
	g := new(G)
	if !InitG(g) {
		t.Fatal("InitG")
	}
	// Newly spawned G (runP == nil) must never touch the P frame cache.
	buf := make([]byte, 8192)
	for i := range buf {
		buf[i] = 0x5a
	}
	if PutCachedFrame(g, unsafe.Pointer(&buf[0]), 8192, 4247) {
		t.Fatal("PutCachedFrame succeeded with nil runP")
	}
	if TakeCachedFrame(g, 8192) != nil {
		t.Fatal("TakeCachedFrame succeeded with nil runP")
	}

	g.runP = p
	const used = 4247
	// Simulate all-zero untouched tail [used, 8192).
	for i := used; i < len(buf); i++ {
		buf[i] = 0
	}
	stored := PutCachedFrame(g, unsafe.Pointer(&buf[0]), 8192, used)
	if AuditEnabled {
		if stored {
			t.Fatal("AuditEnabled unexpectedly cached frame on P")
		}
		runtime.KeepAlive(buf)
		return
	}
	if !stored {
		t.Fatal("PutCachedFrame failed for empty slot")
	}
	// Second frame of same 8192B bucket fits (cap=2), third declines without mutation.
	buf2 := make([]byte, 8192)
	for i := 0; i < used; i++ {
		buf2[i] = 0xaa
	}
	if !PutCachedFrame(g, unsafe.Pointer(&buf2[0]), 8192, used) {
		t.Fatal("PutCachedFrame failed for second 8192B frame (cap=2)")
	}
	buf3 := make([]byte, 8192)
	buf3[0] = 0xbb
	if PutCachedFrame(g, unsafe.Pointer(&buf3[0]), 8192, used) {
		t.Fatal("PutCachedFrame exceeded cap=2 for 8192B bucket")
	}
	if buf3[0] != 0xbb {
		t.Fatal("declined PutCachedFrame unexpectedly cleared caller buffer")
	}
	// Oversized buckets (> 8192) must decline.
	if PutCachedFrame(g, unsafe.Pointer(&buf3[0]), 16384, 8192) {
		t.Fatal("PutCachedFrame accepted oversized bucket")
	}
	// Verify LIFO pop order and complete zero invariant for both cached frames.
	got2 := TakeCachedFrame(g, 8192)
	if got2 != unsafe.Pointer(&buf2[0]) {
		t.Fatalf("first TakeCachedFrame = %p, want %p", got2, unsafe.Pointer(&buf2[0]))
	}
	for i, b := range buf2 {
		if b != 0 {
			t.Fatalf("cached frame2 byte %d = %#x, want 0", i, b)
		}
	}
	got := TakeCachedFrame(g, 8192)
	if got != unsafe.Pointer(&buf[0]) {
		t.Fatalf("second TakeCachedFrame = %p, want %p", got, unsafe.Pointer(&buf[0]))
	}
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("cached frame byte %d = %#x, want 0", i, b)
		}
	}
	if TakeCachedFrame(g, 8192) != nil {
		t.Fatal("third TakeCachedFrame did not miss after draining bin")
	}
	// Verify every canonical cacheable size class <= 8192B maps to a distinct
	// collision-free slot in [0, pFrameCacheSlots).
	seen := make(map[int]uintptr, pFrameCacheSlots)
	for b := uintptr(256); b <= 1024; b += 32 {
		idx := pFrameCacheIndex(b)
		if idx < 0 || idx >= pFrameCacheSlots {
			t.Fatalf("bucket %d mapped to invalid slot %d", b, idx)
		}
		if prev, dup := seen[idx]; dup {
			t.Fatalf("bucket %d collided with %d at slot %d", b, prev, idx)
		}
		seen[idx] = b
	}
	for b := uintptr(1152); b <= 4096; b += 128 {
		idx := pFrameCacheIndex(b)
		if idx < 0 || idx >= pFrameCacheSlots {
			t.Fatalf("bucket %d mapped to invalid slot %d", b, idx)
		}
		if prev, dup := seen[idx]; dup {
			t.Fatalf("bucket %d collided with %d at slot %d", b, prev, idx)
		}
		seen[idx] = b
	}
	if idx := pFrameCacheIndex(8192); idx != 49 {
		t.Fatalf("bucket 8192 mapped to slot %d, want 49", idx)
	} else if prev, dup := seen[idx]; dup {
		t.Fatalf("bucket 8192 collided with %d at slot %d", prev, idx)
	} else {
		seen[idx] = 8192
	}
	if len(seen) != pFrameCacheSlots {
		t.Fatalf("covered %d slots, want %d", len(seen), pFrameCacheSlots)
	}
	for _, invalid := range []uintptr{0, 64, 255, 272, 1088, 5000, 16384} {
		if idx := pFrameCacheIndex(invalid); idx != -1 {
			t.Fatalf("invalid bucket %d mapped to slot %d, want -1", invalid, idx)
		}
	}
	runtime.KeepAlive(buf)
	runtime.KeepAlive(buf2)
	runtime.KeepAlive(buf3)
}
