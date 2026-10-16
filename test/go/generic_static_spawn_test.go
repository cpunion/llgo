package gotest

import "testing"

func genericStaticSend[T any](ch chan<- T, value T) { ch <- value }

func genericStaticLaunch[T any](ch chan<- T, value T) {
	go func(ch chan<- T, value T) { ch <- value }(ch, value)
}

type genericStaticSender[T any] struct{}

func (genericStaticSender[T]) launch(ch chan<- T, value T) {
	go func(ch chan<- T, value T) { ch <- value }(ch, value)
}

func TestGenericContextFreeStaticSpawn(t *testing.T) {
	ch := make(chan int, 3)
	go genericStaticSend[int](ch, 1)
	genericStaticLaunch[int](ch, 2)
	genericStaticSender[int]{}.launch(ch, 4)
	if got := <-ch | <-ch | <-ch; got != 7 {
		t.Fatalf("generic spawn results: %d", got)
	}
}
