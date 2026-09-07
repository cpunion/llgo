//go:build !baremetal && wasm

package runtime

import "sync/atomic"

// Reuse the standard library's compiler-recognized align64 marker rather than
// placing an atomically accessed bare uint64 in a four-byte-aligned wasm32
// aggregate. Other targets retain their intrinsic-only counter implementation.
type memProfileCounter = atomic.Uint64

func memProfileAddObject(p *memProfileCounter) {
	p.Add(1)
}

func memProfileLoadObjects(p *memProfileCounter) uint64 {
	return p.Load()
}
