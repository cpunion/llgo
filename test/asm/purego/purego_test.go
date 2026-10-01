//go:build darwin || linux

package purego_test

import (
	"bytes"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestPublicNativeCalls(t *testing.T) {
	library := "libc.so.6"
	if runtime.GOOS == "darwin" {
		library = "/usr/lib/libSystem.B.dylib"
	}
	handle, err := purego.Dlopen(library, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := purego.Dlclose(handle); err != nil {
			t.Error(err)
		}
	})
	length, err := purego.Dlsym(handle, "strlen")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "native ABI", "0123456789abcdef"} {
		input := append([]byte(value), 0)
		got, _, _ := purego.SyscallN(length, uintptr(unsafe.Pointer(&input[0])))
		runtime.KeepAlive(input)
		if got != uintptr(len(value)) {
			t.Fatalf("native strlen(%q)=%d", value, got)
		}
	}
	var move func(dst, src *byte, size uintptr) *byte
	purego.RegisterLibFunc(&move, handle, "memmove")
	input := []byte("abcdefghijklmno")
	if move(&input[1], &input[0], 10) != &input[1] || !bytes.Equal(input[:11], []byte("aabcdefghij")) {
		t.Fatal("registered native memmove arguments, result or side effect differs")
	}
	if _, err := purego.Dlsym(handle, "llgo_e2e_symbol_that_does_not_exist"); err == nil {
		t.Fatal("missing native symbol accepted")
	}
}
