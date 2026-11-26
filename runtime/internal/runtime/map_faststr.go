// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.
// See LICENSES/Go-BSD-3-Clause.txt at this module root for license terms.

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/runtime/goarch"
)

type stringStruct struct {
	str unsafe.Pointer
	len int
}

func stringStructOf(s *string) *stringStruct {
	return (*stringStruct)(unsafe.Pointer(s))
}

func memequalString(p, q unsafe.Pointer, size uintptr) bool {
	if size == 0 {
		return true
	}
	return c.Memcmp(p, q, size) == 0
}

func mapStrSlotMatchSmall(b *bmap, i uintptr, keyData unsafe.Pointer, keyLen int) (matched, stop bool) {
	top := bucketTophash(b, i)
	if isEmpty(top) {
		return false, top == emptyRest
	}
	k := (*stringStruct)(add(unsafe.Pointer(b), dataOffset+i*2*goarch.PtrSize))
	if k.len != keyLen {
		return false, false
	}
	return k.str == keyData || memequalString(k.str, keyData, uintptr(keyLen)), false
}

func mapStrSlotMatchTop(b *bmap, i uintptr, keyData unsafe.Pointer, keyLen int, top uint8) bool {
	if bucketTophash(b, i) != top {
		return false
	}
	k := (*stringStruct)(add(unsafe.Pointer(b), dataOffset+i*2*goarch.PtrSize))
	if k.len != keyLen {
		return false
	}
	return k.str == keyData || memequalString(k.str, keyData, uintptr(keyLen))
}

func mapLookupFastStrSmallBucket(t *maptype, b *bmap, keyData unsafe.Pointer, keyLen int) (unsafe.Pointer, bool) {
	if m, s := mapStrSlotMatchSmall(b, 0, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+0*uintptr(t.ValueSize)), true
	} else if s {
		return nil, false
	}
	if m, s := mapStrSlotMatchSmall(b, 1, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+1*uintptr(t.ValueSize)), true
	} else if s {
		return nil, false
	}
	if m, s := mapStrSlotMatchSmall(b, 2, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+2*uintptr(t.ValueSize)), true
	} else if s {
		return nil, false
	}
	if m, s := mapStrSlotMatchSmall(b, 3, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+3*uintptr(t.ValueSize)), true
	} else if s {
		return nil, false
	}
	if m, s := mapStrSlotMatchSmall(b, 4, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+4*uintptr(t.ValueSize)), true
	} else if s {
		return nil, false
	}
	if m, s := mapStrSlotMatchSmall(b, 5, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+5*uintptr(t.ValueSize)), true
	} else if s {
		return nil, false
	}
	if m, s := mapStrSlotMatchSmall(b, 6, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+6*uintptr(t.ValueSize)), true
	} else if s {
		return nil, false
	}
	if m, _ := mapStrSlotMatchSmall(b, 7, keyData, keyLen); m {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+7*uintptr(t.ValueSize)), true
	}
	return nil, false
}

func mapLookupFastStrTopBucket(t *maptype, b *bmap, keyData unsafe.Pointer, keyLen int, top uint8) (unsafe.Pointer, bool) {
	if mapStrSlotMatchTop(b, 0, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+0*uintptr(t.ValueSize)), true
	}
	if mapStrSlotMatchTop(b, 1, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+1*uintptr(t.ValueSize)), true
	}
	if mapStrSlotMatchTop(b, 2, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+2*uintptr(t.ValueSize)), true
	}
	if mapStrSlotMatchTop(b, 3, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+3*uintptr(t.ValueSize)), true
	}
	if mapStrSlotMatchTop(b, 4, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+4*uintptr(t.ValueSize)), true
	}
	if mapStrSlotMatchTop(b, 5, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+5*uintptr(t.ValueSize)), true
	}
	if mapStrSlotMatchTop(b, 6, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+6*uintptr(t.ValueSize)), true
	}
	if mapStrSlotMatchTop(b, 7, keyData, keyLen, top) {
		return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+7*uintptr(t.ValueSize)), true
	}
	return nil, false
}

func mapaccess1_faststr(t *maptype, h *hmap, ky string) unsafe.Pointer {
	elem, ovf, top := mapaccess1_faststr_fast(t, h, ky)
	if elem != nil {
		return elem
	}
	return mapaccess1_faststr_slow(t, h, ovf, ky, top)
}

func mapaccess1_faststr_fast(t *maptype, h *hmap, ky string) (elem unsafe.Pointer, ovf *bmap, top uint8) {
	if h == nil || h.count == 0 {
		return unsafe.Pointer(&zeroVal[0]), nil, 0
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map read and map write")
	}
	keyData := unsafe.Pointer(unsafe.StringData(ky))
	keyLen := len(ky)
	if h.B == 0 && keyLen < 32 {
		b := (*bmap)(h.buckets)
		if e, ok := mapLookupFastStrSmallBucket(t, b, keyData, keyLen); ok {
			return e, nil, 0
		}
		return unsafe.Pointer(&zeroVal[0]), nil, 0
	}
	hash, ok := memhashShort(keyData, uintptr(h.hash0), uintptr(keyLen))
	if !ok {
		return nil, nil, 0
	}
	m := bucketMask(h.B)
	b := (*bmap)(add(h.buckets, (hash&m)*uintptr(t.BucketSize)))
	if c := h.oldbuckets; c != nil {
		if !h.sameSizeGrow() {
			m >>= 1
		}
		oldb := (*bmap)(add(c, (hash&m)*uintptr(t.BucketSize)))
		if !evacuated(oldb) {
			b = oldb
		}
	}
	top = tophash(hash)
	if e, ok := mapLookupFastStrTopBucket(t, b, keyData, keyLen, top); ok {
		return e, nil, 0
	}
	ovf = b.overflow(t)
	if ovf == nil {
		return unsafe.Pointer(&zeroVal[0]), nil, 0
	}
	return nil, ovf, top
}

func mapaccess1_faststr_slow(t *maptype, h *hmap, b *bmap, ky string, top uint8) unsafe.Pointer {
	keyData := unsafe.Pointer(unsafe.StringData(ky))
	keyLen := len(ky)
	if b == nil {
		hash := memhash(keyData, uintptr(h.hash0), uintptr(keyLen))
		m := bucketMask(h.B)
		b = (*bmap)(add(h.buckets, (hash&m)*uintptr(t.BucketSize)))
		if c := h.oldbuckets; c != nil {
			if !h.sameSizeGrow() {
				m >>= 1
			}
			oldb := (*bmap)(add(c, (hash&m)*uintptr(t.BucketSize)))
			if !evacuated(oldb) {
				b = oldb
			}
		}
		top = tophash(hash)
	}
	for ; b != nil; b = b.overflow(t) {
		for i, kptr := uintptr(0), b.keys(); i < abi.MapBucketCount; i, kptr = i+1, add(kptr, 2*goarch.PtrSize) {
			k := (*stringStruct)(kptr)
			if k.len != keyLen || bucketTophash(b, i) != top {
				continue
			}
			if k.str == keyData || memequalString(k.str, keyData, uintptr(keyLen)) {
				return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+i*uintptr(t.ValueSize))
			}
		}
	}
	return unsafe.Pointer(&zeroVal[0])
}

// mapaccess2_faststr should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/ugorji/go/codec
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname mapaccess2_faststr
func mapaccess2_faststr(t *maptype, h *hmap, ky string) (unsafe.Pointer, bool) {
	elem, ok, ovf, top := mapaccess2_faststr_fast(t, h, ky)
	if elem != nil {
		return elem, ok
	}
	return mapaccess2_faststr_slow(t, h, ovf, ky, top)
}

func mapaccess2_faststr_fast(t *maptype, h *hmap, ky string) (elem unsafe.Pointer, found bool, ovf *bmap, top uint8) {
	if h == nil || h.count == 0 {
		return unsafe.Pointer(&zeroVal[0]), false, nil, 0
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map read and map write")
	}
	keyData := unsafe.Pointer(unsafe.StringData(ky))
	keyLen := len(ky)
	if h.B == 0 && keyLen < 32 {
		b := (*bmap)(h.buckets)
		if e, ok := mapLookupFastStrSmallBucket(t, b, keyData, keyLen); ok {
			return e, true, nil, 0
		}
		return unsafe.Pointer(&zeroVal[0]), false, nil, 0
	}
	hash, ok := memhashShort(keyData, uintptr(h.hash0), uintptr(keyLen))
	if !ok {
		return nil, false, nil, 0
	}
	m := bucketMask(h.B)
	b := (*bmap)(add(h.buckets, (hash&m)*uintptr(t.BucketSize)))
	if c := h.oldbuckets; c != nil {
		if !h.sameSizeGrow() {
			m >>= 1
		}
		oldb := (*bmap)(add(c, (hash&m)*uintptr(t.BucketSize)))
		if !evacuated(oldb) {
			b = oldb
		}
	}
	top = tophash(hash)
	if e, ok := mapLookupFastStrTopBucket(t, b, keyData, keyLen, top); ok {
		return e, true, nil, 0
	}
	ovf = b.overflow(t)
	if ovf == nil {
		return unsafe.Pointer(&zeroVal[0]), false, nil, 0
	}
	return nil, false, ovf, top
}

func mapaccess2_faststr_slow(t *maptype, h *hmap, b *bmap, ky string, top uint8) (unsafe.Pointer, bool) {
	keyData := unsafe.Pointer(unsafe.StringData(ky))
	keyLen := len(ky)
	if b == nil {
		hash := memhash(keyData, uintptr(h.hash0), uintptr(keyLen))
		m := bucketMask(h.B)
		b = (*bmap)(add(h.buckets, (hash&m)*uintptr(t.BucketSize)))
		if c := h.oldbuckets; c != nil {
			if !h.sameSizeGrow() {
				m >>= 1
			}
			oldb := (*bmap)(add(c, (hash&m)*uintptr(t.BucketSize)))
			if !evacuated(oldb) {
				b = oldb
			}
		}
		top = tophash(hash)
	}
	for ; b != nil; b = b.overflow(t) {
		for i, kptr := uintptr(0), b.keys(); i < abi.MapBucketCount; i, kptr = i+1, add(kptr, 2*goarch.PtrSize) {
			k := (*stringStruct)(kptr)
			if k.len != keyLen || bucketTophash(b, i) != top {
				continue
			}
			if k.str == keyData || memequalString(k.str, keyData, uintptr(keyLen)) {
				return add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+i*uintptr(t.ValueSize)), true
			}
		}
	}
	return unsafe.Pointer(&zeroVal[0]), false
}

func mapAssignFastStrProbeSlot(b *bmap, i uintptr, keyData unsafe.Pointer, keyLen int, top uint8, inserti uintptr) (nextInserti uintptr, hit, stop bool) {
	th := bucketTophash(b, i)
	if th != top {
		if isEmpty(th) && inserti == abi.MapBucketCount {
			inserti = i
		}
		return inserti, false, th == emptyRest
	}
	k := (*stringStruct)(add(unsafe.Pointer(b), dataOffset+i*2*goarch.PtrSize))
	if k.len != keyLen {
		return inserti, false, false
	}
	if k.str != keyData && !memequalString(k.str, keyData, uintptr(keyLen)) {
		return inserti, false, false
	}
	k.str = keyData
	return inserti, true, false
}

func mapAssignFastStrPrimaryBucket(b *bmap, keyData unsafe.Pointer, keyLen int, top uint8) (slot uintptr, hit bool) {
	inserti := uintptr(abi.MapBucketCount)
	var h, s bool
	if inserti, h, s = mapAssignFastStrProbeSlot(b, 0, keyData, keyLen, top, inserti); h {
		return 0, true
	} else if s {
		return inserti, false
	}
	if inserti, h, s = mapAssignFastStrProbeSlot(b, 1, keyData, keyLen, top, inserti); h {
		return 1, true
	} else if s {
		return inserti, false
	}
	if inserti, h, s = mapAssignFastStrProbeSlot(b, 2, keyData, keyLen, top, inserti); h {
		return 2, true
	} else if s {
		return inserti, false
	}
	if inserti, h, s = mapAssignFastStrProbeSlot(b, 3, keyData, keyLen, top, inserti); h {
		return 3, true
	} else if s {
		return inserti, false
	}
	if inserti, h, s = mapAssignFastStrProbeSlot(b, 4, keyData, keyLen, top, inserti); h {
		return 4, true
	} else if s {
		return inserti, false
	}
	if inserti, h, s = mapAssignFastStrProbeSlot(b, 5, keyData, keyLen, top, inserti); h {
		return 5, true
	} else if s {
		return inserti, false
	}
	if inserti, h, s = mapAssignFastStrProbeSlot(b, 6, keyData, keyLen, top, inserti); h {
		return 6, true
	} else if s {
		return inserti, false
	}
	if inserti, h, _ = mapAssignFastStrProbeSlot(b, 7, keyData, keyLen, top, inserti); h {
		return 7, true
	}
	return inserti, false
}

// mapassign_faststr should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/bytedance/sonic
//   - github.com/cloudwego/frugal
//   - github.com/ugorji/go/codec
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname mapassign_faststr
func mapassign_faststr(t *maptype, h *hmap, s string) unsafe.Pointer {
	elem, hash, hasHash := mapassign_faststr_fast(t, h, s)
	if elem != nil {
		return elem
	}
	return mapassign_faststr_slow(t, h, s, hash, hasHash)
}

func mapassign_faststr_fast(t *maptype, h *hmap, s string) (elem unsafe.Pointer, hash uintptr, hasHash bool) {
	if h == nil || h.buckets == nil || h.growing() {
		return nil, 0, false
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map writes")
	}
	keyData := unsafe.Pointer(unsafe.StringData(s))
	keyLen := len(s)
	hash, ok := memhashShort(keyData, uintptr(h.hash0), uintptr(keyLen))
	if !ok {
		return nil, 0, false
	}

	// Set hashWriting after calling t.hasher for consistency with mapassign.
	h.flags ^= hashWriting

	bucket := hash & bucketMask(h.B)
	b := (*bmap)(add(h.buckets, bucket*uintptr(t.BucketSize)))
	if b.overflow(t) == nil {
		top := tophash(hash)
		slot, hit := mapAssignFastStrPrimaryBucket(b, keyData, keyLen, top)
		if hit {
			elem = add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+slot*uintptr(t.ValueSize))
			if h.flags&hashWriting == 0 {
				fatal("concurrent map writes")
			}
			h.flags &^= hashWriting
			return elem, 0, true
		}
		if slot < abi.MapBucketCount && !overLoadFactor(h.count+1, h.B) && !tooManyOverflowBuckets(h.noverflow, h.B) {
			setBucketTophash(b, slot, top)
			insertk := (*stringStruct)(add(unsafe.Pointer(b), dataOffset+slot*2*goarch.PtrSize))
			insertk.str = keyData
			insertk.len = keyLen
			h.count++
			elem = add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+slot*uintptr(t.ValueSize))
			if h.flags&hashWriting == 0 {
				fatal("concurrent map writes")
			}
			h.flags &^= hashWriting
			return elem, 0, true
		}
	}
	return nil, hash, true
}

func mapassign_faststr_slow(t *maptype, h *hmap, s string, hash uintptr, hasHash bool) unsafe.Pointer {
	if !hasHash {
		if h == nil {
			panic(plainError("assignment to entry in nil map"))
		}
		if h.flags&hashWriting != 0 {
			fatal("concurrent map writes")
		}
		hash = strhash(noescape(unsafe.Pointer(&s)), uintptr(h.hash0))
		h.flags ^= hashWriting
		if h.buckets == nil {
			h.buckets = newobject(t.Bucket) // newarray(t.bucket, 1)
		}
	}
	key := stringStructOf(&s)
again:
	bucket := hash & bucketMask(h.B)
	if h.growing() {
		growWork_faststr(t, h, bucket)
	}
	b := (*bmap)(add(h.buckets, bucket*uintptr(t.BucketSize)))
	top := tophash(hash)

	var insertb *bmap
	var inserti uintptr
	var insertk unsafe.Pointer

bucketloop:
	for {
		for i := uintptr(0); i < abi.MapBucketCount; i++ {
			if b.tophash[i] != top {
				if isEmpty(b.tophash[i]) && insertb == nil {
					insertb = b
					inserti = i
				}
				if b.tophash[i] == emptyRest {
					break bucketloop
				}
				continue
			}
			k := (*stringStruct)(add(unsafe.Pointer(b), dataOffset+i*2*goarch.PtrSize))
			if k.len != key.len {
				continue
			}
			if k.str != key.str && !memequalString(k.str, key.str, uintptr(key.len)) {
				continue
			}
			// already have a mapping for key. Update it.
			inserti = i
			insertb = b
			// Overwrite existing key, so it can be garbage collected.
			// The size is already guaranteed to be set correctly.
			k.str = key.str
			goto done
		}
		ovf := b.overflow(t)
		if ovf == nil {
			break
		}
		b = ovf
	}

	// Did not find mapping for key. Allocate new cell & add entry.

	// If we hit the max load factor or we have too many overflow buckets,
	// and we're not already in the middle of growing, start growing.
	if !h.growing() && (overLoadFactor(h.count+1, h.B) || tooManyOverflowBuckets(h.noverflow, h.B)) {
		hashGrow(t, h)
		goto again // Growing the table invalidates everything, so try again
	}

	if insertb == nil {
		// The current bucket and all the overflow buckets connected to it are full, allocate a new one.
		insertb = h.newoverflow(t, b)
		inserti = 0 // not necessary, but avoids needlessly spilling inserti
	}
	insertb.tophash[inserti&(abi.MapBucketCount-1)] = top // mask inserti to avoid bounds checks

	insertk = add(unsafe.Pointer(insertb), dataOffset+inserti*2*goarch.PtrSize)
	// store new key at insert position
	*((*stringStruct)(insertk)) = *key
	h.count++

done:
	elem := add(unsafe.Pointer(insertb), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+inserti*uintptr(t.ValueSize))
	if h.flags&hashWriting == 0 {
		fatal("concurrent map writes")
	}
	h.flags &^= hashWriting
	return elem
}

func mapdelete_faststr(t *maptype, h *hmap, ky string) {
	if h == nil || h.count == 0 {
		return
	}
	if h.flags&hashWriting != 0 {
		fatal("concurrent map writes")
	}

	keyData := unsafe.Pointer(unsafe.StringData(ky))
	keyLen := len(ky)
	hash := memhash(keyData, uintptr(h.hash0), uintptr(keyLen))

	// Set hashWriting after calling t.hasher for consistency with mapdelete
	h.flags ^= hashWriting

	bucket := hash & bucketMask(h.B)
	if h.growing() {
		growWork_faststr(t, h, bucket)
	}
	b := (*bmap)(add(h.buckets, bucket*uintptr(t.BucketSize)))
	bOrig := b
	top := tophash(hash)
search:
	for ; b != nil; b = b.overflow(t) {
		for i, kptr := uintptr(0), b.keys(); i < abi.MapBucketCount; i, kptr = i+1, add(kptr, 2*goarch.PtrSize) {
			k := (*stringStruct)(kptr)
			if k.len != keyLen || bucketTophash(b, i) != top {
				continue
			}
			if k.str != keyData && !memequalString(k.str, keyData, uintptr(keyLen)) {
				continue
			}
			// Clear key's pointer.
			k.str = nil
			e := add(unsafe.Pointer(b), dataOffset+abi.MapBucketCount*2*goarch.PtrSize+i*uintptr(t.ValueSize))
			if t.Elem.Pointers() {
				memclrHasPointers(e, t.Elem.Size_)
			} else {
				memclrNoHeapPointers(e, t.Elem.Size_)
			}
			b.tophash[i] = emptyOne
			// If the bucket now ends in a bunch of emptyOne states,
			// change those to emptyRest states.
			if i == abi.MapBucketCount-1 {
				if b.overflow(t) != nil && b.overflow(t).tophash[0] != emptyRest {
					goto notLast
				}
			} else {
				if b.tophash[i+1] != emptyRest {
					goto notLast
				}
			}
			for {
				b.tophash[i] = emptyRest
				if i == 0 {
					if b == bOrig {
						break // beginning of initial bucket, we're done.
					}
					// Find previous bucket, continue at its last entry.
					c := b
					for b = bOrig; b.overflow(t) != c; b = b.overflow(t) {
					}
					i = abi.MapBucketCount - 1
				} else {
					i--
				}
				if b.tophash[i] != emptyOne {
					break
				}
			}
		notLast:
			h.count--
			// Reset the hash seed to make it more difficult for attackers to
			// repeatedly trigger hash collisions. See issue 25237.
			if h.count == 0 {
				h.hash0 = fastrand()
			}
			break search
		}
	}

	if h.flags&hashWriting == 0 {
		fatal("concurrent map writes")
	}
	h.flags &^= hashWriting
}

func growWork_faststr(t *maptype, h *hmap, bucket uintptr) {
	// make sure we evacuate the oldbucket corresponding
	// to the bucket we're about to use
	evacuate_faststr(t, h, bucket&h.oldbucketmask())

	// evacuate one more oldbucket to make progress on growing
	if h.growing() {
		evacuate_faststr(t, h, h.nevacuate)
	}
}

func evacuate_faststr(t *maptype, h *hmap, oldbucket uintptr) {
	b := (*bmap)(add(h.oldbuckets, oldbucket*uintptr(t.BucketSize)))
	newbit := h.noldbuckets()
	if !evacuated(b) {
		// TODO: reuse overflow buckets instead of using new ones, if there
		// is no iterator using the old buckets.  (If !oldIterator.)

		// xy contains the x and y (low and high) evacuation destinations.
		var xy [2]evacDst
		x := &xy[0]
		x.b = (*bmap)(add(h.buckets, oldbucket*uintptr(t.BucketSize)))
		x.k = add(unsafe.Pointer(x.b), dataOffset)
		x.e = add(x.k, abi.MapBucketCount*2*goarch.PtrSize)

		if !h.sameSizeGrow() {
			// Only calculate y pointers if we're growing bigger.
			// Otherwise GC can see bad pointers.
			y := &xy[1]
			y.b = (*bmap)(add(h.buckets, (oldbucket+newbit)*uintptr(t.BucketSize)))
			y.k = add(unsafe.Pointer(y.b), dataOffset)
			y.e = add(y.k, abi.MapBucketCount*2*goarch.PtrSize)
		}

		for ; b != nil; b = b.overflow(t) {
			k := add(unsafe.Pointer(b), dataOffset)
			e := add(k, abi.MapBucketCount*2*goarch.PtrSize)
			for i := 0; i < abi.MapBucketCount; i, k, e = i+1, add(k, 2*goarch.PtrSize), add(e, uintptr(t.ValueSize)) {
				top := b.tophash[i]
				if isEmpty(top) {
					b.tophash[i] = evacuatedEmpty
					continue
				}
				if top < minTopHash {
					throw("bad map state")
				}
				var useY uint8
				if !h.sameSizeGrow() {
					// Compute hash to make our evacuation decision (whether we need
					// to send this key/elem to bucket x or bucket y).
					hash := strhash(k, uintptr(h.hash0))
					if hash&newbit != 0 {
						useY = 1
					}
				}

				b.tophash[i] = evacuatedX + useY // evacuatedX + 1 == evacuatedY, enforced in makemap
				dst := &xy[useY]                 // evacuation destination

				if dst.i == abi.MapBucketCount {
					dst.b = h.newoverflow(t, dst.b)
					dst.i = 0
					dst.k = add(unsafe.Pointer(dst.b), dataOffset)
					dst.e = add(dst.k, abi.MapBucketCount*2*goarch.PtrSize)
				}
				dst.b.tophash[dst.i&(abi.MapBucketCount-1)] = top // mask dst.i as an optimization, to avoid a bounds check

				// Copy key.
				*(*string)(dst.k) = *(*string)(k)

				typedmemmove(t.Elem, dst.e, e)
				dst.i++
				// These updates might push these pointers past the end of the
				// key or elem arrays.  That's ok, as we have the overflow pointer
				// at the end of the bucket to protect against pointing past the
				// end of the bucket.
				dst.k = add(dst.k, 2*goarch.PtrSize)
				dst.e = add(dst.e, uintptr(t.ValueSize))
			}
		}
		// Unlink the overflow buckets & clear key/elem to help GC.
		if h.flags&oldIterator == 0 && t.Bucket.Pointers() {
			b := add(h.oldbuckets, oldbucket*uintptr(t.BucketSize))
			// Preserve b.tophash because the evacuation
			// state is maintained there.
			ptr := add(b, dataOffset)
			n := uintptr(t.BucketSize) - dataOffset
			memclrHasPointers(ptr, n)
		}
	}

	if oldbucket == h.nevacuate {
		advanceEvacuationMark(h, t, newbit)
	}
}
