package ffi

import "unsafe"

const deliveryVersionV3 uint32 = 3
const deliveryRequiresState uint32 = 1

// The nested base retains its complete target padding, matching the compiler's
// cold-tail allocation. Hashes are independent expectations in that tail, not
// values inferred by reading a helper's own descriptor.
type deliveryTailV3 struct {
	Version, Flags               uint32
	Initializer, Drop            unsafe.Pointer
	InitializerLo, InitializerHi uint64
	DropLo, DropHi               uint64
}

type deliveryDescriptorV3 struct {
	Base dispatchDescriptorV2
	Tail deliveryTailV3
}

func validateDeliveryHelper(pointer unsafe.Pointer, lo, hi uint64) {
	if pointer == nil {
		panic("llgo: missing resource delivery helper")
	}
	d := (*dispatchDescriptorV2)(pointer)
	if d.Version != dispatchVersionV2 || d.Flags&^dispatchKnownFlags != 0 ||
		!validDispatchPlainUnwindFlags(d.Flags) || !validDispatchEntries(d) ||
		d.Flags&dispatchNoCapture == 0 || d.Flags&(dispatchRuntimeTyped|dispatchHasDelivery|dispatchDeliveryUnbound|dispatchMethodResultPrototype) != 0 ||
		d.CodeEntry == nil || d.ResultSize != 0 || d.ResultAlign != 1 || d.HashLo != lo || d.HashHi != hi {
		panic("llgo: invalid typed resource delivery helper")
	}
}

func checkedDeliveryTail(descriptor unsafe.Pointer) deliveryTailV3 {
	d := (*dispatchDescriptorV2)(descriptor)
	if d == nil || d.Flags&dispatchHasDelivery == 0 {
		panic("llgo: missing resource delivery capability")
	}
	tail := (*deliveryDescriptorV3)(descriptor).Tail
	if tail.Version != deliveryVersionV3 || tail.Flags&^deliveryRequiresState != 0 {
		panic("llgo: incompatible resource delivery tail")
	}
	validateDeliveryHelper(tail.Initializer, tail.InitializerLo, tail.InitializerHi)
	validateDeliveryHelper(tail.Drop, tail.DropLo, tail.DropHi)
	return tail
}

// HasResultDelivery is a checked capability query, never permission to invoke
// it. Unguarded CallLLGo and CoroEntry continue rejecting resource producers.
func HasResultDelivery(descriptor unsafe.Pointer) bool {
	if descriptor == nil {
		panic("reflect.Value.Call: call of nil function")
	}
	d := (*dispatchDescriptorV2)(descriptor)
	_ = CodeEntry(descriptor)
	if d.Flags&dispatchDeliveryUnbound != 0 {
		rejectUnboundResourceDelivery(d.Flags)
	}
	return d.Flags&dispatchHasDelivery != 0
}

// ResultStateOffset is the guard-v5 private prefix offset. Reflection supplies
// GC-managed, correctly aligned complete storage before any resource action.
func ResultStateOffset(alignment uintptr) uintptr {
	if alignment == 0 || alignment&(alignment-1) != 0 || alignment > (^uintptr(0)>>1) {
		panic("llgo: invalid resource result alignment")
	}
	word := unsafe.Sizeof(unsafe.Pointer(nil))
	return (word + alignment - 1) &^ (alignment - 1)
}

type deliverySignatures struct{ plain, outcome, coro *Signature }

func newDeliverySignatures(arguments []*Type) deliverySignatures {
	makeSignature := func(prefix int, result *Type) *Signature {
		args := make([]*Type, prefix+len(arguments))
		for i := 0; i < prefix; i++ {
			args[i] = TypePointer
		}
		copy(args[prefix:], arguments)
		sig, err := NewSignature(result, args...)
		if err != nil {
			panic(err)
		}
		return sig
	}
	return deliverySignatures{makeSignature(1, TypeVoid), makeSignature(4, TypeVoid), makeSignature(3, TypePointer)}
}

// ResultDelivery is retained by reflect's outer borrowed-result guard through
// boxing and caller acceptance. It uses the ordinary managed invocation and
// cleanup drainer; it owns no cancellation decision or scheduler operation.
type ResultDelivery struct {
	descriptor, env, runtimeType, ret unsafe.Pointer
	size, align                       uintptr
	state                             *unsafe.Pointer
	nativeState                       unsafe.Pointer
	args                              []unsafe.Pointer
	initializer, pendingDrop, drop    unsafe.Pointer
	requiresState                     bool
	initSig, dropSig                  deliverySignatures
}

func NewResultDelivery(descriptor, env, runtimeType, ret unsafe.Pointer, state *unsafe.Pointer,
	size, alignment uintptr, argumentTypes []*Type, args []unsafe.Pointer) *ResultDelivery {
	d := validateLLGoDescriptor(descriptor, env, runtimeType, size, alignment)
	if d.Flags&dispatchDeliveryUnbound != 0 {
		rejectUnboundResourceDelivery(d.Flags)
	}
	tail := checkedDeliveryTail(descriptor)
	offset := ResultStateOffset(alignment)
	if ret == nil || state == nil || unsafe.Add(ret, -int(offset)) != unsafe.Pointer(state) || *state != nil || len(argumentTypes) != len(args) {
		panic("llgo: resource delivery requires its owned result prefix and argument snapshot")
	}
	dropArgs := make([]*Type, 2+len(argumentTypes))
	dropArgs[0], dropArgs[1] = TypePointer, TypePointer
	copy(dropArgs[2:], argumentTypes)
	return &ResultDelivery{descriptor: descriptor, env: env, runtimeType: runtimeType, ret: ret, state: state, size: size, align: alignment,
		args: args, initializer: tail.Initializer, pendingDrop: tail.Drop, requiresState: tail.Flags&deliveryRequiresState != 0,
		initSig: newDeliverySignatures([]*Type{TypePointer}), dropSig: newDeliverySignatures(dropArgs)}
}

func (s *ResultDelivery) Initialize() {
	if s == nil || s.initializer == nil || s.drop != nil {
		panic("llgo: invalid resource delivery initialization")
	}
	helper := s.initializer
	s.initializer = nil
	CallLLGo(s.initSig.plain, s.initSig.outcome, s.initSig.coro, helper, nil, nil, nil, 0, 1, unsafe.Pointer(&s.ret))
	s.drop = s.pendingDrop
	s.pendingDrop = nil
}

func (s *ResultDelivery) Invoke(plain, outcome, coroutine *Signature) {
	if s == nil || s.drop == nil || s.descriptor == nil {
		panic("llgo: resource invocation has no initialized outer custody")
	}
	callLLGoManaged(plain, outcome, coroutine, s.descriptor, s.env, s.runtimeType, s.ret, s.size, s.align, s, s.args...)
	s.descriptor = nil
}

func (s *ResultDelivery) FinishInvocation() {
	if s == nil || s.descriptor != nil || s.drop == nil {
		panic("llgo: resource invocation did not complete")
	}
	// These are only managed pointer loads/stores, with no call or safepoint.
	// Returned Values can retain the native result without retaining snapshots.
	if state := *s.state; state != nil {
		if s.nativeState != nil {
			panic("llgo: duplicate resource invocation state")
		}
		s.nativeState = state
		*s.state = nil
	}
}

func (s *ResultDelivery) Drop() {
	if s == nil || s.drop == nil {
		return
	}
	helper := s.drop
	s.drop = nil
	if s.nativeState != nil {
		*s.state = s.nativeState
		s.nativeState = nil
	}
	if !s.requiresState || *s.state != nil {
		args := make([]unsafe.Pointer, 2+len(s.args))
		args[0], args[1] = unsafe.Pointer(&s.ret), unsafe.Pointer(&s.env)
		copy(args[2:], s.args)
		CallLLGo(s.dropSig.plain, s.dropSig.outcome, s.dropSig.coro, helper, nil, nil, nil, 0, 1, args...)
	}
	*s.state = nil
	s.args = nil
}
