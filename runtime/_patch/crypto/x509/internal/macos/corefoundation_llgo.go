//go:build darwin

package macos

import (
	"bytes"
	"unsafe"
)

// CFData owns foreign memory. Preserve the C pointer type at this boundary
// instead of reconstructing a pointer from an untyped syscall result.
//llgo:skip CFDataToSlice

//go:linkname llgoCFDataGetLength C.CFDataGetLength
func llgoCFDataGetLength(data CFRef) int

//go:linkname llgoCFDataGetBytePtr C.CFDataGetBytePtr
func llgoCFDataGetBytePtr(data CFRef) *byte

func CFDataToSlice(data CFRef) []byte {
	length := llgoCFDataGetLength(data)
	ptr := llgoCFDataGetBytePtr(data)
	return bytes.Clone(unsafe.Slice(ptr, length))
}
