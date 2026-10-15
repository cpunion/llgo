package abi

import (
	"testing"
	"unsafe"
)

func TestKindUsesInternalABIWidth(t *testing.T) {
	if got, want := unsafe.Sizeof(Kind(0)), unsafe.Sizeof(uint8(0)); got != want {
		t.Fatalf("Kind width = %d, want %d", got, want)
	}
}
