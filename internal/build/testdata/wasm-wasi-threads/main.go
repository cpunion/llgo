package main

import (
	"sync"
	"time"
	_ "unsafe"
)

const LLGoFiles = "_wrap/stack.c"

//go:linkname workerStackBounds C.llgo_wasi_worker_stack_bounds
func workerStackBounds() int32

//go:linkname gmpForTesting github.com/xgo-dev/llgo/runtime/internal/runtime.GMPForTesting
func gmpForTesting() (goid, parentGoid uint64, mid int64, pid int32, gstatus, pstatus uint32, linked bool)

//go:linkname gStateForTesting github.com/xgo-dev/llgo/runtime/internal/runtime.GStateForTesting
func gStateForTesting() (count uint64, mainExited bool)

func main() {
	_, _, mainMID, _, _, _, linked := gmpForTesting()
	if !linked {
		panic("main G/M/P is not linked")
	}
	// Start the timer service before measuring G retirement. It is a
	// persistent goroutine, so its count is part of the baseline.
	time.Sleep(time.Millisecond)
	baseline, _ := gStateForTesting()

	const workers = 4
	var wait sync.WaitGroup
	ids := make(chan int64, workers)
	release := make(chan struct{})
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if workerStackBounds() != 1 {
				panic("WAMR pthread C stack bounds unavailable")
			}
			_, _, mid, _, _, _, linked := gmpForTesting()
			if !linked {
				panic("worker G/M/P is not linked")
			}
			ids <- mid
			<-release
			time.Sleep(time.Millisecond)
		}()
	}
	seen := map[int64]bool{mainMID: true}
	for i := 0; i < workers; i++ {
		mid := <-ids
		if seen[mid] {
			panic("goroutines reused a host M")
		}
		seen[mid] = true
	}
	close(release)
	wait.Wait()
	if len(seen) != workers+1 {
		panic("not all WASI pthreads ran")
	}

	// WaitGroup completion precedes mexit. Wait for the host threads to leave
	// before starting the next batch, so the probe also exercises retirement
	// and reuse within WAMR's bounded thread limit.
	waitForBaseline(baseline)
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
		waitForBaseline(baseline)
	}
	println("wasi threads ok")
}

type threadValue struct {
	value int
	next  *threadValue
}

func waitForBaseline(baseline uint64) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		count, exited := gStateForTesting()
		if count == baseline && !exited {
			return
		}
		if time.Now().After(deadline) {
			panic("WASI pthreads did not retire")
		}
		time.Sleep(time.Millisecond)
	}
}
