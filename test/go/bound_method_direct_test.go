package gotest

import (
	"runtime"
	"testing"
)

type directBoundReceiver struct{ N int }

func (r directBoundReceiver) Add(n int) int { return r.N + n }

type directBoundZeroReceiver struct{}

func (directBoundZeroReceiver) Add(n int) int { return 17 + n }

type directBoundGeneric[T ~int] struct{ N T }

func (r directBoundGeneric[T]) Add(n T) T { return r.N + n }

// A statically known MakeClosure remains directly callable when another use
// publishes that same captured method through the descriptor function ABI.
func TestBoundMethodDirectUsesCapturedReceiver(t *testing.T) {
	for _, procs := range []int{1, 4} {
		old := runtime.GOMAXPROCS(procs)
		func() {
			defer runtime.GOMAXPROCS(old)
			receiver := directBoundReceiver{11}
			bound := receiver.Add
			receiver.N = 31
			runtime.GC()
			if got := bound(3); got != 14 {
				t.Fatalf("captured receiver: %d, want 14", got)
			}
			done := make(chan int, 1)
			go func(fn func(int) int) { done <- fn(10) }(bound)
			if got := <-done; got != 21 {
				t.Fatalf("published receiver: %d, want 21", got)
			}
			if got := bound(5); got != 16 {
				t.Fatalf("receiver after publication: %d, want 16", got)
			}
			zero := directBoundZeroReceiver{}.Add
			if got := zero(3); got != 20 {
				t.Fatalf("zero receiver: %d, want 20", got)
			}
			go func(fn func(int) int) { done <- fn(4) }(zero)
			if got := <-done; got != 21 {
				t.Fatalf("published zero receiver: %d, want 21", got)
			}
			generic := directBoundGeneric[int]{23}
			genericBound := generic.Add
			generic.N = 91
			if got := genericBound(3); got != 26 {
				t.Fatalf("generic receiver: %d, want 26", got)
			}
			go func(fn func(int) int) { done <- fn(5) }(genericBound)
			if got := <-done; got != 28 {
				t.Fatalf("published generic receiver: %d, want 28", got)
			}
		}()
	}
}
