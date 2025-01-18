package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/bdwgc"
)

func mallocgc(size uintptr, typ *_type, needzero bool) unsafe.Pointer {
	p := bdwgc.Malloc(size)
	if needzero {
		clite.Memset(p, 0, size)
	}
	return p
}
