package maphash

import (
	"internal/abi"
	"internal/runtime/maps"
	"unsafe"
)

// The standard implementation reads gc's map Hasher function value. LLGo
// owns its type metadata and uses the same static type hash implementation
// as ordinary maps, without depending on that private callback representation.
//llgo:skip comparableHash

//go:linkname llgoTypeHash github.com/xgo-dev/llgo/runtime/internal/runtime.typehashImpl
func llgoTypeHash(typ *abi.Type, value unsafe.Pointer, seed uintptr) uintptr

func comparableHash[T comparable](value T, seed Seed) uint64 {
	typ := abi.TypeFor[T]()
	pointer := abi.NoEscape(unsafe.Pointer(&value))
	if maps.Use64BitHash {
		return uint64(llgoTypeHash(typ, pointer, uintptr(seed.s)))
	}
	lo := llgoTypeHash(typ, pointer, uintptr(uint32(seed.s)))
	hi := llgoTypeHash(typ, pointer, uintptr(seed.s>>32))
	return uint64(hi)<<32 | uint64(lo)
}
