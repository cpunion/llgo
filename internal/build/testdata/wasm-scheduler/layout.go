package main

import "unsafe"

type wasmStructLayoutProbe struct {
	prefix byte
	wide   uint64
	ptr    unsafe.Pointer
}

func checkWasmStructLayout() {
	var value wasmStructLayoutProbe
	base := uintptr(unsafe.Pointer(&value))
	if got, want := uintptr(unsafe.Pointer(&value.wide))-base, unsafe.Offsetof(value.wide); got != want {
		panic("uint64 field layout mismatch")
	}
	if got, want := uintptr(unsafe.Pointer(&value.ptr))-base, unsafe.Offsetof(value.ptr); got != want {
		panic("pointer field layout mismatch")
	}

	var values [2]wasmStructLayoutProbe
	stride := uintptr(unsafe.Pointer(&values[1])) - uintptr(unsafe.Pointer(&values[0]))
	if stride != unsafe.Sizeof(value) {
		panic("struct size layout mismatch")
	}
}
