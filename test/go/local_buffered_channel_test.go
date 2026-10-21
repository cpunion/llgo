// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"math"
	"runtime"
	"testing"
)

func TestLocalSingleSlotChannelValuesAndGC(t *testing.T) {
	values := make(chan uint64, 1)
	for i := uint64(0); i < 64; i++ {
		want := uint64(1<<63) | i*0x123456789
		values <- want
		if len(values) != 1 || cap(values) != 1 {
			t.Fatal("full single-slot channel length/capacity")
		}
		if i%16 == 0 {
			runtime.GC()
		}
		if got, ok := <-values; !ok || got != want || len(values) != 0 {
			t.Fatalf("received %#x, %t; want %#x", got, ok, want)
		}
	}
	values <- 0xfedcba9876543210
	close(values)
	if got, ok := <-values; !ok || got != 0xfedcba9876543210 {
		t.Fatalf("closed buffered receive = %#x, %t", got, ok)
	}
	if got, ok := <-values; ok || got != 0 {
		t.Fatalf("closed empty receive = %#x, %t", got, ok)
	}
}

func TestLocalSingleSlotChannelDirectionAndSignedValue(t *testing.T) {
	type numbers chan int32
	values := make(numbers, 1)
	sender := (chan<- int32)(values)
	receiver := (<-chan int32)(values)
	for i := int32(-32); i < 32; i++ {
		sender <- i
		if got := <-receiver; got != i {
			t.Fatalf("received %d, want %d", got, i)
		}
	}
}

func TestLocalSingleSlotChannelFloatBits(t *testing.T) {
	values := make(chan float64, 1)
	want := uint64(0x7ff8000000001234)
	values <- math.Float64frombits(want)
	if got := math.Float64bits(<-values); got != want {
		t.Fatalf("NaN payload = %#x, want %#x", got, want)
	}
	narrow := make(chan float32, 1)
	narrow <- math.Float32frombits(0x7fc01234)
	if got := math.Float32bits(<-narrow); got != 0x7fc01234 {
		t.Fatalf("float32 NaN payload = %#x", got)
	}
}

func TestLocalSingleSlotChannelClosedSend(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("send on closed private channel returned")
		}
		if text, ok := value.(error); !ok || text.Error() != "send on closed channel" {
			t.Fatalf("closed send panic = %v", value)
		}
	}()
	values := make(chan int, 1)
	close(values)
	values <- 1
}

func TestSingleSlotChannelCapturedByGoroutine(t *testing.T) {
	values := make(chan int, 1)
	go func() {
		for i := 1; i <= 1000; i++ {
			values <- i
		}
		close(values)
	}()
	sum := 0
	for value := range values {
		sum += value
	}
	if sum != 1000*1001/2 {
		t.Fatalf("shared channel sum = %d", sum)
	}
}
