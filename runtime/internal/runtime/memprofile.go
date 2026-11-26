package runtime

import "unsafe"

// MemProfileRecord describes allocations aggregated by size class.
type MemProfileRecord struct {
	ObjectSize                int64
	AllocBytes, FreeBytes     int64
	AllocObjects, FreeObjects int64
	Stack0                    [32]uintptr
}

func (r *MemProfileRecord) InUseBytes() int64 {
	return r.AllocBytes - r.FreeBytes
}

func (r *MemProfileRecord) InUseObjects() int64 {
	return r.AllocObjects - r.FreeObjects
}

func (r *MemProfileRecord) Stack() []uintptr {
	for i, pc := range r.Stack0 {
		if pc == 0 {
			return r.Stack0[:i]
		}
	}
	return r.Stack0[:]
}

const memProfileBucketCount = 27

var memProfileBucketSizes = [memProfileBucketCount]uintptr{
	16,
	32,
	64,
	128,
	256,
	512,
	1024,
	2048,
	4096,
	8192,
	16384,
	32768,
	65536,
	131072,
	262144,
	524288,
	1048576,
	2097152,
	4194304,
	8388608,
	16777216,
	33554432,
	67108864,
	134217728,
	268435456,
	536870912,
	1073741824,
}

func memProfileBucketIndex(size uintptr) int {
	if size <= 16 {
		return 0
	}
	x := uint64(size - 1)
	n := 0
	if x >= 1<<32 {
		x >>= 32
		n += 32
	}
	if x >= 1<<16 {
		x >>= 16
		n += 16
	}
	if x >= 1<<8 {
		x >>= 8
		n += 8
	}
	if x >= 1<<4 {
		x >>= 4
		n += 4
	}
	if x >= 1<<2 {
		x >>= 2
		n += 2
	}
	if x >= 1<<1 {
		n++
	}
	// n == floor(log2(size-1)) >= 4 because size >= 17.
	// Bucket 0 is 16 (2^4), bucket 1 is 32 (2^5), ..., bucket 26 is 2^30.
	idx := n - 3
	if idx >= memProfileBucketCount {
		return memProfileBucketCount - 1
	}
	return idx
}

func recordMemProfileAlloc(size uintptr) {
	recordMemProfileAllocHint(size, 0)
}

func recordMemProfileAllocHint(size, hint uintptr) {
	if size == 0 {
		return
	}
	memProfileAddObject(memProfileBucketIndex(size), hint)
}

func memProfileSizeClass(size uintptr) uintptr {
	idx := memProfileBucketIndex(size)
	return *(*uintptr)(unsafe.Add(unsafe.Pointer(&memProfileBucketSizes[0]), uintptr(idx)*unsafe.Sizeof(uintptr(0))))
}

func MemProfile(p []MemProfileRecord, inuseZero bool) (n int, ok bool) {
	for i := 0; i < memProfileBucketCount; i++ {
		if memProfileLoadObjects(i) != 0 {
			n++
		}
	}
	if len(p) < n {
		return n, false
	}
	j := 0
	for i := 0; i < memProfileBucketCount; i++ {
		objects := memProfileLoadObjects(i)
		if objects == 0 {
			continue
		}
		size := memProfileBucketSizes[i]
		p[j] = MemProfileRecord{
			ObjectSize:   int64(size),
			AllocBytes:   int64(uint64(size) * uint64(objects)),
			AllocObjects: int64(objects),
		}
		j++
	}
	return n, true
}
