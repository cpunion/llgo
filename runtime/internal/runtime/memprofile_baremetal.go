//go:build baremetal

package runtime

import "unsafe"

type memProfileCounter = uintptr

var memProfileObjects [memProfileBucketCount]memProfileCounter

func memProfileAddObject(bucket int, _ uintptr) {
	if uint(bucket) >= memProfileBucketCount {
		bucket = memProfileBucketCount - 1
	}
	cell := (*memProfileCounter)(unsafe.Add(
		unsafe.Pointer(&memProfileObjects[0]),
		uintptr(bucket)*unsafe.Sizeof(memProfileCounter(0)),
	))
	*cell = *cell + 1
}

func memProfileLoadObjects(bucket int) memProfileCounter {
	if uint(bucket) >= memProfileBucketCount {
		return 0
	}
	return memProfileObjects[bucket]
}
