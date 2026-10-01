//go:build amd64 || arm64

package websocket_test

import (
	"bytes"
	"math/bits"
	"testing"
	_ "unsafe"
)

// In the exact issue version (v1.8.15), public mask() deliberately calls
// maskGo. This separate, explicitly private-symbol test proves maskAsm's
// execution without editing the library or claiming a public assembly path.
//
//go:linkname maskAssembly github.com/coder/websocket.maskAsm
func maskAssembly(data *byte, size int, key uint32) uint32

func TestDisabledAssemblyKernelDirect(t *testing.T) {
	for _, size := range []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 4097} {
		for offset := 1; offset <= 32; offset++ {
			for _, key := range []uint32{0, 0x12345678, 0xffffffff} {
				storage := bytes.Repeat([]byte{0xa5}, size+offset+1)
				want := bytes.Clone(storage)
				for i := 0; i < size; i++ {
					storage[offset+i] = byte(i*131 + 17)
					want[offset+i] = storage[offset+i] ^ byte(key>>uint((i%4)*8))
				}
				got := maskAssembly(&storage[offset], size, key)
				wantKey := bits.RotateLeft32(key, -8*(size%4))
				if got != wantKey || !bytes.Equal(storage, want) {
					t.Fatalf("private maskAsm length %d offset %d key %08x: result %08x, want %08x, payload match %t", size, offset, key, got, wantKey, bytes.Equal(storage, want))
				}
			}
		}
	}
}
