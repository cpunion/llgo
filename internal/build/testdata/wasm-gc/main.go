package main

import "runtime"

type payload struct {
	value uint64
}

var (
	globalRoot *payload
	garbage    *payload
	liveChunks [][]byte
)

func main() {
	testReadMemStatsNil()
	if testAlignedAlloc() == 0 {
		panic("aligned allocation failed")
	}
	testRoots()
	testRecoveredRootChain()
	testReclamation()
	testHeapGrowth()
	println("wasm gc ok")
}

//go:noinline
func usePayload(*payload) {}

//go:noinline
func panicWithRoot(value *payload) {
	usePayload(value)
	panic("root-chain unwind")
}

//go:noinline
func clobberStack(depth int, value uint64) uint64 {
	var words [32]uint64
	for i := range words {
		words[i] = value + uint64(i)
	}
	if depth != 0 {
		return words[depth%len(words)] + clobberStack(depth-1, value+1)
	}
	return words[0]
}

func testRecoveredRootChain() {
	live := &payload{value: 0xabcdef01}
	func() {
		defer func() {
			if recover() == nil {
				panic("panic was not recovered")
			}
		}()
		panicWithRoot(live)
	}()

	_ = clobberStack(32, 1)
	runtime.GC()
	if live.value != 0xabcdef01 {
		panic("root chain was not restored after recover")
	}
}

func testReadMemStatsNil() {
	panicked := false
	func() {
		defer func() {
			panicked = recover() != nil
		}()
		runtime.ReadMemStats(nil)
	}()
	if !panicked {
		panic("runtime.ReadMemStats(nil) did not panic")
	}
}

func testRoots() {
	globalRoot = &payload{value: 0x12345678}
	runtime.GC()
	if globalRoot.value != 0x12345678 {
		panic("global root was not retained")
	}
	globalRoot = nil
}

//go:noinline
func allocateGarbage() {
	for i := 0; i < 1024; i++ {
		garbage = &payload{value: uint64(i)}
	}
	garbage = nil
}

func testReclamation() {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	allocateGarbage()
	runtime.GC()

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.TotalAlloc <= before.TotalAlloc || after.Mallocs <= before.Mallocs {
		panic("allocation statistics did not advance")
	}
	if after.Frees <= before.Frees {
		panic("unreachable objects were not reclaimed")
	}
}

func testHeapGrowth() {
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	const chunkSize = 1 << 20
	chunkCount := int(before.HeapSys/chunkSize) + 8
	if chunkCount > 128 {
		panic("initial heap is too large for the bounded growth test")
	}
	liveChunks = make([][]byte, 0, chunkCount)
	for i := 0; i < chunkCount; i++ {
		chunk := make([]byte, chunkSize)
		chunk[0] = byte(i + 1)
		chunk[len(chunk)-1] = byte(i + 2)
		liveChunks = append(liveChunks, chunk)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapSys <= before.HeapSys {
		panic("heap did not grow")
	}
	runtime.GC()
	for i, chunk := range liveChunks {
		if chunk[0] != byte(i+1) || chunk[len(chunk)-1] != byte(i+2) {
			panic("live object was corrupted during heap growth")
		}
	}
	liveChunks = nil
}
