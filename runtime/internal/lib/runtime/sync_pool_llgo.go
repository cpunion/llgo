package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/clite/tls"
)

//go:linkname syncRuntimePoolLocalAlloc sync.runtime_poolLocalAlloc
func syncRuntimePoolLocalAlloc(_ *unsafe.Pointer) unsafe.Pointer {
	// Pool permits cached values to disappear at any time. Use a static TLS
	// slot so a foreign-thread destructor never needs to invoke a captured Go
	// closure while the coroutine scheduler is unavailable.
	handle := tls.AllocStatic[unsafe.Pointer]()
	return unsafe.Pointer(&handle)
}

//go:linkname syncRuntimePoolLocalGet sync.runtime_poolLocalGet
func syncRuntimePoolLocalGet(handle unsafe.Pointer) unsafe.Pointer {
	return (*tls.StaticHandle[unsafe.Pointer])(handle).Get()
}

//go:linkname syncRuntimePoolLocalSet sync.runtime_poolLocalSet
func syncRuntimePoolLocalSet(handle, local unsafe.Pointer) {
	(*tls.StaticHandle[unsafe.Pointer])(handle).Set(local)
}
