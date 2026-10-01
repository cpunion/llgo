package modernc_test

import (
	"testing"
	"unsafe"

	"modernc.org/libc"
)

func TestABI0Uint128Wrapper(t *testing.T) {
	tls := libc.NewTLS()
	defer tls.Close()
	for _, vector := range []struct {
		a, b, want libc.Uint128
		overflow   int32
	}{
		{libc.Uint128{Lo: 7}, libc.Uint128{Lo: 9}, libc.Uint128{Lo: 63}, 0},
		{libc.Uint128{Lo: 3, Hi: 2}, libc.Uint128{Lo: 5}, libc.Uint128{Lo: 15, Hi: 10}, 0},
		{libc.Uint128{Lo: ^uint64(0)}, libc.Uint128{Lo: 2}, libc.Uint128{Lo: ^uint64(0) - 1, Hi: 1}, 0},
		{libc.Uint128{Hi: 1 << 63}, libc.Uint128{Lo: 2}, libc.Uint128{}, 1},
	} {
		var result libc.Uint128
		got := libc.Y__builtin_mul_overflowUint128(tls, vector.a, vector.b, uintptr(unsafe.Pointer(&result)))
		if got != vector.overflow || result != vector.want {
			t.Fatalf("Y multiply %+v * %+v: %+v overflow %d, want %+v overflow %d", vector.a, vector.b, result, got, vector.want, vector.overflow)
		}
	}
}
