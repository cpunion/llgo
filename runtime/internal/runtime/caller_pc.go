package runtime

import (
	"sync/atomic"
	"unsafe"
)

// Logical PCs are process-wide identities, just like native text addresses.
// G-local sequence numbers alias in the shared symbol caches and cannot be
// interpreted after a caller passes its PCs to another G. Only immutable
// metadata is shared here; live shadow stacks remain G-local.
const callerPCBucketBits = 6
const callerPCBucketCount = 1 << callerPCBucketBits

type callerSyntheticFrame struct {
	next     *callerSyntheticFrame
	sequence uintptr
	frame    CallerFrame
}

var callerPCBuckets [callerPCBucketCount]unsafe.Pointer
var callerPCSequences [callerPCBucketCount]uintptr

func callerPCBucket(frame CallerFrame) uintptr {
	hash := frame.Entry ^ uintptr(frame.Line)*0x9e3779b1 ^ uintptr(frame.StartLine)*0x85ebca6b
	hash ^= uintptr(len(frame.Function))<<4 ^ uintptr(len(frame.File))<<8
	hash ^= hash >> 16
	hash *= 0x7feb352d
	hash ^= hash >> 15
	return hash & (callerPCBucketCount - 1)
}

func nextCallerPCSequence(counter *uintptr) uintptr {
	// Reserve the final counter round, so no encoded ID can map to PC base 0.
	limit := (callerPCSyntheticTop >> (2 + callerPCBucketBits)) - 1
	for {
		previous := atomic.LoadUintptr(counter)
		if previous >= limit {
			coroRuntimeAbort("logical caller PC namespace exhausted")
			return 0
		}
		if atomic.CompareAndSwapUintptr(counter, previous, previous+1) {
			return previous + 1
		}
	}
}

func internSyntheticCallerFrame(frame CallerFrame) *callerSyntheticFrame {
	frame.captured = 0
	bucket := callerPCBucket(frame)
	var candidate *callerSyntheticFrame
	for {
		head := atomic.LoadPointer(&callerPCBuckets[bucket])
		for node := (*callerSyntheticFrame)(head); node != nil; node = node.next {
			if sameSyntheticFrame(node.frame, frame) {
				return node
			}
		}
		if candidate == nil {
			candidate = &callerSyntheticFrame{frame: frame}
		}
		candidate.sequence = nextCallerPCSequence(&callerPCSequences[bucket])
		candidate.next = (*callerSyntheticFrame)(head)
		id := candidate.sequence<<callerPCBucketBits | bucket
		candidate.frame.PC = callerSyntheticPCBase(id) | callerPCValue
		if atomic.CompareAndSwapPointer(&callerPCBuckets[bucket], head, unsafe.Pointer(candidate)) {
			return candidate
		}
		// A failed CAS has not published candidate or its PC. Reuse that private
		// allocation; another publisher may already have interned this frame.
	}
}

func syntheticFrameForPC(pc uintptr) (CallerFrame, bool) {
	// Besides Caller (tag 1) and Callers (tag 3), symbolization accepts the
	// conventional return-PC - 1 (tag 2), used by panic-spliced Caller and
	// FuncForPC callers. All three name the same immutable registry entry.
	if pc&callerPCMask == 0 {
		return CallerFrame{}, false
	}
	id := callerSyntheticPCSequence(pc)
	sequence := id >> callerPCBucketBits
	if sequence == 0 {
		return CallerFrame{}, false
	}
	bucket := id & (callerPCBucketCount - 1)
	for node := (*callerSyntheticFrame)(atomic.LoadPointer(&callerPCBuckets[bucket])); node != nil; node = node.next {
		if node.sequence == sequence {
			frame := node.frame
			frame.PC = pc
			if frame.Entry == 0 {
				frame.Entry = pc
			}
			return frame, true
		}
	}
	return CallerFrame{}, false
}
