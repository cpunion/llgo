package main

import (
	"sync"
	"time"
	_ "unsafe"
)

//go:linkname gmpForTesting github.com/xgo-dev/llgo/runtime/internal/runtime.GMPForTesting
func gmpForTesting() (goid, parentGoid uint64, mid int64, pid int32, gstatus, pstatus uint32, linked bool)

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
			time.Sleep(time.Millisecond)
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
	println("wasi threads ok")
}
