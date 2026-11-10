//go:build llgo

package ffi_test

import (
	"runtime"
	"testing"

	ffi "github.com/xgo-dev/llgo/runtime/internal/ffi"
)

// Preparing a signature calls native libffi symbols linked by LLGo. The
// storage-only tests remain available to the standard Go compatibility suite.
//
//go:noinline
func makeOwnedSignature(t *testing.T) *ffi.Signature {
	t.Helper()
	result := ffi.StructOf(ffi.TypeInt64, ffi.TypeInt32)
	sig, err := ffi.NewSignature(result, ffi.TypeInt32)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestPreparedSignatureOwnsReturnType(t *testing.T) {
	if runtime.GOOS == "wasip1" {
		t.Skip("WASI uses the unsupported-libffi stub")
	}
	sig := makeOwnedSignature(t)
	runtime.GC()
	ret := sig.ReturnType()
	if ret == nil || ffi.TypeElement(ret, 0) != ffi.TypeInt64 || ffi.TypeElement(ret, 1) != ffi.TypeInt32 || ffi.TypeElement(ret, 2) != nil || ret.ByteSize() < 12 {
		t.Fatal("prepared CIF lost its return type or element storage")
	}
	if sig.ArgumentType(0) != ffi.TypeInt32 {
		t.Fatal("prepared CIF lost its argument type storage")
	}
	if sig.ArgumentType(1) != nil || sig.ArgumentType(^uintptr(0)) != nil {
		t.Fatal("prepared CIF accepted an out-of-range argument")
	}
	if (*ffi.Signature)(nil).ArgumentType(0) != nil || new(ffi.Signature).ArgumentType(0) != nil {
		t.Fatal("prepared CIF accepted absent argument storage")
	}
	runtime.KeepAlive(sig)
}
