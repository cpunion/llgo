//go:build !llgo

package ffi

import (
	"testing"
	"unsafe"
)

func testDeliveryDescriptor() *deliveryDescriptorV3 {
	entry := unsafe.Pointer(new(byte))
	init := &dispatchDescriptorV2{Version: dispatchVersionV2, Flags: dispatchHasOutcome | dispatchNoCapture,
		StructuredEntry: entry, CodeEntry: entry, ResultAlign: 1, HashLo: 11, HashHi: 12}
	drop := *init
	drop.HashLo, drop.HashHi = 21, 22
	return &deliveryDescriptorV3{Base: dispatchDescriptorV2{Version: dispatchVersionV2, Flags: dispatchHasCoro | dispatchHasDelivery,
		StructuredEntry: entry, CodeEntry: entry, ResultSize: 8, ResultAlign: 8}, Tail: deliveryTailV3{
		Version: deliveryVersionV3, Flags: deliveryRequiresState, Initializer: unsafe.Pointer(init), Drop: unsafe.Pointer(&drop),
		InitializerLo: 11, InitializerHi: 12, DropLo: 21, DropHi: 22}}
}

func TestDeliveryTailValidatesIndependentHelperExpectations(t *testing.T) {
	d := testDeliveryDescriptor()
	if !HasResultDelivery(unsafe.Pointer(d)) || checkedDeliveryTail(unsafe.Pointer(d)) != d.Tail {
		t.Fatal("valid typed resource tail changed")
	}
	for name, change := range map[string]func(*deliveryDescriptorV3){
		"old-version":         func(d *deliveryDescriptorV3) { d.Tail.Version = 2 },
		"unknown-flags":       func(d *deliveryDescriptorV3) { d.Tail.Flags = 2 },
		"missing-initializer": func(d *deliveryDescriptorV3) { d.Tail.Initializer = nil },
		"wrong-drop-type":     func(d *deliveryDescriptorV3) { d.Tail.DropLo++ },
		"captured-helper":     func(d *deliveryDescriptorV3) { (*dispatchDescriptorV2)(d.Tail.Drop).Flags &^= dispatchNoCapture },
		"runtime-helper":      func(d *deliveryDescriptorV3) { (*dispatchDescriptorV2)(d.Tail.Drop).Flags |= dispatchRuntimeTyped },
		"resource-helper":     func(d *deliveryDescriptorV3) { (*dispatchDescriptorV2)(d.Tail.Drop).Flags |= dispatchHasDelivery },
		"helper-output":       func(d *deliveryDescriptorV3) { (*dispatchDescriptorV2)(d.Tail.Drop).ResultSize = 8 },
	} {
		t.Run(name, func(t *testing.T) {
			d := testDeliveryDescriptor()
			change(d)
			defer func() {
				if recover() == nil {
					t.Fatal("accepted malformed delivery before acquisition")
				}
			}()
			checkedDeliveryTail(unsafe.Pointer(d))
		})
	}
}

func TestDeliveryQueryKeepsOrdinaryRuntimeValuesUnowned(t *testing.T) {
	descriptor := NewRuntimeCoroDescriptor(unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte)), 8, 8)
	if HasResultDelivery(descriptor) {
		t.Fatal("arbitrary MakeFunc acquired an inferred resource contract")
	}
	ordinary := &dispatchDescriptorV2{Version: dispatchVersionV2, Flags: dispatchHasCoro,
		StructuredEntry: unsafe.Pointer(new(byte)), CodeEntry: unsafe.Pointer(new(byte)), ResultSize: 8, ResultAlign: 8}
	if HasResultDelivery(unsafe.Pointer(ordinary)) {
		t.Fatal("base-only descriptor acquired a cold-tail read")
	}
}

func TestResultStateOffsetRejectsInvalidAlignment(t *testing.T) {
	for _, alignment := range []uintptr{1, 2, 4, 8, 16, 64} {
		offset := ResultStateOffset(alignment)
		if offset < unsafe.Sizeof(unsafe.Pointer(nil)) || offset%alignment != 0 {
			t.Fatal("misaligned private result prefix", alignment, offset)
		}
	}
	for _, alignment := range []uintptr{0, 3, ^uintptr(0)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("accepted invalid prefix alignment", alignment)
				}
			}()
			ResultStateOffset(alignment)
		}()
	}
}

func TestBoundMethodPrototypeRetainsIndependentContract(t *testing.T) {
	for _, resource := range []bool{false, true} {
		d := testDeliveryDescriptor()
		prototype := &methodResultPrototype{HashLo: 31, HashHi: 32, Size: d.Base.ResultSize, Align: d.Base.ResultAlign, Tail: d.Tail}
		d.Base.Flags |= dispatchMethodResultPrototype
		var source unsafe.Pointer
		if resource {
			source = unsafe.Pointer(&struct {
				Base      deliveryDescriptorV3
				Prototype unsafe.Pointer
			}{*d, unsafe.Pointer(prototype)})
		} else {
			d.Base.Flags &^= dispatchHasDelivery
			source = unsafe.Pointer(&struct {
				Base      dispatchDescriptorV2
				Prototype unsafe.Pointer
			}{d.Base, unsafe.Pointer(prototype)})
		}
		p := MethodResultPrototype(source)
		bound := NewBoundMethodDescriptor(p, d.Base.StructuredEntry, 8, 8)
		base := (*dispatchDescriptorV2)(bound)
		if base.HashLo != 31 || base.HashHi != 32 || base.Flags != dispatchHasCoro|dispatchHasDelivery || checkedDeliveryTail(bound) != d.Tail {
			t.Fatal("bound method lost its compiler-published result or helper ABI")
		}
		prototype.Tail.DropLo++
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("bound method accepted a mismatched helper expectation")
				}
			}()
			NewBoundMethodDescriptor(p, d.Base.StructuredEntry, 8, 8)
		}()
	}
}
