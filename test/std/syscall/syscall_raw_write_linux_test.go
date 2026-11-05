//go:build linux

package syscall_test

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

func TestRawWriteResultAndEffects(t *testing.T) {
	calls := []struct {
		name string
		call func(uintptr, uintptr, uintptr, uintptr) (uintptr, uintptr, syscall.Errno)
	}{
		{"Syscall", syscall.Syscall},
		{"RawSyscall", syscall.RawSyscall},
		{"Syscall6", func(trap, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno) {
			return syscall.Syscall6(trap, a1, a2, a3, 0, 0, 0)
		}},
		{"RawSyscall6", func(trap, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno) {
			return syscall.RawSyscall6(trap, a1, a2, a3, 0, 0, 0)
		}},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			var pipe [2]int
			if err := syscall.Pipe(pipe[:]); err != nil {
				t.Fatal(err)
			}
			defer syscall.Close(pipe[0])
			defer syscall.Close(pipe[1])
			for _, size := range []struct {
				name  string
				count int
			}{{"one", 1}, {"seven", 7}, {"twenty", 20}} {
				t.Run(size.name, func(t *testing.T) {
					msg := []byte("Hello from Syscall!\n")[:size.count]
					written, second, errno := call.call(syscall.SYS_WRITE, uintptr(pipe[1]), uintptr(unsafe.Pointer(&msg[0])), uintptr(len(msg)))
					runtime.KeepAlive(msg)
					// Only the byte count and errno define write's public result.
					t.Logf("write: count=%d second=%d errno=%d", written, second, errno)
					if written != uintptr(len(msg)) || errno != 0 {
						t.Fatalf("write = (%d, %d), want (%d, 0)", written, errno, len(msg))
					}
					got := make([]byte, len(msg))
					count, err := syscall.Read(pipe[0], got)
					if count != len(msg) || err != nil {
						t.Fatalf("read = (%d, %v)", count, err)
					}
					for i, want := range msg {
						if got[i] != want {
							t.Fatalf("byte %d = %d, want %d", i, got[i], want)
						}
					}
				})
			}
			t.Run("bad-descriptor", func(t *testing.T) {
				msg := []byte("invalid descriptor")
				written, _, errno := call.call(syscall.SYS_WRITE, ^uintptr(0), uintptr(unsafe.Pointer(&msg[0])), uintptr(len(msg)))
				runtime.KeepAlive(msg)
				if written != ^uintptr(0) || errno != syscall.EBADF {
					t.Fatalf("invalid write = (%#x, %v), want (-1, EBADF)", written, errno)
				}
			})
		})
	}
}
