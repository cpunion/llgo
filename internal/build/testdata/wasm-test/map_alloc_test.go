//go:build llgo && llgo.wasm.gc.linear

package wasmtest

import (
	"runtime"
	"testing"
)

func TestMapKeyHashNoAlloc(t *testing.T) {
	// Multiple buckets force hashing. High bits exercise the full uint64 key
	// on both memory32 and memory64; single-bucket lookups can bypass hashing.
	m := make(map[uint64]uint64, 64)
	for i := uint64(0); i < 64; i++ {
		key := i | 1<<40
		m[key] = key
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := uint64(0); i < 4096; i++ {
		key := i%64 | 1<<40
		if m[key] != key {
			t.Fatal("single-result lookup returned the wrong value")
		}
		value, ok := m[key]
		if !ok || value != key {
			t.Fatal("comma-ok lookup returned the wrong value")
		}
		m[key] = value
		delete(m, key)
		m[key] = key
	}
	runtime.ReadMemStats(&after)
	if after.Mallocs != before.Mallocs {
		t.Fatalf("integer map hashing allocated %d objects", after.Mallocs-before.Mallocs)
	}
}
