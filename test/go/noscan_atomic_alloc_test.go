// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

type noscanPOD struct {
	a int64
	b [32]byte
	c uint32
	d float64
}

type tracedNode struct {
	id   int
	name string
	next *tracedNode
}

//go:noinline
func allocNoscanPOD() *noscanPOD {
	return new(noscanPOD)
}

func TestNoscanAtomicAllocZeroInitAndAppend(t *testing.T) {
	// Dirty heap first so reused blocks would expose any missing zeroing.
	for round := 0; round < 32; round++ {
		dirty := make([]byte, 4096)
		for i := range dirty {
			dirty[i] = 0xA5
		}
		dirty64 := make([]int64, 512)
		for i := range dirty64 {
			dirty64[i] = -1
		}
		p := allocNoscanPOD()
		p.a = 0x12345678
		for i := range p.b {
			p.b[i] = 0xFF
		}
		p.c = 0xDEADBEEF
		p.d = 3.14159
	}
	runtime.GC()

	// Verify zero initialization across sizes for AllocAtomicZ, MakeSliceAtomic,
	// and SliceAppendAtomic (including unused capacity tail after growth).
	for _, n := range []int{1, 7, 16, 63, 256, 1024, 4096, 16384} {
		bs := make([]byte, n)
		for i, v := range bs {
			if v != 0 {
				t.Fatalf("make([]byte, %d)[%d] = %#x, want 0", n, i, v)
			}
		}
		is := make([]int64, n)
		for i, v := range is {
			if v != 0 {
				t.Fatalf("make([]int64, %d)[%d] = %d, want 0", n, i, v)
			}
		}
		p := allocNoscanPOD()
		if p.a != 0 || p.c != 0 || p.d != 0 {
			t.Fatalf("new(noscanPOD) non-zero scalar fields: %+v", *p)
		}
		for i, v := range p.b {
			if v != 0 {
				t.Fatalf("new(noscanPOD).b[%d] = %#x, want 0", i, v)
			}
		}
	}

	// Verify append growth preserves existing elements and zeroes the new tail.
	var grown []byte
	for i := 0; i < 2048; i++ {
		grown = append(grown, byte(i&0xFF))
	}
	for i := 0; i < 2048; i++ {
		if grown[i] != byte(i&0xFF) {
			t.Fatalf("grown[%d] = %d, want %d", i, grown[i], byte(i&0xFF))
		}
	}
	full := grown[:cap(grown)]
	for i := len(grown); i < len(full); i++ {
		if full[i] != 0 {
			t.Fatalf("grown tail[%d] = %#x, want 0", i, full[i])
		}
	}

	// Verify string allocations (AllocAtomicU) survive GC and match content.
	parts := make([]string, 0, 128)
	for i := 0; i < 128; i++ {
		s := fmt.Sprintf("key-%04d", i) + ":" + strings.Repeat("x", (i%32)+1)
		parts = append(parts, s)
	}
	runtime.GC()
	for i, s := range parts {
		want := fmt.Sprintf("key-%04d", i) + ":" + strings.Repeat("x", (i%32)+1)
		if s != want {
			t.Fatalf("string[%d] = %q, want %q", i, s, want)
		}
	}
}

func TestPointerBearingAllocationsRetainedAcrossGC(t *testing.T) {
	// Pointer-bearing slices and structs must remain GC-scanned.
	var nodes []*tracedNode
	for i := 0; i < 512; i++ {
		child := &tracedNode{id: i * 2, name: fmt.Sprintf("child-%d", i)}
		parent := &tracedNode{id: i*2 + 1, name: fmt.Sprintf("parent-%d", i), next: child}
		nodes = append(nodes, parent)
	}
	for round := 0; round < 5; round++ {
		junk := make([]byte, 64*1024)
		for i := range junk {
			junk[i] = byte(round + i)
		}
		runtime.GC()
	}
	for i, n := range nodes {
		if n == nil || n.id != i*2+1 || n.name != fmt.Sprintf("parent-%d", i) {
			t.Fatalf("parent[%d] corrupted after GC: %+v", i, n)
		}
		if n.next == nil || n.next.id != i*2 || n.next.name != fmt.Sprintf("child-%d", i) {
			t.Fatalf("child[%d] corrupted after GC: %+v", i, n.next)
		}
	}
}
