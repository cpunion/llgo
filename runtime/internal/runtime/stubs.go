// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	"github.com/goplus/llgo/runtime/internal/runtime/math"
)

//go:nosplit
func add(p unsafe.Pointer, x uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) + x)
}

// implementation of new builtin
// compiler (both frontend and SSA backend) knows the signature
// of this function.
func newobject(typ *_type) unsafe.Pointer {
	return AllocZ(typ.Size_)
}

// TODO
func roundupsize(size uintptr) uintptr {
	// if size < _MaxSmallSize {
	// 	if size <= smallSizeMax-8 {
	// 		return uintptr(class_to_size[size_to_class8[divRoundUp(size, smallSizeDiv)]])
	// 	} else {
	// 		return uintptr(class_to_size[size_to_class128[divRoundUp(size-smallSizeMax, largeSizeDiv)]])
	// 	}
	// }
	// if size+_PageSize < size {
	// 	return size
	// }
	// return alignUp(size, _PageSize)
	return size
}

// newarray allocates an array of n elements of type typ.
func newarray(typ *_type, n int) unsafe.Pointer {
	if n == 1 {
		return AllocZ(typ.Size_)
	}
	mem, overflow := math.MulUintptr(typ.Size_, uintptr(n))
	if overflow || mem > maxAlloc || n < 0 {
		panic(plainError("runtime: allocation size out of range"))
	}
	return AllocZ(mem)
}

const (
	// _64bit = 1 on 64-bit systems, 0 on 32-bit systems
	_64bit       = 1 << (^uintptr(0) >> 63) / 2
	heapAddrBits = (_64bit)*48 + (1-_64bit)*(32)
	maxAlloc     = (1 << heapAddrBits) - (1-_64bit)*1
)

func memclrHasPointers(ptr unsafe.Pointer, n uintptr) {
	// bulkBarrierPreWrite(uintptr(ptr), 0, n)
	// memclrNoHeapPointers(ptr, n)
}

func memclrNoHeapPointers(ptr unsafe.Pointer, n uintptr) {
}

func fatal(s string) {
	coroRuntimeAbort(s)
}

func throw(s string) {
	coroRuntimeAbort(s)
}

func atomicOr8(ptr *uint8, v uint8) uint8 {
	return (uint8)(atomic.Or((*uint)(unsafe.Pointer(ptr)), uint(v)))
}

func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}
