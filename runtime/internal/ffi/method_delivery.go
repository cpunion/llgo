package ffi

import "unsafe"

type methodResultPrototype struct {
	HashLo, HashHi uint64
	Size, Align    uintptr
	Tail           deliveryTailV3
}

// MethodResultPrototype reads cold metadata only under its checked flag.
// Complete nested descriptors retain their target-dependent trailing padding.
func MethodResultPrototype(descriptor unsafe.Pointer) unsafe.Pointer {
	_ = CodeEntry(descriptor)
	d := (*dispatchDescriptorV2)(descriptor)
	if d.Flags&dispatchMethodResultPrototype == 0 {
		return nil
	}
	var prototype unsafe.Pointer
	if d.Flags&dispatchHasDelivery != 0 {
		prototype = (*struct {
			Base      deliveryDescriptorV3
			Prototype unsafe.Pointer
		})(descriptor).Prototype
	} else {
		prototype = (*struct {
			Base      dispatchDescriptorV2
			Prototype unsafe.Pointer
		})(descriptor).Prototype
	}
	if prototype == nil {
		panic("llgo: missing method result prototype")
	}
	return prototype
}

// NewBoundMethodDescriptor consumes a compiler-published receiver-free ABI.
// The runtime supplies only the already-typed libffi entry; hashes and helper
// expectations come from the independent compiler contract.
func NewBoundMethodDescriptor(prototype, entry unsafe.Pointer, size, alignment uintptr) unsafe.Pointer {
	p := (*methodResultPrototype)(prototype)
	if p == nil || entry == nil || size != p.Size || alignment != p.Align || size == 0 ||
		p.Tail.Version != deliveryVersionV3 || p.Tail.Flags != deliveryRequiresState {
		panic("llgo: invalid bound method result prototype")
	}
	validateDeliveryHelper(p.Tail.Initializer, p.Tail.InitializerLo, p.Tail.InitializerHi)
	validateDeliveryHelper(p.Tail.Drop, p.Tail.DropLo, p.Tail.DropHi)
	return unsafe.Pointer(&deliveryDescriptorV3{Base: dispatchDescriptorV2{
		Version: dispatchVersionV2, Flags: dispatchHasCoro | dispatchHasDelivery,
		HashLo: p.HashLo, HashHi: p.HashHi, StructuredEntry: entry,
		ResultSize: size, ResultAlign: alignment, CodeEntry: entry,
	}, Tail: p.Tail})
}

// BorrowedResultCoroEntry is reserved for reflect's fixed source invoker,
// whose output is the checked one-pointer custody record. Its public method
// bridge owns that output, or passes nil to use the ordinary source fallback.
func BorrowedResultCoroEntry(descriptor unsafe.Pointer, size, alignment uintptr) unsafe.Pointer {
	d := (*dispatchDescriptorV2)(descriptor)
	_ = CodeEntry(descriptor)
	if d == nil || d.Flags != dispatchHasCoro|dispatchNoCapture|dispatchHasDelivery ||
		d.ResultSize != size || d.ResultAlign != alignment || size != unsafe.Sizeof(unsafe.Pointer(nil)) {
		panic("llgo: invalid borrowed method invoker")
	}
	_ = checkedDeliveryTail(descriptor)
	return d.StructuredEntry
}
