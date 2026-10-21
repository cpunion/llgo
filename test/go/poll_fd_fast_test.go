//go:build llgo

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPollFDFastFileIO(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fd-fast-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for index := 0; index < 1000; index++ {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		want := []byte{byte(index), byte(index >> 8), 17}
		if n, err := f.Write(want); err != nil || n != len(want) {
			t.Fatalf("write=%d,%v", n, err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(want))
		if _, err := io.ReadFull(f, got); err != nil {
			t.Fatal(err)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("read=%v want=%v", got, want)
			}
		}
	}
}

func TestPollFDFastCloseWithActiveRawOperation(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "read"
		if write {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			defer w.Close()
			target := r
			if write {
				target = w
			}
			raw, err := target.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			returned := make(chan error, 1)
			var once sync.Once
			// RawConn invokes this callback only after acquiring the FD's read/write
			// lock. The barrier therefore proves the reference is live before Close;
			// false keeps the operation inside that lock until Close wakes the poll.
			callback := func(uintptr) bool { once.Do(func() { close(entered) }); return false }
			go func() {
				if write {
					returned <- raw.Write(callback)
				} else {
					returned <- raw.Read(callback)
				}
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("raw operation never acquired its lock")
			}
			closed := make(chan error, 1)
			go func() { closed <- target.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Close did not join the active raw operation")
			}
			wantClosed := raw.Control(func(uintptr) { t.Error("closed FD ran a raw callback") })
			if wantClosed == nil {
				t.Fatal("closed FD control succeeded")
			}
			select {
			case err := <-returned:
				if !errors.Is(err, wantClosed) {
					t.Fatalf("raw operation=%v want %v", err, wantClosed)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("last closed reference did not finish destruction")
			}
		})
	}
}
