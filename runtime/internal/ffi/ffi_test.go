//go:build !llgo

package ffi

import (
	"testing"
	"unsafe"
)

func TestDispatchDescriptorABIBits(t *testing.T) {
	if dispatchVersionV2 != 2 || dispatchHasPlain != 1 || dispatchHasOutcome != 2 || dispatchHasCoro != 4 ||
		dispatchNoCapture != 8 || dispatchRuntimeTyped != 16 || dispatchPlainNoUnwind != 32 {
		t.Fatalf(
			"dispatch descriptor v%d flags = plain:%d outcome:%d coro:%d no-capture:%d runtime-typed:%d plain-no-unwind:%d, want v2 1/2/4/8/16/32",
			dispatchVersionV2, dispatchHasPlain, dispatchHasOutcome, dispatchHasCoro,
			dispatchNoCapture, dispatchRuntimeTyped, dispatchPlainNoUnwind,
		)
	}
}

func TestDispatchDescriptorPlainUnwindFlags(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags uint32
		want  bool
	}{
		{"outcome only", dispatchHasOutcome, true},
		{"coro only", dispatchHasCoro, true},
		{"plain proved", dispatchHasPlain | dispatchPlainNoUnwind, true},
		{"plain outcome", dispatchHasPlain | dispatchHasOutcome, true},
		{"dual", dispatchHasPlain | dispatchHasCoro, true},
		{"plain unproved", dispatchHasPlain, false},
		{"orphan proof", dispatchHasCoro | dispatchPlainNoUnwind, false},
		{"outcome coro conflict", dispatchHasOutcome | dispatchHasCoro, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validDispatchPlainUnwindFlags(test.flags); got != test.want {
				t.Fatalf("validDispatchPlainUnwindFlags(%#x)=%t, want %t", test.flags, got, test.want)
			}
		})
	}
}

func TestDispatchDescriptorStructuredEntryIsCompactAndExclusive(t *testing.T) {
	plain, structured, code := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	for _, test := range []struct {
		name string
		d    dispatchDescriptorV2
		want bool
	}{
		{"plain", dispatchDescriptorV2{Flags: dispatchHasPlain, PlainEntry: plain, CodeEntry: code}, true},
		{"outcome", dispatchDescriptorV2{Flags: dispatchHasOutcome, StructuredEntry: structured, CodeEntry: code}, true},
		{"coro", dispatchDescriptorV2{Flags: dispatchHasCoro, StructuredEntry: structured, CodeEntry: code}, true},
		{"plain outcome", dispatchDescriptorV2{Flags: dispatchHasPlain | dispatchHasOutcome, PlainEntry: plain, StructuredEntry: structured, CodeEntry: code}, true},
		{"missing structured", dispatchDescriptorV2{Flags: dispatchHasOutcome, CodeEntry: code}, false},
		{"stray structured", dispatchDescriptorV2{Flags: dispatchHasPlain, PlainEntry: plain, StructuredEntry: structured, CodeEntry: code}, false},
		{"conflict", dispatchDescriptorV2{Flags: dispatchHasOutcome | dispatchHasCoro, StructuredEntry: structured, CodeEntry: code}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validDispatchEntries(&test.d); got != test.want {
				t.Fatalf("validDispatchEntries(%+v)=%t, want %t", test.d, got, test.want)
			}
		})
	}
	if got, want := unsafe.Sizeof(dispatchDescriptorV2{}), uintptr(8+16+5*unsafe.Sizeof(uintptr(0))); got != want {
		t.Fatalf("descriptor size = %d, want compact nine-field size %d", got, want)
	}
}

func TestRuntimeCoroDescriptorUsesV2StructuredEntry(t *testing.T) {
	runtimeType, entry := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	descriptor := NewRuntimeCoroDescriptor(runtimeType, entry, 8, 8)
	d := (*dispatchDescriptorV2)(descriptor)
	if d.Version != dispatchVersionV2 || d.Flags != dispatchHasCoro|dispatchRuntimeTyped ||
		d.StructuredEntry != entry || d.CodeEntry != entry || d.HashHi != dispatchRuntimeTypeMagicV2 {
		t.Fatalf("runtime descriptor = %+v", d)
	}
	if got := CoroEntry(descriptor); got != entry {
		t.Fatalf("CoroEntry = %p, want %p", got, entry)
	}
	if got := CodeEntry(descriptor); got != entry {
		t.Fatalf("CodeEntry = %p, want %p", got, entry)
	}
}
