/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/runtime/math"
)

// -----------------------------------------------------------------------------

// Slice is the runtime representation of a slice.
type Slice struct {
	data unsafe.Pointer
	len  int
	cap  int
}

func NewSlice2(base unsafe.Pointer, eltSize, cap int, i, j int64, iSigned, jSigned, upperIsLen bool) (s Slice) {
	upperCode := boundsSliceAcap
	if upperIsLen {
		upperCode = boundsSliceAlen
	}
	if boundsOutOfRange(j, jSigned, cap, true) {
		panicBounds(j, jSigned, cap, upperCode)
	}
	if boundsAbove(i, iSigned, j) {
		panicBounds(i, iSigned, int(j), boundsSliceB)
	}
	return newSliceUnchecked(base, eltSize, int(i), int(j), cap)
}

func NewSlice3Bounds(base unsafe.Pointer, eltSize, cap int, i, j, k int64, iSigned, jSigned, kSigned, upperIsLen bool) (s Slice) {
	upperCode := boundsSlice3Acap
	if upperIsLen {
		upperCode = boundsSlice3Alen
	}
	if boundsOutOfRange(k, kSigned, cap, true) {
		panicBounds(k, kSigned, cap, upperCode)
	}
	if boundsAbove(j, jSigned, k) {
		panicBounds(j, jSigned, int(k), boundsSlice3B)
	}
	if boundsAbove(i, iSigned, j) {
		panicBounds(i, iSigned, int(j), boundsSlice3C)
	}
	return newSliceUnchecked(base, eltSize, int(i), int(j), int(k))
}

func NewSlice3(base unsafe.Pointer, eltSize, cap, i, j, k int) (s Slice) {
	if k < 0 || k > cap {
		panic(boundsError{x: int64(k), signed: true, y: cap, code: boundsSlice3Acap})
	}
	if j < 0 || j > k {
		panic(boundsError{x: int64(j), signed: true, y: k, code: boundsSlice3B})
	}
	if i < 0 || i > j {
		panic(boundsError{x: int64(i), signed: true, y: j, code: boundsSlice3C})
	}
	return newSliceUnchecked(base, eltSize, i, j, k)
}

func newSliceUnchecked(base unsafe.Pointer, eltSize, i, j, k int) (s Slice) {
	s.len = j - i
	s.cap = k - i
	if k-i > 0 {
		s.data = c.Advance(base, i*eltSize)
	} else {
		s.data = base
	}
	return
}

// SliceAppend append elem data and returns a slice.
func SliceAppend(src Slice, data unsafe.Pointer, num, etSize int) Slice {
	oldLen := src.len
	src = GrowSlice(src, num, etSize)
	if etSize == 0 {
		return src
	}
	c.Memcpy(c.Advance(src.data, oldLen*etSize), data, uintptr(num*etSize))
	return src
}

// SliceAppendAtomic appends pointer-free elem data using noscan allocation when
// the backing array must grow.
func SliceAppendAtomic(src Slice, data unsafe.Pointer, num, etSize int) Slice {
	oldLen := src.len
	src = GrowSliceAtomic(src, num, etSize)
	if etSize == 0 {
		return src
	}
	c.Memcpy(c.Advance(src.data, oldLen*etSize), data, uintptr(num*etSize))
	return src
}

// GrowSlice grows slice and returns the grown slice.
func GrowSlice(src Slice, num, etSize int) Slice {
	oldLen := src.len
	newLen := oldLen + num
	if newLen < 0 {
		panicgrowslicelen()
	}
	if newLen > src.cap {
		newCap := nextslicecap(newLen, src.cap)
		mem, overflow := math.MulUintptr(uintptr(etSize), uintptr(newCap))
		if overflow || mem > maxAlloc {
			panicgrowslicelen()
		}
		p := AllocZ(mem)
		if oldLen != 0 {
			c.Memcpy(p, src.data, uintptr(oldLen*etSize))
		}
		src.data = p
		src.cap = newCap
	}
	src.len = newLen
	return src
}

// GrowSliceAtomic grows a pointer-free slice using noscan allocation while
// zeroing the newly available tail [oldLen..newCap) to preserve Go zero-value
// semantics.
func GrowSliceAtomic(src Slice, num, etSize int) Slice {
	oldLen := src.len
	newLen := oldLen + num
	if newLen < 0 {
		panicgrowslicelen()
	}
	if newLen > src.cap {
		newCap := nextslicecap(newLen, src.cap)
		mem, overflow := math.MulUintptr(uintptr(etSize), uintptr(newCap))
		if overflow || mem > maxAlloc {
			panicgrowslicelen()
		}
		p := AllocAtomicU(mem)
		oldBytes := oldLen * etSize
		if oldBytes != 0 {
			c.Memcpy(p, src.data, uintptr(oldBytes))
		}
		if mem > uintptr(oldBytes) {
			c.Memset(c.Advance(p, oldBytes), 0, mem-uintptr(oldBytes))
		}
		src.data = p
		src.cap = newCap
	}
	src.len = newLen
	return src
}

// nextslicecap computes the next appropriate slice length.
func nextslicecap(newLen, oldCap int) int {
	newcap := oldCap
	doublecap := newcap + newcap
	if newLen > doublecap {
		return newLen
	}

	const threshold = 256
	if oldCap < threshold {
		return doublecap
	}
	// Transition from growing 2x for small slices to growing 1.25x for large
	// slices. Since oldCap >= threshold and newLen <= 2*oldCap, each step grows
	// newcap by strictly more than 1.25x (1.25^4 > 2), so at most four steps
	// are ever needed to reach newLen or overflow (where uint(newcap) >=
	// uint(newLen) because newLen > 0). Unrolling keeps nextslicecap,
	// GrowSlice, and SliceAppend loop-free plain outcome helpers.
	newcap += (newcap + 3*threshold) >> 2
	if uint(newcap) < uint(newLen) {
		newcap += (newcap + 3*threshold) >> 2
		if uint(newcap) < uint(newLen) {
			newcap += (newcap + 3*threshold) >> 2
			if uint(newcap) < uint(newLen) {
				newcap += (newcap + 3*threshold) >> 2
			}
		}
	}

	// Set newcap to the requested cap when
	// the newcap calculation overflowed.
	if newcap <= 0 {
		return newLen
	}
	return newcap
}

// SliceCopy copy data to slice and returns a slice.
func SliceCopy(dst Slice, data unsafe.Pointer, num int, etSize int) int {
	n := dst.len
	if n > num {
		n = num
	}
	if n > 0 {
		c.Memmove(dst.data, data, uintptr(n*etSize))
	}
	return n
}

func MakeSlice(len, cap int, etSize int) Slice {
	mem, overflow := math.MulUintptr(uintptr(etSize), uintptr(cap))
	if overflow || mem > maxAlloc || len < 0 || len > cap {
		mem, overflow := math.MulUintptr(uintptr(etSize), uintptr(len))
		if overflow || mem > maxAlloc || len < 0 {
			panicmakeslicelen()
		}
		panicmakeslicecap()
	}
	return Slice{AllocZ(mem), len, cap}
}

// MakeSliceAtomic allocates a zero-initialized backing array for a pointer-free
// slice element type using noscan collector storage.
func MakeSliceAtomic(len, cap int, etSize int) Slice {
	mem, overflow := math.MulUintptr(uintptr(etSize), uintptr(cap))
	if overflow || mem > maxAlloc || len < 0 || len > cap {
		mem, overflow := math.MulUintptr(uintptr(etSize), uintptr(len))
		if overflow || mem > maxAlloc || len < 0 {
			panicmakeslicelen()
		}
		panicmakeslicecap()
	}
	return Slice{AllocAtomicZ(mem), len, cap}
}

func panicmakeslicelen() {
	panic(errorString("makeslice: len out of range"))
}

func panicmakeslicecap() {
	panic(errorString("makeslice: cap out of range"))
}

func panicgrowslicelen() {
	panic(errorString("growslice: len out of range"))
}

func SliceClear(t *abi.SliceType, s Slice) {
	c.Memset(s.data, 0, uintptr(s.len)*t.Elem.Size())
}

func unsafeslice(et *abi.Type, ptr unsafe.Pointer, len int) {
	if len < 0 {
		panic(errorString("unsafe.Slice: len out of range"))
	}

	if et.Size_ == 0 {
		if ptr == nil && len > 0 {
			panic(errorString("unsafe.Slice: ptr is nil and len is not zero"))
		}
	}

	mem, overflow := math.MulUintptr(et.Size_, uintptr(len))
	if overflow || mem > -uintptr(ptr) {
		if ptr == nil {
			panic(errorString("unsafe.Slice: ptr is nil and len is not zero"))
		}
		panic(errorString("unsafe.Slice: len out of range"))
	}
}

// -----------------------------------------------------------------------------
