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
	// Verify all hot frame sizes map to distinct slots.
	hot := []uintptr{256, 640, 992, 2944, 8192}
	seen := make(map[uintptr]uintptr, len(hot))
	for _, b := range hot {
		idx := pFrameCacheIndex(b)
		if prev, dup := seen[idx]; dup {
			t.Fatalf("hot bucket %d collided with %d at slot %d", b, prev, idx)
		}
		seen[idx] = b
	}
	runtime.KeepAlive(buf)
	runtime.KeepAlive(buf2)
	runtime.KeepAlive(buf3)
}
