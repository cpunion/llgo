//go:build !llgo

package ffi

import "testing"

func TestDispatchDescriptorABIBits(t *testing.T) {
	if dispatchHasPlain != 1 || dispatchHasCoro != 2 || dispatchNoCapture != 4 || dispatchRuntimeTyped != 8 {
		t.Fatalf(
			"dispatch descriptor flags = plain:%d coro:%d no-capture:%d runtime-typed:%d, want 1/2/4/8",
			dispatchHasPlain, dispatchHasCoro, dispatchNoCapture, dispatchRuntimeTyped,
		)
	}
}
