package main

import (
	"sync"
	"time"
	_ "unsafe"
)

//go:linkname gmpForTesting github.com/xgo-dev/llgo/runtime/internal/runtime.GMPForTesting
func gmpForTesting() (goid, parentGoid uint64, mid int64, pid int32, gstatus, pstatus uint32, linked bool)

//go:linkname gStateForTesting github.com/xgo-dev/llgo/runtime/internal/runtime.GStateForTesting
func gStateForTesting() (count uint64, mainExited bool)

func main() {
	_, _, mainMID, _, _, _, linked := gmpForTesting()
	if !linked {
		panic("main G/M/P is not linked")
	}

	const workers = 4
	var wait sync.WaitGroup
	ids := make(chan int64, workers)
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, mid, _, _, _, linked := gmpForTesting()
			if !linked {
				panic("worker G/M/P is not linked")
			}
			ids <- mid
		}()
	}
	wait.Wait()
	close(ids)

	seen := map[int64]bool{mainMID: true}
	for mid := range ids {
		if seen[mid] {
			panic("goroutines reused a host M")
		}
		seen[mid] = true
	}
	if len(seen) != workers+1 {
		panic("not all WASI pthreads ran")
	}

	// WaitGroup completion precedes mexit. Wait for the host threads to leave
	// before starting the next batch, so the probe also exercises retirement
	// and reuse within WAMR's bounded thread limit.
	waitForMainThreadOnly()
	for round := 0; round < 12; round++ {
		var batch sync.WaitGroup
		values := make(chan *threadValue, workers)
		for i := 0; i < workers; i++ {
			batch.Add(1)
			go func(index int) {
				defer batch.Done()
				inner := &threadValue{value: round*workers + index}
				values <- &threadValue{value: inner.value + 1, next: inner}
			}(i)
		}
		batch.Wait()
		close(values)
		seenValues := make(map[int]bool, workers)
		for value := range values {
			if value == nil || value.next == nil || value.value != value.next.value+1 || seenValues[value.next.value] {
				panic("cross-thread pointer handoff failed")
			}
			seenValues[value.next.value] = true
		}
		for i := 0; i < workers; i++ {
			if !seenValues[round*workers+i] {
				panic("missing cross-thread pointer handoff")
			}
		}
		waitForMainThreadOnly()
	}
	println("wasi threads ok")
}

type threadValue struct {
	value int
	next  *threadValue
}

func waitForMainThreadOnly() {
	deadline := time.Now().Add(5 * time.Second)
	for {
		count, exited := gStateForTesting()
		if count == 1 && !exited {
			return
		}
		if time.Now().After(deadline) {
			panic("WASI pthreads did not retire")
		}
	}
}
