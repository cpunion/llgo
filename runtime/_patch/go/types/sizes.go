package types

import (
	"runtime"
	"unsafe"
)

//llgo:cdecl
type llgoNativePointerProbe struct{ P unsafe.Pointer }

//llgo:cdecl
type llgoNativeAlignProbe struct{ F float64 }

// LLGo intentionally accepts both compiler identities. Neither name asks for
// cmd/compile storage when this package is built by LLGo.
func SizesFor(compiler, arch string) Sizes {
	switch compiler {
	case "gc", "llgo":
		if s := LLGoSizesFor(arch); s != nil {
			if arch == runtime.GOARCH {
				// GOARCH alone cannot distinguish wasm Memory32/Memory64 or
				// the platform-dependent C double alignment on 386.
				s.WordSize = int64(unsafe.Sizeof(uintptr(0)))
				s.MaxAlign = int64(unsafe.Alignof(complex128(0)))
				s.PointerSize = int64(unsafe.Sizeof(llgoNativePointerProbe{}))
				s.NativeAlign64 = int64(unsafe.Alignof(llgoNativeAlignProbe{}))
			}
			s.Layout = &llgoSourceLayout
			return s
		}
	case "gccgo":
		if s, ok := gccgoArchSizes[arch]; ok {
			return s
		}
	}
	return nil
}

var llgoSourceLayout LLGoLayout
