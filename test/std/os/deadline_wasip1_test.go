//go:build wasip1 && wasm

package os_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWASIFileDeadlineReadWriteAndReset(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "deadline"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.SetDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired Read = %v", err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired Write = %v", err)
	}
	if err := f.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x")); err != nil {
		t.Fatalf("cleared write deadline: %v", err)
	}
	if _, err := f.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write reset changed read deadline: %v", err)
	}
	if err := f.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if n, err := f.Read(b[:]); err != nil || n != 1 || b[0] != 'x' {
		t.Fatalf("cleared read deadline: n=%d byte=%q err=%v", n, b, err)
	}
}
