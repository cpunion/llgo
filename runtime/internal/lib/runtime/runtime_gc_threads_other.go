//go:build !nogc && !baremetal && !windows && (darwin || linux)

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"

func enableForeignThreadRegistration() {
	bdwgc.AllowRegisterThreads()
}
