package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	_ "unsafe"

	corort "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

//go:linkname run internal/synctest.Run
func run(func())

//go:linkname wait internal/synctest.Wait
func wait()

//go:linkname inBubble internal/synctest.IsInBubble
func inBubble() bool

var finalizerDone, cleanupDone chan struct{}

func checkCollectorCallback(done chan struct{}) {
	if inBubble() {
		panic("collector callback inherited bubble")
	}
	run(func() { time.Sleep(time.Hour) })
	close(done)
}

//go:noinline
func allocateFinalizedObject() {
	object := new([4096]byte)
	runtime.SetFinalizer(object, func(*[4096]byte) { checkCollectorCallback(finalizerDone) })
	runtime.KeepAlive(object)
}

//go:noinline
func allocateCleanupObject() {
	object := new([4096]byte)
	runtime.AddCleanup(object, func(int) { checkCollectorCallback(cleanupDone) }, 1)
	runtime.KeepAlive(object)
}

func checkCollectorScopes() {
	allocateFinalizedObject()
	allocateCleanupObject()
	finalized, cleaned := false, false
	for i := 0; i < 100 && (!finalized || !cleaned); i++ {
		runtime.GC()
		runtime.Gosched()
		select {
		case <-finalizerDone:
			finalized = true
		default:
		}
		select {
		case <-cleanupDone:
			cleaned = true
		default:
		}
	}
	if !finalized || !cleaned {
		panic("collector callbacks did not complete")
	}
}

func main() {
	if inBubble() {
		panic("outside bubble")
	}
	if _, active := corort.SynctestNow(); active {
		panic("ordinary task acquired fake time in a domain-enabled program")
	}
	detachedDone := make(chan struct{})
	finalizerDone, cleanupDone = make(chan struct{}), make(chan struct{})
	run(func() {
		if !inBubble() {
			panic("missing membership")
		}
		start := time.Now()
		if start.Unix() != 946684800 {
			panic("wrong fake epoch")
		}
		if now, active := corort.SynctestNow(); !active || now != start.UnixNano() {
			panic("enabled clock service lost the bubble epoch")
		}
		corort.GoOutsideExecutionDomain(func() {
			if inBubble() {
				panic("detached callback inherited bubble")
			}
			run(func() { time.Sleep(time.Hour) })
			close(detachedDone)
		})
		<-detachedDone
		if !inBubble() || !time.Now().Equal(start) {
			panic("detached nested Run changed parent bubble")
		}
		checkCollectorScopes()
		if !inBubble() || !time.Now().Equal(start) {
			panic("collector callbacks changed parent bubble")
		}
		calls := 0
		go func() {
			if !inBubble() {
				panic("child did not inherit membership")
			}
			calls++
			go func() { calls++ }()
		}()
		wait()
		if calls != 2 || !time.Now().Equal(start) {
			panic("Wait did not join children without advancing time")
		}
		ch := make(chan struct{})
		started, done := false, false
		go func() {
			started = true
			<-ch
			done = true
		}()
		wait()
		if !started || done || !time.Now().Equal(start) {
			panic("Wait did not observe durable channel block")
		}
		close(ch)
		wait()
		if !done {
			panic("channel completion lost its activity credit")
		}
		left, right := make(chan struct{}), make(chan struct{})
		selected := 0
		go func() {
			select {
			case <-left:
				selected = 1
			case <-right:
				selected = 2
			}
		}()
		wait()
		if selected != 0 {
			panic("select falsely ready")
		}
		close(right)
		wait()
		if selected != 2 {
			panic("select completion did not resume")
		}
		var mu sync.Mutex
		cond := sync.NewCond(&mu)
		condition, signaled := false, false
		go func() {
			mu.Lock()
			for !condition {
				cond.Wait()
			}
			signaled = true
			mu.Unlock()
		}()
		wait()
		if signaled {
			panic("Cond falsely ready")
		}
		mu.Lock()
		condition = true
		cond.Signal()
		mu.Unlock()
		wait()
		if !signaled {
			panic("Cond notification did not resume")
		}
		time.Sleep(48 * time.Hour)
		if time.Since(start) != 48*time.Hour {
			panic("Sleep did not advance fake time")
		}
		if now, active := corort.SynctestNow(); !active || now != start.Add(48*time.Hour).UnixNano() {
			panic("enabled clock service lost fake-time advancement")
		}
		before := time.Now()
		timer := time.NewTimer(time.Hour)
		if got := <-timer.C; !got.Equal(before.Add(time.Hour)) {
			panic("Timer did not use fake clock")
		}
		if timer.Stop() || timer.Reset(0) {
			panic("expired Timer remained active")
		}
		select {
		case <-timer.C:
		default:
			panic("Timer.Reset(0) did not publish a real ready value")
		}
		timer.Reset(time.Hour)
		if !timer.Stop() {
			panic("Timer.Stop lost active state")
		}
		tick := time.NewTicker(time.Minute)
		before = time.Now()
		for i := 1; i <= 3; i++ {
			if got := <-tick.C; !got.Equal(before.Add(time.Duration(i) * time.Minute)) {
				panic("Ticker deadline mismatch")
			}
		}
		tick.Stop()
		callback := false
		time.AfterFunc(time.Hour, func() {
			if !inBubble() {
				panic("AfterFunc did not inherit bubble")
			}
			callback = true
		})
		time.Sleep(time.Hour)
		wait()
		if !callback {
			panic("AfterFunc did not run")
		}
		callback = false
		time.AfterFunc(0, func() { callback = true })
		wait()
		if !callback {
			panic("AfterFunc(0) was not published before Wait")
		}
	})
	if inBubble() {
		panic("membership leaked")
	}
	fmt.Println("synctest Run/Wait/channel/select/Cond/Sleep/Timer/Ticker/AfterFunc ok")
}
