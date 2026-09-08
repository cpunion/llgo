//go:build wasm && llgo.wasm.gc.linear && !llgo.wasm.workers && !llgo.wasi_threads

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/clite/bitcast"
)

// Buckets are permanent cumulative statistics; samples are weak entries for
// currently live sampled objects. Neither a raw object address nor a random
// stack hash may be stored here: conservative scanning would retain garbage.
type wasmMemBucket struct {
	// Put the int64-aligned record first: wasm32 Go and LLVM disagree about
	// int64 field alignment after an odd number of pointer-sized fields.
	record MemProfileRecord
	next   *wasmMemBucket
	size   uintptr
	nstack int
}

type wasmMemSample struct {
	next   *wasmMemSample
	bucket *wasmMemBucket
	// Match finalizers' weak-address encoding: do not store the sampled
	// object's raw address. Conservative false positives remain possible.
	key uintptr
}

const wasmMemBucketSlots = 512
const wasmMemSampleSlots = 1024

var wasmMemBuckets [wasmMemBucketSlots]*wasmMemBucket
var wasmMemSamples [wasmMemSampleSlots]*wasmMemSample
var wasmMemFreeSamples *wasmMemSample
var wasmMemRemaining uintptr
var wasmMemLastRate int

// Sampling is paused during capture and cannot schedule another goroutine.
// Reuse this single-worker scratch rather than heap-allocating a stack array
// on every sampled allocation. Clear it after capture so it is not a root.
var wasmMemStack [32]uintptr

func recordWasmMemProfileAlloc(ptr unsafe.Pointer, size uintptr) {
	if !wasmMemProfileEnabled || memProfilePauseDepth != 0 || memProfileRatePtr == nil || size == 0 {
		return
	}
	rate := *memProfileRatePtr
	if rate != wasmMemLastRate {
		wasmMemRemaining = 0
		wasmMemLastRate = rate
	}
	if rate <= 0 {
		return
	}
	if rate != 1 {
		if wasmMemRemaining == 0 {
			wasmMemRemaining = wasmMemNextThreshold(rate)
		}
		if size < wasmMemRemaining {
			wasmMemRemaining -= size
			return
		}
		// The exponential distribution is memoryless. Restart at the end of
		// this allocation and count it once even if it spans several events.
		wasmMemRemaining = wasmMemNextThreshold(rate)
	}
	MemProfilePause()
	sampleWasmMemProfile(ptr, size)
	wasmMemStack = [32]uintptr{}
	MemProfileResume()
}

//go:noinline
func sampleWasmMemProfile(ptr unsafe.Pointer, size uintptr) {
	pcs := wasmMemStack[:]
	n := memProfileCaptureStack(pcs)
	if n == 0 {
		return
	}
	// Retain only the low slot bits, not the random full-width hash.
	slot := size & (wasmMemBucketSlots - 1)
	for _, pc := range pcs[:n] {
		slot = (slot*33 + pc) & (wasmMemBucketSlots - 1)
	}
	var bucket *wasmMemBucket
	for b := wasmMemBuckets[slot]; b != nil; b = b.next {
		if b.size != size || b.nstack != n {
			continue
		}
		equal := true
		for i, pc := range pcs[:n] {
			if b.record.Stack0[i] != pc {
				equal = false
				break
			}
		}
		if equal {
			bucket = b
			break
		}
	}
	if bucket == nil {
		bucket = &wasmMemBucket{next: wasmMemBuckets[slot], size: size, nstack: n}
		copy(bucket.record.Stack0[:], pcs[:n])
		wasmMemBuckets[slot] = bucket
	}
	sample := wasmMemFreeSamples
	if sample == nil {
		sample = new(wasmMemSample)
	} else {
		wasmMemFreeSamples = sample.next
	}
	key := ^uintptr(ptr)
	sampleSlot := wasmMemSampleSlot(key)
	sample.key = key
	sample.bucket = bucket
	sample.next = wasmMemSamples[sampleSlot]
	wasmMemSamples[sampleSlot] = sample
	bucket.record.AllocObjects++
	bucket.record.AllocBytes += int64(size)
}

func wasmMemSampleSlot(key uintptr) uintptr {
	return (key >> 4) & (wasmMemSampleSlots - 1)
}

// memProfileFree runs under gcMutex from both Free and sweep. It never
// allocates or yields, and remains active when the user sets the rate to zero
// or pauses sampling: historical live samples still need their frees recorded.
func memProfileFree(address uintptr) {
	if !wasmMemProfileEnabled {
		return
	}
	key := ^address
	link := &wasmMemSamples[wasmMemSampleSlot(key)]
	for sample := *link; sample != nil; sample = *link {
		if sample.key != key {
			link = &sample.next
			continue
		}
		*link = sample.next
		bucket := sample.bucket
		bucket.record.FreeObjects++
		bucket.record.FreeBytes += int64(bucket.size)
		sample.key = 0
		sample.bucket = nil
		sample.next = wasmMemFreeSamples
		wasmMemFreeSamples = sample
		return
	}
}

func wasmMemProfile(p []MemProfileRecord, inuseZero bool) (n int, ok bool) {
	// This walk neither allocates nor yields. In a single worker it is a
	// coherent snapshot, including while sampling is paused for pprof copying.
	for _, head := range wasmMemBuckets {
		for bucket := head; bucket != nil; bucket = bucket.next {
			if inuseZero || bucket.record.InUseObjects() != 0 {
				n++
			}
		}
	}
	if len(p) < n {
		return n, false
	}
	i := 0
	for _, head := range wasmMemBuckets {
		for bucket := head; bucket != nil; bucket = bucket.next {
			if inuseZero || bucket.record.InUseObjects() != 0 {
				p[i] = bucket.record
				i++
			}
		}
	}
	return n, true
}

func wasmMemNextThreshold(rate int) uintptr {
	// Match Go's Poisson sampling rather than a bounded uniform interval.
	// Reuse the runtime RNG instead of retaining another random scalar that
	// the conservative collector could mistake for an object address.
	r := uint64(fastrand()&0x7fffffff)<<22 | uint64(fastrand()&0x3fffff)
	u := float64(r) / (1 << 53)
	if u < 1e-12 {
		u = 1e-12
	}
	next := -wasmMemLog(u) * float64(rate)
	if next < 1 {
		return 1
	}
	if max := float64(^uintptr(0)); next >= max {
		return ^uintptr(0)
	}
	return uintptr(next)
}

// wasmMemLog follows the exponent split and short atanh approximation used
// by the native sampled profiler; its error is below the sampling noise.
func wasmMemLog(u float64) float64 {
	const ln2 = 0.6931471805599453
	bits := uint64(bitcast.FromFloat64(u))
	exponent := int((bits>>52)&0x7ff) - 1023
	mantissa := (bits &^ (uint64(0x7ff) << 52)) | (uint64(1023) << 52)
	m := bitcast.ToFloat64(int64(mantissa))
	z := (m - 1) / (m + 1)
	z2 := z * z
	return float64(exponent)*ln2 + 2*z*(1+z2/3+z2*z2/5+z2*z2*z2/7)
}
