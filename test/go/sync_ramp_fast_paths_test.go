// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSyncPoolLocalCacheAndSpill(t *testing.T) {
	var created int
	var mu sync.Mutex
	p := &sync.Pool{
		New: func() any {
			mu.Lock()
			created++
			id := created
			mu.Unlock()
			return &struct{ id int }{id: id}
		},
	}

	// Put >16 items on one goroutine to exercise private slot, 16-slot local
	// cache, and spill into shared poolChain.
	const count = 48
	ptrs := make([]*struct{ id int }, count)
	for i := 0; i < count; i++ {
		ptrs[i] = &struct{ id int }{id: i + 1}
		p.Put(ptrs[i])
	}
	p.Put(nil) // nil Put must be ignored without panicking

	seen := make(map[int]bool, count)
	for i := 0; i < count; i++ {
		v := p.Get().(*struct{ id int })
		if v == nil || v.id < 1 || v.id > count {
			t.Fatalf("unexpected pooled item: %+v", v)
		}
		seen[v.id] = true
	}
	if len(seen) == 0 {
		t.Fatal("expected pooled items to be retrieved")
	}

	// Concurrent Get/Put across multiple goroutines.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				v := p.Get().(*struct{ id int })
				if v == nil || v.id <= 0 {
					t.Errorf("invalid item from concurrent Get: %+v", v)
					return
				}
				p.Put(v)
			}
		}()
	}
	wg.Wait()
}

func TestCancelCtxDoneFastPathAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// First call initializes lazy done channel (slow path with mutex + defer).
	ch1 := ctx.Done()
	if ch1 == nil {
		t.Fatal("ctx.Done() returned nil")
	}
	// Repeated calls hit the pre-defer fast path (atomic.Value.Load + type assert).
	for i := 0; i < 64; i++ {
		if got := ctx.Done(); got != ch1 {
			t.Fatalf("ctx.Done() mismatch on iter %d", i)
		}
	}
	select {
	case <-ch1:
		t.Fatal("ctx.Done() closed before cancel")
	default:
	}

	// Verify concurrent Done() calls while canceling.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = ctx.Done()
			}
		}()
	}
	cancel()
	wg.Wait()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("ctx.Done() not closed after cancel")
	}
}

type syncRampReflectSample struct {
	ID      int
	Name    string
	private int
	Fn      func() int
}

func TestReflectValueFieldElemTypeFastPaths(t *testing.T) {
	sample := &syncRampReflectSample{
		ID:      42,
		Name:    "hello-world-sync-ramp-test",
		private: 99,
		Fn:      func() int { return 7 },
	}
	pv := reflect.ValueOf(sample)
	if pv.Type().Kind() != reflect.Pointer {
		t.Fatalf("pv.Type().Kind() = %v, want Pointer", pv.Type().Kind())
	}
	sv := pv.Elem()
	if sv.Kind() != reflect.Struct {
		t.Fatalf("sv.Kind() = %v, want Struct", sv.Kind())
	}
	if sv.Field(0).Int() != 42 {
		t.Fatalf("Field(0) = %d, want 42", sv.Field(0).Int())
	}
	if sv.Field(1).String() != "hello-world-sync-ramp-test" {
		t.Fatalf("Field(1) = %q", sv.Field(1).String())
	}
	if sv.Field(2).Int() != 99 {
		t.Fatalf("Field(2) = %d, want 99", sv.Field(2).Int())
	}
	// Func field exercises the slow bailout path (closureOf).
	if fn := sv.Field(3); fn.Kind() != reflect.Func || fn.IsNil() {
		t.Fatalf("Field(3) = %v, want non-nil Func", fn)
	}

	// Out-of-range Field must still panic cleanly.
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on out-of-range Field")
			}
		}()
		_ = sv.Field(99)
	}()

	// Nil pointer Elem returns zero Value.
	var nilPtr *syncRampReflectSample
	if zero := reflect.ValueOf(nilPtr).Elem(); zero.IsValid() {
		t.Fatalf("nil pointer Elem should be invalid, got %v", zero)
	}
}

func TestBufioAndJSONSyncRampPaths(t *testing.T) {
	// Bufio Writer.Write / WriteString fast path (fits in buffer) + slow flush.
	var dst bytes.Buffer
	bw := bufio.NewWriterSize(&dst, 32)
	if n, err := bw.WriteString("hello "); err != nil || n != 6 {
		t.Fatalf("WriteString = (%d, %v)", n, err)
	}
	if n, err := bw.Write([]byte("world")); err != nil || n != 5 {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	big := strings.Repeat("abcdefghij", 10)
	if n, err := bw.WriteString(big); err != nil || n != len(big) {
		t.Fatalf("large WriteString = (%d, %v)", n, err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	want := "hello world" + big
	if dst.String() != want {
		t.Fatalf("bufio writer output mismatch: len %d vs %d", dst.Len(), len(want))
	}

	// Bufio Reader.Read / ReadSlice fast path + EOF.
	br := bufio.NewReaderSize(strings.NewReader("line1\nline2\nlong-" + strings.Repeat("z", 64)), 32)
	line, err := br.ReadSlice('\n')
	if err != nil || string(line) != "line1\n" {
		t.Fatalf("ReadSlice(1) = (%q, %v)", line, err)
	}
	buf := make([]byte, 6)
	if n, err := io.ReadFull(br, buf); err != nil || n != 6 || string(buf) != "line2\n" {
		t.Fatalf("ReadFull = (%d, %q, %v)", n, buf, err)
	}

	// JSON round-trip covering short strings, 48-byte strings, numbers, bools,
	// null, nested arrays/objects, and malformed input error handling.
	type payload struct {
		ID     int      `json:"id"`
		Name   string   `json:"name"`
		Long   string   `json:"long"`
		Active bool     `json:"active"`
		Opt    *string  `json:"opt"`
		Tags   []string `json:"tags"`
	}
	in := payload{
		ID:     123456,
		Name:   "medium-length-identifier-for-bounded-loop",
		Long:   strings.Repeat("L", 200),
		Active: true,
		Tags:   []string{"alpha", "beta", "gamma"},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out payload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("JSON round-trip mismatch:\n got %+v\nwant %+v", out, in)
	}
	if err := json.Unmarshal([]byte(`{"id": "bad"`), &out); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

//go:noinline
func deferredSyncFastHelper(counter *int, fail bool) int {
	if fail {
		// Force coroutine slow path with a channel op that won't run on fast path.
		ch := make(chan int, 1)
		ch <- 1
		*counter += <-ch
		return *counter
	}
	*counter++
	return *counter
}

func TestDeferDiscardedSyncFastResultAndPanic(t *testing.T) {
	c := 0
	func() {
		defer deferredSyncFastHelper(&c, false)
	}()
	if c != 1 {
		t.Fatalf("deferredSyncFastHelper fast path counter = %d, want 1", c)
	}

	// Also verify deferred sync-fast call runs during panic unwind.
	recovered := false
	func() {
		defer func() {
			if recover() != nil {
				recovered = true
			}
		}()
		defer deferredSyncFastHelper(&c, false)
		panic("boom")
	}()
	if !recovered || c != 2 {
		t.Fatalf("recovered=%v counter=%d, want true, 2", recovered, c)
	}
}

func TestStageOSyncRampInterfaceAndASCIISetPaths(t *testing.T) {
	// 1. Direct-pointer empty-interface equality in context.(*cancelCtx).Value
	//    and zero-slow-work promoted Deadline() wrappers.
	type keyT struct{ name string }
	k1 := &keyT{name: "k1"}
	k2 := &keyT{name: "k2"}
	base := context.WithValue(context.Background(), k1, int64(42))
	ctx, cancel := context.WithTimeout(base, time.Second)
	defer cancel()
	if got := ctx.Value(k1); got != int64(42) {
		t.Fatalf("ctx.Value(k1) = %v, want 42", got)
	}
	if got := ctx.Value(k2); got != nil {
		t.Fatalf("ctx.Value(k2) = %v, want nil", got)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("ctx.Deadline() = ok:false, want true")
	}
	child, childCancel := context.WithCancel(ctx)
	defer childCancel()
	if dl, ok := child.Deadline(); !ok || dl.IsZero() {
		t.Fatalf("promoted child.Deadline() = (%v, %v), want non-zero true", dl, ok)
	}

	// 2. BoundedLoop local-alloc + store (bytes/strings makeASCIISet) on both
	//    short ASCII fast paths and non-ASCII / long slow paths.
	if got := string(bytes.TrimLeft([]byte(" \t\r\nhello"), " \t\r\n")); got != "hello" {
		t.Fatalf("bytes.TrimLeft ASCII = %q, want hello", got)
	}
	if got := strings.TrimLeft("abcbaz", "abc"); got != "z" {
		t.Fatalf("strings.TrimLeft ASCII = %q, want z", got)
	}
	if got := string(bytes.TrimLeft([]byte("ééok"), "é")); got != "ok" {
		t.Fatalf("bytes.TrimLeft non-ASCII = %q, want ok", got)
	}

	// 3. Pointer-free MakeInterface noscan boxing (AllocAtomicU) round-trip.
	type pod struct {
		A int64
		B [4]uint16
		C bool
	}
	var m sync.Map
	wantPod := pod{A: 987654321, B: [4]uint16{1, 2, 3, 4}, C: true}
	m.Store("pod", wantPod)
	if v, ok := m.Load("pod"); !ok || v != wantPod {
		t.Fatalf("sync.Map boxed pod = (%v, %v), want %+v", v, ok, wantPod)
	}

	// 4. Dynamic non-nil interface comparison must still panic on identical
	//    uncomparable dynamic types via bailout to $coro.slow.
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		var a any = []int{1, 2}
		var b any = []int{1, 2}
		_ = (a == b)
	}()
	if !panicked {
		t.Fatal("comparing two []int interfaces failed to panic")
	}
}
