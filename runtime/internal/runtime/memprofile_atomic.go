//go:build !baremetal

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/sync/atomic"
)

type memProfileCounter = uint64

const memProfileShardCount = 16

// memProfileShard pads each per-shard bucket table to 256 bytes (2 x 128B
// cache lines on Apple Silicon) so concurrent allocations across Ps do not
// contend on a single global cache line.
type memProfileShard struct {
	objects [memProfileBucketCount]memProfileCounter
	_       [5]memProfileCounter
}

var memProfileShards [memProfileShardCount]memProfileShard

func memProfileAddObject(bucket int, hint uintptr) {
	if uint(bucket) >= memProfileBucketCount {
		bucket = memProfileBucketCount - 1
	}
	shard := ((hint >> 12) ^ (hint >> 18)) & (memProfileShardCount - 1)
	base := unsafe.Pointer(&memProfileShards[0].objects[0])
	cell := (*memProfileCounter)(unsafe.Add(
		base,
		shard*unsafe.Sizeof(memProfileShard{})+uintptr(bucket)*unsafe.Sizeof(memProfileCounter(0)),
	))
	atomic.Add(cell, memProfileCounter(1))
}

func memProfileLoadObjects(bucket int) memProfileCounter {
	if uint(bucket) >= memProfileBucketCount {
		return 0
	}
	var total memProfileCounter
	for shard := 0; shard < memProfileShardCount; shard++ {
		total += atomic.Load(&memProfileShards[shard].objects[bucket])
	}
	return total
}
