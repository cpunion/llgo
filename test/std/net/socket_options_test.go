package net_test

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
)

// The Go js/wasip1 loopback network has no host TCP socket options and no
// write-buffer resizing. Require the documented implementation error, while
// retaining successful option-setting assertions on native sockets.
func checkSocketOption(t *testing.T, name string, err error, wasmError syscall.Errno) {
	t.Helper()
	var want error
	if runtime.GOARCH == "wasm" {
		want = wasmError
	}
	if !errors.Is(err, want) {
		t.Errorf("%s = %v, want %v", name, err, want)
	}
}

func checkWriteBuffer(t *testing.T, err error) {
	t.Helper()
	checkSocketOption(t, "SetWriteBuffer", err, syscall.EOPNOTSUPP)
}

func checkTCPOption(t *testing.T, name string, err error) {
	t.Helper()
	checkSocketOption(t, name, err, syscall.ENOPROTOOPT)
}
