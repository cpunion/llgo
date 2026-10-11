//go:build !nogc && !baremetal && !wasm

package runtime

import psync "github.com/xgo-dev/llgo/runtime/internal/sync"

var explicitGC struct {
	once      psync.Once
	mu        psync.Mutex
	work      psync.Cond
	done      psync.Cond
	requested uint64
	completed uint64
}

func initExplicitGC() {
	explicitGC.mu.Init(nil)
	explicitGC.work.Init(nil)
	explicitGC.done.Init(nil)
	go runExplicitGC()
}

func requestExplicitGC() {
	explicitGC.once.Do(initExplicitGC)
	explicitGC.mu.Lock()
	explicitGC.requested++
	request := explicitGC.requested
	explicitGC.work.Signal()
	for explicitGC.completed < request {
		explicitGC.done.Wait(&explicitGC.mu)
	}
	explicitGC.mu.Unlock()
}

func runExplicitGC() {
	markSystemGoroutine()
	for {
		explicitGC.mu.Lock()
		for explicitGC.completed == explicitGC.requested {
			explicitGC.work.Wait(&explicitGC.mu)
		}
		// Requests arriving after this point need another collection batch.
		// Completing the latest requested sequence would let them return
		// before a collection that started after their request.
		request := explicitGC.requested
		explicitGC.mu.Unlock()

		collectAndEnqueueFinalizers()
		// The extra cycle completes collector-side weak-pointer cleanup.
		// User finalizers and map/pool cleanup run on their existing threads.
		collectAndEnqueueFinalizers()

		explicitGC.mu.Lock()
		explicitGC.completed = request
		explicitGC.done.Broadcast()
		explicitGC.mu.Unlock()
	}
}
