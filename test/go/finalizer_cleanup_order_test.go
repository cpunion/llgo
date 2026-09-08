//go:build go1.24

package gotest

import (
	"runtime"
	"testing"
)

type finalizerCleanupObject struct {
	value int
	pad   [64]byte // Do not depend on tiny-allocation batching.
}

var resurrectedCleanupObject *finalizerCleanupObject

func TestFinalizerBeforeInterleavedCleanups(t *testing.T) {
	finalized := make(chan int, 1)
	cleaned := make(chan int, 2)
	otherCleaned := make(chan int, 2)
	registerFinalizerForTest(func() {
		object := &finalizerCleanupObject{value: 42}
		runtime.AddCleanup(object, func(value int) { cleaned <- value }, 1)
		runtime.AddCleanup(new(finalizerCleanupObject), func(value int) { otherCleaned <- value }, 2)
		runtime.SetFinalizer(object, func(object *finalizerCleanupObject) {
			resurrectedCleanupObject = object
			finalized <- object.value
		})
		runtime.AddCleanup(new(finalizerCleanupObject), func(value int) { otherCleaned <- value }, 4)
		runtime.AddCleanup(object, func(value int) { cleaned <- value }, 3)
	})
	defer func() { resurrectedCleanupObject = nil }()
	waitForFinalizerValue(t, finalized, 42)
	for range 3 {
		runGCWithTimeout(t)
	}
	select {
	case value := <-cleaned:
		t.Fatalf("cleanup %d ran while the finalizer-resurrected object was reachable", value)
	default:
	}
	resurrectedCleanupObject = nil
	// Callback order within each cleanup group is unspecified. Check sums and
	// channel exhaustion so all records, including nonadjacent ones, run once.
	for _, group := range []struct {
		channel chan int
		want    int
	}{{cleaned, 4}, {otherCleaned, 6}} {
		values := make(chan int, 1)
		go func() { values <- <-group.channel + <-group.channel }()
		waitForFinalizerValue(t, values, group.want)
		select {
		case value := <-group.channel:
			t.Fatalf("duplicate cleanup %d", value)
		default:
		}
	}
}
