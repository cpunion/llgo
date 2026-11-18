package gcroot

import (
	"testing"
	"unsafe"
)

func TestRootValueTracksStackFieldMutation(t *testing.T) {
	field := uint64(0x12345678)
	address := unsafe.Pointer(&field)
	if got := RootValue(&address, 8); got != uintptr(field) {
		t.Fatal(got)
	}
	field = 0x76543210
	if got := RootValue(&address, 8); got != uintptr(field) {
		t.Fatal("stale field", got)
	}
	// The neighboring word is deliberately non-zero. A Go-word read would
	// interpret it as the high half of the physical C pointer.
	words := [2]uint32{0x12345678, 0xffffffff}
	address = unsafe.Pointer(&words[0])
	if got := RootValue(&address, 4); got != uintptr(words[0]) {
		t.Fatalf("C pointer overread: %#x", got)
	}
	if got := RootValue(&address, 0); got != uintptr(address) {
		t.Fatal("direct root changed", got)
	}
	assertPanics(t, func() { RootValue(&address, 3) })
}
