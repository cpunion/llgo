// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var boundedLoopEscapeASCII = [128]uint8{
	0x00: 1, 0x01: 1, 0x02: 1, 0x03: 1, 0x04: 1, 0x05: 1, 0x06: 1, 0x07: 1,
	0x08: 1, 0x09: 1, 0x0a: 1, 0x0b: 1, 0x0c: 1, 0x0d: 1, 0x0e: 1, 0x0f: 1,
	0x10: 1, 0x11: 1, 0x12: 1, 0x13: 1, 0x14: 1, 0x15: 1, 0x16: 1, 0x17: 1,
	0x18: 1, 0x19: 1, 0x1a: 1, 0x1b: 1, 0x1c: 1, 0x1d: 1, 0x1e: 1, 0x1f: 1,
	'"': 1, '\\': 1, '<': 1, '>': 1, '&': 1,
}

var boundedLoopTokenTable = [128]bool{
	'0': true, '1': true, '2': true, '3': true, '4': true,
	'5': true, '6': true, '7': true, '8': true, '9': true,
	'A': true, 'B': true, 'C': true, 'D': true, 'E': true, 'F': true,
	'a': true, 'b': true, 'c': true, 'd': true, 'e': true, 'f': true,
	'-': true, '_': true,
}

func boundedLoopConsumeWS(b []byte) (n int) {
	for len(b) > n && (b[n] == ' ' || b[n] == '\t' || b[n] == '\r' || b[n] == '\n') {
		n++
	}
	return n
}

//go:noinline
func refConsumeWS(b []byte) (n int) {
	for len(b) > n && (b[n] == ' ' || b[n] == '\t' || b[n] == '\r' || b[n] == '\n') {
		n++
	}
	return n
}

func boundedLoopConsumeSimpleString(b []byte) (n int) {
	if len(b) > 0 && b[0] == '"' {
		n++
		for len(b) > n && b[n] < 0x80 && boundedLoopEscapeASCII[b[n]] == 0 {
			n++
		}
		if uint(len(b)) > uint(n) && b[n] == '"' {
			n++
			return n
		}
	}
	return 0
}

//go:noinline
func refConsumeSimpleString(b []byte) (n int) {
	if len(b) > 0 && b[0] == '"' {
		n++
		for len(b) > n && b[n] < 0x80 && boundedLoopEscapeASCII[b[n]] == 0 {
			n++
		}
		if uint(len(b)) > uint(n) && b[n] == '"' {
			n++
			return n
		}
	}
	return 0
}

func boundedLoopValidHeaderName(v string) bool {
	if len(v) == 0 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] >= 0x80 || !boundedLoopTokenTable[v[i]] {
			return false
		}
	}
	return true
}

//go:noinline
func refValidHeaderName(v string) bool {
	if len(v) == 0 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] >= 0x80 || !boundedLoopTokenTable[v[i]] {
			return false
		}
	}
	return true
}

func boundedLoopSumBytes(b []byte) int {
	sum := 0
	for i := 0; i < len(b); i++ {
		sum += int(b[i])
	}
	return sum
}

//go:noinline
func refSumBytes(b []byte) int {
	sum := 0
	for i := 0; i < len(b); i++ {
		sum += int(b[i])
	}
	return sum
}

func TestBoundedLoopSyncRampBoundariesAndAbnormalInputs(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 15, 16, 17, 32, 64, 256, 4096}

	// 1. Whitespace boundaries (pure whitespace, whitespace + trailer, mixed WS, nil/empty).
	if got := boundedLoopConsumeWS(nil); got != 0 {
		t.Fatalf("ConsumeWS(nil) = %d, want 0", got)
	}
	for _, n := range lengths {
		ws := bytes.Repeat([]byte{' '}, n)
		if got, want := boundedLoopConsumeWS(ws), refConsumeWS(ws); got != want || got != n {
			t.Fatalf("ConsumeWS(spaces=%d) = %d, want %d", n, got, want)
		}
		withTrailer := append(append([]byte(nil), ws...), 'x', ' ')
		if got, want := boundedLoopConsumeWS(withTrailer), refConsumeWS(withTrailer); got != want || got != n {
			t.Fatalf("ConsumeWS(spaces=%d+x) = %d, want %d", n, got, want)
		}
		mixed := make([]byte, n+1)
		wsChars := []byte{' ', '\t', '\r', '\n'}
		for i := 0; i < n; i++ {
			mixed[i] = wsChars[i%len(wsChars)]
		}
		mixed[n] = 0xff
		if got, want := boundedLoopConsumeWS(mixed), refConsumeWS(mixed); got != want || got != n {
			t.Fatalf("ConsumeWS(mixed=%d) = %d, want %d", n, got, want)
		}
	}

	// 2. Simple string boundaries & abnormal inputs (escapes, non-ASCII, control, <, >, &, unterminated).
	abnormalStrs := [][]byte{
		nil,
		{},
		[]byte(`"`),
		[]byte(`""`),
		[]byte(`"a"`),
		[]byte(`"abc`),
		[]byte(`"abcdefghij`),
		[]byte(`abc"`),
		[]byte(`"ab\"cd"`),
		[]byte(`"ab\\cd"`),
		[]byte("\"ab\ncd\""),
		[]byte("\"ab\x00cd\""),
		[]byte("\"ab\x1fcd\""),
		[]byte(`"a<b"`),
		[]byte(`"a>b"`),
		[]byte(`"a&b"`),
		[]byte("\"ab\x80cd\""),
		[]byte("\"ab\xc3\xa9cd\""),
		[]byte("\"ab\xffcd\""),
		[]byte(`"abcdefghij\n"`),
		[]byte(`"abcdefghij<"`),
	}
	for _, in := range abnormalStrs {
		if got, want := boundedLoopConsumeSimpleString(in), refConsumeSimpleString(in); got != want {
			t.Fatalf("ConsumeSimpleString(%q) = %d, want %d", in, got, want)
		}
	}
	for _, n := range lengths {
		in := append([]byte{'"'}, bytes.Repeat([]byte{'a'}, n)...)
		in = append(in, '"', ',', '1')
		if got, want := boundedLoopConsumeSimpleString(in), refConsumeSimpleString(in); got != want || got != n+2 {
			t.Fatalf("ConsumeSimpleString(len=%d) = %d, want %d", n, got, want)
		}
		unterm := append([]byte{'"'}, bytes.Repeat([]byte{'a'}, n)...)
		if got, want := boundedLoopConsumeSimpleString(unterm), refConsumeSimpleString(unterm); got != want || got != 0 {
			t.Fatalf("ConsumeSimpleString(unterminated=%d) = %d, want %d", n, got, want)
		}
	}

	// 3. String-indexed loop boundaries & abnormal inputs (ValidHeaderName).
	for _, n := range lengths {
		s := strings.Repeat("a", n)
		if got, want := boundedLoopValidHeaderName(s), refValidHeaderName(s); got != want {
			t.Fatalf("ValidHeaderName(len=%d) = %t, want %t", n, got, want)
		}
		badEnd := s + "!"
		if got, want := boundedLoopValidHeaderName(badEnd), refValidHeaderName(badEnd); got != want || got {
			t.Fatalf("ValidHeaderName(badEnd len=%d) = %t, want %t", n, got, want)
		}
		badUTF8 := s + "\x80"
		if got, want := boundedLoopValidHeaderName(badUTF8), refValidHeaderName(badUTF8); got != want || got {
			t.Fatalf("ValidHeaderName(badUTF8 len=%d) = %t, want %t", n, got, want)
		}
	}

	// 4. Full-scan no-interior-exit loop (entry length guard + backedge budget).
	if got := boundedLoopSumBytes(nil); got != 0 {
		t.Fatalf("SumBytes(nil) = %d, want 0", got)
	}
	for _, n := range lengths {
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte((i*17 + 3) & 0xff)
		}
		if got, want := boundedLoopSumBytes(buf), refSumBytes(buf); got != want {
			t.Fatalf("SumBytes(len=%d) = %d, want %d", n, got, want)
		}
	}

	// 5. Real stdlib encoding/json boundary & abnormal payloads (exercises real jsonwire fast+slow paths).
	type doc struct {
		K string `json:"k"`
		V int    `json:"v"`
	}
	for _, pad := range []int{0, 1, 3, 4, 5, 6, 32, 256} {
		for _, slen := range []int{0, 1, 4, 5, 6, 7, 8, 9, 32, 256} {
			val := strings.Repeat("x", slen)
			raw := []byte(strings.Repeat(" ", pad) + `{"k":"` + val + `","v":123456}` + strings.Repeat("\t", pad))
			var out doc
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("json.Unmarshal(pad=%d, slen=%d) err=%v", pad, slen, err)
			}
			if out.K != val || out.V != 123456 {
				t.Fatalf("json.Unmarshal(pad=%d, slen=%d) got=%+v", pad, slen, out)
			}
		}
	}
	// Escaped, UTF-8, and HTML-escaped characters in json.Unmarshal.
	var escaped doc
	if err := json.Unmarshal([]byte(`{"k":"a\"b\\c<>&\u00e9","v":7}`), &escaped); err != nil {
		t.Fatalf("json.Unmarshal escaped failed: %v", err)
	}
	if escaped.K != "a\"b\\c<>&\u00e9" || escaped.V != 7 {
		t.Fatalf("json.Unmarshal escaped got=%+v", escaped)
	}
	var bad doc
	if err := json.Unmarshal([]byte(`{"k":"unterminated`), &bad); err == nil {
		t.Fatal("json.Unmarshal unterminated unexpectedly succeeded")
	}

	// 6. Spawned and indirect function-value invocations of bounded-loop sync-ramp targets.
	done := make(chan int, 2)
	fnWS := boundedLoopConsumeWS
	fnSS := boundedLoopConsumeSimpleString
	go func() { done <- fnWS([]byte("   x")) }()
	go func() { done <- fnSS([]byte(`"abc"`)) }()
	if a, b := <-done, <-done; a+b != 3+5 {
		t.Fatalf("spawned/indirect bounded-loop calls sum=%d, want 8", a+b)
	}
}

func TestBoundedLoopSyncRampPreemptionProgress(t *testing.T) {
	// Case A: Long inputs exceeding the bounded-loop work budget must fall back
	// to $coro.slow and yield so a ready peer goroutine makes timely progress
	// under GOMAXPROCS=1.
	t.Run("long-input-peer-progress", func(t *testing.T) {
		longWS := bytes.Repeat([]byte{' '}, 256*1024)
		longStr := append([]byte{'"'}, bytes.Repeat([]byte{'a'}, 256*1024)...)
		longStr = append(longStr, '"')
		longBuf := bytes.Repeat([]byte{1}, 256*1024)

		t.Run("ConsumeWS-direct", func(t *testing.T) {
			var peerDone atomic.Uint32
			start := time.Now()
			go func() { peerDone.Store(1) }()
			calls := 0
			for peerDone.Load() == 0 {
				if got := boundedLoopConsumeWS(longWS); got != len(longWS) {
					t.Fatalf("ConsumeWS(long) = %d, want %d", got, len(longWS))
				}
				calls++
				if time.Since(start) > 2*time.Second && peerDone.Load() == 0 {
					t.Fatalf("ConsumeWS long-input starved ready peer (procs=%d, calls=%d)", runtime.GOMAXPROCS(0), calls)
				}
			}
		})

		t.Run("ConsumeSimpleString-direct", func(t *testing.T) {
			var peerDone atomic.Uint32
			start := time.Now()
			go func() { peerDone.Store(1) }()
			calls := 0
			for peerDone.Load() == 0 {
				if got := boundedLoopConsumeSimpleString(longStr); got != len(longStr) {
					t.Fatalf("ConsumeSimpleString(long) = %d, want %d", got, len(longStr))
				}
				calls++
				if time.Since(start) > 2*time.Second && peerDone.Load() == 0 {
					t.Fatalf("ConsumeSimpleString long-input starved ready peer (procs=%d, calls=%d)", runtime.GOMAXPROCS(0), calls)
				}
			}
		})

		t.Run("SumBytes-direct", func(t *testing.T) {
			var peerDone atomic.Uint32
			start := time.Now()
			go func() { peerDone.Store(1) }()
			calls := 0
			for peerDone.Load() == 0 {
				if got := boundedLoopSumBytes(longBuf); got != len(longBuf) {
					t.Fatalf("SumBytes(long) = %d, want %d", got, len(longBuf))
				}
				calls++
				if time.Since(start) > 2*time.Second && peerDone.Load() == 0 {
					t.Fatalf("SumBytes long-input starved ready peer (procs=%d, calls=%d)", runtime.GOMAXPROCS(0), calls)
				}
			}
		})
	})

	// Case B: Direct repeated short calls at the same call site (every call
	// completing on the sync-fast path without allocating a frame) must NOT
	// starve ready peer goroutines under GOMAXPROCS=1: the caller's preemption
	// countdown remains intact.
	t.Run("repeated-short-same-callsite-progress", func(t *testing.T) {
		shortWS := []byte("  x")
		shortStr := []byte(`"ab"`)
		shortName := "ab-1"
		shortBuf := []byte{1, 2, 3}

		t.Run("ConsumeWS-direct", func(t *testing.T) {
			var peerDone atomic.Uint32
			start := time.Now()
			go func() { peerDone.Store(1) }()
			calls, acc := 0, 0
			for peerDone.Load() == 0 {
				acc += boundedLoopConsumeWS(shortWS)
				calls++
				if calls >= 65536 && calls%4096 == 0 && time.Since(start) > 2*time.Second && peerDone.Load() == 0 {
					t.Fatalf("ConsumeWS repeated short calls starved ready peer (procs=%d, calls=%d, acc=%d)", runtime.GOMAXPROCS(0), calls, acc)
				}
			}
			if acc == 0 {
				t.Fatal("ConsumeWS unexpected zero accumulator")
			}
		})

		t.Run("ConsumeSimpleString-direct", func(t *testing.T) {
			var peerDone atomic.Uint32
			start := time.Now()
			go func() { peerDone.Store(1) }()
			calls, acc := 0, 0
			for peerDone.Load() == 0 {
				acc += boundedLoopConsumeSimpleString(shortStr)
				calls++
				if calls >= 65536 && calls%4096 == 0 && time.Since(start) > 2*time.Second && peerDone.Load() == 0 {
					t.Fatalf("ConsumeSimpleString repeated short calls starved ready peer (procs=%d, calls=%d, acc=%d)", runtime.GOMAXPROCS(0), calls, acc)
				}
			}
			if acc == 0 {
				t.Fatal("ConsumeSimpleString unexpected zero accumulator")
			}
		})

		t.Run("ValidHeaderName-direct", func(t *testing.T) {
			var peerDone atomic.Uint32
			start := time.Now()
			go func() { peerDone.Store(1) }()
			calls, acc := 0, 0
			for peerDone.Load() == 0 {
				if boundedLoopValidHeaderName(shortName) {
					acc++
				}
				calls++
				if calls >= 65536 && calls%4096 == 0 && time.Since(start) > 2*time.Second && peerDone.Load() == 0 {
					t.Fatalf("ValidHeaderName repeated short calls starved ready peer (procs=%d, calls=%d, acc=%d)", runtime.GOMAXPROCS(0), calls, acc)
				}
			}
			if acc == 0 {
				t.Fatal("ValidHeaderName unexpected zero accumulator")
			}
		})

		t.Run("SumBytes-direct", func(t *testing.T) {
			var peerDone atomic.Uint32
			start := time.Now()
			go func() { peerDone.Store(1) }()
			calls, acc := 0, 0
			for peerDone.Load() == 0 {
				acc += boundedLoopSumBytes(shortBuf)
				calls++
				if calls >= 65536 && calls%4096 == 0 && time.Since(start) > 2*time.Second && peerDone.Load() == 0 {
					t.Fatalf("SumBytes repeated short calls starved ready peer (procs=%d, calls=%d, acc=%d)", runtime.GOMAXPROCS(0), calls, acc)
				}
			}
			if acc == 0 {
				t.Fatal("SumBytes unexpected zero accumulator")
			}
		})
	})
}
