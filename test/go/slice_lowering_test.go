package gotest

import "testing"

//go:noinline
func loweredByteSlice(s []byte, low, high, max uint64) []byte {
	return s[low:high:max]
}

//go:noinline
func loweredStringSlice(s string, low, high uint64) string {
	return s[low:high]
}

func TestSliceLoweredHeaders(t *testing.T) {
	s := []byte{10, 20, 30, 40}
	s = s[:2]
	got := loweredByteSlice(s, 1, 3, 4)
	if len(got) != 2 || cap(got) != 3 || &got[0] != &s[1] || got[1] != 30 {
		t.Fatalf("slice = %v, len/cap = %d/%d", got, len(got), cap(got))
	}
	if got := loweredStringSlice("hello", 1, 4); got != "ell" {
		t.Fatalf("string slice = %q", got)
	}
	if got := loweredByteSlice(nil, 0, 0, 0); got != nil {
		t.Fatalf("nil slice = %v", got)
	}
	var a [4]uint32
	low, high := 1, 3
	words := (&a)[low:high]
	if len(words) != 2 || cap(words) != 3 || &words[0] != &a[1] {
		t.Fatalf("array slice has incorrect element stride or header")
	}
	var zero [4]struct{}
	if got := (&zero)[low:high:high]; len(got) != 2 || cap(got) != 2 {
		t.Fatalf("zero-sized array slice len/cap = %d/%d", len(got), cap(got))
	}
}

func TestSliceLoweredWideBounds(t *testing.T) {
	for _, index := range []uint64{1<<32 + 1, 1 << 63, ^uint64(0)} {
		t.Run("high", func(t *testing.T) {
			expectRuntimeBoundsPanic(t, "runtime error: slice bounds out of range [:"+fullSlicePanicString(index)+"] with length 3", func() {
				_ = loweredStringSlice("abc", 0, index)
			})
		})
		t.Run("max", func(t *testing.T) {
			expectRuntimeBoundsPanic(t, "runtime error: slice bounds out of range [::"+fullSlicePanicString(index)+"] with capacity 3", func() {
				_ = loweredByteSlice([]byte{1, 2, 3}, 0, 0, index)
			})
		})
	}
}
