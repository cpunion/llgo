package wasmtest

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

//go:noinline
func wasmUnsafeString(data *byte, length int) string { return unsafe.String(data, length) }

//go:noinline
func wasmUnsafeSlice(data *uint64, length int) []uint64 { return unsafe.Slice(data, length) }

func TestUnsafeBuiltinBounds(t *testing.T) {
	data := [3]byte{'a', 'b', 'c'}
	if got := wasmUnsafeString(&data[0], len(data)); got != "abc" {
		t.Fatalf("string = %q", got)
	}
	if got := wasmUnsafeString(nil, 0); got != "" {
		t.Fatal("nil empty string changed")
	}
	words := [2]uint64{13, 29}
	if got := wasmUnsafeSlice(&words[0], len(words)); len(got) != 2 || got[1] != 29 {
		t.Fatalf("slice = %v", got)
	}
	if got := wasmUnsafeSlice(nil, 0); got != nil {
		t.Fatal("nil empty slice changed")
	}
	for _, tc := range []struct {
		name, want string
		run        func()
	}{
		{"string negative", "unsafe.String: len out of range", func() { wasmUnsafeString(&data[0], -1) }},
		{"string nil", "unsafe.String:", func() { wasmUnsafeString(nil, 1) }},
		{"string wrap", "unsafe.String: len out of range", func() { wasmUnsafeString((*byte)(unsafe.Pointer(^uintptr(0))), 2) }},
		{"slice negative", "unsafe.Slice: len out of range", func() { wasmUnsafeSlice(&words[0], -1) }},
		{"slice nil", "unsafe.Slice:", func() { wasmUnsafeSlice(nil, 1) }},
		{"slice multiply", "unsafe.Slice: len out of range", func() { wasmUnsafeSlice(&words[0], int(^uintptr(0)>>1)) }},
		{"slice wrap", "unsafe.Slice: len out of range", func() { wasmUnsafeSlice((*uint64)(unsafe.Pointer(^uintptr(0)-7)), 2) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				value := recover()
				err, ok := value.(runtime.Error)
				if !ok || !strings.Contains(err.Error(), tc.want) ||
					(strings.HasSuffix(tc.name, " nil") && !strings.Contains(err.Error(), "nil")) {
					t.Fatalf("panic = %v, want %q", value, tc.want)
				}
			}()
			tc.run()
		})
	}
}
