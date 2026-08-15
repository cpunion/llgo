//go:build !nogc && !baremetal && windows

package runtime

import "github.com/goplus/llgo/runtime/internal/clite/bdwgc"

func enableForeignThreadRegistration() {
	bdwgc.AllowRegisterThreads()
}
