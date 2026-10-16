package runtime

import (
	"sync"
	"sync/atomic"
	"testing"
)

func coroRuntimeAbort(message string) { panic(message) }

func TestStoreIndependentIdentityAndMemo(t *testing.T) {
	var first, second callerLocationStore
	left := CallerFrame{Entry: 42, Function: "first", File: "caller.go", Line: 10, StartLine: 1}
	right := left
	right.Function = "second"
	a := first.captureFrame(left, callersPCValue)
	b := second.captureFrame(right, callersPCValue)
	if a.PC == b.PC || a.PC == 0 || b.PC == 0 {
		t.Fatal("G-local stores reused one process PC for different frames")
	}
	for _, want := range []CallerFrame{a, b} {
		got, ok := syntheticFrameForPC(want.PC)
		if !ok || got.PC != want.PC || !sameSyntheticFrame(got, want) {
			t.Fatalf("process lookup got=%+v want=%+v found=%t", got, want, ok)
		}
	}
	if again := second.captureFrame(left, callersPCValue); again.PC != a.PC {
		t.Fatal("same content acquired a different PC in a second store")
	}
	pc := first.capturePC(&left, callerPCValue)
	if first.capturePC(&left, callerPCValue) != pc || left.captured == 0 {
		t.Fatal("unchanged frame lost its O(1) memoized PC")
	}
	left.Line++
	left.captured = 0 // The existing source-location updater owns invalidation.
	if first.capturePC(&left, callerPCValue) == pc {
		t.Fatal("a changed source line reused the earlier PC")
	}
	if earlier, ok := syntheticFrameForPC(pc); !ok || earlier.Line != 10 {
		t.Fatal("later capture changed immutable metadata for an issued PC")
	}
}

func TestHashCollisionAndInvalidPC(t *testing.T) {
	first := CallerFrame{Entry: 1234, Function: "collision", File: "caller.go", Line: 1}
	second := first
	for second.Line = 2; second.Line < 10000 && callerPCBucket(second) != callerPCBucket(first); second.Line++ {
	}
	if second.Line == 10000 {
		t.Fatal("fixture did not find a bucket collision")
	}
	a, b := internSyntheticCallerFrame(first), internSyntheticCallerFrame(second)
	if a == b || a.sequence == b.sequence || a.frame.PC == b.frame.PC {
		t.Fatal("colliding different frames aliased")
	}
	for _, node := range []*callerSyntheticFrame{a, b} {
		if got, ok := syntheticFrameForPC(node.frame.PC); !ok || !sameSyntheticFrame(got, node.frame) {
			t.Fatal("collision lookup returned another frame")
		}
	}
	// runtime.Caller's panic-splice lane uses the conventional return-PC - 1
	// adjustment. It must decode the same process-wide identity as Callers.
	returnPC := a.frame.PC&^callerPCMask | callersPCValue
	if got, ok := syntheticFrameForPC(returnPC - 1); !ok || !sameSyntheticFrame(got, a.frame) || got.PC != returnPC-1 {
		t.Fatal("adjusted panic-splice PC lost its immutable frame identity")
	}
	if _, ok := syntheticFrameForPC(a.frame.PC &^ callerPCMask); ok {
		t.Fatal("aligned non-synthetic PC tag was accepted")
	}
	if _, ok := syntheticFrameForPC(0); ok {
		t.Fatal("nil PC was accepted")
	}
}

func TestConcurrentPublishAndLookup(t *testing.T) {
	var identities [16]uintptr
	var group sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		group.Go(func() {
			for iteration := 0; iteration < 100; iteration++ {
				index := (worker + iteration) % len(identities)
				frame := CallerFrame{Entry: uintptr(index + 9000), Function: "parallel", File: "race.go", Line: index + 1}
				pc := internSyntheticCallerFrame(frame).frame.PC
				atomic.CompareAndSwapUintptr(&identities[index], 0, pc)
				if atomic.LoadUintptr(&identities[index]) != pc {
					t.Error("concurrent publishers produced different identities for one frame")
				}
				if got, ok := syntheticFrameForPC(pc); !ok || !sameSyntheticFrame(got, frame) {
					t.Error("published PC was visible before its immutable metadata")
				}
			}
		})
	}
	group.Wait()
}

func TestSequenceExhaustionDoesNotWrap(t *testing.T) {
	limit := (callerPCSyntheticTop >> (2 + callerPCBucketBits)) - 1
	counter := limit - 1
	if nextCallerPCSequence(&counter) != limit {
		t.Fatal("last valid sequence was rejected")
	}
	defer func() {
		if recover() == nil || counter != limit {
			t.Fatal("exhaustion wrapped or aliased a published PC")
		}
	}()
	nextCallerPCSequence(&counter)
}
