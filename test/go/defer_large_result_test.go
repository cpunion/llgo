package gotest

import (
	"runtime"
	"testing"
)

type deferredLargeResult [131073]byte

var deferredLargeEffects int

//go:noinline
func produceDeferredLargeResult(id int) deferredLargeResult {
	runtime.Gosched()
	deferredLargeEffects = deferredLargeEffects*10 + id
	var result deferredLargeResult
	result[0], result[len(result)-1] = byte(id), byte(id+1)
	return result
}

//go:noinline
func deferLargeStatic() {
	defer produceDeferredLargeResult(1)
	defer produceDeferredLargeResult(2)
}

//go:noinline
func deferLargeDynamic(f func(int) deferredLargeResult) {
	for id := 1; id <= 3; id++ {
		defer f(id)
	}
}

func TestDeferredLargeResultDiscard(t *testing.T) {
	deferredLargeEffects = 0
	deferLargeStatic()
	if deferredLargeEffects != 21 {
		t.Fatalf("static deferred effects = %d, want 21", deferredLargeEffects)
	}
	deferredLargeEffects = 0
	deferLargeDynamic(produceDeferredLargeResult)
	if deferredLargeEffects != 321 {
		t.Fatalf("dynamic loop deferred effects = %d, want 321", deferredLargeEffects)
	}
}

func TestDeferredLargeResultDiscardDuringPanic(t *testing.T) {
	deferredLargeEffects = 0
	recovered := false
	func() {
		defer func() {
			recovered = recover() == "large-result"
		}()
		defer produceDeferredLargeResult(4)
		panic("large-result")
	}()
	if !recovered || deferredLargeEffects != 4 {
		t.Fatalf("deferred panic recovered=%t effects=%d, want true/4", recovered, deferredLargeEffects)
	}
}
