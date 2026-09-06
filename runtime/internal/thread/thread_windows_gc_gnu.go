//go:build windows && llgo_windows_gnu && !nogc && !baremetal

package thread

import _ "github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"

const (
	LLGoFiles   = "_wrap/thread_windows_gc_gnu.c"
	LLGoPackage = "link"
)
