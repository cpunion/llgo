//go:build !llgo

package ffi

import "testing"

func TestDispatchDescriptorABIBits(t *testing.T) {
	if dispatchHasPlain != 1 || dispatchHasCoro != 2 || dispatchNoCapture != 4 || dispatchRuntimeTyped != 8 || dispatchPlainNoUnwind != 16 {
		t.Fatalf(
			"dispatch descriptor flags = plain:%d coro:%d no-capture:%d runtime-typed:%d plain-no-unwind:%d, want 1/2/4/8/16",
			dispatchHasPlain, dispatchHasCoro, dispatchNoCapture, dispatchRuntimeTyped, dispatchPlainNoUnwind,
		)
	}
}

func TestDispatchDescriptorPlainUnwindFlags(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags uint32
		want  bool
	}{
		{"coro only", dispatchHasCoro, true},
		{"plain proved", dispatchHasPlain | dispatchPlainNoUnwind, true},
		{"dual", dispatchHasPlain | dispatchHasCoro, true},
		{"plain unproved", dispatchHasPlain, false},
		{"orphan proof", dispatchHasCoro | dispatchPlainNoUnwind, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validDispatchPlainUnwindFlags(test.flags); got != test.want {
				t.Fatalf("validDispatchPlainUnwindFlags(%#x)=%t, want %t", test.flags, got, test.want)
			}
		})
	}
}
